package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ZarirDev/novabot/internal/audio"
	"github.com/ZarirDev/novabot/internal/client"
	"github.com/ZarirDev/novabot/internal/client/bootserver"
	clientcfg "github.com/ZarirDev/novabot/internal/client/config"
	"github.com/ZarirDev/novabot/internal/client/grub"
	"github.com/ZarirDev/novabot/internal/client/register"
	"github.com/ZarirDev/novabot/internal/client/tui"
)

const usage = `novabot-client — stream system audio to a novabot server

usage:
  novabot-client run          start streaming + boot agent (systemd unit)
  novabot-client tui          interactive menu
  novabot-client boot <name>  set the next GRUB boot entry (one-shot)
  novabot-client entries      list boot entries
  novabot-client grub-diag    show what's on disk for GRUB
  novabot-client register     push boot entries to the server
  novabot-client checkin      ask the server for a pending boot
  novabot-client install      install as a system service (requires root)
  novabot-client update       pull, rebuild, reinstall, restart (root)
  novabot-client uninstall    remove the system service (root)
  novabot-client config       print the resolved config path and values

flags:
  --config <path>    override config path
  --server <addr>    server hostname or IP
  --port <n>         UDP audio port (default 4000)
  --http-port <n>    server HTTP port (default 8080)
  --quiet            suppress periodic stats logs
  --source <path>    source checkout (install / update)
`

func main() {
	log.SetFlags(log.Ltime)

	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "run":
		cmdRun(rest)
	case "tui", "menu":
		cmdTUI(rest)
	case "boot":
		cmdBoot(rest)
	case "entries":
		cmdEntries(rest)
	case "grub-diag":
		cmdGrubDiag(rest)
	case "register":
		cmdRegister(rest)
	case "checkin":
		cmdCheckin(rest)
	case "install":
		cmdInstall(rest)
	case "update":
		cmdUpdate(rest)
	case "uninstall":
		cmdUninstall(rest)
	case "config":
		cmdConfig(rest)
	case "__grub-list":
		cmdGrubListInternal(rest)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		fmt.Print(usage)
		os.Exit(2)
	}
}

func cmdGrubListInternal(args []string) {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "__grub-list is an internal command; run via sudo")
		os.Exit(1)
	}
	_ = args
	r := grub.ScanDirect()
	out, err := json.Marshal(r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal scan: %v\n", err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(out)
}

type commonFlags struct {
	config   string
	server   string
	port     int
	httpPort int
	quiet    bool
	source   string
}

func parseCommon(name string, args []string) commonFlags {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	var cf commonFlags
	fs.StringVar(&cf.config, "config", "", "config file path")
	fs.StringVar(&cf.server, "server", "", "server address")
	fs.IntVar(&cf.port, "port", 0, "UDP audio port")
	fs.IntVar(&cf.httpPort, "http-port", 0, "server HTTP port")
	fs.BoolVar(&cf.quiet, "quiet", false, "suppress stats logging")
	fs.StringVar(&cf.source, "source", "", "source checkout (install/update)")
	_ = fs.Parse(args)
	return cf
}

func loadConfig(cf commonFlags) (*clientcfg.Config, string, error) {
	path := clientcfg.ResolvePath(cf.config)
	cfg, err := clientcfg.Load(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, path, err
		}
		cfg = &clientcfg.Config{}
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
	if cf.quiet {
		cfg.Quiet = true
	}
	if err := cfg.Validate(); err != nil {
		return nil, path, err
	}
	return cfg, path, nil
}

// ── run ────────────────────────────────────────────────

func cmdRun(args []string) {
	cf := parseCommon("run", args)
	cfg, _, err := loadConfig(cf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	log.Printf("[CLIENT] starting: server=%s udpPort=%d httpPort=%d bootListen=%s",
		cfg.Server, cfg.Port, cfg.HTTPPort, cfg.BootListen)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sig
		log.Printf("[CLIENT] received %v — shutting down", s)
		cancel()
	}()

	var wg sync.WaitGroup

	if grub.CanSetNext() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv := bootserver.New(cfg.BootListen, cfg.BootToken)
			if err := srv.Start(ctx); err != nil {
				log.Printf("[BOOT] server stopped: %v", err)
			} else {
				log.Printf("[BOOT] server stopped cleanly")
			}
		}()
	} else {
		log.Printf("[BOOT] agent disabled — GRUB/efibootmgr not usable")
		log.Printf("[BOOT] run `novabot-client grub-diag` for details")
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		startupSequence(ctx, cfg)
	}()

	if err := runStream(ctx, cfg); err != nil {
		log.Printf("[CLIENT] streamer error: %v", err)
		cancel()
	}

	wg.Wait()
	log.Printf("[CLIENT] all workers exited")
}

// startupSequence waits for network, registers, then polls the server
// for a pending boot until one is found or the deadline expires.
func startupSequence(ctx context.Context, cfg *clientcfg.Config) {
	log.Printf("[STARTUP] === begin ===")

	var mac, ip string
	for attempt := 1; attempt <= 60; attempt++ {
		if ctx.Err() != nil {
			log.Printf("[STARTUP] cancelled during network wait")
			return
		}
		ip = register.LocalIP()
		mac = register.LocalMAC()
		if ip != "" && mac != "" {
			log.Printf("[STARTUP] network up (attempt %d): ip=%s mac=%s", attempt, ip, mac)
			break
		}
		if attempt == 1 || attempt%5 == 0 {
			log.Printf("[STARTUP] waiting for network (attempt %d/60) ip=%q mac=%q",
				attempt, ip, mac)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	if ip == "" || mac == "" {
		log.Printf("[STARTUP] network never came up — aborting startup sequence")
		return
	}

	log.Printf("[STARTUP] === register ===")
	if err := registerWithRetry(ctx, cfg, mac, ip); err != nil {
		log.Printf("[STARTUP] registration failed: %v", err)
		// Continue anyway — checkin might still work.
	}

	log.Printf("[STARTUP] === checkin loop ===")
	checkinLoop(ctx, cfg, mac)

	log.Printf("[STARTUP] === complete ===")
}

func registerWithRetry(ctx context.Context, cfg *clientcfg.Config, mac, ip string) error {
	entries := grub.ListAll()
	_, port, _ := splitListen(cfg.BootListen)
	bootAddr := fmt.Sprintf("http://%s:%s", ip, port)

	reg := register.Registration{
		Hostname: register.Hostname(),
		MAC:      mac,
		IP:       ip,
		BootAddr: bootAddr,
		Token:    cfg.BootToken,
		Entries:  entries,
		Default:  grub.Default(),
	}

	var lastErr error
	for attempt := 1; attempt <= 10; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("[REGISTER] attempt %d/10 → %s:%d (entries=%d bootAddr=%s)",
			attempt, cfg.Server, cfg.HTTPPort, len(entries), bootAddr)
		if err := register.Register(cfg.Server, cfg.HTTPPort, reg); err == nil {
			log.Printf("[REGISTER] OK")
			return nil
		} else {
			lastErr = err
			log.Printf("[REGISTER] attempt %d failed: %v", attempt, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return lastErr
}

// checkinLoop polls the server every second until we get either a
// pending boot to act on, or a clear "nothing to do" response.
//
// Stops early on: set-and-reboot (we act then return), or action=none
// without reason=unknown-device (nothing to do). Keeps retrying on
// network errors and on unknown-device (registration may not have
// landed yet).
func checkinLoop(ctx context.Context, cfg *clientcfg.Config, mac string) {
	deadline := time.Now().Add(120 * time.Second)
	attempt := 0
	consecutiveErr := 0

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			log.Printf("[CHECKIN] cancelled")
			return
		}
		attempt++
		reply, err := doCheckin(ctx, cfg, mac)
		if err != nil {
			consecutiveErr++
			// Log first, then every 10th, to avoid spam.
			if consecutiveErr == 1 || consecutiveErr%10 == 0 {
				log.Printf("[CHECKIN] attempt %d error (%d consecutive): %v",
					attempt, consecutiveErr, err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}
		consecutiveErr = 0

		log.Printf("[CHECKIN] attempt %d action=%q id=%q source=%q reason=%q",
			attempt, reply.Action, reply.ID, reply.Source, reply.Reason)

		switch reply.Action {
		case "set-and-reboot":
			log.Printf("[CHECKIN] got pending boot: %s/%s — acting", reply.Source, reply.ID)
			if err := actOnPending(ctx, reply); err != nil {
				log.Printf("[CHECKIN] act failed: %v", err)
				// Keep retrying in case the server still has it (it
				// only clears after a successful response, so if
				// SetEntry failed we can retry).
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
				continue
			}
			return

		case "", "none":
			if reply.Reason == "unknown-device" {
				log.Printf("[CHECKIN] server doesn't know us yet — retrying")
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
				}
				continue
			}
			log.Printf("[CHECKIN] nothing to do (%q) — stopping", reply.Reason)
			return

		default:
			log.Printf("[CHECKIN] unknown action %q — stopping", reply.Action)
			return
		}
	}
	log.Printf("[CHECKIN] loop timed out after 120s")
}

type checkinReply struct {
	Action string `json:"action"`
	ID     string `json:"id"`
	Source string `json:"source"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

func doCheckin(ctx context.Context, cfg *clientcfg.Config, mac string) (*checkinReply, error) {
	url := fmt.Sprintf("http://%s:%d/api/v1/wol/boot-checkin", cfg.Server, cfg.HTTPPort)
	body, _ := json.Marshal(map[string]string{
		"mac":      mac,
		"hostname": register.Hostname(),
	})

	httpCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var reply checkinReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

func actOnPending(ctx context.Context, reply *checkinReply) error {
	if alreadyBooted(reply.ID, reply.Source) {
		log.Printf("[CHECKIN] already booted into %s/%s — no action", reply.Source, reply.ID)
		return nil
	}

	log.Printf("[CHECKIN] setting next boot to %s/%s and rebooting", reply.Source, reply.ID)
	if err := grub.SetEntry(grub.Entry{
		ID:     reply.ID,
		Source: reply.Source,
		Title:  reply.Title,
	}); err != nil {
		return fmt.Errorf("SetEntry: %w", err)
	}
	log.Printf("[CHECKIN] SetEntry OK")

	select {
	case <-ctx.Done():
		return nil
	case <-time.After(2 * time.Second):
	}

	log.Printf("[CHECKIN] executing sudo systemctl reboot")
	if err := grub.Reboot(); err != nil {
		return fmt.Errorf("reboot: %w", err)
	}
	return nil
}

func alreadyBooted(id, source string) bool {
	if source != "efi" {
		return false
	}
	for _, e := range grub.ListAll() {
		if e.ID == id && e.Source == "efi" && e.Current {
			return true
		}
	}
	return false
}

func runStream(ctx context.Context, cfg *clientcfg.Config) error {
	log.Printf("[CLIENT] streamer starting → %s:%d", cfg.Server, cfg.Port)

	if q := os.Getenv("AUDIO_QUALITY"); q != "" {
		_ = audio.SetActiveQuality(q)
	}

	var qualityCh <-chan string
	if !cfg.NoSync {
		qualityCh = client.WatchQuality(cfg.Server, cfg.HTTPPort, 500*time.Millisecond)
		select {
		case name := <-qualityCh:
			if err := audio.SetActiveQuality(name); err != nil {
				log.Printf("[CLIENT] invalid quality %q: %v", name, err)
			}
		case <-time.After(3 * time.Second):
			log.Printf("[CLIENT] server did not respond — using local quality %q",
				audio.ActiveQuality().Name)
		}
	}

	streamer, err := client.NewStreamer(cfg.Server, cfg.Port)
	if err != nil {
		return fmt.Errorf("streamer: %w", err)
	}
	streamer.SetQuiet(cfg.Quiet)
	defer streamer.Close()

	capture, err := client.NewCapture()
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	defer capture.Close()

	if err := capture.Start(streamer.Send); err != nil {
		return fmt.Errorf("start capture: %w", err)
	}

	if qualityCh != nil {
		go func() {
			for name := range qualityCh {
				if name == audio.ActiveQuality().Name {
					continue
				}
				log.Printf("[CLIENT] quality → %q", name)
				if err := audio.SetActiveQuality(name); err != nil {
					continue
				}
				if err := capture.Restart(streamer.Send); err != nil {
					log.Printf("[CLIENT] restart: %v", err)
				}
			}
		}()
	}

	<-ctx.Done()
	log.Println("[CLIENT] shutting down streamer")
	return nil
}

// ── tui ────────────────────────────────────────────────

func cmdTUI(args []string) {
	cf := parseCommon("tui", args)
	cfg, _, err := loadConfig(cf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attached := serviceActive()
	started := time.Now()
	var wg sync.WaitGroup
	if !attached {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := runStream(ctx, cfg); err != nil {
				log.Printf("[CLIENT] streamer stopped: %v", err)
			}
		}()
	}

	snap := func() tui.State {
		s := tui.State{
			Server:        fmt.Sprintf("%s:%d", cfg.Server, cfg.Port),
			Connected:     attached,
			Quality:       audio.ActiveQuality().Name,
			Uptime:        time.Since(started).Round(time.Second).String(),
			CanListGRUB:   grub.CanList(),
			CanSetNext:    grub.CanSetNext(),
			GRUBDiagnosis: grub.Diagnose(),
		}
		if s.CanListGRUB {
			s.GRUBEntries = grub.ListAll()
			s.GRUBDefault = grub.Default()
		}
		return s
	}

	actions := tui.Actions{
		SetQuality: func(name string) error {
			return audio.SetActiveQuality(name)
		},
		RegisterBoot: func() error {
			return registerNow(cfg)
		},
		Quit: cancel,
	}

	if err := tui.Run(snap, actions); err != nil {
		fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		os.Exit(1)
	}
	wg.Wait()
}

func serviceActive() bool {
	return exec.Command("systemctl", "is-active", "--quiet", "novabot-client").Run() == nil
}

func registerNow(cfg *clientcfg.Config) error {
	entries := grub.ListAll()
	ip := register.LocalIP()
	mac := register.LocalMAC()
	if ip == "" {
		return fmt.Errorf("could not determine local IP")
	}
	if mac == "" {
		return fmt.Errorf("could not determine local MAC")
	}
	_, port, _ := splitListen(cfg.BootListen)
	bootAddr := fmt.Sprintf("http://%s:%s", ip, port)

	reg := register.Registration{
		Hostname: register.Hostname(),
		MAC:      mac,
		IP:       ip,
		BootAddr: bootAddr,
		Token:    cfg.BootToken,
		Entries:  entries,
		Default:  grub.Default(),
	}
	log.Printf("[REGISTER] manual: mac=%s ip=%s entries=%d", mac, ip, len(entries))
	return register.Register(cfg.Server, cfg.HTTPPort, reg)
}

func splitListen(addr string) (host, port string, err error) {
	if addr == "" {
		return "", "9753", nil
	}
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:], nil
		}
	}
	return addr, "9753", nil
}

// ── boot / entries / diag / register / checkin ─────────

func cmdBoot(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: novabot-client boot <entry-title>")
		os.Exit(2)
	}
	title := args[0]
	if err := grub.SetNext(title); err != nil {
		fmt.Fprintf(os.Stderr, "boot: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("next boot → %q\n", title)
}

func cmdEntries(args []string) {
	_ = args
	entries := grub.ListAll()
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "no boot entries found")
		os.Exit(1)
	}
	def := grub.Default()
	for _, e := range entries {
		marker := " "
		if e.Current {
			marker = "●"
		} else if e.Title == def {
			marker = "·"
		}
		fmt.Printf("  %s %-4s  %s  (%s)\n", marker, e.ID, e.Title, e.Source)
	}
}

func cmdGrubDiag(args []string) {
	_ = args
	fmt.Print(grub.Diagnose())
}

func cmdRegister(args []string) {
	cf := parseCommon("register", args)
	cfg, _, err := loadConfig(cf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	if err := registerNow(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "register: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ boot entries registered with the server")
}

func cmdCheckin(args []string) {
	cf := parseCommon("checkin", args)
	cfg, _, err := loadConfig(cf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	mac := register.LocalMAC()
	if mac == "" {
		fmt.Fprintln(os.Stderr, "checkin: could not determine local MAC")
		os.Exit(1)
	}
	reply, err := doCheckin(context.Background(), cfg, mac)
	if err != nil {
		fmt.Fprintf(os.Stderr, "checkin: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ checkin: action=%q id=%q source=%q reason=%q\n",
		reply.Action, reply.ID, reply.Source, reply.Reason)
}

func cmdConfig(args []string) {
	cf := parseCommon("config", args)
	path := clientcfg.ResolvePath(cf.config)
	cfg, err := clientcfg.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no config at %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("path:        %s\n", path)
	fmt.Printf("server:      %s\n", cfg.Server)
	fmt.Printf("port:        %d\n", cfg.Port)
	fmt.Printf("httpPort:    %d\n", cfg.HTTPPort)
	fmt.Printf("noSync:      %v\n", cfg.NoSync)
	fmt.Printf("quiet:       %v\n", cfg.Quiet)
	fmt.Printf("bootListen:  %s\n", cfg.BootListen)
	fmt.Printf("bootToken:   %s\n", maskToken(cfg.BootToken))
}

func maskToken(t string) string {
	if len(t) <= 8 {
		return t
	}
	return t[:8] + "…"
}
