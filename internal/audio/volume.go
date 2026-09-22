package audio

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	volumeMu      sync.RWMutex
	volumePercent = 100 // 0..200
)

// Volume returns the current target volume (0-200).
func Volume() int {
	volumeMu.RLock()
	defer volumeMu.RUnlock()
	return volumePercent
}

// SetVolume updates the target and applies it to every currently-running
// aplay/mpv stream in PulseAudio. Returns the value actually stored.
func SetVolume(pct int) int {
	if pct < 0 {
		pct = 0
	}
	if pct > 200 {
		pct = 200
	}
	volumeMu.Lock()
	volumePercent = pct
	volumeMu.Unlock()

	applyToRunningStreams()
	return pct
}

// applyToRunningStreams finds every aplay/mpv sink-input and sets its
// volume to the current target. Called whenever SetVolume changes, so
// playing streams update immediately without restarting.
func applyToRunningStreams() {
	out, err := exec.Command("pactl", "list", "sink-inputs").Output()
	if err != nil {
		return
	}

	pct := Volume()
	targets := map[string]bool{
		"aplay":                true,
		"ALSA plug-in [aplay]": true,
		"mpv":                  true,
		"ALSA plug-in [mpv]":   true,
	}

	currentID := 0
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		if strings.HasPrefix(line, "Sink Input #") {
			if id, err := strconv.Atoi(strings.TrimPrefix(line, "Sink Input #")); err == nil {
				currentID = id
			}
			continue
		}

		if strings.HasPrefix(line, "application.name = ") && currentID > 0 {
			name := strings.Trim(strings.TrimPrefix(line, "application.name = "), `"`)
			if targets[name] {
				_ = exec.Command("pactl", "set-sink-input-volume",
					strconv.Itoa(currentID),
					fmt.Sprintf("%d%%", pct)).Run()
			}
		}
	}
}

// applyVolumeToPID is called from player.go right after spawning a new
// aplay so it starts at the correct volume even if SetVolume hasn't been
// called since it launched.
func applyVolumeToPID(pid int) {
	pct := Volume()
	for i := 0; i < 8; i++ {
		id, err := sinkInputForPID(pid)
		if err == nil && id > 0 {
			_ = exec.Command("pactl", "set-sink-input-volume",
				strconv.Itoa(id), fmt.Sprintf("%d%%", pct)).Run()
			return
		}
		time.Sleep(60 * time.Millisecond)
	}
}

func sinkInputForPID(pid int) (int, error) {
	out, err := exec.Command("pactl", "list", "sink-inputs").Output()
	if err != nil {
		return 0, err
	}
	needle := fmt.Sprintf("application.process.id = \"%d\"", pid)
	currentID := 0
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "Sink Input #") {
			if id, err := strconv.Atoi(strings.TrimPrefix(line, "Sink Input #")); err == nil {
				currentID = id
			}
		}
		if strings.Contains(line, needle) && currentID > 0 {
			return currentID, nil
		}
	}
	return 0, fmt.Errorf("no sink-input for pid %d", pid)
}
