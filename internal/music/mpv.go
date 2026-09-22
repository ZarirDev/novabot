package music

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const socketPath = "/tmp/novabot-mpv.sock"

// Player wraps a single mpv subprocess controlled via JSON IPC.
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

	_ = os.Remove(socketPath)

	cmd := exec.Command("mpv",
		"--idle=yes",
		"--no-video",
		"--input-ipc-server="+socketPath,
		"--volume=70",
		"--audio-display=no",
	)

	// Log the environment mpv will inherit. This is the single most
	// useful diagnostic when the binary works on dev but not on a
	// systemd-managed server — the two environments differ.
	env := cmd.Environ()
	sort.Strings(env)
	interesting := []string{"PATH", "HOME", "PULSE_SERVER", "PULSE_SINK",
		"XDG_RUNTIME_DIR", "DISPLAY", "WAYLAND_DISPLAY"}
	log.Printf("[MUSIC] mpv environment:")
	for _, kv := range env {
		for _, key := range interesting {
			if strings.HasPrefix(kv, key+"=") {
				log.Printf("[MUSIC]   %s", kv)
			}
		}
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("mpv stderr pipe: %w", err)
	}

	// Also capture stdout — mpv prints some diagnostics there.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mpv stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mpv start: %w", err)
	}
	p.cmd = cmd

	go forwardLines("mpv-out", stdout)
	go forwardLines("mpv-err", stderr)

	// Wait for the socket.
	var conn net.Conn
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

	// Log mpv's version and audio driver detection once, at startup.
	go p.logStartupInfo()

	// Reap and log exit code.
	go func() {
		waitErr := cmd.Wait()
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
		if waitErr != nil {
			log.Printf("[MUSIC] mpv exited: %v", waitErr)
		} else {
			log.Println("[MUSIC] mpv exited cleanly")
		}
	}()

	return nil
}

func forwardLines(tag string, r interface{ Read([]byte) (int, error) }) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		log.Printf("[%s] %s", tag, line)
	}
}

// logStartupInfo queries mpv for version, audio device, and codec
// support so we know exactly what binary is running and what it sees.
func (p *Player) logStartupInfo() {
	time.Sleep(500 * time.Millisecond)

	if v, err := p.GetString("mpv-version"); err == nil {
		log.Printf("[MUSIC] %s", v)
	}
	if v, err := p.GetString("audio-device"); err == nil {
		log.Printf("[MUSIC] audio-device: %q", v)
	}
	if v, err := p.GetString("audio-codec"); err == nil && v != "" {
		log.Printf("[MUSIC] audio-codec: %q", v)
	}

	// List available audio devices so we can see what mpv could pick.
	if data, err := p.command("get_property", "audio-device-list"); err == nil {
		var devices []map[string]interface{}
		if json.Unmarshal(data, &devices) == nil {
			log.Printf("[MUSIC] mpv audio-device-list (%d entries):", len(devices))
			for _, d := range devices {
				log.Printf("[MUSIC]   %v — %v", d["name"], d["description"])
			}
		}
	}
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
			// Not a reply — could be an event. Log it, then continue.
			log.Printf("[MUSIC] mpv event: %s", strings.TrimSpace(string(line)))
			continue
		}
		if resp.RequestID != id {
			log.Printf("[MUSIC] mpv event (other req): %s", strings.TrimSpace(string(line)))
			continue
		}
		if resp.Error != "success" {
			return nil, fmt.Errorf("mpv error: %s", resp.Error)
		}
		return resp.Data, nil
	}
	return nil, fmt.Errorf("mpv command timeout")
}

// ── Public commands ────────────────────────────────────────

// Load queues a URL for playback and, after a short delay, logs mpv's
// state so we can tell whether it actually started or silently failed.
func (p *Player) Load(url string) error {
	if _, err := p.command("loadfile", url, "replace"); err != nil {
		return err
	}
	go p.verifyLoad(url)
	return nil
}

// verifyLoad waits for mpv to either start playing or drop back to idle,
// then logs everything relevant.
func (p *Player) verifyLoad(url string) {
	for _, delay := range []time.Duration{1 * time.Second, 3 * time.Second, 6 * time.Second} {
		time.Sleep(delay - time.Since(time.Now().Add(-delay))) // approximate
	}

	// Simpler: poll at three checkpoints.
	for i, d := range []time.Duration{1, 2, 3} {
		time.Sleep(time.Second)
		idle, _ := p.GetBool("idle-active")
		count, _ := p.GetFloat("playlist-count")
		title, _ := p.GetString("media-title")
		path, _ := p.GetString("path")

		log.Printf("[MUSIC] verify[%d] after %ds: idle=%v playlist-count=%.0f title=%q path=%q",
			i, d, idle, count, title, path)

		if !idle && title != "" {
			// We're playing something with a real title.
			log.Printf("[MUSIC] verify: playback started successfully")
			return
		}
	}
	_ = url
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

// DebugState returns a full snapshot for the /api/v1/music/debug endpoint.
func (p *Player) DebugState() map[string]interface{} {
	out := make(map[string]interface{})
	if !p.running {
		out["running"] = false
		return out
	}
	out["running"] = true

	for _, prop := range []string{
		"idle-active", "pause", "media-title", "path",
		"playlist-count", "playlist-pos", "duration", "time-pos",
		"volume", "mute", "audio-codec-name", "audio-device",
		"audio-params", "audio-format", "audio-samplerate",
		"audio-channels", "audio-out-detected-device",
		"core-idle", "eof-reached",
	} {
		if data, err := p.command("get_property", prop); err == nil {
			var v interface{}
			if json.Unmarshal(data, &v) == nil {
				out[prop] = v
			}
		}
	}
	return out
}

// ── Status ─────────────────────────────────────────────────

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

	s.Playing = !s.Idle && !s.Paused

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
