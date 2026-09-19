package audio

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"strings"
)

// FrameSize is 512 samples = 32ms at 16kHz
const FrameSize = 512
const SampleRate = 16000

type MicStream struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
}

// CaptureDevice returns the ALSA PCM name to capture from.
// Override with MIC_DEVICE=pulse / MIC_DEVICE=plughw:1,0 / etc.
func CaptureDevice() string {
	if d := os.Getenv("MIC_DEVICE"); d != "" {
		return d
	}
	return "default"
}

// logCaptureDevice prints what arecord will open, plus the resolved
// PipeWire/PulseAudio source if we can find it.
func logCaptureDevice(device string) {
	log.Printf("[MIC] arecord device: %s", device)

	if out, err := exec.Command("pactl", "info").Output(); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "Default Source:") {
				log.Printf("[MIC] pulse default source: %s",
					strings.TrimSpace(strings.TrimPrefix(line, "Default Source:")))
				return
			}
		}
	}

	if out, err := exec.Command("arecord", "-l").Output(); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "card ") {
				log.Printf("[MIC] ALSA %s", sc.Text())
			}
		}
	}
}

// StartMicCapture spawns an unbuffered ALSA capture pipeline
func StartMicCapture(ctx context.Context) (*MicStream, error) {
	device := CaptureDevice()
	logCaptureDevice(device)

	// arecord grabs raw PCM 16-bit Little Endian, Mono, 16kHz from the chosen input
	cmd := exec.CommandContext(ctx, "arecord",
		"-q",
		"-D", device,
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
