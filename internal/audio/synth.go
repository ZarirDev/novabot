package audio

import (
	"bytes"
	"encoding/binary"
	"math"
)

// pcmTone generates raw 16-bit mono PCM (no header).
func pcmTone(freq float64, durationMs int, sampleRate int) []byte {
	numSamples := (sampleRate * durationMs) / 1000
	buf := new(bytes.Buffer)

	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		envelope := math.Exp(-4.0 * float64(i) / float64(numSamples))
		sample := math.Sin(2.0*math.Pi*freq*t) * envelope
		val := int16(sample * 32767.0)
		_ = binary.Write(buf, binary.LittleEndian, val)
	}
	return buf.Bytes()
}

// wrapWAV prepends a WAV header to raw PCM.
func wrapWAV(pcm []byte, sampleRate, numChannels, bitsPerSample int) []byte {
	buf := new(bytes.Buffer)
	numSamples := len(pcm) / (numChannels * bitsPerSample / 8)
	writeWAVHeader(buf, numSamples, sampleRate, numChannels, bitsPerSample)
	buf.Write(pcm)
	return buf.Bytes()
}

// GenerateTone returns a single-tone WAV (header + PCM).
func GenerateTone(freq float64, durationMs int, sampleRate int) []byte {
	return wrapWAV(pcmTone(freq, durationMs, sampleRate), sampleRate, 1, 16)
}

// GenerateTTSAudio returns a two-tone confirmation WAV in a single container.
func GenerateTTSAudio(sampleRate int) []byte {
	pcm := append(
		pcmTone(880.0, 120, sampleRate),
		pcmTone(1320.0, 200, sampleRate)...,
	)
	return wrapWAV(pcm, sampleRate, 1, 16)
}

func writeWAVHeader(buf *bytes.Buffer, numSamples, sampleRate, numChannels, bitsPerSample int) {
	dataSize := numSamples * numChannels * (bitsPerSample / 8)
	fileSize := 36 + dataSize

	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(fileSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(numChannels))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate*numChannels*bitsPerSample/8))
	_ = binary.Write(buf, binary.LittleEndian, uint16(numChannels*bitsPerSample/8))
	_ = binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataSize))
}
