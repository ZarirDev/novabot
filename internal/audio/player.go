package audio

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
)

// SpeakerDevice returns the aplay device to output to.
// Override with SPEAKER_DEVICE=plughw:1,0 / hdmi:CARD=... / etc.
func SpeakerDevice() string {
	if d := os.Getenv("SPEAKER_DEVICE"); d != "" {
		return d
	}
	return "pulse"
}

type AudioPlayer struct {
	mu           sync.Mutex
	udpListening bool
}

func NewAudioPlayer() *AudioPlayer {
	return &AudioPlayer{}
}

func (ap *AudioPlayer) PlayWAV(wavData []byte) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	device := SpeakerDevice()
	cmd := exec.Command("aplay", "-D", device, "-")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("aplay stdin error: %w", err)
	}

	stderrPipe, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("aplay start error: %w", err)
	}

	if _, err := stdin.Write(wavData); err != nil {
		return fmt.Errorf("aplay write error: %w", err)
	}
	_ = stdin.Close()

	var stderrBuf []byte
	if stderrPipe != nil {
		buf := make([]byte, 4096)
		n, _ := stderrPipe.Read(buf)
		stderrBuf = buf[:n]
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("aplay failed (device=%s): %w | stderr: %s",
			device, err, string(stderrBuf))
	}
	return nil
}

func (ap *AudioPlayer) StartUDPStreamListener(port int, stopChan <-chan struct{}) {
	ap.mu.Lock()
	if ap.udpListening {
		ap.mu.Unlock()
		return
	}
	ap.udpListening = true
	ap.mu.Unlock()

	addr := net.UDPAddr{Port: port, IP: net.ParseIP("0.0.0.0")}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		log.Printf("[AUDIO] Failed to bind UDP stream: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("[AUDIO] Low-latency UDP streaming active on port %d...", port)

	cmd := exec.Command("aplay", "-q", "-D", SpeakerDevice(),
		"-t", "raw", "-f", "S16_LE", "-r", "48000", "-c", "2")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Printf("[AUDIO] Player pipe error: %v", err)
		return
	}
	_ = cmd.Start()

	buf := make([]byte, 4096)
	for {
		select {
		case <-stopChan:
			log.Println("[AUDIO] UDP streaming stopped.")
			_ = stdin.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			ap.mu.Lock()
			ap.udpListening = false
			ap.mu.Unlock()
			return
		default:
			n, _, err := conn.ReadFromUDP(buf)
			if err == nil && n > 0 {
				_, _ = stdin.Write(buf[:n])
			}
		}
	}
}
