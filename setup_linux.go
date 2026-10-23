//go:build linux

package main

// 리눅스 설치: 처음 켜면 ~/.local/share/koreawar-launcher/app 으로 복사하고
// 앱 메뉴(~/.local/share/applications)와 바탕화면에 아이콘을 만든 뒤 설치된 런처를 켬.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func linuxInstallDir() string { return filepath.Join(dataDir, "app") }

func desktopDir() string {
	if out, err := exec.Command("xdg-user-dir", "DESKTOP").Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			if home, _ := os.UserHomeDir(); d != home {
				return d
			}
		}
	}
	home, _ := os.UserHomeDir()
	for _, n := range []string{"Desktop", "바탕화면", "바탕 화면"} {
		if st, err := os.Stat(filepath.Join(home, n)); err == nil && st.IsDir() {
			return filepath.Join(home, n)
		}
	}
	return ""
}

func writeDesktopFiles(target string) {
	icon := filepath.Join(filepath.Dir(target), "icon.png")
	if b, err := webFS.ReadFile("web/icon.png"); err == nil {
		_ = os.WriteFile(icon, b, 0o644)
	}
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Korea War 런처\nComment=Korea War 서버 전용 런처\nExec=\"%s\"\nIcon=%s\nTerminal=false\nCategories=Game;\nStartupWMClass=KoreaWarLauncher\n", target, icon)
	home, _ := os.UserHomeDir()
	apps := filepath.Join(home, ".local", "share", "applications")
	_ = os.MkdirAll(apps, 0o755)
	_ = os.WriteFile(filepath.Join(apps, "koreawar-launcher.desktop"), []byte(entry), 0o755)
	if d := desktopDir(); d != "" {
		p := filepath.Join(d, "koreawar-launcher.desktop")
		_ = os.WriteFile(p, []byte(entry), 0o755)
		_ = exec.Command("gio", "set", p, "metadata::trusted", "true").Run() // 그놈: "실행 허용"
	}
}

func maybeInstall(args []string) bool {
	if skipInstall(args) {
		return false
	}
	exe, target := selfExe(), filepath.Join(linuxInstallDir(), "KoreaWarLauncher")
	if filepath.Clean(exe) == filepath.Clean(target) {
		return false
	}
	_ = os.MkdirAll(linuxInstallDir(), 0o755)
	if err := copyExe(exe, target); err != nil {
		logger.Printf("설치 실패: %v (그 자리에서 그냥 켬)", err)
		return false
	}
	writeDesktopFiles(target)
	logger.Printf("설치함: %s", target)
	if err := startDetached(target, "--installed"); err != nil {
		logger.Printf("설치된 런처를 켜지 못함: %v", err)
		return false
	}
	return true
}
