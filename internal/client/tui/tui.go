package tui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ZarirDev/novabot/internal/client/grub"
)

type State struct {
	Server      string
	Connected   bool
	Quality     string
	PacketsSent uint64
	KBps        float64
	Uptime      string

	CanListGRUB   bool
	CanSetNext    bool
	GRUBEntries   []grub.Entry
	GRUBDefault   string
	GRUBDiagnosis string
}

type Actions struct {
	SetQuality   func(name string) error
	RegisterBoot func() error
	Quit         func()
}

func Run(snap func() State, actions Actions) error {
	reader := bufio.NewReader(os.Stdin)
	for {
		state := snap()
		printMenu(state)

		fmt.Print("  > ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)

		switch line {
		case "", "r":
		case "q", "quit", "exit":
			if actions.Quit != nil {
				actions.Quit()
			}
			fmt.Println("\n  bye")
			return nil
		case "1":
			chooseQuality(reader, state, actions)
		case "2":
			if !state.CanSetNext {
				fmt.Println("\n  GRUB boot control not available.")
				fmt.Println(state.GRUBDiagnosis)
				waitEnter(reader)
				continue
			}
			chooseBoot(reader, state)
		case "3":
			if !state.CanListGRUB {
				fmt.Println("\n  no GRUB entries readable.")
				fmt.Println(state.GRUBDiagnosis)
				waitEnter(reader)
				continue
			}
			showEntries(state)
			waitEnter(reader)
		case "4":
			if !state.CanListGRUB {
				fmt.Println("\n  no GRUB entries readable.")
				fmt.Println(state.GRUBDiagnosis)
				waitEnter(reader)
				continue
			}
			if actions.RegisterBoot == nil {
				fmt.Println("\n  register action unavailable")
				waitEnter(reader)
				continue
			}
			if err := actions.RegisterBoot(); err != nil {
				fmt.Printf("\n  failed: %v\n", err)
			} else {
				fmt.Println("\n  ✓ boot entries registered with the server")
			}
			waitEnter(reader)
		case "5", "diag":
			clearScreen()
			fmt.Println()
			fmt.Println(state.GRUBDiagnosis)
			waitEnter(reader)
		default:
			fmt.Println("\n  unknown option")
			waitEnter(reader)
		}
	}
}

func printMenu(s State) {
	clearScreen()
	fmt.Println()
	fmt.Println("  novabot client")
	fmt.Println("  " + strings.Repeat("─", 58))
	fmt.Println()
	fmt.Printf("  server     %s\n", s.Server)
	conn := "disconnected"
	if s.Connected {
		conn = "connected"
	}
	fmt.Printf("  status     %s\n", conn)
	fmt.Printf("  quality    %s\n", s.Quality)
	fmt.Printf("  tx         %.0f kbps · %d packets\n", s.KBps, s.PacketsSent)
	fmt.Printf("  uptime     %s\n", s.Uptime)

	switch {
	case s.CanListGRUB && s.CanSetNext:
		fmt.Printf("  boot       GRUB · %d entries · default %q\n",
			len(s.GRUBEntries), s.GRUBDefault)
	case s.CanListGRUB:
		fmt.Printf("  boot       GRUB · %d entries (boot control unavailable)\n",
			len(s.GRUBEntries))
	default:
		fmt.Printf("  boot       (single-OS machine — press 5 for diagnostics)\n")
	}
	fmt.Println()
	fmt.Println("  " + strings.Repeat("─", 58))
	fmt.Println()
	fmt.Println("  1) change audio quality")
	if s.CanSetNext {
		fmt.Println("  2) set next boot entry")
	}
	if s.CanListGRUB {
		fmt.Println("  3) list boot entries")
		fmt.Println("  4) register boot entries with server")
	}
	fmt.Println("  5) GRUB diagnostics")
	fmt.Println("  r) refresh    q) quit")
	fmt.Println()
}

func chooseQuality(reader *bufio.Reader, s State, actions Actions) {
	clearScreen()
	presets := []string{"low", "standard", "high", "studio"}
	fmt.Println()
	fmt.Println("  audio quality")
	fmt.Println()
	for i, p := range presets {
		marker := " "
		if p == s.Quality {
			marker = "●"
		}
		fmt.Printf("  %s %d) %s\n", marker, i+1, p)
	}
	fmt.Println()
	fmt.Print("  choice (enter to cancel): ")
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	idx, err := strconv.Atoi(line)
	if err != nil || idx < 1 || idx > len(presets) {
		fmt.Println("\n  invalid choice")
		waitEnter(reader)
		return
	}
	if err := actions.SetQuality(presets[idx-1]); err != nil {
		fmt.Printf("\n  failed: %v\n", err)
		waitEnter(reader)
		return
	}
	fmt.Printf("\n  quality set to %q\n", presets[idx-1])
	waitEnter(reader)
}

func chooseBoot(reader *bufio.Reader, s State) {
	clearScreen()
	if len(s.GRUBEntries) == 0 {
		fmt.Println("\n  no GRUB entries found")
		waitEnter(reader)
		return
	}
	fmt.Println()
	fmt.Println("  boot into which OS on next reboot?")
	fmt.Println()
	for i, e := range s.GRUBEntries {
		marker := " "
		if e.Title == s.GRUBDefault {
			marker = "●"
		}
		fmt.Printf("  %s %d) %s\n", marker, i+1, e.Title)
	}
	fmt.Println()
	fmt.Print("  choice (enter to cancel): ")
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	idx, err := strconv.Atoi(line)
	if err != nil || idx < 1 || idx > len(s.GRUBEntries) {
		fmt.Println("\n  invalid choice")
		waitEnter(reader)
		return
	}
	entry := s.GRUBEntries[idx-1]
	if err := grub.SetNext(entry.Title); err != nil {
		fmt.Printf("\n  failed: %v\n", err)
		waitEnter(reader)
		return
	}
	fmt.Printf("\n  next boot → %q\n", entry.Title)
	fmt.Println("  (one-shot: GRUB clears this after the next boot)")
	waitEnter(reader)
}

func showEntries(s State) {
	clearScreen()
	fmt.Println()
	fmt.Println("  grub entries")
	fmt.Println()
	for i, e := range s.GRUBEntries {
		marker := " "
		if e.Title == s.GRUBDefault {
			marker = "●"
		}
		fmt.Printf("  %s [%d] %s\n", marker, i, e.Title)
	}
	fmt.Println()
}

func waitEnter(reader *bufio.Reader) {
	fmt.Print("\n  press enter to continue ")
	_, _ = reader.ReadString('\n')
}

func clearScreen() {
	fmt.Print("\033[2J\033[H")
}
