package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ZarirDev/novabot/internal/audio"
	"github.com/ZarirDev/novabot/internal/client"
)

func main() {
	var (
		server   = flag.String("server", "", "novabot server address (required)")
		port     = flag.Int("port", 4000, "UDP audio port on the server")
		httpPort = flag.Int("http-port", 8080, "server HTTP port (for quality sync)")
		noSync   = flag.Bool("no-sync", false, "skip quality sync with the server")
	)
	flag.Parse()

	if *server == "" {
		flag.Usage()
		os.Exit(2)
	}

	log.SetFlags(log.Ltime)
	log.Println("novabot client starting…")

	// Read local env as a fallback — the server value wins if reachable.
	if q := os.Getenv("AUDIO_QUALITY"); q != "" {
		_ = audio.SetActiveQuality(q)
	}

	// Watch the server for quality changes before we even start capturing,
	// so the first capture uses the right format.
	var qualityCh <-chan string
	if !*noSync {
		qualityCh = client.WatchQuality(*server, *httpPort, 500*time.Millisecond)

		// Block briefly for the first value so we don't spin up with the
		// wrong format.
		select {
		case name := <-qualityCh:
			if err := audio.SetActiveQuality(name); err != nil {
				log.Printf("[CLIENT] invalid quality %q from server: %v", name, err)
			} else {
				log.Printf("[CLIENT] using quality %q from server", name)
			}
		case <-time.After(3 * time.Second):
			log.Printf("[CLIENT] server did not respond in time — using local quality %q",
				audio.ActiveQuality().Name)
		}
	}

	streamer, err := client.NewStreamer(*server, *port)
	if err != nil {
		log.Fatalf("streamer: %v", err)
	}
	defer streamer.Close()

	capture, err := client.NewCapture()
	if err != nil {
		log.Fatalf("capture: %v", err)
	}
	defer capture.Close()

	if err := capture.Start(streamer.Send); err != nil {
		log.Fatalf("start capture: %v", err)
	}

	log.Printf("streaming system audio to %s:%d", *server, *port)

	// React to quality changes.
	if qualityCh != nil {
		go func() {
			for name := range qualityCh {
				if name == audio.ActiveQuality().Name {
					continue
				}
				log.Printf("[CLIENT] quality changed to %q — restarting capture", name)
				if err := audio.SetActiveQuality(name); err != nil {
					log.Printf("[CLIENT] bad quality %q: %v", name, err)
					continue
				}
				if err := capture.Restart(streamer.Send); err != nil {
					log.Printf("[CLIENT] capture restart failed: %v", err)
				}
			}
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("shutting down…")
}
