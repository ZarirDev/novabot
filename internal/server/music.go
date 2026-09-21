package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/ZarirDev/novabot/internal/music"
)

type MusicServer struct {
	player *music.Player
}

func NewMusicServer() *MusicServer {
	p := music.NewPlayer()
	if err := p.Start(); err != nil {
		// mpv not installed or socket failed — log once, endpoints will
		// return errors, but the rest of the bot keeps running.
		log.Printf("[MUSIC] player unavailable: %v", err)
	}
	return &MusicServer{player: p}
}

func (m *MusicServer) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/music/search", m.handleSearch)
	mux.HandleFunc("/api/v1/music/play", m.handlePlay)
	mux.HandleFunc("/api/v1/music/pause", m.handlePause)
	mux.HandleFunc("/api/v1/music/next", m.handleNext)
	mux.HandleFunc("/api/v1/music/prev", m.handlePrev)
	mux.HandleFunc("/api/v1/music/seek", m.handleSeek)
	mux.HandleFunc("/api/v1/music/volume", m.handleVolume)
	mux.HandleFunc("/api/v1/music/status", m.handleStatus)
	mux.HandleFunc("/api/v1/music/stop", m.handleStop)
}

func (m *MusicServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, `{"error":"missing q"}`, http.StatusBadRequest)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10
	}

	log.Printf("[MUSIC] HTTP /search q=%q limit=%d", q, limit)

	tracks, err := music.Search(q, limit)
	if err != nil {
		log.Printf("[MUSIC] HTTP /search error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"tracks": tracks,
		"count":  len(tracks),
	})
}

func (m *MusicServer) handlePlay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		http.Error(w, `{"error":"missing url"}`, http.StatusBadRequest)
		return
	}

	if err := m.player.Start(); err != nil {
		http.Error(w, `{"error":"mpv not available"}`, http.StatusServiceUnavailable)
		return
	}
	if err := m.player.Load(req.URL); err != nil {
		http.Error(w, `{"error":"play failed"}`, http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]string{"status": "playing"})
}

func (m *MusicServer) handlePause(w http.ResponseWriter, r *http.Request) {
	if err := m.player.TogglePause(); err != nil {
		http.Error(w, `{"error":"pause failed"}`, http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (m *MusicServer) handleNext(w http.ResponseWriter, r *http.Request) {
	_ = m.player.Next()
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (m *MusicServer) handlePrev(w http.ResponseWriter, r *http.Request) {
	_ = m.player.Prev()
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (m *MusicServer) handleStop(w http.ResponseWriter, r *http.Request) {
	_ = m.player.StopPlayback()
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (m *MusicServer) handleSeek(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Position float64 `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if err := m.player.Seek(req.Position); err != nil {
		http.Error(w, `{"error":"seek failed"}`, http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (m *MusicServer) handleVolume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Volume float64 `json:"volume"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if err := m.player.SetVolume(req.Volume); err != nil {
		http.Error(w, `{"error":"volume failed"}`, http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (m *MusicServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(m.player.Status())
}
