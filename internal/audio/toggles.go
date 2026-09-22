package audio

import (
	"os"
	"sync/atomic"
)

// Runtime toggles for log verbosity, controlled from the settings UI.
// Both default to on, but can be silenced to keep journalctl readable.

var (
	statsLog    atomic.Bool
	wakewordLog atomic.Bool
)

func init() {
	statsLog.Store(os.Getenv("AUDIO_STATS_LOG") != "0")
	wakewordLog.Store(os.Getenv("WAKEWORD_LOG") != "0")
}

func StatsLogEnabled() bool { return statsLog.Load() }
func SetStatsLog(v bool)    { statsLog.Store(v) }

func WakewordLogEnabled() bool { return wakewordLog.Load() }
func SetWakewordLog(v bool)    { wakewordLog.Store(v) }
