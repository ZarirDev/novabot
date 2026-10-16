package client

import (
	"fmt"
	"log"
	"runtime"
	"strings"

	"github.com/gen2brain/malgo"
)

type DeviceInfo struct {
	Name     string
	IsLoop   bool
	DeviceID malgo.DeviceID
}

func ResolveCaptureDevice(ctx *malgo.AllocatedContext) (DeviceInfo, error) {
	switch runtime.GOOS {
	case "windows":
		return resolveWindows(ctx)
	case "linux":
		return resolveLinux(ctx)
	default:
		return DeviceInfo{}, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func resolveWindows(_ *malgo.AllocatedContext) (DeviceInfo, error) {
	return DeviceInfo{
		Name:     "default output (WASAPI loopback)",
		IsLoop:   true,
		DeviceID: malgo.DeviceID{},
	}, nil
}

// resolveLinux picks a monitor of a real output sink. Dummy/null
// sinks are skipped because they produce silence — streaming them is
// useless. When nothing real is available yet (audio stack still
// coming up at boot), returns an error so the caller can retry.
func resolveLinux(ctx *malgo.AllocatedContext) (DeviceInfo, error) {
	devices, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("enumerate capture devices: %w", err)
	}

	log.Printf("[CLIENT] malgo sees %d capture device(s):", len(devices))
	for _, d := range devices {
		tags := ""
		if d.IsDefault != 0 {
			tags += " [default]"
		}
		if isDummyName(d.Name()) {
			tags += " [dummy]"
		}
		log.Printf("[CLIENT]   %q%s", d.Name(), tags)
	}

	var monitors []malgo.DeviceInfo
	var others []malgo.DeviceInfo
	skippedDummy := 0
	for _, d := range devices {
		if isDummyName(d.Name()) {
			skippedDummy++
			continue
		}
		if strings.Contains(strings.ToLower(d.Name()), "monitor") {
			monitors = append(monitors, d)
		} else {
			others = append(others, d)
		}
	}

	pick := func(list []malgo.DeviceInfo) (malgo.DeviceInfo, bool) {
		if len(list) == 0 {
			return malgo.DeviceInfo{}, false
		}
		chosen := list[0]
		for _, d := range list {
			if d.IsDefault != 0 {
				chosen = d
				break
			}
		}
		return chosen, true
	}

	if chosen, ok := pick(monitors); ok {
		log.Printf("[CLIENT] using real monitor: %q", chosen.Name())
		return DeviceInfo{
			Name:     chosen.Name(),
			IsLoop:   false,
			DeviceID: chosen.ID,
		}, nil
	}
	if chosen, ok := pick(others); ok {
		log.Printf("[CLIENT] no monitor found; falling back to: %q", chosen.Name())
		return DeviceInfo{
			Name:     chosen.Name(),
			IsLoop:   false,
			DeviceID: chosen.ID,
		}, nil
	}

	return DeviceInfo{}, fmt.Errorf(
		"no usable capture device (%d dummy device(s) skipped). "+
			"connect a real audio output on this machine, or check that "+
			"PulseAudio/PipeWire is running with a non-null default sink",
		skippedDummy)
}

// isDummyName matches the names ALSA/PulseAudio/PipeWire give to
// placeholder null sinks. These produce silence at full rate, so any
// capture from them yields empty audio.
func isDummyName(name string) bool {
	n := strings.ToLower(name)
	for _, s := range []string{
		"dummy",
		"null",
		"auto_null",
		"auto-null",
		"auto_null.monitor",
	} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}
