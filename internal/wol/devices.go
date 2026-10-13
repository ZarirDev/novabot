package wol

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Device is one WoL target.
type Device struct {
	Name      string `json:"name"`
	MAC       string `json:"mac"`
	IP        string `json:"ip,omitempty"`
	Broadcast string `json:"broadcast,omitempty"`
	Port      int    `json:"port,omitempty"`
	Manual    bool   `json:"manual"`
}

// Store keeps two maps: manually added devices (persisted to disk) and
// scanned devices (ephemeral, refreshed on every scan). Manual entries
// always win in List/Get.
type Store struct {
	mu      sync.RWMutex
	path    string
	manual  map[string]Device
	scanned map[string]Device
}

func NewStore(path string) (*Store, error) {
	s := &Store{
		path:    path,
		manual:  make(map[string]Device),
		scanned: make(map[string]Device),
	}
	if err := s.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var list []Device
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	for _, d := range list {
		d.Manual = true
		s.manual[normalizeMAC(d.MAC)] = d
	}
	return nil
}

func (s *Store) save() error {
	list := make([]Device, 0, len(s.manual))
	for _, d := range s.manual {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(s.path, data, 0o644)
}

// List returns manual + scanned, manual taking precedence.
func (s *Store) List() []Device {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Device, 0, len(s.manual)+len(s.scanned))
	seen := make(map[string]struct{}, len(s.manual))
	for k, d := range s.manual {
		out = append(out, d)
		seen[k] = struct{}{}
	}
	for k, d := range s.scanned {
		if _, ok := seen[k]; ok {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Add inserts or replaces a manual device and persists.
func (s *Store) Add(d Device) error {
	hw, err := ParseMAC(d.MAC)
	if err != nil {
		return err
	}
	d.MAC = formatMAC(hw)
	d.Manual = true
	if d.Port == 0 {
		d.Port = 9
	}
	if d.Name == "" {
		d.Name = d.MAC
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manual[normalizeMAC(d.MAC)] = d
	return s.save()
}

// Remove deletes a manual entry (and hides the scanned entry so it
// doesn't reappear until the next scan).
func (s *Store) Remove(mac string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := normalizeMAC(mac)
	delete(s.manual, key)
	delete(s.scanned, key)
	return s.save()
}

func (s *Store) Get(mac string) (Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := normalizeMAC(mac)
	if d, ok := s.manual[key]; ok {
		return d, true
	}
	d, ok := s.scanned[key]
	return d, ok
}

// Merge replaces the scanned set with the latest discovery results.
// Manual entries are never overwritten.
func (s *Store) Merge(scanned []Device) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]Device, len(scanned))
	for _, d := range scanned {
		if d.MAC == "" {
			continue
		}
		if d.Port == 0 {
			d.Port = 9
		}
		if d.Name == "" {
			d.Name = d.IP
		}
		next[normalizeMAC(d.MAC)] = d
	}
	s.scanned = next
}
