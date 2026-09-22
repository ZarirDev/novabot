package main

import (
	"log"
	"net/http"

	"github.com/ZarirDev/novabot/internal/audio"
	"github.com/ZarirDev/novabot/internal/config"
	"github.com/ZarirDev/novabot/internal/mode"
	"github.com/ZarirDev/novabot/internal/server"
	"github.com/ZarirDev/novabot/internal/wakeword"
)

func main() {
	log.Println("Initializing Novabot...")

	cfg := config.Load()
	player := audio.NewAudioPlayer()
	detector := wakeword.NewDetector(cfg.WakeWord)
	manager := mode.NewManager(detector, player, cfg.UDPAudioPort)

	if err := manager.SetMode(mode.ModeAssistant); err != nil {
		log.Fatalf("Failed to initialize mode: %v", err)
	}

	srv := server.NewServer(manager)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	log.Printf("HTTP control API listening on :%s", cfg.HTTPPort)
	log.Printf("UDP audio streaming port: %d", cfg.UDPAudioPort)

	if err := http.ListenAndServe(":"+cfg.HTTPPort, mux); err != nil {
		log.Fatalf("HTTP server failure: %v", err)
	}
}
