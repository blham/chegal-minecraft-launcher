//go:build windows

package main

// 설치형 (윈도우): 받은 exe(KoreaWarSetup.exe 등)를 켜면 %LOCALAPPDATA%\Programs\KoreaWarLauncher 에 복사하고
// 바탕화면·시작 메뉴 바로가기 + "앱 및 기능" 제거 항목을 만든 뒤, 설치된 런처를 켬.
// 설치된 런처는 켤 때마다 서버에서 새 버전을 확인해 스스로 바꿈 (update.go).

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const appName = "Korea War 런처"

func installDir() string {
	la := os.Getenv("LOCALAPPDATA")
	if la == "" {
		home, _ := os.UserHomeDir()
		la = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(la, "Programs", "KoreaWarLauncher")
}

func installedExe() string { return filepath.Join(installDir(), "KoreaWarLauncher.exe") }

func selfExe() string {
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe
}

func msgBox(text string, flags uint32) int {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(appName)
	r, _, _ := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), uintptr(flags))
	return int(r)
}

// 파워셸 스크립트 실행 (한글·따옴표 걱정 없게 -EncodedCommand)
func powershell(script string) error {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[2*i], b[2*i+1] = byte(v), byte(v>>8)
	}
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	hideWindow(c)
	out, err := c.CombinedOutput()
	if err != nil {
		logger.Printf("powershell 실패: %v %s", err, out)
	}
	return err
}

func psq(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// 바로가기 + 제거 항목
func writeShortcuts(target string) {
	dir := filepath.Dir(target)
	_ = powershell(fmt.Sprintf(`
$ws = New-Object -ComObject WScript.Shell
foreach ($d in @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'))) {
  $l = $ws.CreateShortcut((Join-Path $d %s))
  $l.TargetPath = %s; $l.WorkingDirectory = %s; $l.IconLocation = %s; $l.Description = %s
  $l.Save()
}`, psq(appName+".lnk"), psq(target), psq(dir), psq(target+",0"), psq("Korea War 서버 전용 런처")))
	registerUninstall(target)
}

func registerUninstall(target string) {
	var size int64
	if st, err := os.Stat(target); err == nil {
		size = st.Size() / 1024
	}
	_ = powershell(fmt.Sprintf(`
$k = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\KoreaWarLauncher'
New-Item -Path $k -Force | Out-Null
Set-ItemProperty $k DisplayName %s
Set-ItemProperty $k DisplayIcon %s
Set-ItemProperty $k DisplayVersion %s
Set-ItemProperty $k Publisher 'Korea War'
Set-ItemProperty $k InstallLocation %s
Set-ItemProperty $k UninstallString %s
New-ItemProperty $k EstimatedSize -Value %d -PropertyType DWord -Force | Out-Null
New-ItemProperty $k NoModify -Value 1 -PropertyType DWord -Force | Out-Null
New-ItemProperty $k NoRepair -Value 1 -PropertyType DWord -Force | Out-Null`,
		psq(appName), psq(target+",0"), psq(LauncherVersion), psq(filepath.Dir(target)), psq(`"`+target+`" --uninstall`), size))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	out.Close()
	_ = os.Remove(dst + ".old")
	_ = os.Rename(dst, dst+".old") // 켜져 있는 예전 런처도 이름은 바꿀 수 있음
	return os.Rename(tmp, dst)
}

// 설치된 자리가 아니면 설치하고 설치된 런처를 켬. true = 이 프로세스는 끝내야 함
func maybeInstall(args []string) bool {
	for _, a := range args {
		switch a {
		case "--uninstall":
			uninstall()
			return true
		case "--portable":
			return false
		}
	}
	exe, target := selfExe(), installedExe()
	if strings.EqualFold(filepath.Clean(exe), filepath.Clean(target)) {
		return false
	}
	_ = os.MkdirAll(installDir(), 0o755)
	if err := copyFile(exe, target); err != nil {
		msgBox("설치하지 못했어요: "+err.Error()+"\n\n런처가 켜져 있다면 끄고 다시 해 주세요. (작업 관리자에서 KoreaWarLauncher 끝내기)", 0x10)
		return true
	}
	writeShortcuts(target)
	logger.Printf("설치함: %s", target)
	stopRunningLauncher()
	c := exec.Command(target, "--installed")
	c.Dir = installDir()
	detach(c)
	if err := c.Start(); err != nil {
		msgBox("설치는 됐지만 켜지 못했어요. 바탕화면의 \""+appName+"\" 를 눌러 주세요.", 0x30)
	}
	return true
}

func uninstall() {
	if msgBox(appName+"를 이 컴퓨터에서 지울까요?", 0x4|0x20) != 6 {
		return
	}
	data := msgBox("게임 자료(로그인 정보·마인크래프트·모드·스크린샷)도 같이 지울까요?\n\n[아니요]를 누르면 다시 설치했을 때 그대로 쓸 수 있어요.", 0x4|0x20|0x100) == 6
	// 켜져 있는 런처 끝내기 (창이 떠 있으면)
	_, _ = httpc.Post(fmt.Sprintf("http://127.0.0.1:%d/api/quit-uninstall", uiPort), "text/plain", nil)
	_ = powershell(fmt.Sprintf(`
foreach ($d in @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'))) { Remove-Item -LiteralPath (Join-Path $d %s) -Force -ErrorAction SilentlyContinue }
Remove-Item -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\KoreaWarLauncher' -Recurse -Force -ErrorAction SilentlyContinue`, psq(appName+".lnk")))
	del := `ping 127.0.0.1 -n 3 > nul & rmdir /s /q "` + installDir() + `"`
	if data {
		del += ` & rmdir /s /q "` + dataDir + `"`
	}
	c := exec.Command("cmd", "/c", del)
	hideWindow(c)
	detach(c)
	_ = c.Start()
	time.Sleep(200 * time.Millisecond)
	msgBox(appName+"를 지웠어요.", 0x40)
}

// 설치된 런처가 새 버전으로 바뀐 뒤: 제거 항목의 버전 갱신
func afterUpdate() { registerUninstall(selfExe()) }

// 1.2.4 예전 런처가 켜져 있으면(게임 중이 아니면) 끄고 포트가 빌 때까지 기다림 → 새로 설치한 런처가 제대로 켜지게
func stopRunningLauncher() {
	addr := fmt.Sprintf("127.0.0.1:%d", uiPort)
	if c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err != nil {
		return
	} else {
		c.Close()
	}
	logger.Printf("켜져 있는 예전 런처를 끔")
	if r, err := httpc.Post("http://"+addr+"/api/quit-uninstall", "text/plain", nil); err == nil {
		r.Body.Close()
	}
	for i := 0; i < 25; i++ {
		time.Sleep(200 * time.Millisecond)
		c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err != nil {
			return
		}
		c.Close()
	}
}
