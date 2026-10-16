package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

type Config struct {
	Server     string `json:"server"`
	Port       int    `json:"port"`
	HTTPPort   int    `json:"http_port"`
	NoSync     bool   `json:"no_sync"`
	Quiet      bool   `json:"quiet"`
	BootListen string `json:"boot_listen,omitempty"`
	BootToken  string `json:"boot_token,omitempty"`
}

const SystemConfigPath = "/etc/novabot/client.json"

func UserConfigPath() string {
	if p := os.Getenv("NOVABOT_CLIENT_CONFIG"); p != "" {
		return p
	}

	// Windows: use %AppData%\novabot\client.json — the idiomatic
	// location. Fall back to the Linux-style path if AppData isn't
	// set for some reason.
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("AppData"); appData != "" {
			return filepath.Join(appData, "novabot", "client.json")
		}
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
	// System config is Linux-only; ignore it on Windows.
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(SystemConfigPath); err == nil {
			return SystemConfigPath
		}
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