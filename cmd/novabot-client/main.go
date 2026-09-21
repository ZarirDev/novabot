package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ZarirDev/novabot/internal/client"
)

func main() {
	var (
		server = flag.String("server", "192.168.1.100", "novabot server address")
		port   = flag.Int("port", 4000, "UDP audio port on the server")
	)
	flag.Parse()

	log.SetFlags(log.Ltime)
	log.Println("novabot client starting…")

	streamer, err := client.NewStreamer(*server, *port)
	if err != nil {
		log.Fatalf("streamer: %v", err)
	}
	defer streamer.Close()

	capture, err := client.NewCapture()
	if err != nil {
		log.Fatalf("capture: %v", err)
	}
	defer capture.Stop()

	if err := capture.Start(streamer.Send); err != nil {
		log.Fatalf("start capture: %v", err)
	}

	log.Printf("streaming system audio to %s:%d", *server, *port)

	// Block until Ctrl+C.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("shutting down…")
}
