package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ZarirDev/novabot/internal/wol"
)

type WoLServer struct {
	store *wol.Store
}

func NewWoLServer(path string) (*WoLServer, error) {
	store, err := wol.NewStore(path)
	if err != nil {
		return nil, err
	}
	return &WoLServer{store: store}, nil
}

// DefaultWoLDevicesPath resolves where the manual device list lives.
func DefaultWoLDevicesPath() string {
	if p := os.Getenv("WOL_DEVICES_FILE"); p != "" {
		return p
	}
	return "wol-devices.json"
}

func (s *WoLServer) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/wol/devices", s.handleDevices)
	mux.HandleFunc("/api/v1/wol/devices/", s.handleDevice)
	mux.HandleFunc("/api/v1/wol/scan", s.handleScan)
	mux.HandleFunc("/api/v1/wol/wake/", s.handleWake)
}

func (s *WoLServer) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *WoLServer) handleDevices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writeJSON(w, http.StatusOK, map[string]any{"devices": s.store.List()})
	case http.MethodPost:
		var d wol.Device
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Name == "" || d.MAC == "" {
			http.Error(w, "name and mac are required", http.StatusBadRequest)
			return
		}
		if err := s.store.Add(d); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.writeJSON(w, http.StatusCreated, d)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *WoLServer) handleDevice(w http.ResponseWriter, r *http.Request) {
	mac := strings.TrimPrefix(r.URL.Path, "/api/v1/wol/devices/")
	if mac == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if err := s.store.Remove(mac); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *WoLServer) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	found := wol.ScanLAN(ctx)
	s.store.Merge(found)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"found":   len(found),
		"devices": s.store.List(),
	})
}

func (s *WoLServer) handleWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mac := strings.TrimPrefix(r.URL.Path, "/api/v1/wol/wake/")
	d, ok := s.store.Get(mac)
	if !ok {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	if err := wol.SendToAll(d.MAC, d.IP, d.Broadcast, d.Port); err != nil {
		log.Printf("wol: send to %s failed: %v", d.MAC, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("wol: sent wake to %s (%s)", d.Name, d.MAC)
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "sent", "mac": d.MAC})
}
