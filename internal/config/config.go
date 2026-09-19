package config

import (
	"os"
	"strconv"
)

type Config struct {
	HTTPPort     string
	UDPAudioPort int
	WakeWord     string
	DetectorType string // "vosk" | "heuristic"
	ModelPath    string // path to the vosk model directory
}

func Load() *Config {
	return &Config{
		HTTPPort:     getEnv("HTTP_PORT", "8080"),
		UDPAudioPort: getEnvAsInt("UDP_AUDIO_PORT", 4000),
		WakeWord:     getEnv("WAKE_WORD", "nova"),
		DetectorType: getEnv("DETECTOR_TYPE", "vosk"),
		ModelPath:    getEnv("MODEL_PATH", "/opt/vosk-models/default"),
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
