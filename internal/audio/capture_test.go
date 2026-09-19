package audio

import (
	"math"
	"testing"
)

func TestCalculateRMS(t *testing.T) {
	// Pure sine wave mock samples
	samples := make([]int16, 160)
	for i := range samples {
		samples[i] = int16(32767.0 * math.Sin(2.0*math.Pi*440.0*float64(i)/16000.0))
	}

	rms := CalculateRMS(samples)
	if rms < 15000.0 || rms > 25000.0 {
		t.Errorf("Expected RMS around ~23170 for full scale sine wave, got %.2f", rms)
	}
}
