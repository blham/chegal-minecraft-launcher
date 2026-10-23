package main

// 서명 확인 (1.2.6~, v2 는 1.2.7~): 서버의 launcher/kw-signed.json 이 서버장 비밀 열쇠로 서명된 것(kw-signed.sig)인지
// 아래 확인용 열쇠로 보고, 맞으면 그 안의 것만 믿음:
//   files(파일별 sha256) · launcherVersion(업데이트할 버전) · clientId · server · blockedMods
//   serial(서명한 시각): 본 적 있는 것보다 작으면 거부 → 예전 목록·예전 런처로 되돌리는 공격 막기
// 서명 안 된 manifest.json 의 같은 값들은 쓰지 않음.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const signPub = "9QaY6jmmp+5wo2+Wm+/ybKpqDcshIb3fACyc1sLHMmE="

var verRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$`)

type SignedDoc struct {
	V               int               `json:"v"`
	Serial          int64             `json:"serial"`
	Expires         int64             `json:"expires"`
	LauncherVersion string            `json:"launcherVersion"`
	ClientID        string            `json:"clientId"`
	Server          string            `json:"server"`
	BlockedMods     []string          `json:"blockedMods"`
	Files           map[string]string `json:"files"`
}

var (
	lastUnsigned []string // 서명이 없어 건너뛴 편의성 모드 (화면에 알림)
	signedMu     sync.Mutex
	signedCache  *SignedDoc
	signedAt     time.Time
)

func getBytes(url string, max int64) ([]byte, error) {
	resp, err := httpc.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

func serialPath() string { return filepath.Join(dataDir, "signed-serial") }

// 빌드할 때 넣는 가장 낮은 서명 번호 (-ldflags "-X main.minSerial=...") → 처음 설치한 런처도 그보다 예전 목록은 거부
var minSerial = "0"

func seenSerial() int64 {
	b, _ := os.ReadFile(serialPath())
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if m, _ := strconv.ParseInt(minSerial, 10, 64); m > n {
		n = m
	}
	return n
}

func fetchSignedDoc() (*SignedDoc, error) {
	signedMu.Lock()
	defer signedMu.Unlock()
	if signedCache != nil && time.Since(signedAt) < 20*time.Second {
		return signedCache, nil
	}
	bad := errors.New("서버 파일의 서명을 확인하지 못했어요. 서버장이 kwsign 으로 서명해야 해요 (그 전엔 안전을 위해 모드·업데이트를 받지 않아요)")
	b, err := getBytes(filesBase()+"/launcher/kw-signed.json", 4<<20)
	if err != nil {
		logger.Printf("서명 목록 못 받음: %v", err)
		if !strings.Contains(err.Error(), "HTTP 404") {
			return nil, errors.New("서버에 연결하지 못했어요. 서버가 꺼져 있거나 인터넷 연결을 확인해 주세요")
		}
		return nil, bad
	}
	sb, err := getBytes(filesBase()+"/launcher/kw-signed.sig", 4096)
	if err != nil {
		logger.Printf("서명 못 받음: %v", err)
		return nil, bad
	}
	sig, err1 := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sb)))
	pub, err2 := base64.StdEncoding.DecodeString(signPub)
	if err1 != nil || err2 != nil || !ed25519.Verify(ed25519.PublicKey(pub), b, sig) {
		logger.Printf("서명이 맞지 않음")
		return nil, errors.New("서버 파일의 서명이 맞지 않아요! 누군가 파일을 바꿔치기했을 수 있어서 받지 않았어요. 서버장에게 알려 주세요")
	}
	var d SignedDoc
	if err := json.Unmarshal(b, &d); err != nil || d.Files == nil {
		return nil, bad
	}
	if d.V < 2 || d.Serial <= 0 {
		return nil, errors.New("서버의 서명이 예전 형식이에요. 서버장이 새 kwsign 으로 다시 서명해야 해요")
	}
	if d.Expires <= 0 || time.Now().Unix() > d.Expires { // 기한 지난 목록은 안 믿음 (예전 목록을 계속 보내 묶어 두는 공격 막기)
		logger.Printf("서명 기한 지남: %d", d.Expires)
		return nil, errors.New("서버 파일의 서명 기한이 지났어요. 서버장이 kwsign 으로 다시 서명해야 해요")
	}
	if seen := seenSerial(); d.Serial < seen {
		logger.Printf("예전 서명 목록 거부: %d < %d", d.Serial, seen)
		return nil, errors.New("서버가 예전 서명 목록을 보냈어요. 예전 파일로 되돌리는 공격일 수 있어서 받지 않았어요. 서버장에게 알려 주세요")
	} else if d.Serial > seen {
		_ = os.WriteFile(serialPath(), []byte(strconv.FormatInt(d.Serial, 10)), 0o600)
	}
	if d.LauncherVersion != "" && !verRe.MatchString(d.LauncherVersion) {
		return nil, bad
	}
	signedCache, signedAt = &d, time.Now()
	return &d, nil
}

func fetchSigned() (map[string]string, error) {
	d, err := fetchSignedDoc()
	if err != nil {
		return nil, err
	}
	return d.Files, nil
}

// 서명 안 된 manifest 의 보안 값을 서명된 값으로 바꿈 (서명이 없으면 비움 → 그 기능은 안 함)
func applySigned(m *Manifest) {
	d, err := fetchSignedDoc()
	m.Launcher, m.ClientID, m.Server, m.BlockedMods = "", "", "", nil
	if err != nil {
		m.Mods = nil
		return
	}
	m.Launcher, m.ClientID, m.Server, m.BlockedMods = d.LauncherVersion, d.ClientID, d.Server, d.BlockedMods
	size := map[string]int64{}
	for _, f := range m.Mods {
		size[f.File] = f.Size
	}
	m.Mods = nil // 필수 모드 = 서명 목록의 mod/<파일>
	for k := range d.Files {
		if f, ok := strings.CutPrefix(k, "mod/"); ok && !strings.Contains(f, "/") && strings.HasSuffix(strings.ToLower(f), ".jar") {
			m.Mods = append(m.Mods, ModFile{File: f, Size: size[f]})
		}
	}
}
