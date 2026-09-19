package audio

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"sync"
)

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

	cmd := exec.Command("aplay", "-q", "-")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("aplay stdin error: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("aplay start error: %w", err)
	}

	_, _ = stdin.Write(wavData)
	_ = stdin.Close()
	return cmd.Wait()
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

	cmd := exec.Command("aplay", "-q", "-t", "raw", "-f", "S16_LE", "-r", "48000", "-c", "2")
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
