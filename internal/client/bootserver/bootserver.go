package bootserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ZarirDev/novabot/internal/client/grub"
)

type Server struct {
	addr  string
	token string
	srv   *http.Server
}

func New(addr, token string) *Server {
	return &Server{addr: addr, token: token}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/boot", s.handleBoot)
	mux.HandleFunc("/entries", s.handleEntries)
	mux.HandleFunc("/health", s.handleHealth)

	s.srv = &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	if ip := outboundIP(); ip != "" {
		host, port, _ := net.SplitHostPort(s.addr)
		if host == "" || host == "0.0.0.0" || host == "::" {
			log.Printf("[BOOT] listening on %s — reachable at http://%s:%s",
				s.addr, ip, port)
		} else {
			log.Printf("[BOOT] listening on %s", s.addr)
		}
	} else {
		log.Printf("[BOOT] listening on %s", s.addr)
	}

	go func() {
		<-ctx.Done()
		log.Printf("[BOOT] context cancelled — shutting down http server")
		sdCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.srv.Shutdown(sdCtx); err != nil {
			log.Printf("[BOOT] shutdown error: %v", err)
		}
	}()

	err := s.srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) auth(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	given := r.Header.Get("Authorization")
	want := "Bearer " + s.token
	ok := subtle.ConstantTimeCompare([]byte(given), []byte(want)) == 1
	if !ok {
		log.Printf("[BOOT] auth rejected (got %d-char header, expected %d-char)",
			len(given), len(want))
	}
	return ok
}

func (s *Server) handleBoot(w http.ResponseWriter, r *http.Request) {
	log.Printf("[BOOT] >>> POST /boot from %s", r.RemoteAddr)

	if r.Method != http.MethodPost {
		log.Printf("[BOOT] rejected: method %s", r.Method)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.auth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	raw, _ := io.ReadAll(r.Body)
	log.Printf("[BOOT] raw body: %s", string(raw))

	var req struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		log.Printf("[BOOT] invalid json: %v", err)
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	log.Printf("[BOOT] parsed: id=%q source=%q title=%q", req.ID, req.Source, req.Title)

	if req.Source == "" {
		req.Source = "grub"
		if req.ID == "" {
			req.ID = req.Title
		}
		log.Printf("[BOOT] source missing — defaulting to grub, id=%q", req.ID)
	}

	// Short-circuit: already booted into the target.
	if isCurrentlyRunning(req.ID, req.Source) {
		log.Printf("[BOOT] already booted into %s/%s — no action", req.Source, req.ID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "already-running",
			"id":     req.ID,
			"source": req.Source,
		})
		return
	}

	entry := grub.Entry{
		ID:     req.ID,
		Title:  req.Title,
		Source: req.Source,
	}
	log.Printf("[BOOT] calling grub.SetEntry(%+v)", entry)

	if err := grub.SetEntry(entry); err != nil {
		log.Printf("[BOOT] SetEntry FAILED: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[BOOT] SetEntry OK — next boot will use %s/%s", req.Source, req.ID)

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
	entries := grub.ListAll()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
		"default": grub.Default(),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"host":   hostname(),
	})
}

// isCurrentlyRunning returns true if the running OS corresponds to
// the given boot target. Only meaningful for EFI targets.
func isCurrentlyRunning(id, source string) bool {
	if source != "efi" {
		return false
	}
	for _, e := range grub.ListAll() {
		if e.ID == id && e.Source == "efi" && e.Current {
			return true
		}
	}
	return false
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

func outboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
