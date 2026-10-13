package main

import (
	"fmt"
	"os"
	"time"
)

func cmdUninstall(args []string) {
	_ = args
	if os.Geteuid() != 0 {
		fatal("uninstall must run as root: sudo novabot-client uninstall")
	}

	_ = runCmd(20*time.Second, "systemctl", "disable", "--now", "novabot-client")

	paths := []string{
		unitPath,
		sudoersPath,
		configPath,
		sourceMarker,
		binInstallPath,
	}
	for _, p := range paths {
		if err := os.Remove(p); err != nil {
			if !os.IsNotExist(err) {
				fmt.Printf("⚠  could not remove %s: %v\n", p, err)
			}
		} else {
			fmt.Printf("✓ removed %s\n", p)
		}
	}
	_ = os.Remove(installDir) // succeeds only if empty

	_ = runCmd(20*time.Second, "systemctl", "daemon-reload")
	fmt.Println("✓ uninstalled")
}
