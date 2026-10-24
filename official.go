package main

// 1.3.0 공식 마인크래프트 런처 연동.
// 모장이 로그인 앱을 허가해 주지 않아서, 로그인·게임 실행은 공식 런처가 하고 이 런처는 준비만 함:
//   - Fabric 버전 정보를 공식 런처 폴더(.minecraft/versions)에 넣음 (게임 파일·자바는 공식 런처가 받음)
//   - 보조·편의성 모드는 이 런처의 게임 폴더(game/mods)에 받음 (원래 마인크래프트 모드 폴더와 섞이지 않음)
//   - 공식 런처에 "Korea War" 설치 프로필을 만들고(게임 폴더·메모리 지정) 공식 런처를 열어 줌
//   - "서버로 바로 접속"은 게임 폴더의 koreawar-join.txt 를 보조 모드(1.2.5)가 읽어서 함

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var httpc = &http.Client{Timeout: 60 * time.Second}

const profileKey = "koreawar"

// 공식 런처 폴더 (.minecraft)
func mcDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, ".minecraft")
		}
		return filepath.Join(home, "AppData", "Roaming", ".minecraft")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "minecraft")
	}
	d := filepath.Join(home, ".minecraft")
	fp := filepath.Join(home, ".var", "app", "com.mojang.Minecraft", ".minecraft") // 플랫팩판
	if _, err := os.Stat(filepath.Join(d, "launcher_profiles.json")); err != nil {
		if _, err := os.Stat(filepath.Join(fp, "launcher_profiles.json")); err == nil {
			return fp
		}
	}
	return d
}

func fabricID(man *Manifest) string {
	return "fabric-loader-" + man.FabricLoader + "-" + man.Minecraft
}

// Fabric 버전 정보를 .minecraft/versions/<id>/ 에 (공식 런처가 이걸 보고 게임·라이브러리를 받음)
func installFabricVersion(man *Manifest) (string, error) {
	if man.FabricLoader == "" {
		return "", errors.New("서버가 Fabric 버전을 알려 주지 않았어요")
	}
	if !verRe.MatchString(man.FabricLoader) || !verRe.MatchString(man.Minecraft) {
		return "", errors.New("서버가 알려 준 버전 글자가 이상해요")
	}
	id := fabricID(man)
	dir := filepath.Join(mcDir(), "versions", id)
	js := filepath.Join(dir, id+".json")
	if _, err := os.Stat(js); err != nil {
		url := "https://meta.fabricmc.net/v2/versions/loader/" + man.Minecraft + "/" + man.FabricLoader + "/profile/json"
		tmp := dirs("fabric-" + id + ".json")
		if err := download(dlTask{URL: url, Path: tmp}); err != nil {
			return "", fmt.Errorf("Fabric 정보를 받지 못했어요: %v", err)
		}
		b, err := os.ReadFile(tmp)
		_ = os.Remove(tmp)
		var chk struct {
			ID string `json:"id"`
		}
		if err != nil || json.Unmarshal(b, &chk) != nil || chk.ID != id {
			return "", errors.New("Fabric 정보가 이상해요")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("마인크래프트 폴더에 쓰지 못했어요: %v", err)
		}
		if err := os.WriteFile(js, b, 0o644); err != nil {
			return "", fmt.Errorf("마인크래프트 폴더에 쓰지 못했어요: %v", err)
		}
	}
	jar := filepath.Join(dir, id+".jar") // Fabric 설치기처럼 빈 jar (예전 공식 런처가 찾음)
	if _, err := os.Stat(jar); err != nil {
		_ = os.WriteFile(jar, nil, 0o644)
	}
	return id, nil
}

func javaArgs() string {
	m := getCfg().MemoryMB
	return fmt.Sprintf("-Xmx%dM -XX:+UnlockExperimentalVMOptions -XX:+UseG1GC -XX:G1NewSizePercent=20 -XX:G1ReservePercent=20 -XX:MaxGCPauseMillis=50 -XX:G1HeapRegionSize=32M", m)
}

// 공식 런처 설치 프로필 파일들 (마이크로소프트 스토어판은 따로)
func profileFiles() []string {
	d := mcDir()
	var out []string
	for _, n := range []string{"launcher_profiles.json", "launcher_profiles_microsoft_store.json"} {
		p := filepath.Join(d, n)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	if len(out) == 0 { // 공식 런처를 아직 한 번도 안 켰음: 기본 파일을 만들어 둠
		out = append(out, filepath.Join(d, "launcher_profiles.json"))
	}
	return out
}

func writeProfile(versionID string) error {
	icon := ""
	if b, err := webFS.ReadFile("web/icon.png"); err == nil {
		icon = "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	var lastErr error
	wrote := 0
	for _, p := range profileFiles() {
		doc := map[string]any{}
		if b, err := os.ReadFile(p); err == nil {
			if err := json.Unmarshal(b, &doc); err != nil {
				lastErr = fmt.Errorf("공식 런처 설정 파일을 읽지 못했어요 (%s)", filepath.Base(p))
				continue
			}
			_ = os.WriteFile(p+".koreawar-backup", b, 0o644) // 혹시 몰라 한 벌 남김
		} else {
			doc["settings"] = map[string]any{}
			doc["version"] = 3
		}
		profiles, _ := doc["profiles"].(map[string]any)
		if profiles == nil {
			profiles = map[string]any{}
		}
		pr, _ := profiles[profileKey].(map[string]any)
		if pr == nil {
			pr = map[string]any{"created": now}
		}
		pr["name"] = "Korea War"
		pr["type"] = "custom"
		pr["lastUsed"] = now // 맨 위(처음 고른 것)로
		pr["lastVersionId"] = versionID
		pr["gameDir"] = gameDir()
		pr["javaArgs"] = javaArgs()
		if icon != "" {
			pr["icon"] = icon
		}
		profiles[profileKey] = pr
		doc["profiles"] = profiles
		b, _ := json.MarshalIndent(doc, "", "  ")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, b, 0o644); err != nil {
			lastErr = err
			continue
		}
		if err := os.Rename(tmp, p); err != nil {
			lastErr = err
			continue
		}
		wrote++
	}
	if wrote == 0 && lastErr != nil {
		return fmt.Errorf("공식 런처에 Korea War 프로필을 만들지 못했어요: %v", lastErr)
	}
	return nil
}

// 서버로 바로 접속: 보조 모드가 첫 화면에서 읽고 지움 (10분 안에 켠 게임만)
func writeAutoJoin() {
	p := filepath.Join(gameDir(), "koreawar-join.txt")
	if !getCfg().AutoJoin {
		_ = os.Remove(p)
		return
	}
	exp := time.Now().Add(10 * time.Minute).UnixMilli()
	_ = os.WriteFile(p, []byte(fmt.Sprintf("%s\n%d\n", serverAddr(), exp)), 0o644)
}

// 1.3.0 예전(직접 실행) 방식으로 받아 둔 게임 파일·로그인 정보 정리 (한 번만)
func cleanupOldInstall() {
	mark := dirs(".official-1.3.0")
	if _, err := os.Stat(mark); err == nil {
		return
	}
	for _, d := range []string{"runtime", "libraries", "assets", "versions", "natives"} {
		_ = os.RemoveAll(dirs(d))
	}
	_ = os.Remove(dirs("account.json")) // 이제 로그인 정보를 안 가지고 있음
	_ = os.WriteFile(mark, []byte(LauncherVersion), 0o644)
	logger.Printf("예전 방식 게임 파일·로그인 정보 정리함")
}

var (
	playMu     sync.Mutex
	handoffMu  sync.Mutex
	handoffAt  time.Time
	launcherOK bool
)

// 공식 런처에 넘긴 직후 잠깐은 "실행 중" 으로 보임 (두 번 누르기 막기)
func gameRunning() bool {
	handoffMu.Lock()
	defer handoffMu.Unlock()
	return launcherOK && time.Since(handoffAt) < 20*time.Second
}

// 게임 시작 버튼: 서버 정보 -> Fabric -> 보조 모드 -> 프로필 -> 공식 런처 열기
func play() {
	if !playMu.TryLock() {
		return
	}
	defer playMu.Unlock()
	fail := func(err error) {
		logger.Printf("실패: %v", err)
		statusMu.Lock()
		status = Status{Phase: "error", Text: "준비하지 못했어요", Error: err.Error()}
		statusMu.Unlock()
	}
	setStatus("prepare", "서버 정보 확인 중", 0)
	man, err := fetchManifest()
	if err != nil {
		fail(err)
		return
	}
	setStatus("prepare", "Fabric "+man.FabricLoader+" 준비 중", 0)
	id, err := installFabricVersion(man)
	if err != nil {
		fail(err)
		return
	}
	if err := syncMods(man); err != nil {
		if runtime.GOOS == "windows" && strings.Contains(err.Error(), "used by another process") {
			err = errors.New("게임이 켜져 있어서 모드를 바꾸지 못했어요. 게임을 끄고 다시 눌러 주세요.")
		}
		fail(err)
		return
	}
	writeGameDefaults()
	writeAutoJoin()
	setStatus("prepare", "공식 런처에 Korea War 프로필 만드는 중", 0)
	if err := writeProfile(id); err != nil {
		fail(err)
		return
	}
	ok := openOfficialLauncher()
	handoffMu.Lock()
	launcherOK, handoffAt = ok, time.Now()
	handoffMu.Unlock()
	if !ok {
		statusMu.Lock()
		status = Status{Phase: "error", Text: "준비는 끝났어요", Error: "공식 마인크래프트 런처를 찾지 못했어요. minecraft.net 에서 설치하고 로그인한 뒤, 설치 프로필에서 'Korea War' 를 골라 플레이하세요."}
		statusMu.Unlock()
		return
	}
	msg := "공식 런처를 열었어요. 'Korea War' 를 골라 플레이를 누르세요."
	if len(lastBlocked) > 0 {
		msg += " (미니맵·지도 모드는 이 서버에서 못 써서 mods-blocked 폴더로 뺐어요: " + strings.Join(lastBlocked, ", ") + ")"
	} else if len(lastUnsigned) > 0 {
		msg += " (서명이 없어 안 받은 편의성 모드: " + strings.Join(lastUnsigned, ", ") + ")"
	}
	setStatus("done", msg, 0)
	logger.Printf("공식 런처로 넘김 (프로필 %s, 버전 %s)", profileKey, id)
}
