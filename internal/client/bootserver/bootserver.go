package bootserver

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/ZarirDev/novabot/internal/client/grub"
)

// Server exposes this machine's GRUB entries over HTTP so the novabot
// server can list them and trigger a next-boot change.
type Server struct {
	addr  string
	token string
}

func New(addr, token string) *Server {
	return &Server{addr: addr, token: token}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/boot", s.handleBoot)
	mux.HandleFunc("/entries", s.handleEntries)
	mux.HandleFunc("/health", s.handleHealth)

	srv := &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Log the reachable address(es) so the user knows what to expect.
	if ip := outboundIP(); ip != "" {
		host, port, _ := net.SplitHostPort(s.addr)
		if host == "" || host == "0.0.0.0" || host == "::" {
			log.Printf("[BOOTAGENT] listening on %s — reachable at http://%s:%s",
				s.addr, ip, port)
		} else {
			log.Printf("[BOOTAGENT] listening on %s", s.addr)
		}
	} else {
		log.Printf("[BOOTAGENT] listening on %s", s.addr)
	}

	return srv.ListenAndServe()
}

func (s *Server) auth(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	given := r.Header.Get("Authorization")
	want := "Bearer " + s.token
	return subtle.ConstantTimeCompare([]byte(given), []byte(want)) == 1
}

func (s *Server) handleBoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.auth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
	if req.Source == "" {
		req.Source = "grub"
		if req.ID == "" {
			req.ID = req.Title
		}
	}

	if err := grub.SetEntry(grub.Entry{
		ID:     req.ID,
		Title:  req.Title,
		Source: req.Source,
	}); err != nil {
		log.Printf("[BOOTAGENT] set next failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[BOOTAGENT] next boot → %s (%s)", req.ID, req.Source)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"id":     req.ID,
		"source": req.Source,
	})
}

func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	if !s.auth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	entries, err := grub.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
		"default": grub.Default(),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"host":   hostname(),
	})
}

func hostname() string {
	h, _ := net.LookupCNAME("localhost")
	if h == "" {
		return "unknown"
	}
	return fmt.Sprint(h)
}

func outboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
