package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

type ServerInfo struct {
	Online  bool   `json:"online"`
	Players int    `json:"players"`
	Max     int    `json:"max"`
	Motd    string `json:"motd"`
	Names   []string `json:"names"`
	Version string `json:"version"`
	Ping    int64  `json:"ping"`
}

var (
	srvInfo   ServerInfo
	srvInfoMu sync.Mutex
)

func registerAPI(mux *http.ServeMux) {
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.Header.Get("X-KW-Token") != apiToken {
				http.Error(w, "forbidden", 403)
				return
			}
			h(w, r)
		}
	}
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		pollMu.Lock()
		lastPoll = time.Now()
		pollMu.Unlock()
		a := currentAccount()
		var acc any
		if a != nil {
			acc = map[string]string{"name": a.Name, "uuid": a.UUID}
		}
		srvInfoMu.Lock()
		si := srvInfo
		srvInfoMu.Unlock()
		c := getCfg()
		m := cachedManifest()
		newer := ""
		if m != nil && m.Launcher != "" && m.Launcher != LauncherVersion {
			newer = m.Launcher
		}
		writeJSON(w, map[string]any{
			"account": acc, "status": getStatus(), "server": si, "authError": authError,
			"config":  c, "address": serverAddr(), "version": LauncherVersion, "newerLauncher": newer,
			"running": gameRunning(), "hasClientId": clientID() != "",
		})
	})
	mux.HandleFunc("/api/login", guard(func(w http.ResponseWriter, r *http.Request) {
		u, err := beginLogin()
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		authError = ""
		writeJSON(w, map[string]string{"url": u})
	}))
	mux.HandleFunc("/api/logout", guard(func(w http.ResponseWriter, r *http.Request) {
		logout()
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/play", guard(func(w http.ResponseWriter, r *http.Request) {
		go play()
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/config", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			MemoryMB *int    `json:"memoryMB"`
			Host     *string `json:"host"`
			AutoJoin *bool   `json:"autoJoin"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
		cfgMu.Lock()
		if in.MemoryMB != nil && *in.MemoryMB >= 1024 && *in.MemoryMB <= 32768 {
			cfg.MemoryMB = *in.MemoryMB
		}
		if in.Host != nil {
			h := strings.TrimSpace(*in.Host)
			if regexp.MustCompile(`^[A-Za-z0-9.\-]+$`).MatchString(h) {
				cfg.Host = h
				manifestMu.Lock()
				manifest = nil
				manifestMu.Unlock()
				_ = os.Remove(dirs("manifest.json"))
			}
		}
		if in.AutoJoin != nil {
			cfg.AutoJoin = *in.AutoJoin
		}
		cfgMu.Unlock()
		saveConfig()
		go pingServer()
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/openfolder", guard(func(w http.ResponseWriter, r *http.Request) {
		openBrowser(gameDir())
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/quit", guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"ok": true})
		go func() { time.Sleep(300 * time.Millisecond); os.Exit(0) }()
	}))
}

func serverWatcher() {
	_, _ = fetchManifest()
	for {
		pingServer()
		time.Sleep(15 * time.Second)
	}
}

func pingServer() {
	si, err := slp(serverAddr())
	if err != nil {
		si = ServerInfo{}
	}
	srvInfoMu.Lock()
	srvInfo = si
	srvInfoMu.Unlock()
}

// ---------------------------------------------------------------- 서버 목록 핑 (접속자 수·MOTD)
func writeVarInt(b []byte, v int) []byte {
	u := uint32(v)
	for {
		if u&^0x7F == 0 {
			return append(b, byte(u))
		}
		b = append(b, byte(u&0x7F|0x80))
		u >>= 7
	}
}

func readVarInt(r *bufio.Reader) (int, error) {
	var n uint32
	for i := 0; i < 5; i++ {
		c, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		n |= uint32(c&0x7F) << (7 * i)
		if c&0x80 == 0 {
			return int(int32(n)), nil
		}
	}
	return 0, errors.New("varint too long")
}

func slp(addr string) (ServerInfo, error) {
	host, port := addr, "25565"
	if h, p, err := net.SplitHostPort(addr); err == nil {
		host, port = h, p
	}
	t0 := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 4*time.Second)
	if err != nil {
		return ServerInfo{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var pkt []byte
	pkt = writeVarInt(pkt, 0)
	pkt = writeVarInt(pkt, -1)
	pkt = writeVarInt(pkt, len(host))
	pkt = append(pkt, host...)
	var pb [2]byte
	var pn int
	fmt.Sscanf(port, "%d", &pn)
	binary.BigEndian.PutUint16(pb[:], uint16(pn))
	pkt = append(pkt, pb[:]...)
	pkt = writeVarInt(pkt, 1)
	out := writeVarInt(nil, len(pkt))
	out = append(out, pkt...)
	out = append(out, 1, 0) // 상태 요청
	if _, err := conn.Write(out); err != nil {
		return ServerInfo{}, err
	}
	r := bufio.NewReader(conn)
	if _, err := readVarInt(r); err != nil {
		return ServerInfo{}, err
	}
	if _, err := readVarInt(r); err != nil {
		return ServerInfo{}, err
	}
	n, err := readVarInt(r)
	if err != nil || n <= 0 || n > 1<<21 {
		return ServerInfo{}, errors.New("bad status")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return ServerInfo{}, err
	}
	var st struct {
		Version struct {
			Name string `json:"name"`
		} `json:"version"`
		Players struct {
			Max    int `json:"max"`
			Online int `json:"online"`
			Sample []struct {
				Name string `json:"name"`
			} `json:"sample"`
		} `json:"players"`
		Description json.RawMessage `json:"description"`
	}
	if err := json.Unmarshal(buf, &st); err != nil {
		return ServerInfo{}, err
	}
	si := ServerInfo{Online: true, Players: st.Players.Online, Max: st.Players.Max, Version: st.Version.Name, Ping: time.Since(t0).Milliseconds()}
	for _, s := range st.Players.Sample {
		si.Names = append(si.Names, s.Name)
	}
	si.Motd = motdText(st.Description)
	return si, nil
}

// 채팅 컴포넌트 -> 글자 (색 코드 뺌)
func motdText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return stripCodes(s)
	}
	var c struct {
		Text  string            `json:"text"`
		Extra []json.RawMessage `json:"extra"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return ""
	}
	out := c.Text
	for _, e := range c.Extra {
		out += motdText(e)
	}
	return stripCodes(out)
}

func stripCodes(s string) string { return regexp.MustCompile(`§.`).ReplaceAllString(s, "") }
