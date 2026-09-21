package client

import (
	"fmt"
	"log"
	"net"
	"sync/atomic"
	"time"
)

// Streamer sends captured PCM to the server over UDP.
// Lightweight: no retransmit, no ordering — the server just plays
// packets as they arrive. Packet loss is heard as a click, which
// is acceptable for LAN streaming.
type Streamer struct {
	conn    *net.UDPConn
	addr    *net.UDPAddr
	packets atomic.Uint64
	bytes   atomic.Uint64
	lastLog time.Time
}

// NewStreamer dials the server's UDP port.
func NewStreamer(server string, port int) (*Streamer, error) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", server, port))
	if err != nil {
		return nil, fmt.Errorf("resolve %s:%d: %w", server, port, err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("dial udp: %w", err)
	}

	// 1 MB send buffer — enough for ~2.5 seconds of 48 kHz stereo S16
	// in flight, which is far more than any LAN needs.
	_ = conn.SetWriteBuffer(1 << 20)

	return &Streamer{
		conn:    conn,
		addr:    addr,
		lastLog: time.Now(),
	}, nil
}

// Send writes one PCM buffer as a UDP datagram.
// Called from the audio callback thread — keep it fast.
func (s *Streamer) Send(pcm []byte) {
	if len(pcm) == 0 {
		return
	}

	// 960 frames * 2 ch * 2 bytes = 3840 bytes for a 20 ms period.
	// That fits comfortably in a single UDP datagram (MTU ~1500 on
	// most LANs, but jumbo frames or even standard 1500 with UDP
	// fragmentation handles this fine — miniaudio buffers on the
	// receive side).
	_, err := s.conn.Write(pcm)
	if err != nil {
		// Don't log on every packet — would spam. Count and report periodically.
		return
	}

	s.packets.Add(1)
	s.bytes.Add(uint64(len(pcm)))

	now := time.Now()
	if now.Sub(s.lastLog) >= 5*time.Second {
		s.lastLog = now
		kbps := float64(s.bytes.Load()) * 8 / 1000 / 5
		s.bytes.Store(0)
		log.Printf("[CLIENT] streaming %.0f kbps (%d packets)",
			kbps, s.packets.Load())
	}
}

// Close shuts down the UDP socket.
func (s *Streamer) Close() {
	if s.conn != nil {
		_ = s.conn.Close()
	}
}
