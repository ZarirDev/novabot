package audio

import (
	"testing"
)

func TestGenerateTone(t *testing.T) {
	sampleRate := 44100
	durationMs := 100
	pcmData := GenerateTone(440.0, durationMs, sampleRate)

	// Header (44 bytes) + PCM data
	expectedPCMBytes := (sampleRate * durationMs / 1000) * 2
	expectedTotalSize := 44 + expectedPCMBytes

	if len(pcmData) != expectedTotalSize {
		t.Errorf("expected generated WAV size of %d bytes, got %d", expectedTotalSize, len(pcmData))
	}

	if string(pcmData[0:4]) != "RIFF" || string(pcmData[8:12]) != "WAVE" {
		t.Errorf("invalid WAV header signature")
	}
}

func TestGenerateTTSAudio(t *testing.T) {
	ttsData := GenerateTTSAudio(44100)
	if len(ttsData) == 0 {
		t.Fatalf("TTS audio payload should not be empty")
	}
}
