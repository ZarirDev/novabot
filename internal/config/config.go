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

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

func getEnvAsInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}
