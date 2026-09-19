package wakeword

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/ZarirDev/novabot/internal/audio"
)

type HeuristicDetector struct {
	wakeWord   string
	active     bool
	mu         sync.Mutex
	cancelFunc context.CancelFunc
}

func NewHeuristicDetector(wakeWord string) *HeuristicDetector {
	return &HeuristicDetector{wakeWord: wakeWord}
}

func (d *HeuristicDetector) Start(ctx context.Context, onDetected func()) error {
	d.mu.Lock()
	subCtx, cancel := context.WithCancel(ctx)
	d.cancelFunc = cancel
	d.active = true
	d.mu.Unlock()

	log.Printf("[WAKEWORD] Heuristic detector started for '%s' (fallback mode)", d.wakeWord)

	go func() {
		stream, err := audio.StartMicCapture(subCtx)
		if err != nil {
			log.Printf("[WAKEWORD] Mic open error: %v", err)
			return
		}
		defer stream.Close()

		samples := make([]int16, audio.FrameSize)
		var burstCount int
		var cooldownUntil time.Time

		for {
			select {
			case <-subCtx.Done():
				return
			default:
				if err := stream.ReadFrame(samples); err != nil {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				if time.Now().Before(cooldownUntil) {
					continue
				}

				rms := audio.CalculateRMS(samples)
				if rms > 1200.0 {
					zc := calculateZeroCrossings(samples)
					if zc > 15 && zc < 90 {
						burstCount++
					}
					if burstCount >= 4 {
						burstCount = 0
						cooldownUntil = time.Now().Add(3 * time.Second)
						go onDetected()
					}
				} else if burstCount > 0 {
					burstCount--
				}
			}
		}
	}()
	return nil
}

func (d *HeuristicDetector) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancelFunc != nil {
		d.cancelFunc()
		d.cancelFunc = nil
	}
	d.active = false
}

func calculateZeroCrossings(samples []int16) int {
	crossings := 0
	for i := 1; i < len(samples); i++ {
		if (samples[i] >= 0 && samples[i-1] < 0) || (samples[i] < 0 && samples[i-1] >= 0) {
			crossings++
		}
	}
	return crossings
}
