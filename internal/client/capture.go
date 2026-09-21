package client

import (
	"fmt"
	"log"
	"sync"

	"github.com/gen2brain/malgo"
)

const (
	// Match the server's expectation: 48 kHz, stereo, S16_LE.
	SampleRate = 48000
	Channels   = 2
	Format     = malgo.FormatS16
	// 20 ms of audio per callback — low latency, reasonable packet size.
	FramesPerPeriod = 960
)

// Capture wraps a malgo device and delivers raw PCM to a callback.
type Capture struct {
	ctx    *malgo.AllocatedContext
	device *malgo.Device
	info   DeviceInfo

	mu      sync.Mutex
	onData  func([]byte)
	running bool
}

// NewCapture opens the system audio capture device.
func NewCapture() (*Capture, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init context: %w", err)
	}

	info, err := ResolveCaptureDevice(ctx)
	if err != nil {
		ctx.Uninit()
		ctx.Free()
		return nil, err
	}

	devCfg := malgo.DefaultDeviceConfig(malgo.Capture)
	devCfg.Capture.Format = Format
	devCfg.Capture.Channels = Channels
	devCfg.SampleRate = SampleRate
	devCfg.PeriodSizeInFrames = FramesPerPeriod
	devCfg.Alsa.NoMMap = 1 // avoid mmap issues on some ALSA setups

	if info.IsLoop {
		// Windows: switch to loopback mode.
		devCfg = malgo.DefaultDeviceConfig(malgo.Loopback)
		devCfg.Capture.Format = Format
		devCfg.Capture.Channels = Channels
		devCfg.SampleRate = SampleRate
		devCfg.PeriodSizeInFrames = FramesPerPeriod
	} else {
		// Linux: point at the monitor source explicitly.
		devCfg.Capture.DeviceID = info.DeviceID.Pointer()
	}

	c := &Capture{
		ctx:  ctx,
		info: info,
	}

	callbacks := malgo.DeviceCallbacks{
		Data: c.onRecvFrames,
	}

	dev, err := malgo.InitDevice(ctx.Context, devCfg, callbacks)
	if err != nil {
		ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("init device: %w", err)
	}

	c.device = dev
	log.Printf("[CLIENT] capture device: %s", info.Name)
	return c, nil
}

// Start begins capturing. The onData callback receives raw S16_LE interleaved PCM.
func (c *Capture) Start(onData func([]byte)) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return nil
	}

	c.onData = onData

	if err := c.device.Start(); err != nil {
		return fmt.Errorf("start device: %w", err)
	}

	c.running = true
	return nil
}

// Stop halts capture and releases resources.
func (c *Capture) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		_ = c.device.Stop()
		c.running = false
	}

	c.device.Uninit()
	c.ctx.Uninit()
	c.ctx.Free()
}

// onRecvFrames is called by miniaudio on the realtime audio thread.
// Keep it minimal: copy the bytes and hand them off.
func (c *Capture) onRecvFrames(out, in []byte, frameCount uint32) {
	if len(in) == 0 {
		return
	}

	// in is already S16_LE interleaved stereo at our requested rate.
	buf := make([]byte, len(in))
	copy(buf, in)

	c.mu.Lock()
	fn := c.onData
	c.mu.Unlock()

	if fn != nil {
		fn(buf)
	}
}
