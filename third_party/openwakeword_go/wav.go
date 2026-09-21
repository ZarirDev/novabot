package openwakeword

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// WAV contains decoded mono audio and its source format metadata.
type WAV struct {
	SampleRate int
	Channels   int
	Samples    Samples
}

// ReadWAV loads a mono 16-bit PCM WAV file at SampleRate.
func ReadWAV(path string) (wav WAV, err error) {
	file, err := os.Open(path)

	if err != nil {
		return WAV{}, err
	}

	defer func(file *os.File) {
		if deferErr := file.Close(); deferErr != nil {
			err = errors.Join(err, deferErr)
		}
	}(file)

	return DecodeWAV(file)
}

// DecodeWAV decodes a mono 16-bit PCM WAV stream at SampleRate into Samples.
func DecodeWAV(r io.Reader) (WAV, error) {
	header := make([]byte, 12)
	if _, err := io.ReadFull(r, header); err != nil {
		return WAV{}, fmt.Errorf("read WAV header: %w", err)
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return WAV{}, errors.New("input is not a RIFF/WAVE stream")
	}
	var format uint16
	var channels uint16
	var sampleRate uint32
	var bits uint16
	var data []byte
	for {
		chunk := make([]byte, 8)
		if _, err := io.ReadFull(r, chunk); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return WAV{}, err
		}
		size := binary.LittleEndian.Uint32(chunk[4:])
		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return WAV{}, fmt.Errorf("read WAV chunk %q: %w", chunk[:4], err)
		}
		if size%2 == 1 {
			var padding [1]byte
			_, _ = io.ReadFull(r, padding[:])
		}
		switch string(chunk[:4]) {
		case "fmt ":
			if len(payload) < 16 {
				return WAV{}, errors.New("invalid WAV fmt chunk")
			}
			format = binary.LittleEndian.Uint16(payload[0:2])
			channels = binary.LittleEndian.Uint16(payload[2:4])
			sampleRate = binary.LittleEndian.Uint32(payload[4:8])
			bits = binary.LittleEndian.Uint16(payload[14:16])
		case "data":
			data = payload
		}
	}
	if format != 1 || channels != 1 || sampleRate != SampleRate || bits != 16 {
		return WAV{}, fmt.Errorf("wav must be mono 16-bit PCM at %d Hz, got format=%d channels=%d rate=%d bits=%d",
			SampleRate,
			format, channels, sampleRate, bits)
	}
	if len(data)%2 != 0 {
		return WAV{}, errors.New("wav data has an odd byte count")
	}
	samples := make(Samples, len(data)/2)
	for i := range samples {
		samples[i] = pcm16ToSample(int16(binary.LittleEndian.Uint16(data[i*2:])))
	}
	return WAV{SampleRate: int(sampleRate), Channels: int(channels), Samples: samples}, nil
}

// PredictWAV reads a WAV file and runs PredictClip over its samples.
func (e *Engine) PredictWAV(path string, chunkSize int, padding time.Duration) ([]map[string]float32, error) {
	wav, err := ReadWAV(path)
	if err != nil {
		return nil, err
	}
	return e.PredictClip(wav.Samples, chunkSize, padding)
}

// DetectWAV reads a WAV file and runs DetectClip over its samples.
func (e *Engine) DetectWAV(path string, chunkSize int, padding time.Duration) ([]map[string]bool, error) {
	wav, err := ReadWAV(path)
	if err != nil {
		return nil, err
	}
	return e.DetectClip(wav.Samples, chunkSize, padding)
}

// PredictClip runs streaming prediction over a complete in-memory clip.
func (e *Engine) PredictClip(samples Samples, chunkSize int, padding time.Duration) ([]map[string]float32, error) {
	data, chunkSize := paddedClip(samples, chunkSize, padding)
	predictions := make([]map[string]float32, 0, (len(data)+chunkSize-1)/chunkSize)
	for start := 0; start < len(data); start += chunkSize {
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		prediction, err := e.Predict(data[start:end])
		if err != nil {
			return nil, err
		}
		predictions = append(predictions, prediction)
	}
	return predictions, nil
}

// DetectClip runs streaming threshold detection over a complete in-memory clip.
func (e *Engine) DetectClip(samples Samples, chunkSize int, padding time.Duration) ([]map[string]bool, error) {
	data, chunkSize := paddedClip(samples, chunkSize, padding)
	detections := make([]map[string]bool, 0, (len(data)+chunkSize-1)/chunkSize)
	for start := 0; start < len(data); start += chunkSize {
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		detection, err := e.Detect(data[start:end])
		if err != nil {
			return nil, err
		}
		detections = append(detections, detection)
	}
	return detections, nil
}

func paddedClip(samples Samples, chunkSize int, padding time.Duration) (Samples, int) {
	if chunkSize <= 0 {
		chunkSize = FrameSamples
	}
	paddingSamples := int(float64(SampleRate) * padding.Seconds())
	data := make(Samples, 0, paddingSamples*2+len(samples))
	data = append(data, make(Samples, paddingSamples)...)
	data = append(data, samples...)
	data = append(data, make(Samples, paddingSamples)...)
	return data, chunkSize
}
