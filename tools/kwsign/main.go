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
	"sort"
	"strings"
	"time"
)

type Signed struct {
	V     int               `json:"v"`
	Time  string            `json:"time"`
	Files map[string]string `json:"files"`
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
	s := Signed{V: 1, Time: time.Now().Format(time.RFC3339), Files: map[string]string{}}
	add := func(sub string, onlyJar bool) {
		es, _ := os.ReadDir(filepath.Join(dir, filepath.FromSlash(sub)))
		for _, e := range es {
			n := e.Name()
			if e.IsDir() || strings.HasPrefix(n, "kw-signed.") || (onlyJar && !strings.HasSuffix(strings.ToLower(n), ".jar")) {
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
	fmt.Printf("서명했어요 (%d개 파일):\n", len(names))
	for _, n := range names {
		fmt.Println("  " + n)
	}
	fmt.Println("\n→ launcher\\kw-signed.json, kw-signed.sig 를 만들었어요. 서버를 다시 켤 필요는 없어요.")
	if len(args) == 0 || os.Getenv("KW_SIGN_NOPAUSE") == "" {
		pause()
	}
}
