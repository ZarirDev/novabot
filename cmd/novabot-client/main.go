package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
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
  novabot-client entries      list GRUB boot entries
  novabot-client grub-diag    show what's on disk for GRUB
  novabot-client register     push boot entries to the server
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

// cmdGrubListInternal is the root-side half of the scan. The client
// invokes it via `sudo -n /usr/local/bin/novabot-client __grub-list`
// when running as a non-root user, because /boot is normally only
// readable by root.
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
		<-sig
		cancel()
	}()

	var wg sync.WaitGroup

	if grub.CanSetNext() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv := bootserver.New(cfg.BootListen, cfg.BootToken)
			if err := srv.Start(); err != nil {
				log.Printf("[BOOTAGENT] stopped: %v", err)
			}
		}()
	} else {
		log.Printf("[BOOTAGENT] disabled — GRUB not usable")
	}

	if err := runStream(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		cancel()
		wg.Wait()
		os.Exit(1)
	}
	wg.Wait()
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
			s.GRUBEntries, _ = grub.List()
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

// ── boot / entries / diag / register ───────────────────

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
	entries, err := grub.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "entries: %v\n", err)
		os.Exit(1)
	}
	def := grub.Default()
	for i, e := range entries {
		marker := " "
		if e.Title == def {
			marker = "●"
		}
		fmt.Printf("  %s [%d] %s\n", marker, i, e.Title)
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
