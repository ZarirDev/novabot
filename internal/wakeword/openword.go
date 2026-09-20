package wakeword

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/ZarirDev/novabot/internal/audio"
	oww "github.com/stanislaw-glogowski/openwakeword_go"
	ort "github.com/yalue/onnxruntime_go"
)

// After a match we require this many consecutive non-detections before
// the detector can fire again. ~10 frames * 80ms = 800ms of quiet.
const rearmQuietFrames = 10

type OpenWakeWordDetector struct {
	wakeWord string
	engine   *oww.Engine
	vad      *oww.VAD

	threshold float64
	cooldown  time.Duration

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

	log.Printf("[WAKEWORD] openWakeWord loaded | model=%s | threshold=0.5 | rearm=%d frames",
		wakeWordModel, rearmQuietFrames)

	return &OpenWakeWordDetector{
		wakeWord:  wakeWord,
		engine:    engine,
		vad:       vad,
		threshold: 0.5,
		cooldown:  1500 * time.Millisecond,
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

	// rearm state — "armed" means we're allowed to fire on a detection.
	// After firing we go to "not armed" and must see rearmQuietFrames
	// consecutive non-detections before we re-arm.
	armed := true
	quietFrames := 0
	var cooldownUntil time.Time

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
				log.Printf("[WAKEWORD] ReadFrame error: %v", err)
				time.Sleep(100 * time.Millisecond)
				continue
			}

			rms := audio.CalculateRMS(intSamples)
			if meterOn {
				renderMeter(dbfs(rms), rms >= 500)
			}

			// Always feed the model — never skip frames, or the mel/embedding
			// pipeline loses continuity and the score becomes unreliable.
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
				// Reset the quiet counter — we just saw a detection.
				quietFrames = 0

				if !armed || time.Now().Before(cooldownUntil) {
					// Still in the tail of a previous trigger. Swallow it.
					continue
				}

				if meterOn {
					fmt.Fprintln(os.Stdout)
				}
				log.Printf("[WAKEWORD] match: '%s' detected (rearming after %d quiet frames)",
					d.wakeWord, rearmQuietFrames)

				// Latch: no new fires until the model goes quiet again.
				armed = false
				cooldownUntil = time.Now().Add(d.cooldown)

				go onDetected()
				continue
			}

			// No detection this frame. If we're latched, count consecutive
			// non-detections and re-arm once we've seen enough.
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
