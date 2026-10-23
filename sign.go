package main

// 1.2.6 서명 확인: 서버의 launcher/kw-signed.json 을 서버장의 비밀 열쇠로 서명한 것(kw-signed.sig)이
// 아래 확인용 열쇠로 맞는지 보고, 맞으면 그 안의 sha256 목록에 있는 파일만 설치함.
// 중간에서 바꿔치기하거나 서버(8163)가 해킹당해도 서명이 안 맞아 안 깔림. 서명은 서버장 컴퓨터의 kwsign 으로.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const signPub = "9QaY6jmmp+5wo2+Wm+/ybKpqDcshIb3fACyc1sLHMmE="

var lastUnsigned []string // 서명이 없어 건너뛴 편의성 모드 (화면에 알림)

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

func fetchSigned() (map[string]string, error) {
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
	var s struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(b, &s); err != nil || s.Files == nil {
		return nil, bad
	}
	return s.Files, nil
}
