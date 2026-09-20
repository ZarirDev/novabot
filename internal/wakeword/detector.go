package wakeword

import (
	"context"
	"log"
	"os"
)

type Detector interface {
	Start(ctx context.Context, onDetected func()) error
	Stop()
}

// NewDetector picks the configured backend, falling back gracefully on failure.
//
// DETECTOR_TYPE:
//
//	openwakeword → OpenWakeWord (recommended for wake word)
//	vosk         → Vosk with restricted grammar (legacy path)
//	heuristic    → RMS + zero-crossing (last resort)
func NewDetector(detectorType, wakeWord, modelPath string) Detector {
	switch detectorType {
	case "openwakeword":
		runtimePath := getEnv("ONNX_RUNTIME_PATH", ".dev/runtime/libonnxruntime.so")
		owwDir := getEnv("OWW_MODEL_DIR", ".dev/models/oww")
		wakeModel := getEnv("WAKE_MODEL_FILE", "hey_jarvis_v0.1.onnx")

		d, err := NewOpenWakeWordDetector(wakeWord, runtimePath, owwDir, wakeModel)
		if err != nil {
			log.Printf("[WAKEWORD] openWakeWord unavailable (%v) → trying Vosk", err)
			return fallbackToVosk(wakeWord, modelPath)
		}
		return d

	case "vosk":
		return fallbackToVosk(wakeWord, modelPath)

	default:
		log.Printf("[WAKEWORD] unknown detector %q → heuristic", detectorType)
		return NewHeuristicDetector(wakeWord)
	}
}

func fallbackToVosk(wakeWord, modelPath string) Detector {
	d, err := NewVoskDetector(wakeWord, modelPath)
	if err != nil {
		log.Printf("[WAKEWORD] Vosk unavailable (%v) → heuristic fallback", err)
		return NewHeuristicDetector(wakeWord)
	}
	return d
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
