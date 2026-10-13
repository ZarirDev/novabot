package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	clientcfg "github.com/ZarirDev/novabot/internal/client/config"
)

const (
	unitName       = "novabot-client.service"
	unitPath       = "/etc/systemd/system/" + unitName
	binInstallPath = "/usr/local/bin/novabot-client"
	installDir     = "/etc/novabot"
	configPath     = installDir + "/client.json"
	sourceMarker   = installDir + "/source"
	sudoersPath    = "/etc/sudoers.d/novabot-client"
)

func cmdInstall(args []string) {
	if os.Geteuid() != 0 {
		fatal("install must run as root: sudo novabot-client install --server <addr>")
	}

	cf := parseCommon("install", args)

	invoker, uid, home, err := invokingUser()
	if err != nil {
		fatal("%v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		fatal("resolve self: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	source, err := resolveRepoRoot(cf.source, exe)
	if err != nil {
		fatal("%v", err)
	}

	cfg := &clientcfg.Config{}
	if existing, err := clientcfg.Load(configPath); err == nil {
		cfg = existing
	}
	if cf.server != "" {
		cfg.Server = cf.server
	}
	if cf.port > 0 {
		cfg.Port = cf.port
	}
	if cf.httpPort > 0 {
		cfg.HTTPPort = cf.httpPort
	}
	cfg.Quiet = true
	if err := cfg.Validate(); err != nil {
		fatal("%v", err)
	}

	// 1. binary
	if exe != binInstallPath {
		if err := copyFile(exe, binInstallPath, 0o755); err != nil {
			fatal("copy binary: %v", err)
		}
		fmt.Printf("✓ installed binary → %s\n", binInstallPath)
	} else {
		fmt.Printf("✓ binary already at %s\n", binInstallPath)
	}

	// 2. config
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		fatal("mkdir %s: %v", installDir, err)
	}
	if err := cfg.Save(configPath); err != nil {
		fatal("write config: %v", err)
	}
	_ = os.Chmod(configPath, 0o644)
	fmt.Printf("✓ wrote config → %s\n", configPath)

	// 3. source marker
	if source != "" {
		_ = os.WriteFile(sourceMarker, []byte(source+"\n"), 0o644)
		fmt.Printf("✓ recorded source → %s\n", source)
	} else {
		fmt.Println("⚠  no source checkout found — `update` will need --source")
	}

	// 4. unit
	if err := writeUnit(invoker, uid, home); err != nil {
		fatal("write unit: %v", err)
	}
	fmt.Printf("✓ wrote unit → %s\n", unitPath)

	// 5. sudoers
	if err := writeSudoers(invoker); err != nil {
		fmt.Printf("⚠  could not write sudoers rule: %v\n", err)
		fmt.Printf("   GRUB control will require a password.\n")
	} else {
		fmt.Printf("✓ wrote sudoers rule → %s\n", sudoersPath)
	}

	// 6. lingering
	if err := runCmd(30*time.Second, "loginctl", "enable-linger", invoker); err != nil {
		fmt.Printf("⚠  could not enable lingering: %v\n", err)
		fmt.Printf("   Run manually: sudo loginctl enable-linger %s\n", invoker)
	} else {
		fmt.Printf("✓ enabled lingering for %s\n", invoker)
	}

	// 7. reload + enable
	if err := runCmd(20*time.Second, "systemctl", "daemon-reload"); err != nil {
		fatal("daemon-reload: %v", err)
	}
	_ = runCmd(5*time.Second, "systemctl", "reset-failed", "novabot-client")

	if err := runCmd(20*time.Second, "systemctl", "enable", "novabot-client"); err != nil {
		fatal("enable service: %v", err)
	}
	fmt.Println("✓ novabot-client enabled")

	// 8. start without blocking
	fmt.Println("→ starting service (this may take a few seconds on first run)")
	if err := startAndReport(30 * time.Second); err != nil {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "⚠  service did not come up: %v\n\n", err)
		fmt.Fprintln(os.Stderr, "recent log output:")
		printJournal(30)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "common fixes:")
		fmt.Fprintln(os.Stderr, "  • PulseAudio not reachable — check `systemctl status user@"+
			strconv.Itoa(uid)+".service`")
		fmt.Fprintln(os.Stderr, "  • missing audio tools — install alsa-utils, pulseaudio-utils")
		fmt.Fprintln(os.Stderr, "  • config error — check "+configPath)
		os.Exit(1)
	}
	fmt.Println("✓ novabot-client running")

	fmt.Println()
	fmt.Println("  status:   systemctl status novabot-client")
	fmt.Println("  logs:     journalctl -u novabot-client -f")
	fmt.Println("  interact: novabot-client tui")
	fmt.Println("  grub:     novabot-client grub-diag")
	fmt.Println("  update:   sudo novabot-client update")
}

func startAndReport(timeout time.Duration) error {
	// If a previous version of the binary is running and doesn't
	// respond to SIGTERM, a normal `systemctl restart` would block
	// for TimeoutStopSec. Force-kill it first so start returns fast.
	if exec.Command("systemctl", "is-active", "--quiet", "novabot-client").Run() == nil {
		_ = runCmd(5*time.Second, "systemctl", "kill",
			"--signal=SIGKILL", "novabot-client")
		// Give systemd a beat to reap the process and transition
		// the unit out of "active".
		time.Sleep(500 * time.Millisecond)
	}

	_ = runCmd(5*time.Second, "systemctl", "reset-failed", "novabot-client")

	// --no-block returns immediately; we poll is-active below.
	if err := runCmd(5*time.Second, "systemctl", "start", "--no-block", "novabot-client"); err != nil {
		return fmt.Errorf("start command failed: %w", err)
	}

	deadline := time.Now().Add(timeout)
	var lastState string
	for time.Now().Before(deadline) {
		state, _ := exec.Command("systemctl", "is-active", "novabot-client").Output()
		lastState = strings.TrimSpace(string(state))
		switch lastState {
		case "active":
			return nil
		case "failed":
			return fmt.Errorf("unit entered failed state")
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s (last state: %s)", timeout, lastState)
}

func printJournal(lines int) {
	cmd := exec.Command("journalctl", "-u", "novabot-client", "-n",
		strconv.Itoa(lines), "--no-pager")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

func resolveRepoRoot(explicit, exePath string) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", fmt.Errorf("resolve --source: %w", err)
		}
		if !looksLikeRepo(abs) {
			return "", fmt.Errorf("--source %s is not a novabot checkout "+
				"(expected a go.mod with module github.com/ZarirDev/novabot)", abs)
		}
		return abs, nil
	}

	starts := []string{filepath.Dir(exePath)}
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	for _, start := range starts {
		if root := findRepoRoot(start); root != "" {
			return root, nil
		}
	}
	return "", nil
}

func findRepoRoot(start string) string {
	dir := start
	for {
		if looksLikeRepo(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func invokingUser() (username string, uid int, home string, err error) {
	su := os.Getenv("SUDO_USER")
	if su == "" || su == "root" {
		return "", 0, "", errors.New(
			"cannot determine invoking user — run via `sudo novabot-client install`, " +
				"not as root directly")
	}
	u, err := user.Lookup(su)
	if err != nil {
		return "", 0, "", fmt.Errorf("lookup user %q: %w", su, err)
	}
	parsed, err := strconv.Atoi(u.Uid)
	if err != nil {
		return "", 0, "", fmt.Errorf("parse uid for %q: %w", su, err)
	}
	return u.Username, parsed, u.HomeDir, nil
}

func writeUnit(username string, uid int, home string) error {
	unit := fmt.Sprintf(`[Unit]
Description=novabot audio streaming client
After=network-online.target sound.target
Wants=network-online.target sound.target

[Service]
Type=simple
User=%s
Group=%s
SupplementaryGroups=audio video
Environment=HOME=%s
Environment=XDG_RUNTIME_DIR=/run/user/%d
Environment=PULSE_SERVER=unix:/run/user/%d/pulse/native
ExecStart=%s run --config %s
Restart=on-failure
RestartSec=3
TimeoutStopSec=10
KillMode=mixed
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`, username, username, home, uid, uid, binInstallPath, configPath)

	return os.WriteFile(unitPath, []byte(unit), 0o644)
}

// writeSudoers grants the invoking user permission to run the specific
// root-only operations the client needs: reading /boot via the
// internal __grub-list subcommand, and setting the next boot entry.
// All entries are exact-argument matches, so the rule is minimal.
func writeSudoers(username string) error {
	var entries []string

	entries = append(entries, binInstallPath+" __grub-list")

	for _, tool := range []string{"grub-reboot", "grub-editenv", "efibootmgr"} {
		if p, err := exec.LookPath(tool); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				entries = append(entries, abs)
			}
		}
	}

	content := "# managed by novabot-client — removed by `sudo novabot-client uninstall`\n" +
		username + " ALL=(root) NOPASSWD: " +
		strings.Join(entries, ", ") + "\n"

	if err := os.WriteFile(sudoersPath, []byte(content), 0o440); err != nil {
		return err
	}

	if err := runCmd(10*time.Second, "visudo", "-c", "-f", sudoersPath); err != nil {
		_ = os.Remove(sudoersPath)
		return fmt.Errorf("visudo rejected the rule: %w", err)
	}
	return nil
}

func runCmd(timeout time.Duration, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return fmt.Errorf("timed out after %s", timeout)
	}
}

func looksLikeRepo(dir string) bool {
	if dir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "module github.com/ZarirDev/novabot")
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
