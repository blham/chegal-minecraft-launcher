//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func hideWindow(c *exec.Cmd) {}

// 게임은 런처와 따로 (터미널·런처를 닫아도 게임은 계속)
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
