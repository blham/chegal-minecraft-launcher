package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// 공식 런처 열기: 마이크로소프트 스토어/새 설치판(시작 메뉴 앱) → 예전 설치판 exe
func openOfficialLauncher() bool {
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"$a=Get-StartApps|?{$_.AppID -like 'Microsoft.4297127D64EC6_*!Minecraft'}|select -First 1;"+
			"if(!$a){$a=Get-StartApps|?{$_.Name -eq 'Minecraft Launcher'}|select -First 1};if($a){$a.AppID}")
	hideWindow(c)
	if out, err := c.Output(); err == nil {
		id := strings.TrimSpace(string(out))
		if id != "" && !strings.ContainsAny(id, "\"&|<>^ ") {
			e := exec.Command("explorer.exe", `shell:AppsFolder\`+id)
			if e.Start() == nil {
				logger.Printf("공식 런처 열기: %s", id)
				return true
			}
		}
	}
	var cands []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		if b := os.Getenv(env); b != "" {
			cands = append(cands, filepath.Join(b, "Minecraft Launcher", "MinecraftLauncher.exe"),
				filepath.Join(b, "Programs", "Minecraft Launcher", "MinecraftLauncher.exe"))
		}
	}
	for _, p := range cands {
		if _, err := os.Stat(p); err == nil {
			cmd := exec.Command(p)
			detach(cmd)
			if cmd.Start() == nil {
				logger.Printf("공식 런처 열기: %s", p)
				return true
			}
		}
	}
	return false
}
