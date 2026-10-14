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

// ── flags ──────────────────────────────────────────────

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
		log.Printf("[BOOT] disabled — GRUB / efibootmgr not usable")
	}

	// Startup checkin: ask the server if there's a pending boot. Runs
	// once; gives the network a few seconds to settle first.
	go func() {
		time.Sleep(3 * time.Second)
		if err := bootCheckin(cfg); err != nil {
			log.Printf("[BOOT] startup checkin failed: %v", err)
		}
	}()

	if err := runStream(ctx, cfg); err != nil {
		log.Printf("[CLIENT] streamer error: %v", err)
		cancel()
	}

	wg.Wait()
	log.Printf("[CLIENT] all workers exited")
}

func runStream(ctx context.Context, cfg *clientcfg.Config) error {
	log.Printf("[CLIENT] streaming to %s:%d", cfg.Server, cfg.Port)

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
	log.Println("[CLIENT] shutting down")
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
	if len(entries) == 0 {
		return fmt.Errorf("no boot targets found on this machine")
	}
	ip := register.LocalIP()
	mac := register.LocalMAC()
	if ip == "" {
		return fmt.Errorf("could not determine local IP")
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
	if err := bootCheckin(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "checkin: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ checkin complete")
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

// ── startup checkin ────────────────────────────────────

func bootCheckin(cfg *clientcfg.Config) error {
	mac := register.LocalMAC()
	if mac == "" {
		return fmt.Errorf("could not determine local MAC")
	}
	var lastErr error
	for i := 0; i < 5; i++ {
		if i > 0 {
			time.Sleep(3 * time.Second)
		}
		if err := doCheckin(cfg, mac); err == nil {
			return nil
		} else {
			lastErr = err
			log.Printf("[BOOT] checkin attempt %d/5 failed: %v", i+1, err)
		}
	}
	return lastErr
}

func doCheckin(cfg *clientcfg.Config, mac string) error {
	url := fmt.Sprintf("http://%s:%d/api/v1/wol/boot-checkin", cfg.Server, cfg.HTTPPort)
	body, _ := json.Marshal(map[string]string{
		"mac":      mac,
		"hostname": register.Hostname(),
	})
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}

	var reply struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Source string `json:"source"`
		Title  string `json:"title"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return err
	}

	log.Printf("[BOOT] checkin → action=%q id=%q source=%q reason=%q",
		reply.Action, reply.ID, reply.Source, reply.Reason)

	if reply.Action != "set-and-reboot" {
		return nil
	}

	if alreadyBooted(reply.ID, reply.Source) {
		log.Printf("[BOOT] already booted into %s/%s — no action needed",
			reply.Source, reply.ID)
		return nil
	}

	log.Printf("[BOOT] pending boot to %s/%s — setting BootNext and rebooting",
		reply.Source, reply.ID)

	if err := grub.SetEntry(grub.Entry{
		ID:     reply.ID,
		Source: reply.Source,
		Title:  reply.Title,
	}); err != nil {
		return fmt.Errorf("set entry: %w", err)
	}

	time.Sleep(2 * time.Second)
	log.Printf("[BOOT] rebooting into %s/%s", reply.Source, reply.ID)
	return exec.Command("sudo", "-n", "systemctl", "reboot").Run()
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
