package openwakeword

const (
	// SampleRate is the required sample rate for all input audio.
	SampleRate = 16000

	// FrameSamples is the engine's default 80 ms frame size at SampleRate.
	FrameSamples = 1280
)

// Samples contains mono float32 PCM samples normalized to roughly [-1, 1].
type Samples []float32

func pcm16ToSample(value int16) float32 {
	if value < 0 {
		return float32(value) / 32768
	}
	return float32(value) / 32767
}

func sampleToPCM16Float(value float32) float32 {
	if value < -1 {
		value = -1
	} else if value > 1 {
		value = 1
	}
	if value < 0 {
		return value * 32768
	}
	return value * 32767
}
