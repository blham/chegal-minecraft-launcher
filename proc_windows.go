//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

func hideWindow(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }

// 게임은 런처와 따로 (런처를 닫아도 게임은 계속), 콘솔 창 없이
func detach(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200 | 0x08000000} // CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW
}
