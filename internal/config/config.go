package config

import (
	"os"
	"strconv"
)

type Config struct {
	HTTPPort     string
	UDPAudioPort int
	WakeWord     string
}

func Load() *Config {
	return &Config{
		HTTPPort:     getEnv("HTTP_PORT", "8080"),
		UDPAudioPort: getEnvAsInt("UDP_AUDIO_PORT", 4000),
		WakeWord:     getEnv("WAKE_WORD", "nova"),
	}
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func getEnvAsInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
