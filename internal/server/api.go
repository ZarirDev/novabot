package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

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
