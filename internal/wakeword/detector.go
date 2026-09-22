package wakeword

import (
	"context"
	"log"
	"os"
)

// Detector is the wake-word engine interface.
type Detector interface {
	Start(ctx context.Context, onDetected func()) error
	Stop()
}

// NewDetector constructs the openWakeWord detector. This is now the only
// backend — the Vosk and heuristic fallbacks were removed to eliminate
// ~40 MB of model downloads and a CGO dependency on libvosk.
func NewDetector(wakeWord string) Detector {
	runtimePath := getEnv("ONNX_RUNTIME_PATH", "runtime/libonnxruntime.so")
	modelDir := getEnv("OWW_MODEL_DIR", "models")
	wakeModel := getEnv("WAKE_MODEL_FILE", "hey_nova.onnx")

	d, err := NewOpenWakeWordDetector(wakeWord, runtimePath, modelDir, wakeModel)
	if err != nil {
		log.Fatalf("[WAKEWORD] openWakeWord init failed: %v", err)
	}
	return d
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
