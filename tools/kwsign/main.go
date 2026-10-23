// kwsign: Korea War 서명 도구 (서버장 컴퓨터에서만 씀)
// 서버의 plugins/KoreaWar 폴더 안 mod/*.jar, mod/optional/*.jar, launcher/* 의 sha256 목록을 만들어
// 비밀 열쇠(kw-sign-key.txt)로 서명 → launcher/kw-signed.json + launcher/kw-signed.sig.
// 런처는 이 서명이 맞는 파일만 설치함 (중간에서 바꿔치기하거나 서버가 해킹당해도 안 깔림).
//
// 쓰는 법: plugins\KoreaWar 폴더를 kwsign.exe 위로 끌어다 놓기 (또는 kwsign.exe "폴더 경로")
// 새 열쇠 만들기: kwsign.exe keygen   (열쇠를 바꾸면 런처도 새로 만들어야 함)
package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// v2 (런처 1.2.7~): serial(서명한 시각, 계속 커짐) 로 예전 목록 되돌리기 막기,
// launcherVersion·clientId·server·blockedMods 도 서명 안에 (서버 정보 바꿔치기 막기)
type Signed struct {
	V               int               `json:"v"`
	Serial          int64             `json:"serial"`
	Expires         int64             `json:"expires"` // 이 시각이 지나면 런처가 안 믿음 (120일, 다시 서명하면 연장)
	Time            string            `json:"time"`
	LauncherVersion string            `json:"launcherVersion"`
	ClientID        string            `json:"clientId"`
	Server          string            `json:"server"`
	BlockedMods     []string          `json:"blockedMods"`
	Files           map[string]string `json:"files"`
}

var launcherFile = regexp.MustCompile(`^(KoreaWarLauncher-[A-Za-z0-9.-]+|KoreaWarSetup(-arm64)?\.exe)$`)

var defaultBlocked = []string{"xaerominimap", "xaerominimapfair", "xaeroworldmap", "xaerolib", "journeymap", "voxelmap", "ftbchunks", "minimap", "mapfrontiers", "worldmap"}

// config.yml 에서 "섹션.키" 값 읽기 (두 단계까지, 글자 또는 목록)
func yamlGet(lines []string, section, key string) (string, []string, bool) {
	in := false
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		if strings.HasPrefix(l, section+":") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "#") {
			break
		}
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, key+":") || strings.HasPrefix(l, "    ") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, key+":"))
		if j := strings.Index(v, " #"); j >= 0 && !strings.HasPrefix(v, "\"") {
			v = strings.TrimSpace(v[:j])
		}
		if strings.HasPrefix(v, "[") {
			v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
			var out []string
			for _, x := range strings.Split(v, ",") {
				if x = strings.Trim(strings.TrimSpace(x), "\"'"); x != "" {
					out = append(out, x)
				}
			}
			return "", out, true
		}
		if v == "" { // 아래 줄에 - 목록
			var out []string
			for k := i + 1; k < len(lines); k++ {
				t2 := strings.TrimSpace(lines[k])
				if !strings.HasPrefix(t2, "- ") {
					break
				}
				out = append(out, strings.Trim(strings.TrimSpace(t2[2:]), "\"'"))
			}
			return "", out, true
		}
		if strings.HasPrefix(v, "\"") {
			if j := strings.Index(v[1:], "\""); j >= 0 {
				v = v[1 : j+1]
			}
		} else {
			v = strings.Trim(v, "'")
		}
		return v, nil, true
	}
	return "", nil, false
}

func exeDir() string {
	e, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(e); err == nil {
		e = r
	}
	return filepath.Dir(e)
}

func keyPath() string {
	if p := os.Getenv("KW_SIGN_KEY"); p != "" {
		return p
	}
	return filepath.Join(exeDir(), "kw-sign-key.txt")
}

func pause() {
	fmt.Println("\n엔터를 누르면 닫혀요.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func fail(f string, a ...any) {
	fmt.Printf("\n[실패] "+f+"\n", a...)
	pause()
	os.Exit(1)
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "keygen" {
		if _, err := os.Stat(keyPath()); err == nil {
			fail("이미 열쇠가 있어요: %s\n바꾸려면 그 파일을 먼저 다른 곳으로 옮기세요. (열쇠를 바꾸면 런처도 새로 만들어야 해요)", keyPath())
		}
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		if err := os.WriteFile(keyPath(), []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
			fail("열쇠를 저장하지 못했어요: %v", err)
		}
		fmt.Printf("새 열쇠를 만들었어요: %s\n확인용 열쇠(런처에 넣는 것): %s\n", keyPath(), base64.StdEncoding.EncodeToString(pub))
		return
	}
	kb, err := os.ReadFile(keyPath())
	if err != nil {
		fail("서명 열쇠가 없어요: %s\nkwsign.exe 와 같은 폴더에 kw-sign-key.txt 가 있어야 해요.", keyPath())
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(kb)))
	if err != nil || len(seed) != ed25519.SeedSize {
		fail("서명 열쇠 파일이 망가졌어요: %s", keyPath())
	}
	priv := ed25519.NewKeyFromSeed(seed)

	dir := ""
	if len(args) > 0 {
		dir = args[0]
	} else {
		fmt.Println("서버의 plugins\\KoreaWar 폴더 경로를 넣고 엔터 (폴더를 이 창에 끌어다 놓아도 돼요):")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		dir = strings.Trim(strings.TrimSpace(line), `"`)
	}
	dir = strings.Trim(dir, `"`)
	if st, err := os.Stat(filepath.Join(dir, "mod")); err != nil || !st.IsDir() {
		fail("%s 안에 mod 폴더가 없어요. 서버의 plugins\\KoreaWar 폴더를 골라 주세요.", dir)
	}
	cb, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		fail("%s 안에 config.yml 이 없어요. 서버의 plugins\\KoreaWar 폴더를 골라 주세요.", dir)
	}
	lines := strings.Split(string(cb), "\n")
	lv, _, _ := yamlGet(lines, "launcher", "version")
	if lv == "" {
		fail("config.yml 의 launcher.version 이 비어 있어요. 지금 런처 버전(예: \"1.2.7\")을 적고 다시 해 주세요.")
	}
	cid, _, _ := yamlGet(lines, "launcher", "client-id")
	srv, _, _ := yamlGet(lines, "launcher", "server-address")
	blocked := defaultBlocked
	if en, _, ok := yamlGet(lines, "minimap-block", "enabled"); ok && en == "false" {
		blocked = []string{}
	} else if _, ids, ok := yamlGet(lines, "minimap-block", "mod-ids"); ok {
		blocked = ids
	}
	now := time.Now()
	serial := now.Unix()
	if ob, err := os.ReadFile(filepath.Join(dir, "launcher", "kw-signed.json")); err == nil { // 시계가 뒤로 가도 번호는 항상 커지게
		var old Signed
		if json.Unmarshal(ob, &old) == nil && old.Serial >= serial {
			serial = old.Serial + 1
		}
	}
	s := Signed{V: 2, Serial: serial, Expires: now.AddDate(0, 0, 120).Unix(), Time: now.Format(time.RFC3339), LauncherVersion: lv, ClientID: cid, Server: srv, BlockedMods: blocked, Files: map[string]string{}}
	add := func(sub string, onlyJar bool) {
		es, _ := os.ReadDir(filepath.Join(dir, filepath.FromSlash(sub)))
		for _, e := range es {
			n := e.Name()
			if e.IsDir() || strings.HasPrefix(n, "kw-signed.") || (onlyJar && !strings.HasSuffix(strings.ToLower(n), ".jar")) {
				continue
			}
			if !onlyJar && !launcherFile.MatchString(n) { // 런처 파일만 (서버가 내려주는 것과 같은 규칙)
				continue
			}
			h, err := sha256File(filepath.Join(dir, filepath.FromSlash(sub), n))
			if err != nil {
				fail("%s/%s 를 읽지 못했어요: %v", sub, n, err)
			}
			s.Files[sub+"/"+n] = h
		}
	}
	add("mod", true)
	add("mod/optional", true)
	add("launcher", false)
	b, _ := json.MarshalIndent(s, "", "  ")
	sig := ed25519.Sign(priv, b)
	ld := filepath.Join(dir, "launcher")
	_ = os.MkdirAll(ld, 0o755)
	if err := os.WriteFile(filepath.Join(ld, "kw-signed.json"), b, 0o644); err != nil {
		fail("저장하지 못했어요: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ld, "kw-signed.sig"), []byte(base64.StdEncoding.EncodeToString(sig)), 0o644); err != nil {
		fail("저장하지 못했어요: %v", err)
	}
	names := make([]string, 0, len(s.Files))
	for k := range s.Files {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Printf("런처 버전 %s, 로그인 앱 ID %q, 접속 주소 %q, 막는 모드 %d개\n", lv, cid, srv, len(blocked))
	fmt.Printf("서명했어요 (%d개 파일):\n", len(names))
	for _, n := range names {
		fmt.Println("  " + n)
	}
	fmt.Println("\n→ launcher\\kw-signed.json, kw-signed.sig 를 만들었어요. 서버를 다시 켤 필요는 없어요.")
	fmt.Printf("이 서명은 %s 까지 유효해요. 그 전에 한 번 더 서명해 주세요 (서버 콘솔에도 미리 알려 줘요).\n", time.Unix(s.Expires, 0).Format("2006-01-02"))
	if len(args) == 0 || os.Getenv("KW_SIGN_NOPAUSE") == "" {
		pause()
	}
}
