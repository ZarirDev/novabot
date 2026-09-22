package audio

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
)

type AudioDevice struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
	Kind        string `json:"kind"`
	State       string `json:"state,omitempty"`
	Mute        bool   `json:"mute,omitempty"`
}

func ListSinks() ([]AudioDevice, error) {
	out, err := exec.Command("pactl", "list", "sinks").Output()
	if err != nil {
		return nil, fmt.Errorf("pactl list sinks: %w", err)
	}
	devs := parsePactlDevices(string(out), "Sink")
	markDefault(devs, "Sink")
	return devs, nil
}

func ListSources() ([]AudioDevice, error) {
	out, err := exec.Command("pactl", "list", "sources").Output()
	if err != nil {
		return nil, fmt.Errorf("pactl list sources: %w", err)
	}
	devs := parsePactlDevices(string(out), "Source")
	markDefault(devs, "Source")
	return devs, nil
}

func parsePactlDevices(out, kind string) []AudioDevice {
	var devices []AudioDevice
	var current *AudioDevice

	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		if strings.HasPrefix(line, kind+" #") {
			if current != nil {
				current.Kind = strings.ToLower(kind)
				devices = append(devices, *current)
			}
			current = &AudioDevice{}
			continue
		}
		if current == nil {
			continue
		}

		switch {
		case strings.HasPrefix(line, "Name:"):
			current.Name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
		case strings.HasPrefix(line, "Description:"):
			current.Description = strings.TrimSpace(strings.TrimPrefix(line, "Description:"))
		case strings.HasPrefix(line, "State:"):
			current.State = strings.TrimSpace(strings.TrimPrefix(line, "State:"))
		case strings.HasPrefix(line, "Mute:"):
			current.Mute = strings.TrimSpace(strings.TrimPrefix(line, "Mute:")) == "yes"
		}
	}
	if current != nil {
		current.Kind = strings.ToLower(kind)
		devices = append(devices, *current)
	}
	return devices
}

func markDefault(devs []AudioDevice, kind string) {
	def, err := getDefault(kind)
	if err != nil || def == "" {
		return
	}
	for i := range devs {
		if devs[i].Name == def {
			devs[i].IsDefault = true
		}
	}
}

func getDefault(kind string) (string, error) {
	out, err := exec.Command("pactl", "info").Output()
	if err != nil {
		return "", err
	}
	prefix := "Default Sink:"
	if kind == "Source" {
		prefix = "Default Source:"
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix)), nil
		}
	}
	return "", fmt.Errorf("no default %s found", kind)
}

// DefaultSinkName returns the currently active default sink's name, or
// "" if we can't determine it. Used to pin child processes (aplay, mpv)
// to a specific sink via PULSE_SINK.
func DefaultSinkName() string {
	name, err := getDefault("Sink")
	if err != nil {
		return ""
	}
	return name
}

// SetDefaultSink changes the default output, unmutes it, sets volume to
// the given percentage (pass -1 to leave volume alone), and moves every
// existing sink-input to the new sink.
func SetDefaultSink(name string, volumePct int) error {
	if err := exec.Command("pactl", "set-default-sink", name).Run(); err != nil {
		return fmt.Errorf("set-default-sink: %w", err)
	}

	// Unmute — a muted master is a common cause of "everything is
	// playing but I hear nothing".
	_ = exec.Command("pactl", "set-sink-mute", name, "0").Run()

	if volumePct > 0 {
		_ = exec.Command("pactl", "set-sink-volume", name,
			fmt.Sprintf("%d%%", volumePct)).Run()
	}

	moveAllSinkInputs(name)
	return nil
}

func SetDefaultSource(name string) error {
	if err := exec.Command("pactl", "set-default-source", name).Run(); err != nil {
		return fmt.Errorf("set-default-source: %w", err)
	}
	_ = exec.Command("pactl", "set-source-mute", name, "0").Run()
	return nil
}

// moveAllSinkInputs relocates every playback stream to the given sink.
func moveAllSinkInputs(sink string) {
	out, err := exec.Command("pactl", "list", "sink-inputs", "short").Output()
	if err != nil {
		return
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 1 {
			continue
		}
		id := fields[0]
		_ = exec.Command("pactl", "move-sink-input", id, sink).Run()
	}
}
