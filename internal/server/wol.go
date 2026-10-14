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
	mux.HandleFunc("/api/v1/wol/boot-checkin", s.handleBootCheckin)
	mux.HandleFunc("/api/v1/wol/boot/", s.handleBootDispatch)
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
	log.Printf("[WOL-WAKE] sending WoL to %q (%s)", d.Name, d.MAC)
	if err := wol.SendToAll(d.MAC, d.IP, d.Broadcast, d.Port); err != nil {
		log.Printf("[WOL-WAKE] send failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[WOL-WAKE] sent")
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "sent", "mac": d.MAC})
}

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

	// Preserve pending boot across re-registration.
	existing, _ := s.store.Get(reg.MAC)
	d.PendingBoot = existing.PendingBoot

	if err := s.store.Add(d); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("[WOL-REG] %q (%s) ip=%s bootAddr=%s entries=%d pending=%v",
		d.Name, d.MAC, d.IP, d.BootAddr, len(reg.Entries), d.PendingBoot != nil)
	s.writeJSON(w, http.StatusOK, d)
}

// handleBootDispatch routes POST (set next boot) and DELETE (cancel
// pending) on /api/v1/wol/boot/{mac}.
func (s *WoLServer) handleBootDispatch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleBootNext(w, r)
	case http.MethodDelete:
		s.handleBootCancel(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *WoLServer) handleBootNext(w http.ResponseWriter, r *http.Request) {
	mac := strings.TrimPrefix(r.URL.Path, "/api/v1/wol/boot/")
	log.Printf("[WOL-BOOT] ─── POST /boot/%s from %s ───", mac, r.RemoteAddr)

	d, ok := s.store.Get(mac)
	if !ok {
		log.Printf("[WOL-BOOT] unknown device %q", mac)
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}

	var req struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Title  string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[WOL-BOOT] invalid body: %v", err)
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		req.Source = "grub"
		if req.ID == "" {
			req.ID = req.Title
		}
	}
	log.Printf("[WOL-BOOT] device=%q ip=%s bootAddr=%s pending=%v",
		d.Name, d.IP, d.BootAddr, d.PendingBoot != nil)
	log.Printf("[WOL-BOOT] request id=%q source=%q title=%q",
		req.ID, req.Source, req.Title)

	// ── Default (wake-only) mode ──────────────────────────────
	isDefault := req.Source == "default" ||
		req.ID == "default" ||
		req.ID == "__default__"
	if isDefault {
		log.Printf("[WOL-BOOT] source=default → wake-only, no boot change")
		if err := wol.SendToAll(d.MAC, d.IP, d.Broadcast, d.Port); err != nil {
			log.Printf("[WOL-BOOT] wake-only WoL failed: %v", err)
			http.Error(w, "wol send failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[WOL-BOOT] wake-only WoL sent to %s", d.MAC)
		s.writeJSON(w, http.StatusOK, map[string]string{
			"status": "woke",
			"mode":   "wol-only",
			"mac":    d.MAC,
		})
		return
	}

	// ── Direct path: agent reachable ─────────────────────────
	if d.BootAddr != "" {
		alive := agentAlive(d.BootAddr, 1500*time.Millisecond)
		log.Printf("[WOL-BOOT] agent probe %s → alive=%v", d.BootAddr, alive)
		if alive {
			log.Printf("[WOL-BOOT] trying direct send")
			if err := callBootAgent(d.BootAddr, d.BootToken, req.ID, req.Source, req.Title); err == nil {
				log.Printf("[WOL-BOOT] direct send OK")
				s.writeJSON(w, http.StatusOK, map[string]string{
					"status": "sent",
					"mode":   "direct",
					"mac":    d.MAC,
					"id":     req.ID,
					"source": req.Source,
				})
				return
			} else {
				log.Printf("[WOL-BOOT] direct send failed: %v — falling back to WoL+queue", err)
			}
		}
	} else {
		log.Printf("[WOL-BOOT] no bootAddr on file — cannot direct send")
	}

	// ── Deferred path: queue + WoL ───────────────────────────
	log.Printf("[WOL-BOOT] queueing pending boot id=%s source=%s",
		req.ID, req.Source)
	d.PendingBoot = &wol.PendingBoot{
		ID:     req.ID,
		Source: req.Source,
		Title:  req.Title,
		SetAt:  time.Now(),
	}
	if err := s.store.Add(d); err != nil {
		log.Printf("[WOL-BOOT] save pending failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[WOL-BOOT] pending boot saved")

	log.Printf("[WOL-BOOT] sending WoL to %s", d.MAC)
	if err := wol.SendToAll(d.MAC, d.IP, d.Broadcast, d.Port); err != nil {
		log.Printf("[WOL-BOOT] WoL send failed: %v", err)
		http.Error(w, "wol send failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[WOL-BOOT] WoL sent, pending boot queued for checkin")
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status": "queued",
		"mode":   "wol",
		"mac":    d.MAC,
		"id":     req.ID,
		"source": req.Source,
	})
}

func (s *WoLServer) handleBootCancel(w http.ResponseWriter, r *http.Request) {
	mac := strings.TrimPrefix(r.URL.Path, "/api/v1/wol/boot/")
	log.Printf("[WOL-BOOT] ─── DELETE /boot/%s from %s ───", mac, r.RemoteAddr)

	d, ok := s.store.Get(mac)
	if !ok {
		log.Printf("[WOL-BOOT] cancel: unknown device %q", mac)
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	if d.PendingBoot == nil {
		log.Printf("[WOL-BOOT] cancel: nothing pending for %q", d.Name)
		s.writeJSON(w, http.StatusOK, map[string]string{
			"status": "no-pending",
			"mac":    d.MAC,
		})
		return
	}
	was := d.PendingBoot
	d.PendingBoot = nil
	if err := s.store.Add(d); err != nil {
		log.Printf("[WOL-BOOT] cancel: save failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[WOL-BOOT] cancelled pending boot %s/%s for %q",
		was.Source, was.ID, d.Name)
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status": "cancelled",
		"mac":    d.MAC,
		"was":    was.Title,
	})
}

// handleBootCheckin: called by a client on startup. Asks "do you have a
// pending boot for me?".
func (s *WoLServer) handleBootCheckin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		MAC      string `json:"mac"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MAC == "" {
		http.Error(w, "mac required", http.StatusBadRequest)
		return
	}
	log.Printf("[WOL-CHECKIN] from %s (%s)", req.Hostname, req.MAC)

	d, ok := s.store.Get(req.MAC)
	if !ok {
		log.Printf("[WOL-CHECKIN] unknown device %s", req.MAC)
		s.writeJSON(w, http.StatusOK, map[string]string{
			"action": "none",
			"reason": "unknown-device",
		})
		return
	}

	if d.PendingBoot == nil {
		log.Printf("[WOL-CHECKIN] %q has no pending boot", d.Name)
		s.writeJSON(w, http.StatusOK, map[string]string{"action": "none"})
		return
	}

	if time.Since(d.PendingBoot.SetAt) > 10*time.Minute {
		log.Printf("[WOL-CHECKIN] %q pending boot expired (set %s ago)",
			d.Name, time.Since(d.PendingBoot.SetAt).Round(time.Second))
		d.PendingBoot = nil
		_ = s.store.Add(d)
		s.writeJSON(w, http.StatusOK, map[string]string{
			"action": "none",
			"reason": "expired",
		})
		return
	}

	pb := d.PendingBoot
	d.PendingBoot = nil
	if err := s.store.Add(d); err != nil {
		log.Printf("[WOL-CHECKIN] clear pending: %v", err)
	}
	log.Printf("[WOL-CHECKIN] delivering pending boot %s/%s to %q",
		pb.Source, pb.ID, d.Name)

	s.writeJSON(w, http.StatusOK, map[string]string{
		"action": "set-and-reboot",
		"id":     pb.ID,
		"source": pb.Source,
		"title":  pb.Title,
	})
}

func agentAlive(addr string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	url := strings.TrimRight(addr, "/") + "/health"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func callBootAgent(addr, token, id, source, title string) error {
	body, _ := json.Marshal(map[string]string{
		"id":     id,
		"source": source,
		"title":  title,
	})
	url := strings.TrimRight(addr, "/") + "/boot"
	log.Printf("[WOL-BOOT] → POST %s body=%s", url, string(body))
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
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
		return fmt.Errorf("connect to %s: %w", url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[WOL-BOOT] ← %d %s body=%q",
		resp.StatusCode, http.StatusText(resp.StatusCode),
		strings.TrimSpace(string(respBody)))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("boot agent returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}
