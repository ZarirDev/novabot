package server

import (
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/sensors"
)

const (
	historySize    = 60
	sampleInterval = 1500 * time.Millisecond
)

// ── JSON shapes ────────────────────────────────────────────

type TempReading struct {
	Key  string  `json:"key"`
	Temp float64 `json:"temp"`
}

type DiskReading struct {
	Mount   string  `json:"mount"`
	Fstype  string  `json:"fstype"`
	TotalGB float64 `json:"total_gb"`
	UsedGB  float64 `json:"used_gb"`
	Percent float64 `json:"percent"`
}

type Snapshot struct {
	CPUPercent float64   `json:"cpu_percent"`
	CPUCores   int       `json:"cpu_cores"`
	CPUHistory []float64 `json:"cpu_history"`

	MemTotalMB uint64    `json:"mem_total_mb"`
	MemUsedMB  uint64    `json:"mem_used_mb"`
	MemPercent float64   `json:"mem_percent"`
	MemHistory []float64 `json:"mem_history"`

	Load1       float64   `json:"load_avg_1"`
	Load5       float64   `json:"load_avg_5"`
	Load15      float64   `json:"load_avg_15"`
	LoadRatio   float64   `json:"load_ratio"`   // load1 / cores
	LoadHistory []float64 `json:"load_history"` // ratio over time

	CPUTemp     float64       `json:"cpu_temp"`
	CPUTempHist []float64     `json:"cpu_temp_history"`
	Temps       []TempReading `json:"temps"`

	DiskRoot DiskReading   `json:"disk_root"`
	Disks    []DiskReading `json:"disks"`

	UptimeSeconds uint64 `json:"uptime_seconds"`
	Hostname      string `json:"hostname"`
	Platform      string `json:"platform"`

	Goroutines int    `json:"goroutines"`
	GoHeapMB   uint64 `json:"go_heap_mb"`
	BotUptime  uint64 `json:"bot_uptime_seconds"`
}

// ── Collector ──────────────────────────────────────────────

type StatsCollector struct {
	mu sync.Mutex

	bootTime time.Time
	cores    int

	cpuHistory  []float64
	memHistory  []float64
	loadHistory []float64
	tempHistory []float64

	curCPU    float64
	curMemPct float64
	curMemUse uint64
	curMemTot uint64
	curL1     float64
	curL5     float64
	curL15    float64
	curTemp   float64
	temps     []TempReading
	diskRoot  DiskReading
	disks     []DiskReading

	hostname string
	platform string
}

func NewStatsCollector() *StatsCollector {
	_, _ = cpu.Percent(0, false) // prime the delta

	cores, _ := cpu.Counts(true) // logical cores (incl. HT)
	if cores == 0 {
		cores = runtime.NumCPU()
	}

	s := &StatsCollector{
		bootTime:    time.Now(),
		cores:       cores,
		cpuHistory:  make([]float64, 0, historySize),
		memHistory:  make([]float64, 0, historySize),
		loadHistory: make([]float64, 0, historySize),
		tempHistory: make([]float64, 0, historySize),
	}

	if info, err := host.Info(); err == nil {
		s.hostname = info.Hostname
		s.platform = info.Platform + " " + info.PlatformVersion
	}

	s.collect()
	return s
}

func (s *StatsCollector) Start() {
	go func() {
		t := time.NewTicker(sampleInterval)
		defer t.Stop()
		for range t.C {
			s.collect()
		}
	}()
}

func (s *StatsCollector) collect() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// CPU
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		s.curCPU = pcts[0]
		s.cpuHistory = appendRing(s.cpuHistory, pcts[0])
	}

	// Memory
	if vm, err := mem.VirtualMemory(); err == nil {
		s.curMemPct = vm.UsedPercent
		s.curMemUse = vm.Used / 1024 / 1024
		s.curMemTot = vm.Total / 1024 / 1024
		s.memHistory = appendRing(s.memHistory, vm.UsedPercent)
	}

	// Load — normalised against logical core count
	if la, err := load.Avg(); err == nil {
		s.curL1, s.curL5, s.curL15 = la.Load1, la.Load5, la.Load15
		ratio := la.Load1 / float64(s.cores)
		s.loadHistory = appendRing(s.loadHistory, ratio)
	}

	// Temperatures
	if ts, err := sensors.SensorsTemperatures(); err == nil && len(ts) > 0 {
		readings := make([]TempReading, 0, len(ts))
		var hottest float64
		for _, t := range ts {
			if t.Temperature <= 0 {
				continue
			}
			readings = append(readings, TempReading{
				Key:  t.SensorKey,
				Temp: t.Temperature,
			})
			// Prefer CPU package/core sensors for the headline number.
			if isCPUSensor(t.SensorKey) && t.Temperature > hottest {
				hottest = t.Temperature
			}
		}
		// Fallback: if no CPU-labelled sensor, use the hottest overall.
		if hottest == 0 {
			for _, r := range readings {
				if r.Temp > hottest {
					hottest = r.Temp
				}
			}
		}
		sort.Slice(readings, func(i, j int) bool { return readings[i].Temp > readings[j].Temp })
		s.temps = readings
		s.curTemp = hottest
		s.tempHistory = appendRing(s.tempHistory, hottest)
	}

	// Disk
	s.diskRoot, s.disks = collectDisks()
}

func collectDisks() (DiskReading, []DiskReading) {
	var root DiskReading
	var list []DiskReading

	if u, err := disk.Usage("/"); err == nil {
		root = DiskReading{
			Mount:   "/",
			Fstype:  u.Fstype,
			TotalGB: round2(float64(u.Total) / 1024 / 1024 / 1024),
			UsedGB:  round2(float64(u.Used) / 1024 / 1024 / 1024),
			Percent: round2(u.UsedPercent),
		}
	}

	parts, err := disk.Partitions(false)
	if err != nil {
		return root, nil
	}

	seen := map[string]bool{"/": true}
	for _, p := range parts {
		if seen[p.Mountpoint] || !isRealFS(p.Fstype) {
			continue
		}
		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u.Total < 1<<30 { // skip < 1 GB
			continue
		}
		seen[p.Mountpoint] = true
		list = append(list, DiskReading{
			Mount:   p.Mountpoint,
			Fstype:  p.Fstype,
			TotalGB: round2(float64(u.Total) / 1024 / 1024 / 1024),
			UsedGB:  round2(float64(u.Used) / 1024 / 1024 / 1024),
			Percent: round2(u.UsedPercent),
		})
	}
	return root, list
}

func isRealFS(fs string) bool {
	switch fs {
	case "ext4", "ext3", "ext2", "xfs", "btrfs", "zfs", "f2fs",
		"apfs", "ntfs", "vfat", "exfat":
		return true
	}
	return false
}

func isCPUSensor(key string) bool {
	for _, s := range []string{"package", "tctl", "tdie", "coretemp", "k10temp", "cpu"} {
		if containsFold(key, s) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	s, sub = lower(s), lower(sub)
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ── Snapshot ───────────────────────────────────────────────

func (s *StatsCollector) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	var up uint64
	if u, err := host.Uptime(); err == nil {
		up = u
	}

	ratio := 0.0
	if s.cores > 0 {
		ratio = round2(s.curL1 / float64(s.cores))
	}

	return Snapshot{
		CPUPercent:    round2(s.curCPU),
		CPUCores:      s.cores,
		CPUHistory:    clone(s.cpuHistory),
		MemTotalMB:    s.curMemTot,
		MemUsedMB:     s.curMemUse,
		MemPercent:    round2(s.curMemPct),
		MemHistory:    clone(s.memHistory),
		Load1:         round2(s.curL1),
		Load5:         round2(s.curL5),
		Load15:        round2(s.curL15),
		LoadRatio:     ratio,
		LoadHistory:   clone(s.loadHistory),
		CPUTemp:       round2(s.curTemp),
		CPUTempHist:   clone(s.tempHistory),
		Temps:         s.temps,
		DiskRoot:      s.diskRoot,
		Disks:         s.disks,
		UptimeSeconds: up,
		Hostname:      s.hostname,
		Platform:      s.platform,
		Goroutines:    runtime.NumGoroutine(),
		GoHeapMB:      m.Alloc / 1024 / 1024,
		BotUptime:     uint64(time.Since(s.bootTime).Seconds()),
	}
}

// ── Helpers ────────────────────────────────────────────────

func appendRing(h []float64, v float64) []float64 {
	h = append(h, v)
	if len(h) > historySize {
		h = h[len(h)-historySize:]
	}
	return h
}

func clone(h []float64) []float64 {
	out := make([]float64, len(h))
	copy(out, h)
	return out
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
