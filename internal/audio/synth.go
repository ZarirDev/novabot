package audio

import (
	"bytes"
	"encoding/binary"
	"math"
)

// GenerateTone generates a smooth sine ping in memory
func GenerateTone(freq float64, durationMs int, sampleRate int) []byte {
	numSamples := (sampleRate * durationMs) / 1000
	buf := new(bytes.Buffer)

	writeWAVHeader(buf, numSamples, sampleRate, 1, 16)

	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		envelope := math.Exp(-4.0 * float64(i) / float64(numSamples))
		sample := math.Sin(2.0*math.Pi*freq*t) * envelope
		val := int16(sample * 32767.0)
		_ = binary.Write(buf, binary.LittleEndian, val)
	}

	return buf.Bytes()
}

// GenerateTTSAudio generates two-tone confirmation response ("Testing, hi.")
func GenerateTTSAudio(sampleRate int) []byte {
	b1 := GenerateTone(880.0, 120, sampleRate)
	b2 := GenerateTone(1320.0, 200, sampleRate)
	return append(b1, b2...)
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
