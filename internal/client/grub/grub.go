package grub

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Entry is one bootable target. Source is "efi" for firmware entries
// (managed via efibootmgr) or "grub" for entries inside grub.cfg.
type Entry struct {
	Index  int    `json:"index"`
	ID     string `json:"id"` // EFI: "0001". GRUB: the entry title.
	Title  string `json:"title"`
	Source string `json:"source"` // "efi" | "grub"
	Active bool   `json:"active,omitempty"`
}

type ScanResult struct {
	CfgPath        string            `json:"cfg_path,omitempty"`
	CfgContent     string            `json:"cfg_content,omitempty"`
	GrubenvPath    string            `json:"grubenv_path,omitempty"`
	GrubenvContent string            `json:"grubenv_content,omitempty"`
	Tools          map[string]string `json:"tools,omitempty"`
	Firmware       string            `json:"firmware,omitempty"`
	SystemdBoot    bool              `json:"systemd_boot,omitempty"`
	BootMounts     []string          `json:"boot_mounts,omitempty"`
	EFIOutput      string            `json:"efi_output,omitempty"`
}

var fallbackPaths = []string{
	"/boot/grub/grub.cfg",
	"/boot/grub2/grub.cfg",
	"/boot/efi/EFI/grub/grub.cfg",
	"/boot/efi/EFI/BOOT/grub.cfg",
	"/boot/efi/EFI/arch/grub.cfg",
	"/boot/efi/EFI/ubuntu/grub.cfg",
	"/boot/efi/EFI/fedora/grub.cfg",
	"/boot/efi/EFI/debian/grub.cfg",
	"/boot/EFI/GRUB/grub.cfg",
	"/boot/EFI/BOOT/grub.cfg",
	"/boot/EFI/arch/grub.cfg",
}

var searchRoots = []string{"/boot", "/efi"}

// Match menuentry lines at any indentation. GRUB's own generation
// scripts wrap entries in `if [ ... ]` blocks, which indents them.
var menuentryRe = regexp.MustCompile(`^\s*menuentry\s+['"]([^'"]+)['"]`)

// efibootmgr output line: `Boot0001* Windows Boot Manager`
var efiBootRe = regexp.MustCompile(`^Boot([0-9A-Fa-f]{4})(\*?)\s+(.*)$`)

const installedBin = "/usr/local/bin/novabot-client"

var (
	scanMu     sync.Mutex
	cachedScan *ScanResult
)

func loadScan() (*ScanResult, error) {
	scanMu.Lock()
	defer scanMu.Unlock()
	if cachedScan != nil {
		return cachedScan, nil
	}
	r, err := fetchScan()
	if err != nil {
		return nil, err
	}
	cachedScan = r
	return r, nil
}

func fetchScan() (*ScanResult, error) {
	if os.Geteuid() == 0 {
		return ScanDirect(), nil
	}

	if _, err := os.Stat(installedBin); err != nil {
		return nil, fmt.Errorf(
			"reading /boot requires root and the client isn't installed at %s.\n"+
				"run:  sudo novabot-client install --server <addr>",
			installedBin)
	}

	cmd := exec.Command("sudo", "-n", installedBin, "__grub-list")
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		low := strings.ToLower(stderr)
		if strings.Contains(low, "password") {
			return nil, fmt.Errorf(
				"sudo requires a password — the sudoers rule is missing.\n" +
					"reinstall the client to add it:\n" +
					"    sudo novabot-client install --server <addr>")
		}
		return nil, fmt.Errorf("sudo scan failed: %v: %s", err, stderr)
	}

	var r ScanResult
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("parse scan output: %w (raw: %s)",
			err, truncate(string(out), 200))
	}
	return &r, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ScanDirect runs as root and returns the raw view of /boot plus
// efibootmgr output when UEFI.
func ScanDirect() *ScanResult {
	r := &ScanResult{
		Tools:      map[string]string{},
		BootMounts: []string{},
		Firmware:   "BIOS",
	}
	if _, err := os.Stat("/sys/firmware/efi"); err == nil {
		r.Firmware = "UEFI"
	}

	for _, tool := range []string{
		"grub-reboot", "grub-editenv", "grub-mkconfig", "sudo", "efibootmgr",
	} {
		if path, err := exec.LookPath(tool); err == nil {
			r.Tools[tool] = path
		}
	}

	// grub.cfg
	for _, p := range fallbackPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			r.CfgPath = p
			break
		}
	}
	if r.CfgPath == "" {
		r.CfgPath = scanFor("grub.cfg")
	}
	if r.CfgPath != "" {
		if data, err := os.ReadFile(r.CfgPath); err == nil {
			r.CfgContent = string(data)
		}
	}

	// grubenv
	if r.CfgPath != "" {
		candidate := filepath.Join(filepath.Dir(r.CfgPath), "grubenv")
		if _, err := os.Stat(candidate); err == nil {
			r.GrubenvPath = candidate
		}
	}
	if r.GrubenvPath == "" {
		r.GrubenvPath = scanFor("grubenv")
	}
	if r.GrubenvPath != "" {
		if data, err := os.ReadFile(r.GrubenvPath); err == nil {
			r.GrubenvContent = string(data)
		}
	}

	if _, err := os.Stat("/boot/loader/loader.conf"); err == nil {
		r.SystemdBoot = true
	}

	// efibootmgr output — only useful on UEFI. Ask for verbose output
	// explicitly so we always get the same shape regardless of distro
	// defaults, then let the parser strip the device path back off.
	// Verbose output is the only reliable way to distinguish entries
	// whose names collide (e.g. multiple "UEFI OS" entries).
	if r.Firmware == "UEFI" {
		if _, ok := r.Tools["efibootmgr"]; ok {
			if out, err := exec.Command("efibootmgr", "-v").Output(); err == nil {
				r.EFIOutput = string(out)
			}
		}
	}

	if data, err := os.ReadFile("/proc/mounts"); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) < 3 {
				continue
			}
			mp := f[1]
			if mp == "/boot" || mp == "/efi" ||
				strings.HasPrefix(mp, "/boot/") ||
				strings.HasPrefix(mp, "/efi/") {
				r.BootMounts = append(r.BootMounts, mp+" ("+f[2]+")")
			}
		}
	}
	return r
}

func scanFor(name string) string {
	for _, root := range searchRoots {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		var hit string
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			if strings.Count(rel, string(filepath.Separator)) > 5 {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if d.Name() == name {
				hit = path
				return errors.New("stop")
			}
			return nil
		})
		if hit != "" {
			return hit
		}
	}
	return ""
}

// ── listing ────────────────────────────────────────────

// ListAll returns all boot targets: EFI firmware entries first (they
// work directly, no double-reboot), then any GRUB menuentries that
// aren't already represented by an EFI entry with the same title.
func ListAll() []Entry {
	var out []Entry
	seen := map[string]bool{}

	if efi := listEFIFromCache(); len(efi) > 0 {
		for _, e := range efi {
			out = append(out, e)
			seen[strings.ToLower(e.Title)] = true
		}
	}

	if grub := listGRUBFromCache(); len(grub) > 0 {
		for _, e := range grub {
			// Skip GRUB entries that just wrap an EFI entry we've
			// already listed — they're the same target twice.
			if strings.HasSuffix(e.Title, " (EFI BootNext)") {
				continue
			}
			if seen[strings.ToLower(e.Title)] {
				continue
			}
			out = append(out, e)
		}
	}

	for i := range out {
		out[i].Index = i
	}
	return out
}

// List is kept for backwards compatibility — returns GRUB entries only.
func List() ([]Entry, error) {
	r, err := loadScan()
	if err != nil {
		return nil, err
	}
	if r.CfgContent == "" {
		return nil, errors.New("no grub.cfg readable")
	}
	return parseGrubEntries(r.CfgContent), nil
}

func parseGrubEntries(content string) []Entry {
	var entries []Entry
	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	idx := 0
	for sc.Scan() {
		line := sc.Text()
		m := menuentryRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		entries = append(entries, Entry{
			Index:  idx,
			ID:     m[1],
			Title:  m[1],
			Source: "grub",
		})
		idx++
	}
	return entries
}

func listGRUBFromCache() []Entry {
	r, err := loadScan()
	if err != nil || r.CfgContent == "" {
		return nil
	}
	return parseGrubEntries(r.CfgContent)
}

// ── EFI entries ────────────────────────────────────────

func parseEFIOutput(out string) []Entry {
	var entries []Entry
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	current := ""
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		if strings.HasPrefix(line, "BootCurrent:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				current = strings.ToUpper(fields[1])
			}
			continue
		}
		m := efiBootRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id := strings.ToUpper(m[1])
		active := m[2] == "*"
		label := m[3]

		// efibootmgr sometimes prints the full EFI device path after
		// the label, separated by a tab. Strip it. Also handles the
		// rare case where the path leaks through without a tab but
		// starts with \EFI\.
		if i := strings.IndexByte(label, '\t'); i >= 0 {
			label = label[:i]
		}
		if i := strings.Index(label, `\EFI\`); i >= 0 {
			label = label[:i]
		}
		label = strings.TrimSpace(label)
		if label == "" {
			label = id
		}

		entries = append(entries, Entry{
			ID:     id,
			Title:  label,
			Source: "efi",
			Active: active,
			Index:  boolToInt(id == current),
		})
	}
	return entries
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func listEFIFromCache() []Entry {
	r, err := loadScan()
	if err != nil || r.EFIOutput == "" {
		return nil
	}
	return parseEFIOutput(r.EFIOutput)
}

// ── default ────────────────────────────────────────────

func Default() string {
	if r, err := loadScan(); err == nil && r.GrubenvContent != "" {
		if v := readKey(r.GrubenvContent, "saved_entry"); v != "" {
			return v
		}
	}
	if data, err := os.ReadFile("/etc/default/grub"); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "GRUB_DEFAULT=") {
				v := strings.TrimPrefix(line, "GRUB_DEFAULT=")
				return strings.Trim(v, `"'`)
			}
		}
	}
	return ""
}

func readKey(content, key string) string {
	sc := bufio.NewScanner(strings.NewReader(content))
	prefix := key + "="
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

// ── capability checks ──────────────────────────────────

func CanList() bool {
	r, err := loadScan()
	if err != nil {
		return false
	}
	return r.CfgContent != "" || r.EFIOutput != ""
}

func CanSetNext() bool {
	r, err := loadScan()
	if err != nil {
		return false
	}
	if _, ok := r.Tools["efibootmgr"]; ok && r.Firmware == "UEFI" {
		return true
	}
	if _, ok := r.Tools["grub-reboot"]; ok && r.GrubenvPath != "" {
		return true
	}
	if _, ok := r.Tools["grub-editenv"]; ok && r.GrubenvPath != "" {
		return true
	}
	return false
}

func Available() bool { return CanList() && CanSetNext() }

// ── diagnostics ────────────────────────────────────────

func Diagnose() string {
	var b strings.Builder
	b.WriteString("grub diagnostics:\n\n")

	r, err := loadScan()
	if err != nil {
		fmt.Fprintf(&b, "  ✗ cannot scan /boot: %v\n", err)
		b.WriteString("\n  /boot is usually only readable as root. try:\n")
		b.WriteString("      sudo novabot-client grub-diag\n")
		return b.String()
	}

	if r.CfgPath != "" {
		fmt.Fprintf(&b, "  ✓ config      %s (%d bytes)\n", r.CfgPath, len(r.CfgContent))
	} else {
		b.WriteString("  ✗ no grub.cfg found\n")
	}
	if r.GrubenvPath != "" {
		fmt.Fprintf(&b, "  ✓ grubenv     %s (%d bytes)\n", r.GrubenvPath, len(r.GrubenvContent))
	} else {
		b.WriteString("  ✗ no grubenv found\n")
	}

	b.WriteString("\n  tools:\n")
	for _, tool := range []string{
		"grub-reboot", "grub-editenv", "grub-mkconfig", "sudo", "efibootmgr",
	} {
		if path, ok := r.Tools[tool]; ok {
			fmt.Fprintf(&b, "    ✓ %-14s %s\n", tool, path)
		} else {
			fmt.Fprintf(&b, "    ✗ %-14s not in PATH\n", tool)
		}
	}

	fmt.Fprintf(&b, "\n  firmware: %s\n", r.Firmware)

	if len(r.BootMounts) > 0 {
		b.WriteString("\n  boot mounts:\n")
		for _, m := range r.BootMounts {
			fmt.Fprintf(&b, "    %s\n", m)
		}
	}

	if r.SystemdBoot {
		b.WriteString("\n  ⚠ systemd-boot detected (/boot/loader/loader.conf present)\n")
	}

	if r.EFIOutput != "" {
		efi := parseEFIOutput(r.EFIOutput)
		fmt.Fprintf(&b, "\n  UEFI firmware entries (%d):\n", len(efi))
		for _, e := range efi {
			marker := " "
			if e.Active {
				marker = "●"
			}
			fmt.Fprintf(&b, "    %s %s  %s\n", marker, e.ID, e.Title)
		}
	}

	if r.CfgContent != "" {
		entries := parseGrubEntries(r.CfgContent)
		fmt.Fprintf(&b, "\n  GRUB menuentries (%d):\n", len(entries))
		for _, e := range entries {
			fmt.Fprintf(&b, "    [%d] %s\n", e.Index, e.Title)
		}
	}

	all := ListAll()
	fmt.Fprintf(&b, "\n  effective boot targets (%d):\n", len(all))
	for _, e := range all {
		fmt.Fprintf(&b, "    %-4s  %s  (%s)\n", e.ID, e.Title, e.Source)
	}

	b.WriteString("\n  verdict: ")
	switch {
	case len(all) > 0:
		b.WriteString("boot control available.\n")
	case r.SystemdBoot:
		b.WriteString("systemd-boot only, no GRUB or EFI entries.\n")
	default:
		b.WriteString("no bootable targets found.\n")
	}
	return b.String()
}

// ── setting the next boot ──────────────────────────────

// SetNext is kept for backwards compatibility — takes a GRUB title.
func SetNext(title string) error {
	return SetEntry(Entry{ID: title, Title: title, Source: "grub"})
}

// SetEntry dispatches based on Source.
func SetEntry(e Entry) error {
	log.Printf("[GRUB] SetEntry: source=%q id=%q title=%q", e.Source, e.ID, e.Title)
	switch e.Source {
	case "efi":
		return setEFINext(e.ID)
	case "grub":
		id := e.ID
		if id == "" {
			id = e.Title
		}
		return setGRUBNext(id)
	default:
		return fmt.Errorf("unknown boot source %q", e.Source)
	}
}

// ── EFI ────────────────────────────────────────────────

func setEFINext(id string) error {
	log.Printf("[GRUB] setEFINext(%q)", id)
	if id == "" {
		return errors.New("efi boot id is required")
	}
	if _, err := exec.LookPath("efibootmgr"); err != nil {
		log.Printf("[GRUB] efibootmgr not in PATH")
		return errors.New("efibootmgr not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	log.Printf("[GRUB] attempt 1: direct efibootmgr (we are uid=%d)", os.Geteuid())
	if err := runEFIBootNext(ctx, id, false); err == nil {
		log.Printf("[GRUB] direct efibootmgr succeeded")
		return nil
	} else {
		log.Printf("[GRUB] direct efibootmgr failed: %v", err)
	}

	log.Printf("[GRUB] attempt 2: sudo -n efibootmgr")
	if err := runEFIBootNext(ctx, id, true); err != nil {
		log.Printf("[GRUB] sudo efibootmgr failed: %v", err)
		return fmt.Errorf("efibootmgr --bootnext %s failed: %w", id, err)
	}
	log.Printf("[GRUB] sudo efibootmgr succeeded")
	return nil
}

func runEFIBootNext(ctx context.Context, id string, viaSudo bool) error {
	var cmd *exec.Cmd
	if viaSudo {
		cmd = exec.CommandContext(ctx, "sudo", "-n", "efibootmgr", "--bootnext", id)
	} else {
		cmd = exec.CommandContext(ctx, "efibootmgr", "--bootnext", id)
	}
	out, err := cmd.CombinedOutput()
	log.Printf("[GRUB] efibootmgr (sudo=%v) → output: %q err=%v",
		viaSudo, strings.TrimSpace(string(out)), err)
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ── GRUB ───────────────────────────────────────────────

func setGRUBNext(title string) error {
	log.Printf("[GRUB] setGRUBNext(%q)", title)
	if title == "" {
		return errors.New("title is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var lastErr error

	log.Printf("[GRUB] attempt: grub-reboot direct")
	if err := tryReboot(ctx, title, false); err == nil {
		return nil
	} else {
		lastErr = err
		log.Printf("[GRUB] grub-reboot direct failed: %v", err)
	}

	log.Printf("[GRUB] attempt: sudo -n grub-reboot")
	if err := tryReboot(ctx, title, true); err == nil {
		return nil
	} else {
		lastErr = err
		log.Printf("[GRUB] sudo grub-reboot failed: %v", err)
	}

	r, _ := loadScan()
	if r != nil && r.GrubenvPath != "" {
		log.Printf("[GRUB] attempt: grub-editenv direct on %s", r.GrubenvPath)
		if err := tryEditEnv(ctx, r.GrubenvPath, title, false); err == nil {
			return nil
		} else {
			lastErr = err
			log.Printf("[GRUB] grub-editenv direct failed: %v", err)
		}

		log.Printf("[GRUB] attempt: sudo -n grub-editenv")
		if err := tryEditEnv(ctx, r.GrubenvPath, title, true); err == nil {
			return nil
		} else {
			lastErr = err
			log.Printf("[GRUB] sudo grub-editenv failed: %v", err)
		}
	}

	return fmt.Errorf("set next boot failed: %w", lastErr)
}

func tryReboot(ctx context.Context, title string, viaSudo bool) error {
	if _, err := exec.LookPath("grub-reboot"); err != nil {
		return errors.New("grub-reboot not installed")
	}
	var cmd *exec.Cmd
	if viaSudo {
		cmd = exec.CommandContext(ctx, "sudo", "-n", "grub-reboot", title)
	} else {
		cmd = exec.CommandContext(ctx, "grub-reboot", title)
	}
	out, err := cmd.CombinedOutput()
	log.Printf("[GRUB] grub-reboot (sudo=%v) → output: %q err=%v",
		viaSudo, strings.TrimSpace(string(out)), err)
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func tryEditEnv(ctx context.Context, env, title string, viaSudo bool) error {
	if _, err := exec.LookPath("grub-editenv"); err != nil {
		return errors.New("grub-editenv not installed")
	}
	var cmd *exec.Cmd
	if viaSudo {
		cmd = exec.CommandContext(ctx, "sudo", "-n", "grub-editenv",
			env, "set", "next_entry="+title)
	} else {
		cmd = exec.CommandContext(ctx, "grub-editenv",
			env, "set", "next_entry="+title)
	}
	out, err := cmd.CombinedOutput()
	log.Printf("[GRUB] grub-editenv (sudo=%v) → output: %q err=%v",
		viaSudo, strings.TrimSpace(string(out)), err)
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
