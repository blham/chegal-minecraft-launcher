// KoreaWar 전용 런처: 마이크로소프트 로그인 -> 마인크래프트 26.3 + Fabric + 보조 모드를 받아(업데이트) -> 서버로 바로 접속해 켬.
// 화면은 기본 웹 브라우저에 띄움 (http://localhost:25585). 표준 라이브러리만 씀 (윈도우·맥·리눅스 한 소스).
package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 빌드할 때 -ldflags "-X main.defaultHost=... -X main.defaultClientID=..." 로 바꿀 수 있음
var (
	LauncherVersion  = "1.2.7"
	defaultHost      = "" // 빌드할 때 -ldflags "-X main.defaultHost=서버주소" 로 넣음 (소스에 IP 를 안 남기려고)
	defaultGamePort  = "25565"
	defaultFilesPort = "8163"
	defaultClientID  = ""
)

var devOffline string

// 창을 닫으면: 게임·준비 중·로그인 중이 아니면 끝냄 (게임 중이면 게임이 끝날 때)
// 1.1.0 윈도우 Edge 는 앱 창을 띄운 뒤 처음 프로세스를 바로 끝내기도 함 → 프로세스가 끝났다고 바로 끄지 않고,
// 화면이 아직 상태를 묻고 있는지(살아 있는지) 몇 초 보고 판단
func windowClosed() {
	time.Sleep(8 * time.Second)
	pollMu.Lock()
	alive := time.Since(lastPoll) < 6*time.Second
	pollMu.Unlock()
	if alive {
		logger.Printf("창 프로세스는 끝났지만 화면은 살아 있음 (그대로 둠)")
		return
	}
	if !gameRunning() && !busy() && !loginPending() {
		logger.Printf("창을 닫아 끝냄")
		os.Exit(0)
	}
}

const uiPort = 25585 // Azure 앱 리디렉션 URI: http://localhost:25585/auth

//go:embed web
var webFS embed.FS

type Config struct {
	Host      string          `json:"host"`
	GamePort  int             `json:"gamePort"`
	FilesPort int             `json:"filesPort"`
	MemoryMB  int             `json:"memoryMB"`
	ClientID  string          `json:"clientId,omitempty"` // 비우면 서버가 알려 준 것
	AutoJoin  bool            `json:"autoJoin"`
	Mods      map[string]bool `json:"mods,omitempty"`     // 편의성 모드 켬/끔 (모드 id)
	Exported  string          `json:"exported,omitempty"` // 다른 런처용으로 마지막에 받아 간 보조 모드 버전
}

var (
	dataDir  string
	cfg      Config
	cfgMu    sync.Mutex
	logger   *log.Logger
	apiToken string
	lastPoll = time.Now()
	pollMu   sync.Mutex
)

func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, "KoreaWarLauncher")
		}
		return filepath.Join(home, "AppData", "Roaming", "KoreaWarLauncher")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "KoreaWarLauncher")
	default:
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			return filepath.Join(x, "koreawar-launcher")
		}
		return filepath.Join(home, ".local", "share", "koreawar-launcher")
	}
}

func atoi(s string, d int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return d
	}
	return n
}

func loadConfig() {
	cfg = Config{Host: defaultHost, GamePort: atoi(defaultGamePort, 25565), FilesPort: atoi(defaultFilesPort, 8163), MemoryMB: 4096, AutoJoin: true}
	b, err := os.ReadFile(filepath.Join(dataDir, "launcher.json"))
	if err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	if cfg.Host == "" {
		cfg.Host = defaultHost
	}
	if cfg.MemoryMB < 1024 {
		cfg.MemoryMB = 4096
	}
}

func saveConfig() {
	cfgMu.Lock()
	b, _ := json.MarshalIndent(cfg, "", "  ")
	cfgMu.Unlock()
	_ = os.WriteFile(filepath.Join(dataDir, "launcher.json"), b, 0o644)
}

func getCfg() Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return cfg
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		c = exec.Command("open", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	hideWindow(c)
	if err := c.Start(); err != nil {
		logger.Printf("브라우저를 열지 못함: %v (직접 %s 를 여세요)", err, url)
	}
}

func main() {
	dataDir = defaultDataDir()
	if d := os.Getenv("KW_LAUNCHER_DIR"); d != "" {
		dataDir = d
	}
	_ = os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755)
	// 1.1.1 이어 쓰기 (두 번째로 켠 런처가 첫 런처 기록을 지우지 않게), 2MB 넘으면 새로
	lp := filepath.Join(dataDir, "logs", "launcher.log")
	if st, err := os.Stat(lp); err == nil && st.Size() > 2<<20 {
		_ = os.Rename(lp, lp+".old")
	}
	lf, err := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		logger = log.New(lf, "", log.LstdFlags)
	} else {
		logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	logger.Printf("KoreaWar 런처 %s (%s/%s) 자료 폴더 %s", LauncherVersion, runtime.GOOS, runtime.GOARCH, dataDir)
	loadConfig()
	if maybeInstall(os.Args[1:]) { // 윈도우: 설치 안 된 자리에서 켜면 설치하고 설치된 런처를 켬
		return
	}
	updated := false
	for _, a := range os.Args[1:] {
		if a == "--updated" {
			updated = true
		}
	}
	go cleanOldExe()
	if updated {
		go afterUpdate()
	}
	loadAccount()
	if devOffline != "" {
		account = &Account{Name: devOffline, UUID: "00000000000040008000000000000001", McToken: "0", McExpires: 1 << 40, Xuid: "0", ClientID: "dev"}
	}

	ui := fmt.Sprintf("http://localhost:%d/", uiPort)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", uiPort))
	for i := 0; err != nil && updated && i < 30; i++ { // 업데이트 직후: 예전 런처가 포트를 놓을 때까지
		time.Sleep(300 * time.Millisecond)
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", uiPort))
	}
	if err != nil { // 이미 켜져 있음: 그 창만 다시 열기
		logger.Printf("이미 켜져 있음: %v", err)
		req, _ := http.NewRequest("POST", ui+"api/show", nil) // 이미 켜진 런처에게 창을 다시 열라고 (그 런처의 토큰으로)
		req.Header.Set("X-KW-Token", runningToken())
		if resp, err := httpc.Do(req); err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
		} else {
			openBrowser(ui)
		}
		return
	}
	tb := make([]byte, 16)
	_, _ = rand.Read(tb)
	apiToken = hex.EncodeToString(tb)
	_ = os.WriteFile(filepath.Join(dataDir, "api-token"), []byte(apiToken), 0o600) // 1.2.6 설치·제거 도구가 이 런처를 끌 때 씀 (본인만 읽힘)

	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	static := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			b, _ := webFS.ReadFile("web/index.html")
			nb := make([]byte, 16)
			_, _ = rand.Read(nb)
			nonce := hex.EncodeToString(nb)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			// 1.2.7 화면 보안 규칙: 이 화면이 직접 넣은 스크립트(nonce)만 실행, 외부로 요청·이동 못 함
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'nonce-"+nonce+"'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https://mc-heads.net; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "no-referrer")
			html := strings.Replace(string(b), "__TOKEN__", apiToken, 1)
			html = strings.Replace(html, "__NONCE__", nonce, 1)
			_, _ = w.Write([]byte(html))
			return
		}
		static.ServeHTTP(w, r)
	})
	mux.HandleFunc("/auth", handleAuthCallback)
	registerAPI(mux)
	srv := &http.Server{Handler: hostGuard(mux), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	restartHook = func() { _ = srv.Close() }
	go serverWatcher()
	if updated { // 예전 창이 그대로 붙어 있으면 새 창을 안 띄움
		go func() {
			time.Sleep(10 * time.Second)
			pollMu.Lock()
			alive := time.Since(lastPoll) < 6*time.Second
			pollMu.Unlock()
			if !alive {
				openWindow(ui)
			}
		}()
		pollMu.Lock()
		lastPoll = time.Now().Add(-7 * time.Second) // "창 안 붙음" 으로 시작 (3분 꺼짐 타이머엔 안 걸리게)
		pollMu.Unlock()
	} else {
		openWindow(ui)
	}
	go func() { // 1.2.4 켤 때 한 번 + 켜 둔 동안 10분마다 (서버가 새 런처를 올리면 게임 중이 아닐 때 바로 바뀜)
		_, _ = fetchManifest() // 로그인 앱 ID·접속 주소 등 (서명된 값) 미리
		checkUpdate()
		for {
			time.Sleep(10 * time.Minute)
			if !gameRunning() && !busy() {
				_, _ = fetchManifest()
				checkUpdate()
			}
		}
	}()

	// 창(브라우저 탭)을 닫고 게임도 안 켜져 있으면 잠시 뒤 스스로 끝남
	for {
		time.Sleep(5 * time.Second)
		pollMu.Lock()
		idle := time.Since(lastPoll)
		pollMu.Unlock()
		if idle > 3*time.Minute && !gameRunning() && !busy() && !loginPending() {
			logger.Printf("창이 닫혀 끝냄")
			return
		}
	}
}

// 1.2.6 DNS 리바인딩 막기: 주소창이 localhost / 127.0.0.1 인 요청만 받음
// (악성 사이트가 자기 도메인을 127.0.0.1 로 돌려서 런처 정보를 읽어 가는 것 방지)
func hostGuard(h http.Handler) http.Handler {
	ok := map[string]bool{
		fmt.Sprintf("localhost:%d", uiPort): true,
		fmt.Sprintf("127.0.0.1:%d", uiPort): true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ok[strings.ToLower(r.Host)] {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// 켜져 있는 런처의 토큰 (같은 사용자만 읽힘)
func runningToken() string {
	b, _ := os.ReadFile(filepath.Join(dataDir, "api-token"))
	return strings.TrimSpace(string(b))
}
