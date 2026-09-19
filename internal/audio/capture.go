package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os/exec"
)

// FrameSize is 512 samples = 32ms at 16kHz
const FrameSize = 512
const SampleRate = 16000

type MicStream struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
}

// StartMicCapture spawns an unbuffered ALSA capture pipeline
func StartMicCapture(ctx context.Context) (*MicStream, error) {
	// arecord grabs raw PCM 16-bit Little Endian, Mono, 16kHz from default audio input
	cmd := exec.CommandContext(ctx, "arecord",
		"-q",
		"-D", "default",
		"-r", "16000",
		"-c", "1",
		"-f", "S16_LE",
		"-t", "raw",
		"-B", "10000", // low latency 10ms buffer
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open capture pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start mic capture process (arecord): %w", err)
	}

	return &MicStream{cmd: cmd, stdout: stdout}, nil
}

// ReadFrame reads a single block of PCM int16 samples
func (m *MicStream) ReadFrame(samples []int16) error {
	byteBuf := make([]byte, len(samples)*2)
	_, err := io.ReadFull(m.stdout, byteBuf)
	if err != nil {
		return err
	}

	for i := 0; i < len(samples); i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(byteBuf[i*2 : (i+1)*2]))
	}

	return nil
}

func (m *MicStream) Close() {
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
	}
}

// CalculateRMS computes audio volume level for privacy thresholds and activity detection
func CalculateRMS(samples []int16) float64 {
	var sum float64
	for _, s := range samples {
		f := float64(s)
		sum += f * f
	}
	return math.Sqrt(sum / float64(len(samples)))
}
