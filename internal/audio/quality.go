package audio

import (
	"fmt"
	"os"
	"sync"
)

// Quality describes the PCM format used for the PC_AUDIO UDP stream.
type Quality struct {
	Name         string `json:"name"`
	SampleRate   int    `json:"sample_rate"`
	Channels     int    `json:"channels"`
	Format       string `json:"format"`
	BytesPerSmpl int    `json:"-"`
	Description  string `json:"description"`
}

func (q Quality) Bandwidth() int {
	return q.SampleRate * q.Channels * q.BytesPerSmpl * 8 / 1000
}

func (q Quality) FramesPerPeriod() int {
	return q.SampleRate / 50
}

var qualityPresets = map[string]Quality{
	"low": {
		Name: "low", SampleRate: 16000, Channels: 1,
		Format: "S16_LE", BytesPerSmpl: 2,
		Description: "256 kbps · 16 kHz mono",
	},
	"standard": {
		Name: "standard", SampleRate: 48000, Channels: 2,
		Format: "S16_LE", BytesPerSmpl: 2,
		Description: "1536 kbps · 48 kHz stereo 16-bit",
	},
	"high": {
		Name: "high", SampleRate: 48000, Channels: 2,
		Format: "S32_LE", BytesPerSmpl: 4,
		Description: "3072 kbps · 48 kHz stereo 32-bit",
	},
	"studio": {
		Name: "studio", SampleRate: 96000, Channels: 2,
		Format: "S32_LE", BytesPerSmpl: 4,
		Description: "6144 kbps · 96 kHz stereo 32-bit",
	},
}

var (
	qualityMu     sync.RWMutex
	activeQuality = qualityPresets["standard"]
)

// init seeds the active quality from AUDIO_QUALITY if set. After this,
// changes come from the settings API, not the environment.
func init() {
	if name := os.Getenv("AUDIO_QUALITY"); name != "" {
		if q, ok := qualityPresets[name]; ok {
			activeQuality = q
		}
	}
}

// ActiveQuality returns the current preset. Cheap; takes RLock.
func ActiveQuality() Quality {
	qualityMu.RLock()
	defer qualityMu.RUnlock()
	return activeQuality
}

// SetActiveQuality swaps the preset at runtime. Callers are responsible
// for restarting anything that's currently streaming.
func SetActiveQuality(name string) error {
	q, ok := qualityPresets[name]
	if !ok {
		return fmt.Errorf("unknown audio quality %q", name)
	}
	qualityMu.Lock()
	activeQuality = q
	qualityMu.Unlock()
	return nil
}

// AllQualities returns every preset in ascending order of bandwidth.
func AllQualities() []Quality {
	return []Quality{
		qualityPresets["low"],
		qualityPresets["standard"],
		qualityPresets["high"],
		qualityPresets["studio"],
	}
}
