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
	udpDoneChan chan struct{}
}

func NewManager(detector wakeword.Detector, player *audio.AudioPlayer, udpPort int) *Manager {
	return &Manager{
		detector: detector,
		player:   player,
		udpPort:  udpPort,
	}
}

func (m *Manager) GetMode() AppMode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentMode
}

func (m *Manager) Player() *audio.AudioPlayer {
	return m.player
}

// SetQuality changes the stream format. If we're currently in PC_AUDIO mode
// this restarts the UDP listener with the new format. Clients pick up the
// change on their next quality poll.
func (m *Manager) SetQuality(name string) error {
	if err := audio.SetActiveQuality(name); err != nil {
		return err
	}
	log.Printf("[MODE] audio quality set to %q", name)

	m.mu.Lock()
	inPCAudio := m.currentMode == ModePCAudio
	m.mu.Unlock()

	if inPCAudio {
		log.Printf("[MODE] restarting UDP listener with new quality")
		if err := m.restartUDPListenerLocked(); err != nil {
			return fmt.Errorf("restart listener: %w", err)
		}
	}
	return nil
}

// restartUDPListenerLocked stops the running listener (if any), waits for
// it to fully exit, then starts a fresh one. Caller must not hold m.mu.
func (m *Manager) restartUDPListenerLocked() error {
	m.mu.Lock()
	stop := m.udpStopChan
	done := m.udpDoneChan
	m.udpStopChan = nil
	m.udpDoneChan = nil
	m.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if done != nil {
		<-done // wait for the old listener goroutine to exit
	}

	stop = make(chan struct{})
	done = make(chan struct{})

	m.mu.Lock()
	m.udpStopChan = stop
	m.udpDoneChan = done
	m.mu.Unlock()

	go func() {
		defer close(done)
		m.player.StartUDPStreamListener(m.udpPort, stop)
	}()

	return nil
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
		log.Println("[MODE] disabling wake-word detector, opening UDP stream")
		m.detector.Stop()

		// Same restart logic, just under the held lock.
		if m.udpStopChan != nil {
			close(m.udpStopChan)
		}
		if m.udpDoneChan != nil {
			<-m.udpDoneChan
		}

		m.udpStopChan = make(chan struct{})
		m.udpDoneChan = make(chan struct{})
		stop := m.udpStopChan
		done := m.udpDoneChan

		go func() {
			defer close(done)
			m.player.StartUDPStreamListener(m.udpPort, stop)
		}()

	case ModeAssistant:
		if m.udpStopChan != nil {
			close(m.udpStopChan)
			m.udpStopChan = nil
		}
		if m.udpDoneChan != nil {
			m.udpDoneChan = nil
		}

		ctx := context.Background()
		err := m.detector.Start(ctx, func() {
			log.Println("[NOVABOT] Wake word detected! Playing confirmation ping...")
			pingAudio := audio.GenerateTone(1000.0, 100, 16000)
			if err := m.player.PlayWAV(pingAudio); err != nil {
				log.Printf("[AUDIO] ping playback failed: %v", err)
			}
			ttsAudio := audio.GenerateTTSAudio(16000)
			if err := m.player.PlayWAV(ttsAudio); err != nil {
				log.Printf("[AUDIO] tts playback failed: %v", err)
			}
		})
		if err != nil {
			return fmt.Errorf("failed to restart wake word detector: %w", err)
		}
	}

	m.currentMode = newMode
	return nil
}
