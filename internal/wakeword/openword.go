package wakeword

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/ZarirDev/novabot/internal/audio"
	oww "github.com/stanislaw-glogowski/openwakeword_go"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	rearmQuietFrames = 10

	// RMS below this is treated as silence. Once we see this many
	// consecutive silent frames, we stop calling Detect() entirely until
	// audio returns. This is the dominant CPU fix: the mel/embedding/
	// classifier pipeline costs ~40% of one core while it runs, and it
	// runs on every 80 ms frame by default.
	defaultSilenceRMS    = 400.0
	defaultSilenceFrames = 25 // 25 × 80 ms = 2 s
)

type OpenWakeWordDetector struct {
	wakeWord string
	engine   *oww.Engine
	vad      *oww.VAD

	threshold     float64
	cooldown      time.Duration
	silenceRMS    float64
	silenceFrames int

	mu         sync.Mutex
	cancelFunc context.CancelFunc
	active     bool
}

func NewOpenWakeWordDetector(wakeWord, runtimePath, modelDir, wakeWordModel string) (*OpenWakeWordDetector, error) {
	ort.SetSharedLibraryPath(runtimePath)
	if err := ort.InitializeEnvironment(); err != nil {
		return nil, fmt.Errorf("onnxruntime init: %w", err)
	}

	features, err := oww.NewAudioFeatures(
		modelDir+"/melspectrogram.onnx",
		modelDir+"/embedding_model.onnx",
	)
	if err != nil {
		return nil, fmt.Errorf("audio features: %w", err)
	}

	vad, err := oww.NewVAD(
		modelDir+"/silero_vad.onnx",
		oww.WithVADThreshold(0.5),
	)
	if err != nil {
		return nil, fmt.Errorf("silero vad: %w", err)
	}

	engine, err := oww.New(features, vad)
	if err != nil {
		return nil, fmt.Errorf("oww engine: %w", err)
	}

	if err := engine.AddModel(
		modelDir+"/"+wakeWordModel,
		oww.WithModelName(wakeWord),
		oww.WithModelThreshold(0.5),
		oww.WithModelPredictionHistory(30),
	); err != nil {
		return nil, fmt.Errorf("add wake model: %w", err)
	}

	silenceRMS := envFloat("WAKE_SILENCE_RMS", defaultSilenceRMS)
	silenceFrames := envInt("WAKE_SILENCE_FRAMES", defaultSilenceFrames)

	log.Printf("[WAKEWORD] openWakeWord loaded | model=%s | silence gate=%.0f rms / %d frames",
		wakeWordModel, silenceRMS, silenceFrames)

	return &OpenWakeWordDetector{
		wakeWord:      wakeWord,
		engine:        engine,
		vad:           vad,
		threshold:     0.5,
		cooldown:      1500 * time.Millisecond,
		silenceRMS:    silenceRMS,
		silenceFrames: silenceFrames,
	}, nil
}

func (d *OpenWakeWordDetector) Start(ctx context.Context, onDetected func()) error {
	d.mu.Lock()
	subCtx, cancel := context.WithCancel(ctx)
	d.cancelFunc = cancel
	d.active = true
	d.mu.Unlock()

	log.Printf("[WAKEWORD] openWakeWord detector active — say '%s'", d.wakeWord)
	go d.loop(subCtx, onDetected)
	return nil
}

func (d *OpenWakeWordDetector) loop(ctx context.Context, onDetected func()) {
	stream, err := audio.StartMicCapture(ctx)
	if err != nil {
		log.Printf("[WAKEWORD] mic open failed: %v", err)
		return
	}
	defer stream.Close()

	frame := make(oww.Samples, oww.FrameSamples)
	intSamples := make([]int16, oww.FrameSamples)

	meterOn := os.Getenv("NOVABOT_AUDIO_METER") == "1"

	armed := true
	var readErrCount int
	quietFrames := 0
	var cooldownUntil time.Time

	silentRun := 0
	gated := false

	for {
		select {
		case <-ctx.Done():
			if meterOn {
				fmt.Fprintln(os.Stdout)
			}
			log.Println("[WAKEWORD] openWakeWord loop stopped, mic closed.")
			return
		default:
			if err := stream.ReadFrame(intSamples); err != nil {
				readErrCount++
				if readErrCount == 1 || readErrCount%200 == 0 {
					log.Printf("[WAKEWORD] ReadFrame error (%d consecutive): %v",
						readErrCount, err)
				}
				time.Sleep(500 * time.Millisecond)
				continue
			}
			readErrCount = 0
			rms := audio.CalculateRMS(intSamples)
			if meterOn {
				renderMeter(dbfs(rms), rms >= d.silenceRMS)
			}

			// ── Silence gate ──────────────────────────────────────
			// While sustained silence: skip Detect() entirely. The
			// engine's internal state freezes, which saves ~40% of one
			// core. When audio resumes we reset the engine so the
			// classifier doesn't see stale mel features mixed with
			// fresh audio — that discontinuity is what causes false
			// fires on the first syllable of any speech.
			if rms < d.silenceRMS {
				silentRun++
				if silentRun >= d.silenceFrames {
					if !gated {
						log.Printf("[WAKEWORD] silence gate engaged (rms=%.0f)", rms)
						gated = true
					}
					continue
				}
			} else {
				if gated {
					log.Printf("[WAKEWORD] silence gate released (rms=%.0f)", rms)
					gated = false
					// Flush stale pipeline state before feeding real
					// audio. The engine's history guard (len < 5) then
					// gives us ~400 ms of natural warm-up where no fire
					// can happen while the mel/embedding buffer refills.
					d.engine.Reset()
				}
				silentRun = 0
			}

			for i, s := range intSamples {
				frame[i] = float32(s) / 32768.0
			}

			detections, err := d.engine.Detect(frame)
			if err != nil {
				log.Printf("[WAKEWORD] detect error: %v", err)
				continue
			}

			hit := detections[d.wakeWord]

			if hit {
				quietFrames = 0
				if !armed || time.Now().Before(cooldownUntil) {
					continue
				}
				if meterOn {
					fmt.Fprintln(os.Stdout)
				}
				log.Printf("[WAKEWORD] match: '%s' detected", d.wakeWord)
				armed = false
				cooldownUntil = time.Now().Add(d.cooldown)
				go onDetected()
				continue
			}

			if !armed {
				quietFrames++
				if quietFrames >= rearmQuietFrames && time.Now().After(cooldownUntil) {
					armed = true
					quietFrames = 0
				}
			}
		}
	}
}

func (d *OpenWakeWordDetector) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancelFunc != nil {
		d.cancelFunc()
		d.cancelFunc = nil
	}
	d.active = false
}

func (d *OpenWakeWordDetector) Free() {
	d.Stop()
	if d.engine != nil {
		_ = d.engine.Close()
	}
	if d.vad != nil {
		_ = d.vad.Close()
	}
	ort.DestroyEnvironment()
}

// envInt is local to this file. envFloat lives in vosk.go and is shared
// package-wide — do not redeclare it here.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
