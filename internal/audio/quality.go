package audio

import (
	"fmt"
	"os"
)

// Quality describes the PCM format used for the PC_AUDIO UDP stream.
// Both the client and server read AUDIO_QUALITY and pick the same preset,
// so they stay in sync automatically.
type Quality struct {
	Name         string
	SampleRate   int
	Channels     int
	Format       string // ALSA format string: "S16_LE", "S24_LE", "S32_LE"
	BytesPerSmpl int    // 2 for S16, 3 for S24, 4 for S32
}

// Bandwidth is the raw bitrate of the stream in kbps (no protocol overhead).
func (q Quality) Bandwidth() int {
	return q.SampleRate * q.Channels * q.BytesPerSmpl * 8 / 1000
}

// FramesPerPeriod returns the number of samples per 20 ms packet.
func (q Quality) FramesPerPeriod() int {
	return q.SampleRate / 50
}

// ByteRate is bytes per second of PCM.
func (q Quality) ByteRate() int {
	return q.SampleRate * q.Channels * q.BytesPerSmpl
}

var qualityPresets = map[string]Quality{
	"low": {
		Name: "low", SampleRate: 16000, Channels: 1,
		Format: "S16_LE", BytesPerSmpl: 2,
	}, // 256 kbps — voice-grade
	"standard": {
		Name: "standard", SampleRate: 48000, Channels: 2,
		Format: "S16_LE", BytesPerSmpl: 2,
	}, // 1536 kbps — CD-equivalent
	"high": {
		Name: "high", SampleRate: 48000, Channels: 2,
		Format: "S32_LE", BytesPerSmpl: 4,
	}, // 3072 kbps — 32-bit headroom
	"studio": {
		Name: "studio", SampleRate: 96000, Channels: 2,
		Format: "S32_LE", BytesPerSmpl: 4,
	}, // 6144 kbps — needs wired LAN and a decent sink
}

// ActiveQuality returns the configured preset. Defaults to "standard".
func ActiveQuality() Quality {
	name := os.Getenv("AUDIO_QUALITY")
	if name == "" {
		name = "standard"
	}
	if q, ok := qualityPresets[name]; ok {
		return q
	}
	// Unknown preset name — fall back and warn once at init.
	return qualityPresets["standard"]
}

// QualityByName exposes the lookup for the config package or CLI flags.
func QualityByName(name string) (Quality, error) {
	if q, ok := qualityPresets[name]; ok {
		return q, nil
	}
	return Quality{}, fmt.Errorf("unknown audio quality %q (valid: low, standard, high, studio)", name)
}

// AllQualities returns every preset — used by the web UI to render a picker.
func AllQualities() []Quality {
	return []Quality{
		qualityPresets["low"],
		qualityPresets["standard"],
		qualityPresets["high"],
		qualityPresets["studio"],
	}
}
