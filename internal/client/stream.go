package client

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync/atomic"
	"time"
)

// Streamer sends captured PCM to the server over UDP.
//
// Packet format: [8-byte LE nanosecond send timestamp][raw S16_LE PCM].
// The timestamp lets the server compute one-way latency. Assumes both
// machines have reasonably synced clocks (NTP on a LAN gets you <5ms).
type Streamer struct {
	conn     *net.UDPConn
	addr     *net.UDPAddr
	packets  atomic.Uint64
	bytes    atomic.Uint64
	sendErrs atomic.Uint64

	firstSent bool
	lastLog   time.Time
}

func NewStreamer(server string, port int) (*Streamer, error) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", server, port))
	if err != nil {
		return nil, fmt.Errorf("resolve %s:%d: %w", server, port, err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("dial udp: %w", err)
	}

	_ = conn.SetWriteBuffer(1 << 20)

	log.Printf("[CLIENT] UDP streamer initialized → %s", addr)
	return &Streamer{
		conn:    conn,
		addr:    addr,
		lastLog: time.Now(),
	}, nil
}

// Send writes one PCM buffer with a timestamp header.
// Called from the audio callback thread — keep it allocation-light.
func (s *Streamer) Send(pcm []byte) {
	if len(pcm) == 0 {
		return
	}

	packet := make([]byte, 8+len(pcm))
	binary.LittleEndian.PutUint64(packet[:8], uint64(time.Now().UnixNano()))
	copy(packet[8:], pcm)

	if _, err := s.conn.Write(packet); err != nil {
		s.sendErrs.Add(1)
		return
	}

	s.packets.Add(1)
	s.bytes.Add(uint64(len(packet)))

	if !s.firstSent {
		s.firstSent = true
		log.Printf("[CLIENT] first packet sent (%d bytes: 8 hdr + %d pcm)",
			len(packet), len(pcm))
	}

	now := time.Now()
	elapsed := now.Sub(s.lastLog)
	if elapsed < 5*time.Second {
		return
	}

	// 5-second stats window.
	pkts := s.packets.Load()
	b := s.bytes.Load()
	errs := s.sendErrs.Load()
	kbps := float64(b) * 8 / 1000 / elapsed.Seconds()

	s.bytes.Store(0)
	s.sendErrs.Store(0)
	s.lastLog = now

	msg := fmt.Sprintf("[CLIENT] tx %.0f kbps, %d packets total", kbps, pkts)
	if errs > 0 {
		msg += fmt.Sprintf(", %d send errors", errs)
	}
	log.Println(msg)
}

func (s *Streamer) Close() {
	if s.conn != nil {
		_ = s.conn.Close()
	}
}
