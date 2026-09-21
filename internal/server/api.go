package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"

	"github.com/ZarirDev/novabot/internal/audio"
	"github.com/ZarirDev/novabot/internal/mode"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	mgr   *mode.Manager
	stats *StatsCollector
	music *MusicServer
}

type ModeRequest struct {
	Mode string `json:"mode"`
}

type ModeResponse struct {
	CurrentMode string `json:"current_mode"`
	Status      string `json:"status"`
}

func NewServer(mgr *mode.Manager) *Server {
	stats := NewStatsCollector()
	stats.Start()

	return &Server{
		mgr:   mgr,
		stats: stats,
		music: NewMusicServer(),
	}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/mode", s.handleMode)
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/stats", s.handleStats)
	mux.HandleFunc("/api/v1/audio/status", s.handleAudioStatus)
	mux.HandleFunc("/api/v1/audio/quality", s.handleAudioQuality)
	mux.HandleFunc("/api/v1/audio/volume", s.handleAudioVolume)
	mux.HandleFunc("/api/v1/settings", s.handleSettings)

	s.music.RegisterRoutes(mux)

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(s.stats.Snapshot())
}

func (s *Server) handleAudioStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(s.mgr.Player().UDPStatus())
}

// handleSettings is a thin proxy over the mode system. The "setting"
// pc_audio_enabled is derived from the current mode — there is no separate
// state. Toggling it on switches to PC_AUDIO mode, toggling it off
// switches to ASSISTANT. This matches user expectation: the toggle does
// exactly what it says.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]bool{
			"pc_audio_enabled": s.mgr.GetMode() == mode.ModePCAudio,
		})

	case http.MethodPost:
		var req struct {
			PCAudioEnabled *bool `json:"pc_audio_enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}

		if req.PCAudioEnabled != nil {
			target := mode.ModeAssistant
			if *req.PCAudioEnabled {
				target = mode.ModePCAudio
			}
			if s.mgr.GetMode() != target {
				log.Printf("[SETTINGS] toggle → switching to %s", target)
				if err := s.mgr.SetMode(target); err != nil {
					http.Error(w, `{"error":"failed to switch mode"}`, http.StatusInternalServerError)
					return
				}
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]bool{
			"pc_audio_enabled": s.mgr.GetMode() == mode.ModePCAudio,
		})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAudioQuality(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"current": audio.ActiveQuality(),
			"presets": audio.AllQualities(),
		})

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if err := s.mgr.SetQuality(req.Name); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"current": audio.ActiveQuality(),
			"status":  "applied",
		})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(ModeResponse{
			CurrentMode: string(s.mgr.GetMode()),
			Status:      "ok",
		})
		return
	}

	if r.Method == http.MethodPost {
		var req ModeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}

		targetMode := mode.AppMode(req.Mode)
		if targetMode != mode.ModeAssistant && targetMode != mode.ModePCAudio {
			http.Error(w, `{"error":"invalid mode"}`, http.StatusBadRequest)
			return
		}

		if err := s.mgr.SetMode(targetMode); err != nil {
			http.Error(w, `{"error":"failed to change mode"}`, http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(ModeResponse{
			CurrentMode: string(s.mgr.GetMode()),
			Status:      "switched",
		})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (s *Server) handleAudioVolume(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]int{
			"percent": audio.Volume(),
		})

	case http.MethodPost:
		var req struct {
			Percent *int `json:"percent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Percent == nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		applied := audio.SetVolume(*req.Percent)
		log.Printf("[AUDIO] volume set to %d%%", applied)
		_ = json.NewEncoder(w).Encode(map[string]int{
			"percent": applied,
		})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
