//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

func selfExe() string {
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe
}

func afterUpdate() {}

// 개발용 실행(-tags dev)은 KW_TEST_INSTALL 이 있을 때만 설치 흉내
func skipInstall(args []string) bool {
	for _, a := range args {
		if a == "--portable" || a == "--installed" || a == "--updated" {
			return true
		}
	}
	return devBuild && os.Getenv("KW_TEST_INSTALL") == ""
}

func copyExe(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst + ".old")
	_ = os.Rename(dst, dst+".old")
	return os.Rename(tmp, dst)
}

func startDetached(path string, args ...string) error {
	c := exec.Command(path, args...)
	c.Dir = filepath.Dir(path)
	detach(c)
	return c.Start()
}
