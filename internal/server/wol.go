package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	mux.HandleFunc("/api/v1/wol/boot-register", s.handleBootRegister)
	mux.HandleFunc("/api/v1/wol/boot/", s.handleBootNext)
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
		log.Printf("[WOL] saved device %q (%s, ip=%s)", d.Name, d.MAC, d.IP)
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
		log.Printf("[WOL] removed device %s", mac)
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
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	start := time.Now()
	found := wol.ScanLAN(ctx)
	log.Printf("[WOL] scan found %d devices in %s",
		len(found), time.Since(start).Round(time.Millisecond))

	s.writeJSON(w, http.StatusOK, map[string]any{
		"found":   len(found),
		"devices": found,
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
		log.Printf("[WOL] send to %s failed: %v", d.MAC, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[WOL] sent wake to %q (%s)", d.Name, d.MAC)
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "sent", "mac": d.MAC})
}

// handleBootRegister accepts a POST from a novabot-client on the LAN
// describing its GRUB entries. The device is upserted by MAC.
func (s *WoLServer) handleBootRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var reg struct {
		Hostname  string          `json:"hostname"`
		MAC       string          `json:"mac"`
		IP        string          `json:"ip"`
		BootAddr  string          `json:"boot_addr"`
		BootToken string          `json:"boot_token"`
		Entries   []wol.BootEntry `json:"entries"`
		Default   string          `json:"default"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if reg.MAC == "" {
		http.Error(w, "mac is required", http.StatusBadRequest)
		return
	}

	d, exists := s.store.Get(reg.MAC)
	if !exists {
		d = wol.Device{
			Name: reg.Hostname,
			MAC:  reg.MAC,
			IP:   reg.IP,
		}
		if d.Name == "" {
			d.Name = reg.IP
		}
	}
	if reg.Hostname != "" && (d.Name == "" || d.Name == d.IP) {
		d.Name = reg.Hostname
	}
	d.IP = reg.IP
	d.BootAddr = reg.BootAddr
	d.BootToken = reg.BootToken
	d.BootEntries = reg.Entries
	d.BootDefault = reg.Default

	if err := s.store.Add(d); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("[WOL] boot-register: %q (%s) · %d entries · addr=%s",
		d.Name, d.MAC, len(reg.Entries), reg.BootAddr)

	s.writeJSON(w, http.StatusOK, d)
}

// handleBootNext tells a registered client to set its next GRUB entry.
// POST /api/v1/wol/boot/{mac}  body: {"title": "..."}
func (s *WoLServer) handleBootNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mac := strings.TrimPrefix(r.URL.Path, "/api/v1/wol/boot/")
	d, ok := s.store.Get(mac)
	if !ok {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	if d.BootAddr == "" {
		http.Error(w, "device has no boot agent registered", http.StatusBadRequest)
		return
	}

	var req struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Title  string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.ID == "" && req.Title == "" {
		http.Error(w, "id or title required", http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		// Backwards compat: older UI sent just a title, meaning GRUB.
		req.Source = "grub"
		if req.ID == "" {
			req.ID = req.Title
		}
	}

	if err := callBootAgent(d.BootAddr, d.BootToken, req.ID, req.Source, req.Title); err != nil {
		log.Printf("[WOL] boot %q (%s) on %q failed: %v", req.Title, req.Source, d.Name, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("[WOL] boot %q via %s on %q (%s) — next reboot",
		req.Title, req.Source, d.Name, d.MAC)

	s.writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"mac":    d.MAC,
		"title":  req.Title,
		"source": req.Source,
	})
}

func callBootAgent(addr, token, id, source, title string) error {
	body, _ := json.Marshal(map[string]string{
		"id":     id,
		"source": source,
		"title":  title,
	})
	req, err := http.NewRequest("POST", strings.TrimRight(addr, "/")+"/boot",
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("boot agent returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
