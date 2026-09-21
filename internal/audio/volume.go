package audio

import (
	"bufio"
	"fmt"
	"log"
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

// SetVolume updates the target and applies it to any running aplay
// streams. Returns the value actually stored.
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

	applyVolumeToAll()
	return pct
}

// applyVolumeToAll walks every currently-tracked aplay PID and sets its
// PulseAudio sink-input volume.
func applyVolumeToAll() {
	for _, pid := range trackedAplayPids() {
		applyVolumeToPID(pid)
	}
}

// applyVolumeToPID finds the pactl sink-input belonging to pid and sets
// its volume. Retries briefly — PulseAudio can take ~50-300ms to register
// a new stream after the process starts.
func applyVolumeToPID(pid int) {
	pct := Volume()
	for i := 0; i < 8; i++ {
		id, err := sinkInputForPID(pid)
		if err == nil && id > 0 {
			cmd := exec.Command("pactl", "set-sink-input-volume",
				strconv.Itoa(id), fmt.Sprintf("%d%%", pct))
			if err := cmd.Run(); err != nil {
				log.Printf("[AUDIO] set-sink-input-volume(%d, %d%%) failed: %v", id, pct, err)
			}
			return
		}
		time.Sleep(60 * time.Millisecond)
	}
}

// sinkInputForPID parses `pactl list sink-inputs` and returns the sink
// input ID whose application.process.id matches pid.
func sinkInputForPID(pid int) (int, error) {
	out, err := exec.Command("pactl", "list", "sink-inputs").Output()
	if err != nil {
		return 0, err
	}

	needle := fmt.Sprintf("application.process.id = \"%d\"", pid)
	currentID := 0
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
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

// trackedAplayPids is a registry of aplay subprocesses we've spawned.
// Every entry is registered by PlayWAV / StartUDPStreamListener and
// deregistered when the process exits.
var (
	aplayMu   sync.Mutex
	aplayPids = map[int]struct{}{}
)

func registerAplay(pid int) {
	aplayMu.Lock()
	aplayPids[pid] = struct{}{}
	aplayMu.Unlock()
}

func unregisterAplay(pid int) {
	aplayMu.Lock()
	delete(aplayPids, pid)
	aplayMu.Unlock()
}

func trackedAplayPids() []int {
	aplayMu.Lock()
	defer aplayMu.Unlock()
	out := make([]int, 0, len(aplayPids))
	for pid := range aplayPids {
		out = append(out, pid)
	}
	return out
}
