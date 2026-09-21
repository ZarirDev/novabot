package client

import (
	"fmt"
	"log"
	"sync"

	"github.com/ZarirDev/novabot/internal/audio"
	"github.com/gen2brain/malgo"
)

// Capture wraps a malgo device and delivers raw PCM to a callback.
//
// Restart semantics: the malgo DeviceCallbacks are bound to *this* Capture
// via method values. Do NOT allocate a second Capture and steal its
// internals — the callbacks would point at the wrong receiver and audio
// would go nowhere. Instead, uninit the old device and init a new one on
// the same object; malgo re-registers the callback against the same
// receiver.
type Capture struct {
	ctx    *malgo.AllocatedContext
	device *malgo.Device
	info   DeviceInfo
	qual   audio.Quality

	mu      sync.Mutex
	onData  func([]byte)
	running bool
}

// NewCapture opens a context and a capture device using the currently
// active quality preset.
func NewCapture() (*Capture, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init context: %w", err)
	}

	c := &Capture{ctx: ctx}

	info, err := ResolveCaptureDevice(ctx)
	if err != nil {
		ctx.Uninit()
		ctx.Free()
		return nil, err
	}
	c.info = info

	if err := c.initDevice(); err != nil {
		ctx.Uninit()
		ctx.Free()
		return nil, err
	}

	return c, nil
}

// initDevice (re)creates the malgo device using the currently active
// quality preset. Safe to call repeatedly on the same Capture — it
// uninitializes any previous device first. Must not be called with c.mu held.
func (c *Capture) initDevice() error {
	// Uninit the old device outside the lock — malgo may wait for the
	// currently-running callback to finish, and that callback tries to
	// acquire c.mu, so holding it here would deadlock.
	c.mu.Lock()
	oldDev := c.device
	c.device = nil
	c.mu.Unlock()

	if oldDev != nil {
		oldDev.Uninit()
	}

	q := audio.ActiveQuality()
	c.qual = q

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

	var devCfg malgo.DeviceConfig
	if c.info.IsLoop {
		devCfg = malgo.DefaultDeviceConfig(malgo.Loopback)
	} else {
		devCfg = malgo.DefaultDeviceConfig(malgo.Capture)
		devCfg.Capture.DeviceID = c.info.DeviceID.Pointer()
	}
	devCfg.Capture.Format = mf
	devCfg.Capture.Channels = uint32(q.Channels)
	devCfg.SampleRate = uint32(q.SampleRate)
	devCfg.PeriodSizeInFrames = uint32(q.FramesPerPeriod())
	devCfg.Alsa.NoMMap = 1

	// Important: the callback receiver here is *this* c, not a temporary.
	callbacks := malgo.DeviceCallbacks{
		Data: c.onRecvFrames,
	}

	dev, err := malgo.InitDevice(c.ctx.Context, devCfg, callbacks)
	if err != nil {
		return fmt.Errorf("init device: %w", err)
	}

	c.mu.Lock()
	c.device = dev
	c.mu.Unlock()

	log.Printf("[CLIENT] capture device: %s", c.info.Name)
	log.Printf("[CLIENT] format: %s | %d Hz | %d ch | %s | %d kbps | %d frames/period (~%dms)",
		q.Name, q.SampleRate, q.Channels, q.Format, q.Bandwidth(),
		q.FramesPerPeriod(), q.FramesPerPeriod()*1000/q.SampleRate)
	return nil
}

// Start begins capture, delivering raw PCM to onData.
func (c *Capture) Start(onData func([]byte)) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	c.onData = onData
	dev := c.device
	c.mu.Unlock()

	if dev == nil {
		return fmt.Errorf("start: no device")
	}

	if err := dev.Start(); err != nil {
		return fmt.Errorf("start device: %w", err)
	}

	c.mu.Lock()
	c.running = true
	c.mu.Unlock()

	log.Printf("[CLIENT] capture running")
	return nil
}

// Stop halts capture but leaves the context alive so Restart can reuse it.
func (c *Capture) Stop() {
	c.mu.Lock()
	if c.running && c.device != nil {
		_ = c.device.Stop()
		c.running = false
	}
	dev := c.device
	c.device = nil
	c.mu.Unlock()

	if dev != nil {
		dev.Uninit()
	}
}

// Restart rebuilds the capture device using the currently active quality
// preset, preserving the callback receiver and the onData hookup.
func (c *Capture) Restart(onData func([]byte)) error {
	c.mu.Lock()
	if c.running && c.device != nil {
		_ = c.device.Stop()
		c.running = false
	}
	oldDev := c.device
	c.device = nil
	c.mu.Unlock()

	if oldDev != nil {
		oldDev.Uninit()
	}

	if err := c.initDevice(); err != nil {
		return fmt.Errorf("reinit device: %w", err)
	}

	return c.Start(onData)
}

// Close fully tears down the device and the context. Call once at shutdown.
func (c *Capture) Close() {
	c.Stop()
	if c.ctx != nil {
		c.ctx.Uninit()
		c.ctx.Free()
		c.ctx = nil
	}
}

// onRecvFrames is invoked by malgo on the realtime audio thread.
// It must stay a method value bound to a stable *Capture — never to a
// temporary object.
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
