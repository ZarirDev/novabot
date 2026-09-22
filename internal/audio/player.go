package audio

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"
)

// SpeakerDevice returns the aplay device to output to.
// Override with SPEAKER_DEVICE=plughw:1,0 / hdmi:CARD=... / etc.
func SpeakerDevice() string {
	if d := os.Getenv("SPEAKER_DEVICE"); d != "" {
		return d
	}
	return "pulse"
}

// UDPStatus is a snapshot of the PC_AUDIO stream health, safe to serialise.
type UDPStatus struct {
	Listening    bool      `json:"listening"`
	Connected    bool      `json:"connected"`
	LastPacket   time.Time `json:"last_packet"`
	PacketsTotal uint64    `json:"packets_total"`
	KBps         float64   `json:"kbps"`
	LatencyP50Ms float64   `json:"latency_p50_ms"`
	LatencyP95Ms float64   `json:"latency_p95_ms"`
	Quality      string    `json:"quality"`
	SampleRate   int       `json:"sample_rate"`
	Channels     int       `json:"channels"`
	Format       string    `json:"format"`
	ExpectedKBps int       `json:"expected_kbps"`
}

type AudioPlayer struct {
	mu           sync.Mutex
	udpListening bool

	// telemetry, guarded by teleMu
	teleMu       sync.Mutex
	lastPacket   time.Time
	packetsTotal uint64
	windowKBps   float64
	latP50       time.Duration
	latP95       time.Duration
	connected    bool
}

func NewAudioPlayer() *AudioPlayer {
	return &AudioPlayer{}
}

// aplayEnv builds the environment for a child aplay process.
//
// PULSE_LATENCY_MSEC caps the per-stream buffer PulseAudio allocates.
// The default is 200-500 ms, which is pure latency for an interactive
// stream. 20 ms is a good balance between responsiveness and tolerance
// for scheduler jitter.
//
// PULSE_SINK pins the stream to the currently-selected default sink so
// aplay doesn't race with sink changes — it attaches to the intended
// device the moment it opens the connection, not on first write.
func aplayEnv() []string {
	env := os.Environ()
	env = append(env, "PULSE_LATENCY_MSEC=20")
	if sink := DefaultSinkName(); sink != "" {
		env = append(env, "PULSE_SINK="+sink)
	}
	return env
}

func (ap *AudioPlayer) PlayWAV(wavData []byte) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	device := SpeakerDevice()
	cmd := exec.Command("aplay", "-D", device, "-")
	cmd.Env = aplayEnv()

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

// UDPStatus returns a thread-safe snapshot for the HTTP layer.
func (ap *AudioPlayer) UDPStatus() UDPStatus {
	q := ActiveQuality()

	ap.mu.Lock()
	listening := ap.udpListening
	ap.mu.Unlock()

	ap.teleMu.Lock()
	defer ap.teleMu.Unlock()

	connected := !ap.lastPacket.IsZero() && time.Since(ap.lastPacket) < 3*time.Second

	return UDPStatus{
		Listening:    listening,
		Connected:    connected,
		LastPacket:   ap.lastPacket,
		PacketsTotal: ap.packetsTotal,
		KBps:         ap.windowKBps,
		LatencyP50Ms: float64(ap.latP50.Microseconds()) / 1000,
		LatencyP95Ms: float64(ap.latP95.Microseconds()) / 1000,
		Quality:      q.Name,
		SampleRate:   q.SampleRate,
		Channels:     q.Channels,
		Format:       q.Format,
		ExpectedKBps: q.Bandwidth(),
	}
}

// StartUDPStreamListener binds to port, spawns aplay in the configured
// format, and pumps received PCM to it. Blocks until stopChan is closed.
//
// Packet format: [8-byte LE nanosecond timestamp][raw PCM].
func (ap *AudioPlayer) StartUDPStreamListener(port int, stopChan <-chan struct{}) {
	ap.mu.Lock()
	if ap.udpListening {
		ap.mu.Unlock()
		log.Printf("[AUDIO] UDP listener already running on port %d", port)
		return
	}
	ap.udpListening = true
	ap.mu.Unlock()

	defer func() {
		ap.mu.Lock()
		ap.udpListening = false
		ap.mu.Unlock()

		ap.teleMu.Lock()
		ap.connected = false
		ap.teleMu.Unlock()
	}()

	q := ActiveQuality()

	addr := net.UDPAddr{Port: port, IP: net.ParseIP("0.0.0.0")}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		log.Printf("[AUDIO] failed to bind UDP :%d — %v", port, err)
		return
	}
	defer conn.Close()

	log.Printf("[AUDIO] UDP listener bound on 0.0.0.0:%d (quality=%s, %d Hz, %d ch, %s, expected %d kbps)",
		port, q.Name, q.SampleRate, q.Channels, q.Format, q.Bandwidth())

	cmd := exec.Command("aplay", "-q", "-D", SpeakerDevice(),
		"-t", "raw", "-f", q.Format,
		"-r", fmt.Sprintf("%d", q.SampleRate),
		"-c", fmt.Sprintf("%d", q.Channels))
	cmd.Env = aplayEnv()

	if sink := DefaultSinkName(); sink != "" {
		log.Printf("[AUDIO] aplay pinned to sink %q (20ms latency target)", sink)
	} else {
		log.Printf("[AUDIO] warning: no default sink resolved, aplay will use PulseAudio's choice")
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Printf("[AUDIO] aplay stdin pipe failed: %v", err)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[AUDIO] aplay start failed: %v", err)
		return
	}
	log.Printf("[AUDIO] aplay playback started (pid=%d, device=%s, volume=%d%%)",
		cmd.Process.Pid, SpeakerDevice(), Volume())

	var (
		packets       uint64
		bytes         uint64
		mismatchCount int
		lastStat      = time.Now()
		latencies     []time.Duration
	)

	buf := make([]byte, 32*1024)

	for {
		select {
		case <-stopChan:
			log.Printf("[AUDIO] UDP stream stopping — %d packets / %d bytes total",
				packets, bytes)
			_ = stdin.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			return
		default:
		}

		now := time.Now()

		// Stats tick at the top of the loop so it fires during continuous
		// traffic, not just on read timeouts. Emits every 5 seconds.
		if packets > 0 && now.Sub(lastStat) >= 5*time.Second {
			ap.recordStats(bytes, latencies, now.Sub(lastStat))
			logRXStats(packets, bytes, latencies, now.Sub(lastStat))
			lastStat = now
			bytes = 0
			latencies = latencies[:0]
		}

		_ = conn.SetReadDeadline(now.Add(500 * time.Millisecond))
		n, _, err := conn.ReadFromUDP(buf)

		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				ap.checkDisconnect(now)
				continue
			}
			log.Printf("[AUDIO] UDP read error: %v", err)
			continue
		}

		if n < 8 {
			continue
		}

		// Format mismatch detection: each quality preset produces packets
		// of a fixed size. If the incoming size doesn't match what we're
		// expecting, the client hasn't caught up yet. Drop the packet so
		// we don't feed wrong-format PCM to aplay.
		expectedPCM := q.PacketFrames() * q.Channels * q.BytesPerSmpl
		expectedTotal := expectedPCM + 8

		if n != expectedTotal {
			mismatchCount++
			if mismatchCount == 1 {
				log.Printf("[AUDIO] format mismatch: got %d-byte packet, expected %d — "+
					"client is on a different quality, waiting for it to catch up",
					n, expectedTotal)
			} else if mismatchCount%500 == 0 {
				log.Printf("[AUDIO] still mismatched after %d packets — "+
					"client hasn't detected the quality change", mismatchCount)
			}
			continue
		}
		if mismatchCount > 0 {
			log.Printf("[AUDIO] format recovered after %d dropped packets — resuming",
				mismatchCount)
			mismatchCount = 0
		}

		sentNs := int64(binary.LittleEndian.Uint64(buf[:8]))
		pcm := buf[8:n]

		ap.markPacket(now)

		packets++
		bytes += uint64(len(pcm))

		if sentNs > 0 {
			lat := now.Sub(time.Unix(0, sentNs))
			if lat > -time.Second && lat < 10*time.Second {
				latencies = append(latencies, lat)
				if len(latencies) > 512 {
					latencies = latencies[1:]
				}
			}
		}

		if _, werr := stdin.Write(pcm); werr != nil {
			log.Printf("[AUDIO] aplay write error: %v", werr)
		}
	}
}

// markPacket updates telemetry and logs a single transition line when
// the client first connects (or reconnects after a gap).
func (ap *AudioPlayer) markPacket(now time.Time) {
	ap.teleMu.Lock()
	wasConnected := ap.connected
	ap.lastPacket = now
	ap.packetsTotal++
	if !wasConnected {
		ap.connected = true
	}
	ap.teleMu.Unlock()

	if !wasConnected {
		log.Printf("[AUDIO] client connected — receiving audio")
	}
}

// checkDisconnect emits a single log line when the client has been silent
// for >3 seconds and we were previously connected.
func (ap *AudioPlayer) checkDisconnect(now time.Time) {
	ap.teleMu.Lock()
	wasConnected := ap.connected
	stale := !ap.lastPacket.IsZero() && now.Sub(ap.lastPacket) > 3*time.Second
	if wasConnected && stale {
		ap.connected = false
	}
	ap.teleMu.Unlock()

	if wasConnected && stale {
		log.Printf("[AUDIO] client disconnected — no packets for >3s")
	}
}

// recordStats stores the latest 5s window for the HTTP layer.
func (ap *AudioPlayer) recordStats(bytes uint64, latencies []time.Duration, window time.Duration) {
	ap.teleMu.Lock()
	defer ap.teleMu.Unlock()

	ap.windowKBps = float64(bytes) * 8 / 1000 / window.Seconds()
	if len(latencies) > 0 {
		sorted := make([]time.Duration, len(latencies))
		copy(sorted, latencies)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		ap.latP50 = sorted[len(sorted)*50/100]
		ap.latP95 = sorted[(len(sorted)*95)/100]
	}
}

func logRXStats(packets, bytes uint64, latencies []time.Duration, window time.Duration) {
	kbps := float64(bytes) * 8 / 1000 / window.Seconds()
	msg := fmt.Sprintf("[AUDIO] rx %d pkts, %.0f kbps", packets, kbps)
	if len(latencies) > 0 {
		sorted := make([]time.Duration, len(latencies))
		copy(sorted, latencies)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		p50 := sorted[len(sorted)*50/100]
		p95 := sorted[(len(sorted)*95)/100]
		msg += fmt.Sprintf(", latency p50=%.1fms p95=%.1fms",
			float64(p50.Microseconds())/1000,
			float64(p95.Microseconds())/1000)
	}
	log.Println(msg)
}
