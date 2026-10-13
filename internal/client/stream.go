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

const sendQueueSize = 32

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

	// Quiet suppresses the periodic 5-second stats line. Set by the
	// daemon when running under systemd so journalctl stays clean.
	quiet atomic.Bool
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

// SetQuiet toggles the periodic stats logging.
func (s *Streamer) SetQuiet(v bool) {
	s.quiet.Store(v)
}

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

func (s *Streamer) Send(pcm []byte) {
	if len(pcm) == 0 {
		return
	}
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
		s.drops.Add(1)
	}
	s.maybeLog()
}

func (s *Streamer) maybeLog() {
	if s.quiet.Load() {
		return
	}
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
