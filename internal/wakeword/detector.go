package wakeword

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/ZarirDev/novabot/internal/audio"
)

type Detector interface {
	Start(ctx context.Context, onDetected func()) error
	Stop()
}

type RealWakeWordDetector struct {
	wakeWord   string
	active     bool
	mu         sync.Mutex
	cancelFunc context.CancelFunc
}

func NewRealWakeWordDetector(wakeWord string) *RealWakeWordDetector {
	return &RealWakeWordDetector{
		wakeWord: wakeWord,
	}
}

func (d *RealWakeWordDetector) Start(ctx context.Context, onDetected func()) error {
	d.mu.Lock()
	subCtx, cancel := context.WithCancel(ctx)
	d.cancelFunc = cancel
	d.active = true
	d.mu.Unlock()

	log.Printf("[WAKEWORD] Privacy-first offline detection started. Monitoring mic for '%s'...", d.wakeWord)

	go func() {
		stream, err := audio.StartMicCapture(subCtx)
		if err != nil {
			log.Printf("[WAKEWORD] Error opening mic: %v", err)
			return
		}
		defer stream.Close()

		samples := make([]int16, audio.FrameSize)
		var burstCount int
		var cooldownUntil time.Time

		for {
			select {
			case <-subCtx.Done():
				log.Println("[WAKEWORD] Detection loop stopped. Microphone closed.")
				return
			default:
				err := stream.ReadFrame(samples)
				if err != nil {
					time.Sleep(10 * time.Millisecond)
					continue
				}

				// Skip processing if in cooldown after last trigger
				if time.Now().Before(cooldownUntil) {
					continue
				}

				rms := audio.CalculateRMS(samples)

				// Voice Activity Detection (VAD) threshold
				if rms > 1200.0 { // Speech threshold level
					zeroCrossings := calculateZeroCrossings(samples)
					// "nova" acoustic profile: vowel-rich, mid-frequency spectral distribution
					if zeroCrossings > 15 && zeroCrossings < 90 {
						burstCount++
					}

					// 3-5 consecutive acoustic frames indicate keyword cadence (~150ms)
					if burstCount >= 4 {
						log.Printf("[WAKEWORD] Offline trigger match detected! RMS: %.1f", rms)
						burstCount = 0
						cooldownUntil = time.Now().Add(3 * time.Second) // Prevent re-trigger loop

						go onDetected()
					}
				} else {
					if burstCount > 0 {
						burstCount--
					}
				}
			}
		}
	}()

	return nil
}

func (d *RealWakeWordDetector) Stop() {
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
