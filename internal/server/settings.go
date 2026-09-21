package server

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
)

// Settings holds runtime-tunable toggles that survive across HTTP requests
// but not across bot restarts. Persist to disk later if you want.
type Settings struct {
	mu sync.RWMutex

	PCAudioEnabled bool `json:"pc_audio_enabled"`
}

func NewSettings() *Settings {
	return &Settings{
		PCAudioEnabled: true,
	}
}

func (s *Settings) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Settings{PCAudioEnabled: s.PCAudioEnabled}
}

func (s *Settings) SetPCAudioEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PCAudioEnabled = enabled
	log.Printf("[SETTINGS] pc_audio_enabled = %v", enabled)
}

// HTTP handlers

func (s *Settings) HandleGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(s.Get())
}

func (s *Settings) HandlePost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PCAudioEnabled *bool `json:"pc_audio_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.PCAudioEnabled != nil {
		s.SetPCAudioEnabled(*req.PCAudioEnabled)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.Get())
}
