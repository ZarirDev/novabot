package wakeword

import (
	"context"
	"log"
)

type Detector interface {
	Start(ctx context.Context, onDetected func()) error
	Stop()
}

// NewDetector picks the configured backend, gracefully falling back to the
// legacy heuristic if the Vosk model can't be loaded.
func NewDetector(detectorType, wakeWord, modelPath string) Detector {
	if detectorType == "vosk" {
		d, err := NewVoskDetector(wakeWord, modelPath)
		if err != nil {
			log.Printf("[WAKEWORD] Vosk unavailable (%v) → heuristic fallback", err)
			return NewHeuristicDetector(wakeWord)
		}
		return d
	}
	return NewHeuristicDetector(wakeWord)
}
