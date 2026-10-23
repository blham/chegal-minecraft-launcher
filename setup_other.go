//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

// 맥·리눅스는 설치 과정 없음 (맥: 응용 프로그램 폴더로 끌어다 놓기, 리눅스: install-desktop-entry.sh)
func maybeInstall(args []string) bool { return false }
func afterUpdate()                    {}

func selfExe() string {
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe
}
