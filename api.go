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
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type ServerInfo struct {
	Online  bool     `json:"online"`
	Players int      `json:"players"`
	Max     int      `json:"max"`
	Motd    string   `json:"motd"`
	Names   []string `json:"names"`
	Version string   `json:"version"`
	Ping    int64    `json:"ping"`
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
		if m != nil && m.Launcher != "" && verLess(LauncherVersion, m.Launcher) {
			newer = m.Launcher
		}
		// 1.1.2 화면에 서버 IP 가 안 보이게 (설정·오류 글에서 가림)
		st := getStatus()
		st.Text, st.Error, st.LogTail = hideHost(st.Text), hideHost(st.Error), hideHost(st.LogTail)
		c.Host = ""
		writeJSON(w, map[string]any{
			"account": acc, "status": st, "server": si, "authError": hideHost(authError),
			"config": c, "address": "", "version": LauncherVersion, "newerLauncher": newer,
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
		openBrowser(u) // 1.2.7 주소창이 보이는 기본 브라우저에서 로그인 (런처 창 안에서는 진짜 마이크로소프트인지 확인할 수 없어서)
		writeJSON(w, map[string]bool{"opened": true})
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
	// 편의성 모드 목록 (서버에서 새로 받아 봄, 못 받으면 마지막 것)
	mux.HandleFunc("/api/mods", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-KW-Token") != apiToken { // 1.2.7 읽기(서버에서 새로 받음)도 토큰 있어야
			http.Error(w, "forbidden", 403)
			return
		}
		if r.Method == "POST" {
			var in struct {
				ID string `json:"id"`
				On bool   `json:"on"`
			}
			_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
			if in.ID != "" && len(in.ID) < 128 {
				cfgMu.Lock()
				if cfg.Mods == nil {
					cfg.Mods = map[string]bool{}
				}
				cfg.Mods[in.ID] = in.On
				cfgMu.Unlock()
				saveConfig()
			}
		}
		m := cachedManifest()
		if r.Method == "GET" {
			if f, err := fetchManifest(); err == nil {
				m = f
			}
		}
		type row struct {
			OptMod
			Enabled  bool     `json:"enabled"`
			Active   bool     `json:"active"`
			NeededBy []string `json:"neededBy"`
		}
		out := []row{}
		var req []string
		if m != nil {
			act, nb := optActive(m)
			for _, o := range m.Optional {
				out = append(out, row{o, optEnabled(o), act[o.ID], nb[o.ID]})
			}
			for _, f := range m.Mods {
				req = append(req, f.File)
			}
		}
		// 직접 넣은 모드 = mods 폴더에서 런처가 넣지 않은 .jar
		managed := map[string]bool{}
		var ml []string
		if b, err := os.ReadFile(dirs("managed-mods.json")); err == nil {
			_ = json.Unmarshal(b, &ml)
		}
		for _, f := range ml {
			managed[f] = true
		}
		manual := []string{}
		if es, err := os.ReadDir(filepath.Join(gameDir(), "mods")); err == nil {
			for _, e := range es {
				n := e.Name()
				if !e.IsDir() && strings.HasSuffix(strings.ToLower(n), ".jar") && !managed[n] {
					if m != nil {
						id := strings.ToLower(jarModID(filepath.Join(gameDir(), "mods", n)))
						for _, b := range m.BlockedMods {
							if strings.ToLower(b) == id {
								n += " (막힌 모드: 게임 켤 때 빠져요)"
							}
						}
					}
					manual = append(manual, n)
				}
			}
		}
		mc, fl := "26.3", ""
		if m != nil {
			if m.Minecraft != "" {
				mc = m.Minecraft
			}
			fl = m.FabricLoader
		}
		writeJSON(w, map[string]any{"optional": out, "required": req, "manual": manual, "minecraft": mc, "fabricLoader": fl, "running": gameRunning(), "modVer": currentModVer(m), "exported": getCfg().Exported})
	})
	mux.HandleFunc("/api/exportmods", guard(func(w http.ResponseWriter, r *http.Request) {
		p, err := exportMods()
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		openBrowser(filepath.Dir(p))
		writeJSON(w, map[string]string{"path": p})
	}))
	mux.HandleFunc("/api/openmods", guard(func(w http.ResponseWriter, r *http.Request) {
		d := filepath.Join(gameDir(), "mods")
		_ = os.MkdirAll(d, 0o755)
		openBrowser(d)
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/openfolder", guard(func(w http.ResponseWriter, r *http.Request) {
		openBrowser(gameDir())
		writeJSON(w, map[string]bool{"ok": true})
	}))
	// 제거할 때 켜져 있는 런처를 끝냄 (게임 중이면 그대로)
	mux.HandleFunc("/api/quit-uninstall", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("X-KW-Token") != apiToken || gameRunning() { // 1.2.6 토큰 있어야
			http.Error(w, "forbidden", 403)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		go func() { time.Sleep(200 * time.Millisecond); os.Exit(0) }()
	})
	mux.HandleFunc("/api/show", guard(func(w http.ResponseWriter, r *http.Request) { // 1.2.7 토큰 있어야 (아무 사이트나 창을 띄우지 못하게)
		openWindow(fmt.Sprintf("http://localhost:%d/", uiPort))
		writeJSON(w, map[string]bool{"ok": true})
	}))
	// 1.0.2 서버 화면 정보 (접속자·코어·공지·디스코드), 고화질 지도
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		b, err := serverInfo()
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/api/map.png", func(w http.ResponseWriter, r *http.Request) {
		p, err := mapImage(r.URL.Query().Get("v"))
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Cache-Control", "max-age=86400")
		http.ServeFile(w, r, p)
	})
	mux.HandleFunc("/api/bg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		if p := serverBackground(); p != "" {
			http.ServeFile(w, r, p)
			return
		}
		b, _ := webFS.ReadFile("web/bg.jpg")
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/api/open", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
		ok := regexp.MustCompile(`^https://(discord\.gg|discord\.com|(www\.)?github\.com|modrinth\.com|(www\.)?curseforge\.com)/[A-Za-z0-9/_\-.]+$`).MatchString(in.URL) // 1.2.7 정해진 곳만
		if ok {
			openBrowser(in.URL)
		}
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

// ---------------------------------------------------------------- 서버 화면 정보 (플러그인 /launcher/status.json)
var (
	infoMu    sync.Mutex
	infoCache []byte
	infoAt    time.Time
)

func serverInfo() ([]byte, error) {
	infoMu.Lock()
	defer infoMu.Unlock()
	if infoCache != nil && time.Since(infoAt) < 4*time.Second {
		return infoCache, nil
	}
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(filesBase() + "/launcher/status.json")
	if err != nil {
		if infoCache != nil {
			return infoCache, nil
		}
		return nil, errors.New("서버에 연결하지 못했어요")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("서버 정보 없음 (HTTP %d)", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	infoCache, infoAt = b, time.Now()
	var mv struct {
		Map string `json:"map"`
	}
	if json.Unmarshal(b, &mv) == nil {
		curMapV = mv.Map
	}
	return b, nil
}

var curMapV string // 1.2.7 서버가 알려 준 지금 지도 판 (그 판만 받음)

var mapMu sync.Mutex

// 지도 그림은 서버가 그린 것을 받아 두고 (v = 서버가 알려 준 지도 판), 같은 판이면 받아 둔 것을 씀
func mapImage(v string) (string, error) {
	mapMu.Lock()
	defer mapMu.Unlock()
	safe := regexp.MustCompile(`[^A-Za-z0-9.,@x\-]`).ReplaceAllString(v, "_")
	p := dirs("cache", "map-"+safe+".png")
	if st, err := os.Stat(p); err == nil && st.Size() > 0 && safe != "" {
		return p, nil
	}
	if safe == "" || len(safe) > 80 {
		return "", fmt.Errorf("bad")
	}
	infoMu.Lock()
	cur := curMapV
	infoMu.Unlock()
	if v != cur { // 아무 사이트가 v 를 바꿔 가며 계속 받게 하지 못하게
		return "", fmt.Errorf("not current")
	}
	if err := download(dlTask{URL: filesBase() + "/launcher/map.png", Path: p}); err != nil {
		return "", err
	}
	if old, _ := filepath.Glob(dirs("cache", "map-*.png")); len(old) > 0 { // 1.2.7 지금 것만 남김 (캐시가 끝없이 쌓이지 않게)
		for _, o := range old {
			if o != p {
				_ = os.Remove(o)
			}
		}
	}
	return p, nil
}

// a < b ("1.0.2" < "1.0.10")
func verLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := 0, 0
		if i < len(pa) {
			fmt.Sscanf(pa[i], "%d", &x)
		}
		if i < len(pb) {
			fmt.Sscanf(pb[i], "%d", &y)
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// 서버장이 plugins/KoreaWar/launcher/background.jpg 를 두면 그 그림을 배경으로 (켤 때마다 한 번 받아 봄)
var (
	bgOnce sync.Once
	bgPath string
)

func serverBackground() string {
	bgOnce.Do(func() {
		p := dirs("cache", "background.jpg")
		_ = os.Remove(p)
		if err := download(dlTask{URL: filesBase() + "/launcher/background.jpg", Path: p}); err == nil {
			if st, err := os.Stat(p); err == nil && st.Size() > 1000 {
				bgPath = p
			}
		}
	})
	return bgPath
}

// 글 안의 서버 주소(IP)를 "서버" 로 가림
func hideHost(t string) string {
	h := getCfg().Host
	if h == "" || t == "" {
		return t
	}
	return strings.ReplaceAll(t, h, "서버")
}
