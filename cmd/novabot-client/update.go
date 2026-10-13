package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// cmdUpdate rebuilds the client from source and reinstalls it. Requires
// root because it writes to /usr/local/bin and restarts the service.
func cmdUpdate(args []string) {
	if os.Geteuid() != 0 {
		fatal("update must run as root: sudo novabot-client update")
	}

	source := ""
	if len(args) > 0 {
		source = args[0]
	}
	if source == "" {
		if data, err := os.ReadFile(sourceMarker); err == nil {
			source = strings.TrimSpace(string(data))
		}
	}
	if source == "" {
		fatal("no source checkout recorded.\n" +
			"  pass it explicitly:  sudo novabot-client update /path/to/novabot\n" +
			"  or reinstall:        sudo novabot-client install --source /path/to/novabot")
	}
	if !looksLikeRepo(source) {
		fatal("%s does not look like a novabot checkout", source)
	}

	for _, tool := range []string{"git", "make", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			fatal("missing build tool %q in PATH", tool)
		}
	}

	fmt.Printf("→ git pull in %s\n", source)
	if err := runIn(source, "git", "pull", "--ff-only"); err != nil {
		fatal("git pull: %v", err)
	}

	fmt.Println("→ rebuilding client")
	if err := runIn(source, "make", "client"); err != nil {
		fatal("make client: %v", err)
	}

	newBin := filepath.Join(source, "bin", "novabot-client")
	if _, err := os.Stat(newBin); err != nil {
		fatal("expected new binary at %s: %v", newBin, err)
	}
	if err := copyFile(newBin, binInstallPath, 0o755); err != nil {
		fatal("install binary: %v", err)
	}
	fmt.Printf("✓ installed %s\n", binInstallPath)

	fmt.Println("→ restarting novabot-client")
	if err := runCmd(30*time.Second, "systemctl", "restart", "novabot-client"); err != nil {
		fatal("restart: %v", err)
	}
	fmt.Println("✓ updated and restarted")
}

func runIn(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
