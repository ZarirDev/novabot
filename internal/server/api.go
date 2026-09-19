package server

import (
	"encoding/json"
	"net/http"

	"github.com/ZarirDev/novabot/internal/mode"
)

type Server struct {
	mgr *mode.Manager
}

type ModeRequest struct {
	Mode string `json:"mode"`
}

type ModeResponse struct {
	CurrentMode string `json:"current_mode"`
	Status      string `json:"status"`
}

func NewServer(mgr *mode.Manager) *Server {
	return &Server{mgr: mgr}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/mode", s.handleMode)
	mux.HandleFunc("/api/v1/health", s.handleHealth)
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
