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

type BootEntry struct {
	Index  int    `json:"index"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Source string `json:"source"`
	Active bool   `json:"active,omitempty"`
}

type Device struct {
	Name      string `json:"name"`
	MAC       string `json:"mac"`
	IP        string `json:"ip,omitempty"`
	Broadcast string `json:"broadcast,omitempty"`
	Port      int    `json:"port,omitempty"`
	Manual    bool   `json:"manual"`

	// Boot-agent fields. Populated when a novabot-client on the same
	// machine registers its GRUB entries with us.
	BootAddr    string      `json:"boot_addr,omitempty"`  // http://ip:port
	BootToken   string      `json:"boot_token,omitempty"` // shared secret
	BootEntries []BootEntry `json:"boot_entries,omitempty"`
	BootDefault string      `json:"boot_default,omitempty"`
}

type Store struct {
	mu     sync.RWMutex
	path   string
	manual map[string]Device
}

func NewStore(path string) (*Store, error) {
	s := &Store{
		path:   path,
		manual: make(map[string]Device),
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

func (s *Store) List() []Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Device, 0, len(s.manual))
	for _, d := range s.manual {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

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
		d.Name = d.IP
	}
	if d.Name == "" {
		d.Name = d.MAC
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Preserve existing boot info across manual edits that don't
	// include it — the boot registration path sets it explicitly.
	existing, hadExisting := s.manual[normalizeMAC(d.MAC)]
	if hadExisting {
		if d.BootAddr == "" {
			d.BootAddr = existing.BootAddr
		}
		if d.BootToken == "" {
			d.BootToken = existing.BootToken
		}
		if len(d.BootEntries) == 0 {
			d.BootEntries = existing.BootEntries
		}
		if d.BootDefault == "" {
			d.BootDefault = existing.BootDefault
		}
	}
	s.manual[normalizeMAC(d.MAC)] = d
	return s.save()
}

func (s *Store) Remove(mac string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.manual, normalizeMAC(mac))
	return s.save()
}

func (s *Store) Get(mac string) (Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.manual[normalizeMAC(mac)]
	return d, ok
}
