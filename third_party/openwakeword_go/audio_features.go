package openwakeword

import (
	"errors"
	"fmt"
)

const (
	melBins              = 32
	melWindowFrames      = 76
	embeddingSize        = 96
	featureHistoryFrames = 120
	rawHistorySamples    = SampleRate * 10
	melHistoryFrames     = 970
)

type AudioFeatures struct {
	melspec   *onnxSession
	embedding *onnxSession
	raw       Samples
	remainder Samples
	mels      []float32
	features  []float32
}

// NewAudioFeatures loads the mel-spectrogram and embedding ONNX models used
// to turn streaming audio into wake-word model input features.
func NewAudioFeatures(melspecPath, embeddingPath string) (*AudioFeatures, error) {
	melspec, err := newONNXSession(melspecPath)
	if err != nil {
		return nil, fmt.Errorf("load mel spectrogram model: %w", err)
	}
	embedding, err := newONNXSession(embeddingPath)
	if err != nil {
		_ = melspec.close()
		return nil, fmt.Errorf("load embedding model: %w", err)
	}
	f := &AudioFeatures{melspec: melspec, embedding: embedding}
	f.Reset()
	return f, nil
}

// Process consumes arbitrary sized 16 kHz mono samples and returns the number
// of samples converted into new embedding frames.
func (f *AudioFeatures) Process(samples Samples) (int, error) {
	if len(samples) == 0 {
		return 0, nil
	}
	f.remainder = append(f.remainder, samples...)
	prepared := len(f.remainder) / FrameSamples * FrameSamples
	if prepared == 0 {
		return 0, nil
	}
	current := append(Samples(nil), f.remainder[:prepared]...)
	f.remainder = append(f.remainder[:0], f.remainder[prepared:]...)
	f.appendRaw(current)

	contextSize := prepared + 480
	if contextSize > len(f.raw) {
		contextSize = len(f.raw)
	}
	audio := make([]float32, contextSize)
	for i, sample := range f.raw[len(f.raw)-contextSize:] {
		audio[i] = sampleToPCM16Float(sample)
	}
	mel, _, err := f.melspec.runFloat([]int64{1, int64(len(audio))}, audio)
	if err != nil {
		return 0, fmt.Errorf("compute melspectrogram: %w", err)
	}
	if len(mel)%melBins != 0 {
		return 0, fmt.Errorf("melspectrogram returned %d values, not divisible by %d", len(mel), melBins)
	}
	for _, value := range mel {
		f.mels = append(f.mels, value/10+2)
	}
	if frames := len(f.mels) / melBins; frames > melHistoryFrames {
		f.mels = append(f.mels[:0], f.mels[(frames-melHistoryFrames)*melBins:]...)
	}
	newFrames := prepared / FrameSamples
	windows := make([]float32, 0, newFrames*melWindowFrames*melBins)
	totalMelFrames := len(f.mels) / melBins
	for i := 0; i < newFrames; i++ {
		end := totalMelFrames - (newFrames-1-i)*8
		start := end - melWindowFrames
		if start < 0 || end > totalMelFrames {
			continue
		}
		windows = append(windows, f.mels[start*melBins:end*melBins]...)
	}
	if len(windows) == 0 {
		return prepared, nil
	}
	batch := len(windows) / (melWindowFrames * melBins)
	embeddings, _, err := f.embedding.runFloat(
		[]int64{int64(batch), melWindowFrames, melBins, 1}, windows)
	if err != nil {
		return 0, fmt.Errorf("compute audio embeddings: %w", err)
	}
	if len(embeddings)%embeddingSize != 0 {
		return 0, fmt.Errorf("embedding model returned %d values, not divisible by %d", len(embeddings), embeddingSize)
	}
	f.features = append(f.features, embeddings...)
	if frames := len(f.features) / embeddingSize; frames > featureHistoryFrames {
		f.features = append(f.features[:0], f.features[(frames-featureHistoryFrames)*embeddingSize:]...)
	}
	return prepared, nil
}

// Features returns a copy of the latest embedding frames and the ONNX input
// shape expected by binary wake-word models.
func (f *AudioFeatures) Features(frames, offsetFromLatest int) ([]float32, []int64, error) {
	available := len(f.features) / embeddingSize
	end := available - offsetFromLatest
	start := end - frames
	if frames <= 0 || offsetFromLatest < 0 || start < 0 || end > available {
		return nil, nil, fmt.Errorf("feature request out of range: frames=%d offset=%d available=%d", frames, offsetFromLatest, available)
	}
	data := append([]float32(nil), f.features[start*embeddingSize:end*embeddingSize]...)
	return data, []int64{1, int64(frames), embeddingSize}, nil
}

// Reset clears buffered audio, mel frames, and embedding history.
func (f *AudioFeatures) Reset() {
	f.raw = f.raw[:0]
	f.remainder = f.remainder[:0]
	f.mels = make([]float32, melWindowFrames*melBins)
	for i := range f.mels {
		f.mels[i] = 1
	}
	// Wake-word models need a fixed amount of context before live embeddings
	// arrive. Zeros are deterministic and the engine suppresses startup scores.
	f.features = make([]float32, 16*embeddingSize)
}

// Close releases the ONNX sessions owned by the feature extractor.
func (f *AudioFeatures) Close() error {
	return errors.Join(f.melspec.close(), f.embedding.close())
}

func (f *AudioFeatures) appendRaw(samples Samples) {
	f.raw = append(f.raw, samples...)
	if len(f.raw) > rawHistorySamples {
		f.raw = append(f.raw[:0], f.raw[len(f.raw)-rawHistorySamples:]...)
	}
}
