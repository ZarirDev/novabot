package music

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

const socketPath = "/tmp/novabot-mpv.sock"

// Player wraps a single mpv subprocess controlled via JSON IPC.
// Minimal by design: request/response only, no event listener.
// The web UI polls Status() once a second, which is plenty for a music player.
type Player struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	conn    net.Conn
	reader  *bufio.Reader
	nextID  int
	running bool
}

func NewPlayer() *Player {
	return &Player{}
}

// Start spawns mpv in idle mode with an IPC socket. Idempotent.
func (p *Player) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return nil
	}

	// Remove stale socket from a previous run.
	_ = os.Remove(socketPath)

	cmd := exec.Command("mpv",
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		"--really-quiet",
		"--input-ipc-server="+socketPath,
		"--volume=70",
		"--audio-display=no",
	)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mpv start: %w", err)
	}
	p.cmd = cmd

	// Wait for the socket to appear (mpv creates it asynchronously).
	var conn net.Conn
	var err error
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("mpv socket: %w", err)
	}

	p.conn = conn
	p.reader = bufio.NewReader(conn)
	p.running = true
	p.nextID = 1

	log.Printf("[MUSIC] mpv started (pid=%d, socket=%s)", cmd.Process.Pid, socketPath)

	// Reap the process when it exits so we don't leak zombies.
	go func() {
		_ = cmd.Wait()
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
		log.Println("[MUSIC] mpv exited")
	}()

	return nil
}

// Stop kills mpv and cleans up.
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		p.cmd = nil
	}
	p.running = false
	_ = os.Remove(socketPath)
}

// command sends a JSON command and returns the raw "data" field.
// mpv replies with {"request_id":N,"error":"success","data":...}.
func (p *Player) command(args ...interface{}) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running || p.conn == nil {
		return nil, fmt.Errorf("mpv not running")
	}

	id := p.nextID
	p.nextID++

	req := map[string]interface{}{
		"command":    args,
		"request_id": id,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')

	if _, err := p.conn.Write(payload); err != nil {
		return nil, fmt.Errorf("mpv write: %w", err)
	}

	// Read until we see our request_id (mpv may emit events interleaved).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = p.conn.SetReadDeadline(deadline)
		line, err := p.reader.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("mpv read: %w", err)
		}

		var resp struct {
			RequestID int             `json:"request_id"`
			Error     string          `json:"error"`
			Data      json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &resp); err != nil {
			continue // not a reply (event or malformed line)
		}
		if resp.RequestID != id {
			continue // reply to a different request
		}
		if resp.Error != "success" {
			return nil, fmt.Errorf("mpv error: %s", resp.Error)
		}
		return resp.Data, nil
	}
	return nil, fmt.Errorf("mpv command timeout")
}

// ── Public commands ────────────────────────────────────────

func (p *Player) Load(url string) error {
	_, err := p.command("loadfile", url, "replace")
	return err
}

func (p *Player) TogglePause() error {
	paused, err := p.GetBool("pause")
	if err != nil {
		return err
	}
	_, err = p.command("set_property", "pause", !paused)
	return err
}

func (p *Player) SetPause(paused bool) error {
	_, err := p.command("set_property", "pause", paused)
	return err
}

func (p *Player) Next() error {
	_, err := p.command("playlist-next", "weak")
	return err
}

func (p *Player) Prev() error {
	_, err := p.command("playlist-prev", "weak")
	return err
}

func (p *Player) Seek(seconds float64) error {
	_, err := p.command("seek", seconds, "absolute")
	return err
}

func (p *Player) SetVolume(v float64) error {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	_, err := p.command("set_property", "volume", v)
	return err
}

func (p *Player) StopPlayback() error {
	_, err := p.command("stop")
	return err
}

// ── Property getters ───────────────────────────────────────

func (p *Player) GetFloat(prop string) (float64, error) {
	data, err := p.command("get_property", prop)
	if err != nil {
		return 0, err
	}
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, err
	}
	return f, nil
}

func (p *Player) GetBool(prop string) (bool, error) {
	data, err := p.command("get_property", prop)
	if err != nil {
		return false, err
	}
	var b bool
	if err := json.Unmarshal(data, &b); err != nil {
		return false, err
	}
	return b, nil
}

func (p *Player) GetString(prop string) (string, error) {
	data, err := p.command("get_property", prop)
	if err != nil {
		return "", err
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return "", err
	}
	return s, nil
}

// Status is the JSON shape the web UI polls.
type Status struct {
	Playing  bool    `json:"playing"`
	Paused   bool    `json:"paused"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	Volume   float64 `json:"volume"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Idle     bool    `json:"idle"`
}

func (p *Player) Status() Status {
	s := Status{Idle: true}
	if !p.running {
		return s
	}

	if v, err := p.GetBool("idle-active"); err == nil {
		s.Idle = v
	}
	if v, err := p.GetBool("pause"); err == nil {
		s.Paused = v
	}
	if v, err := p.GetFloat("time-pos"); err == nil {
		s.Position = v
	}
	if v, err := p.GetFloat("duration"); err == nil {
		s.Duration = v
	}
	if v, err := p.GetFloat("volume"); err == nil {
		s.Volume = v
	}
	if v, err := p.GetString("media-title"); err == nil {
		s.Title = v
	}
	if v, err := p.GetString("metadata"); err == nil {
		_ = v
	}

	s.Playing = !s.Idle && !s.Paused

	// Metadata artist — mpv returns an object, so fetch separately.
	if m, err := p.command("get_property", "metadata"); err == nil {
		var meta map[string]interface{}
		if json.Unmarshal(m, &meta) == nil {
			if a, ok := meta["artist"].(string); ok {
				s.Artist = a
			} else if a, ok := meta["uploader"].(string); ok {
				s.Artist = a
			}
		}
	}

	return s
}
