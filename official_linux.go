package main

import "os/exec"

func openOfficialLauncher() bool {
	try := func(name string, args ...string) bool {
		p, err := exec.LookPath(name)
		if err != nil {
			return false
		}
		c := exec.Command(p, args...)
		detach(c)
		return c.Start() == nil
	}
	if try("minecraft-launcher") {
		return true
	}
	if exec.Command("flatpak", "info", "com.mojang.Minecraft").Run() == nil && try("flatpak", "run", "com.mojang.Minecraft") {
		return true
	}
	return false
}
