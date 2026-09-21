package client

import (
	"fmt"
	"log"
	"sync"

	"github.com/ZarirDev/novabot/internal/audio"
	"github.com/gen2brain/malgo"
)

// Capture wraps a malgo device and delivers raw PCM to a callback.
type Capture struct {
	ctx    *malgo.AllocatedContext
	device *malgo.Device
	info   DeviceInfo
	qual   audio.Quality

	mu      sync.Mutex
	onData  func([]byte)
	running bool
}

// NewCapture opens the system audio capture device using the preset from
// AUDIO_QUALITY (default: standard).
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

	q := audio.ActiveQuality()

	// Map our format string to malgo's Format enum.
	var mf malgo.FormatType
	switch q.Format {
	case "S16_LE":
		mf = malgo.FormatS16
	case "S24_LE":
		mf = malgo.FormatS24
	case "S32_LE":
		mf = malgo.FormatS32
	default:
		mf = malgo.FormatS16
	}

	devCfg := malgo.DefaultDeviceConfig(malgo.Capture)
	devCfg.Capture.Format = mf
	devCfg.Capture.Channels = uint32(q.Channels)
	devCfg.SampleRate = uint32(q.SampleRate)
	devCfg.PeriodSizeInFrames = uint32(q.FramesPerPeriod())
	devCfg.Alsa.NoMMap = 1

	if info.IsLoop {
		devCfg = malgo.DefaultDeviceConfig(malgo.Loopback)
		devCfg.Capture.Format = mf
		devCfg.Capture.Channels = uint32(q.Channels)
		devCfg.SampleRate = uint32(q.SampleRate)
		devCfg.PeriodSizeInFrames = uint32(q.FramesPerPeriod())
	} else {
		devCfg.Capture.DeviceID = info.DeviceID.Pointer()
	}

	c := &Capture{
		ctx:  ctx,
		info: info,
		qual: q,
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
	log.Printf("[CLIENT] format: %s | %d Hz | %d ch | %s | %d kbps | %d frames/period (~%dms)",
		q.Name, q.SampleRate, q.Channels, q.Format, q.Bandwidth(),
		q.FramesPerPeriod(), q.FramesPerPeriod()*1000/q.SampleRate)
	return c, nil
}

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

func (c *Capture) onRecvFrames(out, in []byte, frameCount uint32) {
	if len(in) == 0 {
		return
	}

	buf := make([]byte, len(in))
	copy(buf, in)

	c.mu.Lock()
	fn := c.onData
	c.mu.Unlock()

	if fn != nil {
		fn(buf)
	}
}
