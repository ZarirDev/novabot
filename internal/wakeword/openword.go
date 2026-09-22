package wakeword

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ZarirDev/novabot/internal/audio"
	oww "github.com/stanislaw-glogowski/openwakeword_go"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	rearmQuietFrames = 10

	// 0.7 is a conservative threshold. The library's own default is 0.9;
	// 0.5 (which we shipped before) was too permissive and caused fires on
	// unrelated speech. Patience requires sustained score, not just a spike.
	defaultWakeThreshold = 0.70
	defaultPatience      = 2

	// Skip inference entirely during sustained silence.
	defaultSilenceRMS    = 400.0
	defaultSilenceFrames = 25 // 25 × 80 ms = 2 s
)

type OpenWakeWordDetector struct {
	wakeWord string
	engine   *oww.Engine
	vad      *oww.VAD

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

	threshold := float32(envFloat("WAKE_THRESHOLD", defaultWakeThreshold))
	patience := envInt("WAKE_PATIENCE", defaultPatience)

	if err := engine.AddModel(
		modelDir+"/"+wakeWordModel,
		oww.WithModelName(wakeWord),
		oww.WithModelThreshold(threshold),
		oww.WithModelPredictionHistory(30),
		oww.WithModelPatience(patience),
	); err != nil {
		return nil, fmt.Errorf("add wake model: %w", err)
	}

	silenceRMS := envFloat("WAKE_SILENCE_RMS", defaultSilenceRMS)
	silenceFrames := envInt("WAKE_SILENCE_FRAMES", defaultSilenceFrames)

	log.Printf("[WAKEWORD] openWakeWord loaded | model=%s | threshold=%.2f | patience=%d | silence gate=%.0f rms / %d frames",
		wakeWordModel, threshold, patience, silenceRMS, silenceFrames)

	return &OpenWakeWordDetector{
		wakeWord:      wakeWord,
		engine:        engine,
		vad:           vad,
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

	log.Printf("[WAKEWORD] detector active — say '%s'", d.wakeWord)
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
			log.Println("[WAKEWORD] loop stopped, mic closed.")
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

			// Silence gate: skip inference during sustained quiet.
			// We deliberately do NOT reset the engine here — that would
			// create a discontinuity when audio resumes, and the model
			// scores erratically on a cold buffer. The engine's own
			// history-length guard handles warm-up.
			if rms < d.silenceRMS {
				silentRun++
				if silentRun >= d.silenceFrames {
					if !gated {
						if audio.WakewordLogEnabled() {
							log.Printf("[WAKEWORD] silence gate engaged (rms=%.0f)", rms)
						}
						gated = true
					}
					continue
				}
			} else {
				if gated {
					if audio.WakewordLogEnabled() {
						log.Printf("[WAKEWORD] silence gate released (rms=%.0f)", rms)
					}
					gated = false
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

			if detections[d.wakeWord] {
				quietFrames = 0
				if !armed || time.Now().Before(cooldownUntil) {
					continue
				}
				if meterOn {
					fmt.Fprintln(os.Stdout)
				}
				log.Printf("[WAKEWORD] match: '%s'", d.wakeWord)
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

// ── helpers ─────────────────────────────────────────────

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func dbfs(rms float64) float64 {
	if rms < 1 {
		return -100
	}
	db := 20 * math.Log10(rms/32768.0)
	if db < -100 {
		return -100
	}
	return db
}

func renderMeter(db float64, speaking bool) {
	level := int((db + 80) / 80 * 40)
	if level < 0 {
		level = 0
	}
	if level > 40 {
		level = 40
	}
	bar := strings.Repeat("█", level) + strings.Repeat("·", 40-level)
	marker := " "
	if speaking {
		marker = "●"
	}
	fmt.Fprintf(os.Stdout, "\r[MIC] %6.1f dBFS |%s| %s            ", db, bar, marker)
}
