package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Config struct {
	Server   string `json:"server"`
	Port     int    `json:"port"`
	HTTPPort int    `json:"http_port"`
	NoSync   bool   `json:"no_sync"`
	Quiet    bool   `json:"quiet"`

	// BootListen is the address the client's boot agent binds to. The
	// server calls this to trigger a GRUB entry change. Default ":9753".
	BootListen string `json:"boot_listen,omitempty"`

	// BootToken is a shared secret between server and client. The client
	// generates one on install if empty; the server stores whatever the
	// client reports. Leave empty to disable auth (LAN-only setups).
	BootToken string `json:"boot_token,omitempty"`
}

const SystemConfigPath = "/etc/novabot/client.json"

func UserConfigPath() string {
	if p := os.Getenv("NOVABOT_CLIENT_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "novabot-client.json"
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "novabot", "client.json")
}

func ResolvePath(override string) string {
	if override != "" {
		return override
	}
	if _, err := os.Stat(SystemConfigPath); err == nil {
		return SystemConfigPath
	}
	return UserConfigPath()
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (c *Config) Validate() error {
	if c.Server == "" {
		return errors.New("server address is required (--server or config file)")
	}
	if c.Port == 0 {
		c.Port = 4000
	}
	if c.HTTPPort == 0 {
		c.HTTPPort = 8080
	}
	if c.BootListen == "" {
		c.BootListen = ":9753"
	}
	if c.BootToken == "" {
		c.BootToken = generateToken()
	}
	return nil
}

func generateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
