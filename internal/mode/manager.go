package mode

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/ZarirDev/novabot/internal/audio"
	"github.com/ZarirDev/novabot/internal/wakeword"
)

type AppMode string

const (
	ModeAssistant AppMode = "ASSISTANT"
	ModePCAudio   AppMode = "PC_AUDIO"
)

type Manager struct {
	mu          sync.RWMutex
	currentMode AppMode
	detector    wakeword.Detector
	player      *audio.AudioPlayer
	udpPort     int
	udpStopChan chan struct{}
}

func NewManager(detector wakeword.Detector, player *audio.AudioPlayer, udpPort int) *Manager {
	return &Manager{
		currentMode: ModeAssistant,
		detector:    detector,
		player:      player,
		udpPort:     udpPort,
	}
}

func (m *Manager) GetMode() AppMode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentMode
}

func (m *Manager) SetMode(newMode AppMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.currentMode == newMode {
		return nil
	}

	log.Printf("[MODE] Mode change requested: %s -> %s", m.currentMode, newMode)

	switch newMode {
	case ModePCAudio:
		// Shut down wake-word & mic capture completely to free CPU and RAM
		log.Println("[MODE] Completely disabling wake-word detector and closing microphone device...")
		m.detector.Stop()

		if m.udpStopChan != nil {
			close(m.udpStopChan)
		}
		m.udpStopChan = make(chan struct{})

		// Start direct low-latency audio pipe
		go m.player.StartUDPStreamListener(m.udpPort, m.udpStopChan)

	case ModeAssistant:
		// Stop PC audio streaming and reactivate assistant mic listener
		if m.udpStopChan != nil {
			close(m.udpStopChan)
			m.udpStopChan = nil
		}

		ctx := context.Background()
		err := m.detector.Start(ctx, func() {
			log.Println("[NOVABOT] Wake word detected! Playing confirmation ping...")
			pingAudio := audio.GenerateTone(1000.0, 100, 16000)
			_ = m.player.PlayWAV(pingAudio)

			ttsAudio := audio.GenerateTTSAudio(16000)
			_ = m.player.PlayWAV(ttsAudio)
		})
		if err != nil {
			return fmt.Errorf("failed to restart wake word detector: %w", err)
		}
	}

	m.currentMode = newMode
	return nil
}
