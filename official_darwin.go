package main

import "os/exec"

func openOfficialLauncher() bool {
	for _, a := range [][]string{{"-b", "com.mojang.minecraftlauncher"}, {"-a", "Minecraft"}, {"-a", "Minecraft Launcher"}} {
		if exec.Command("open", a...).Run() == nil {
			return true
		}
	}
	return false
}
