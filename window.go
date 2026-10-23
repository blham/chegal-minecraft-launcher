package main

// 1.0.2 전용 창: 크롬 계열 브라우저(엣지·크롬·브레이브·크로미움)의 "앱 창" 모드로 주소창·탭 없는 창을 띄움.
// 창 전용 프로필을 따로 써서 창을 닫으면 그 프로세스가 끝남 -> 게임도 안 켜져 있으면 런처도 끝남. 없으면 기본 브라우저로.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

var (
	winMu   sync.Mutex
	winOpen bool
	usedApp bool
)

func appBrowsers() []string {
	var c []string
	switch runtime.GOOS {
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LOCALAPPDATA")} {
			if base == "" {
				continue
			}
			c = append(c, filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(base, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"))
		}
	case "darwin":
		home, _ := os.UserHomeDir()
		for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
			c = append(c, filepath.Join(root, "Google Chrome.app", "Contents", "MacOS", "Google Chrome"),
				filepath.Join(root, "Microsoft Edge.app", "Contents", "MacOS", "Microsoft Edge"),
				filepath.Join(root, "Brave Browser.app", "Contents", "MacOS", "Brave Browser"),
				filepath.Join(root, "Chromium.app", "Contents", "MacOS", "Chromium"))
		}
	default:
		for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"} {
			if p, err := exec.LookPath(n); err == nil {
				c = append(c, p)
			}
		}
	}
	var out []string
	for _, p := range c {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// 런처 창 열기 (이미 열려 있으면 아무것도 안 함)
func openWindow(url string) {
	// 1.1.1 "열려 있다"고 알고 있어도 화면이 상태를 안 물은 지 6초가 넘었으면 실제로는 안 보이는 것 → 새로 띄움
	pollMu.Lock()
	alive := time.Since(lastPoll) < 6*time.Second
	pollMu.Unlock()
	winMu.Lock()
	known := winOpen || usedApp
	winMu.Unlock()
	if known && alive {
		return
	}
	for _, b := range appBrowsers() {
		cmd := exec.Command(b, "--app="+url, "--window-size=1180,800", "--user-data-dir="+filepath.Join(dataDir, "window"),
			"--no-first-run", "--no-default-browser-check", "--disable-features=Translate")
		hideWindow(cmd)
		if err := cmd.Start(); err != nil {
			continue
		}
		logger.Printf("창: %s", b)
		winMu.Lock()
		winOpen = true
		usedApp = true
		winMu.Unlock()
		go func() {
			_ = cmd.Wait()
			winMu.Lock()
			winOpen = false
			winMu.Unlock()
			logger.Printf("창 닫힘")
			windowClosed()
		}()
		return
	}
	openBrowser(url) // 크롬 계열이 없으면 기본 브라우저 탭
}

// 게임이 끝났을 때: 창(앱 창)도 닫혀 있으면 런처도 끝냄
func gameEnded() {
	winMu.Lock()
	o, a := winOpen, usedApp
	winMu.Unlock()
	pollMu.Lock()
	alive := time.Since(lastPoll) < 6*time.Second // 창 프로세스가 일찍 끝났어도 화면이 살아 있으면 열린 것
	pollMu.Unlock()
	if a && !o && !alive {
		logger.Printf("게임 끝, 창도 닫혀 있어 끝냄")
		os.Exit(0)
	}
}
