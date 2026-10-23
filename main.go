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
	LauncherVersion  = "1.0.3"
	defaultHost      = "210.100.163.165"
	defaultGamePort  = "25565"
	defaultFilesPort = "8163"
	defaultClientID  = ""
)

var devOffline string

// 창을 닫으면: 게임·준비 중이 아니면 끝냄 (게임 중이면 게임이 끝날 때)
func windowClosed() {
	if !gameRunning() && !busy() {
		logger.Printf("창을 닫아 끝냄")
		os.Exit(0)
	}
}

const uiPort = 25585 // Azure 앱 리디렉션 URI: http://localhost:25585/auth

//go:embed web
var webFS embed.FS

type Config struct {
	Host      string `json:"host"`
	GamePort  int    `json:"gamePort"`
	FilesPort int    `json:"filesPort"`
	MemoryMB  int    `json:"memoryMB"`
	ClientID  string `json:"clientId,omitempty"` // 비우면 서버가 알려 준 것
	AutoJoin  bool   `json:"autoJoin"`
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
	lf, err := os.OpenFile(filepath.Join(dataDir, "logs", "launcher.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err == nil {
		logger = log.New(lf, "", log.LstdFlags)
	} else {
		logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	logger.Printf("KoreaWar 런처 %s (%s/%s) 자료 폴더 %s", LauncherVersion, runtime.GOOS, runtime.GOARCH, dataDir)
	loadConfig()
	loadAccount()
	if devOffline != "" {
		account = &Account{Name: devOffline, UUID: "00000000000040008000000000000001", McToken: "0", McExpires: 1 << 40, Xuid: "0", ClientID: "dev"}
	}

	ui := fmt.Sprintf("http://localhost:%d/", uiPort)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", uiPort))
	if err != nil { // 이미 켜져 있음: 그 창만 다시 열기
		logger.Printf("이미 켜져 있음: %v", err)
		if resp, err := httpc.Get(ui + "api/show"); err == nil { // 이미 켜진 런처에게 창을 다시 열라고
			resp.Body.Close()
		} else {
			openBrowser(ui)
		}
		return
	}
	tb := make([]byte, 16)
	_, _ = rand.Read(tb)
	apiToken = hex.EncodeToString(tb)

	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	static := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			b, _ := webFS.ReadFile("web/index.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(strings.Replace(string(b), "__TOKEN__", apiToken, 1)))
			return
		}
		static.ServeHTTP(w, r)
	})
	mux.HandleFunc("/auth", handleAuthCallback)
	registerAPI(mux)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	go serverWatcher()
	openWindow(ui)

	// 창(브라우저 탭)을 닫고 게임도 안 켜져 있으면 잠시 뒤 스스로 끝남
	for {
		time.Sleep(5 * time.Second)
		pollMu.Lock()
		idle := time.Since(lastPoll)
		pollMu.Unlock()
		if idle > 3*time.Minute && !gameRunning() && !busy() {
			logger.Printf("창이 닫혀 끝냄")
			return
		}
	}
}
