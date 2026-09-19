package mode

import (
	"context"
	"testing"

	"github.com/ZarirDev/novabot/internal/audio"
)

type MockDetector struct {
	running bool
}

func (m *MockDetector) Start(ctx context.Context, onDetected func()) error {
	m.running = true
	return nil
}

func (m *MockDetector) Stop() {
	m.running = false
}

func TestModeTransitions(t *testing.T) {
	detector := &MockDetector{}
	player := audio.NewAudioPlayer()
	mgr := NewManager(detector, player, 4000)

	_ = mgr.SetMode(ModePCAudio)
	if detector.running {
		t.Fatalf("Detector should stop in PC_AUDIO mode")
	}

	_ = mgr.SetMode(ModeAssistant)
	if !detector.running {
		t.Fatalf("Detector should run in ASSISTANT mode")
	}
}
