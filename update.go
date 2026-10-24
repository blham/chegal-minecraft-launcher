package main

// 자동 업데이트: 켤 때마다 서버(manifest 의 launcher 버전)와 비교해 새 버전이면 받아서(sha1 확인) 실행 파일을 바꾸고 다시 켬.
// 서버장은 plugins/KoreaWar/launcher/ 에 플랫폼별 런처 파일을 넣어 두면 됨 (이름은 updateAssetName).

import (
	"os"
	"os/exec"
	"runtime"
	"time"
)

func updateAssetName() string {
	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	switch runtime.GOOS {
	case "windows":
		return "KoreaWarLauncher-windows-" + arch + ".exe"
	case "darwin":
		return "KoreaWarLauncher-mac-" + arch
	default:
		return "KoreaWarLauncher-linux-" + arch
	}
}

// 지난번 업데이트에서 남은 예전 실행 파일 지우기 (예전 프로세스가 끝날 때까지 몇 번 시도)
func cleanOldExe() {
	old := selfExe() + ".old"
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(old); err != nil {
			return
		}
		if os.Remove(old) == nil {
			return
		}
		time.Sleep(time.Second)
	}
}

var restartHook func() // 서버(포트) 닫기

func checkUpdate() {
	if devBuild && os.Getenv("KW_TEST_UPDATE") == "" {
		return
	}
	doc, err := fetchSignedDoc() // 1.2.7 업데이트 여부·파일 모두 서명된 값으로만
	if err != nil {
		logger.Printf("런처 업데이트 확인 안 함: %v", err)
		return
	}
	target := doc.LauncherVersion
	if target == "" || !verLess(LauncherVersion, target) { // 같거나 낮은 버전으로는 절대 안 바꿈
		return
	}
	name := updateAssetName()
	sha := doc.Files["launcher/"+name]
	if sha == "" {
		logger.Printf("새 런처 %s 이 있지만 %s 에 서명이 없음", target, name)
		return
	}
	if gameRunning() || busy() {
		return
	}
	exe := selfExe()
	tmp := exe + ".new"
	setStatus("prepare", "새 런처 "+target+" 받는 중", 0)
	if err := download(dlTask{URL: filesBase() + "/launcher/" + name, Path: tmp, Sha256: sha}); err != nil {
		logger.Printf("런처 업데이트 받기 실패: %v", err)
		setStatus("idle", "", 0)
		_ = os.Remove(tmp)
		return
	}
	_ = os.Chmod(tmp, 0o755)
	_ = os.Remove(exe + ".old")
	if err := os.Rename(exe, exe+".old"); err != nil {
		logger.Printf("런처 업데이트 실패 (이름 바꾸기): %v", err)
		setStatus("idle", "", 0)
		return
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Rename(exe+".old", exe)
		logger.Printf("런처 업데이트 실패 (바꿔 넣기): %v", err)
		setStatus("idle", "", 0)
		return
	}
	logger.Printf("런처 %s -> %s 로 바꿈, 다시 켬", LauncherVersion, target)
	setStatus("prepare", "새 런처 "+target+" 로 다시 켜는 중", 0)
	time.Sleep(700 * time.Millisecond) // 화면이 글을 한 번 보게
	if restartHook != nil {
		restartHook()
	}
	c := exec.Command(exe, "--updated")
	detach(c)
	if err := c.Start(); err != nil {
		logger.Printf("새 런처를 켜지 못함: %v", err)
	}
	os.Exit(0)
}
