package client

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/gen2brain/malgo"
)

// DeviceInfo describes the capture source we've selected.
type DeviceInfo struct {
	Name     string
	IsLoop   bool
	DeviceID malgo.DeviceID
}

// ResolveCaptureDevice picks the right source for the current OS.
//
// Windows: default output device, opened in loopback mode.
// Linux:   the default sink's monitor source (name ends in ".monitor").
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

func resolveLinux(ctx *malgo.AllocatedContext) (DeviceInfo, error) {
	devices, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("enumerate capture devices: %w", err)
	}

	// Prefer the monitor of the default sink. PulseAudio names them
	// "<sink-name>.monitor"; PipeWire follows the same convention via
	// its pulse compatibility layer.
	var candidates []malgo.DeviceInfo
	for _, d := range devices {
		if strings.Contains(strings.ToLower(d.Name()), "monitor") {
			candidates = append(candidates, d)
		}
	}

	if len(candidates) == 0 {
		return DeviceInfo{}, fmt.Errorf(
			"no monitor source found; is PulseAudio/PipeWire running and is your user in the 'audio' group?",
		)
	}

	// If one is marked default, use it; otherwise take the first.
	chosen := candidates[0]
	for _, d := range candidates {
		if d.IsDefault != 0 { // malgo's IsDefault is an int (miniaudio ma_bool32)
			chosen = d
			break
		}
	}

	return DeviceInfo{
		Name:     chosen.Name(),
		IsLoop:   false,
		DeviceID: chosen.ID, // struct field, not a method
	}, nil
}
