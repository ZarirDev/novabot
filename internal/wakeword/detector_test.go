package wakeword

import (
	"testing"
)

func TestZeroCrossings(t *testing.T) {
	// Alternating samples = max zero crossings
	samples := []int16{100, -100, 100, -100, 100, -100}
	zc := calculateZeroCrossings(samples)
	if zc != 5 {
		t.Errorf("expected 5 zero crossings, got %d", zc)
	}
}
