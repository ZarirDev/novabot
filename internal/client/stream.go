package client

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// sendQueueSize is the number of packets we'll buffer before dropping.
// Each packet is 10 ms of audio, so 32 packets = 320 ms of slack. Enough
// to ride out a scheduler hiccup, small enough that latency stays bounded.
const sendQueueSize = 32

// Streamer sends captured PCM to the server over UDP.
//
// The audio callback runs on a real-time thread. Any blocking I/O there
// causes capture underruns, so Send() only enqueues — a dedicated
// goroutine does the actual socket writes. If the queue backs up, we
// drop rather than stall the capture thread.
type Streamer struct {
	conn *net.UDPConn
	addr *net.UDPAddr

	sendCh   chan []byte
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	packets  atomic.Uint64
	bytes    atomic.Uint64
	sendErrs atomic.Uint64
	drops    atomic.Uint64

	firstSent atomic.Bool
	lastLogMu sync.Mutex
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

	// Big kernel send buffer so the sender goroutine rarely blocks.
	_ = conn.SetWriteBuffer(4 << 20)

	s := &Streamer{
		conn:    conn,
		addr:    addr,
		sendCh:  make(chan []byte, sendQueueSize),
		stopCh:  make(chan struct{}),
		lastLog: time.Now(),
	}

	s.wg.Add(1)
	go s.run()

	log.Printf("[CLIENT] UDP streamer initialized → %s (queue %d packets)", addr, sendQueueSize)
	return s, nil
}

// run drains the send queue on a dedicated goroutine.
func (s *Streamer) run() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case pkt := <-s.sendCh:
			if _, err := s.conn.Write(pkt); err != nil {
				s.sendErrs.Add(1)
				continue
			}
			s.packets.Add(1)
			s.bytes.Add(uint64(len(pkt)))
		}
	}
}

// Send queues one PCM buffer with a timestamp header. Called from the
// audio callback thread — must not block, must not allocate on the fast
// path beyond the packet itself.
func (s *Streamer) Send(pcm []byte) {
	if len(pcm) == 0 {
		return
	}

	// Allocate a fresh packet — the caller's buffer is reused by malgo
	// after the callback returns, so we can't hold onto it.
	packet := make([]byte, 8+len(pcm))
	binary.LittleEndian.PutUint64(packet[:8], uint64(time.Now().UnixNano()))
	copy(packet[8:], pcm)

	select {
	case s.sendCh <- packet:
		if s.firstSent.CompareAndSwap(false, true) {
			log.Printf("[CLIENT] first packet sent (%d bytes: 8 hdr + %d pcm)",
				len(packet), len(pcm))
		}
	default:
		// Queue is full. Dropping here is better than blocking the
		// capture callback — a dropped packet produces a short gap,
		// a blocked callback produces compounding capture drift.
		s.drops.Add(1)
	}

	s.maybeLog()
}

// maybeLog emits a stats line every 5 seconds.
func (s *Streamer) maybeLog() {
	s.lastLogMu.Lock()
	elapsed := time.Since(s.lastLog)
	if elapsed < 5*time.Second {
		s.lastLogMu.Unlock()
		return
	}
	s.lastLog = time.Now()
	s.lastLogMu.Unlock()

	pkts := s.packets.Swap(0)
	b := s.bytes.Swap(0)
	errs := s.sendErrs.Swap(0)
	drops := s.drops.Swap(0)
	kbps := float64(b) * 8 / 1000 / elapsed.Seconds()

	msg := fmt.Sprintf("[CLIENT] tx %.0f kbps, %d packets in last %.0fs",
		kbps, pkts, elapsed.Seconds())
	if errs > 0 {
		msg += fmt.Sprintf(", %d send errors", errs)
	}
	if drops > 0 {
		msg += fmt.Sprintf(", %d dropped (queue full)", drops)
	}
	log.Println(msg)
}

func (s *Streamer) Close() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.wg.Wait()
		_ = s.conn.Close()
	})
}
