package openwakeword

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// Engine is a streaming wake-word detector. Calls to Predict are serialized so
// callers may share an Engine across goroutines.
type Engine struct {
	sync.Mutex
	audioFeatures *AudioFeatures
	vad           *VAD
	models        map[string]*model
	closed        bool
}

// New creates a streaming wake-word engine from an audio feature extractor and
// an optional VAD instance. The caller must add at least one model before
// calling Predict.
func New(audioFeatures *AudioFeatures, vad *VAD) (*Engine, error) {
	if audioFeatures == nil {
		return nil, errors.New("audio features are required")
	}
	return &Engine{
		audioFeatures: audioFeatures,
		vad:           vad,
		models:        make(map[string]*model),
	}, nil
}

// AddModel loads a binary wake-word ONNX model using functional options.
func (e *Engine) AddModel(path string, options ...func(*ModelOptions)) error {
	if e == nil {
		return errors.New("engine is nil")
	}
	e.Lock()
	defer e.Unlock()
	return e.addModel(path, buildModelOptions(options...))
}

// AddModelWithOptions loads a binary wake-word ONNX model using a ModelOptions
// value directly.
func (e *Engine) AddModelWithOptions(path string, opts ModelOptions) error {
	if e == nil {
		return errors.New("engine is nil")
	}
	e.Lock()
	defer e.Unlock()
	return e.addModel(path, opts)
}

// Predict processes streaming audio and returns the latest score for each
// loaded wake-word model.
func (e *Engine) Predict(samples Samples) (map[string]float32, error) {
	if e == nil {
		return nil, errors.New("engine is nil")
	}
	e.Lock()
	defer e.Unlock()
	return e.predictLocked(samples)
}

// Detect processes streaming audio and reports whether each loaded model's
// current score is at or above its configured threshold.
func (e *Engine) Detect(samples Samples) (map[string]bool, error) {
	if e == nil {
		return nil, errors.New("engine is nil")
	}
	e.Lock()
	defer e.Unlock()
	scores, err := e.predictLocked(samples)
	if err != nil {
		return nil, err
	}
	detections := make(map[string]bool, len(scores))
	for name, score := range scores {
		model := e.models[name]
		detections[name] = model != nil && score >= model.threshold
	}
	return detections, nil
}

// Reset clears feature, model, and VAD history while keeping loaded models.
func (e *Engine) Reset() {
	if e == nil {
		return
	}
	e.Lock()
	defer e.Unlock()
	for _, model := range e.models {
		model.reset()
	}
	if e.audioFeatures != nil {
		e.audioFeatures.Reset()
	}
	if e.vad != nil {
		e.vad.Reset()
	}
}

// Close releases all ONNX sessions owned by the engine.
func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	e.Lock()
	defer e.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	var errs []error
	for _, m := range e.models {
		errs = append(errs, m.close())
	}
	if e.vad != nil {
		errs = append(errs, e.vad.Close())
	}
	if e.audioFeatures != nil {
		errs = append(errs, e.audioFeatures.Close())
	}
	return errors.Join(errs...)
}

func (e *Engine) predictLocked(samples Samples) (map[string]float32, error) {
	if e.closed {
		return nil, errors.New("engine is closed")
	}
	if len(e.models) == 0 {
		return nil, errors.New("no wake-word models loaded")
	}

	prepared, err := e.audioFeatures.Process(samples)
	if err != nil {
		return nil, err
	}
	predictions := make(map[string]float32)
	for name, model := range e.models {
		predictions[name] = model.latest()
		if prepared == 0 {
			continue
		}
		var maxScore float32
		for offset := prepared/FrameSamples - 1; offset >= 0; offset-- {
			features, shape, err := e.audioFeatures.Features(model.inputFrames, offset)
			if err != nil {
				return nil, err
			}
			values, _, err := model.session.runFloat(shape, features)
			if err != nil {
				return nil, fmt.Errorf("predict with %q: %w", name, err)
			}
			if len(values) < 1 {
				return nil, fmt.Errorf("model %q returned no scores", name)
			}
			if values[0] > maxScore {
				maxScore = values[0]
			}
		}
		if len(model.history) < 5 {
			maxScore = 0
		}
		predictions[name] = maxScore
	}

	for name, model := range e.models {
		score := predictions[name]
		if model.patience > 0 && score != 0 {
			if model.countAtLeast(model.patience) < model.patience {
				predictions[name] = 0
			}
		} else if model.debounceTime > 0 && score >= model.threshold && prepared > 0 {
			frameDuration := time.Duration(float64(time.Second) * float64(prepared) / SampleRate)
			n := int(math.Ceil(float64(model.debounceTime) / float64(frameDuration)))
			if model.countAtLeast(n) > 0 {
				predictions[name] = 0
			}
		}
	}
	for name, model := range e.models {
		model.appendHistory(predictions[name])
	}
	if e.vad != nil {
		detected, err := e.vad.DetectContext(samples)

		if err != nil {
			return nil, err
		}
		if !detected {
			for label := range predictions {
				predictions[label] = 0
			}
		}
	}
	return predictions, nil
}

func (e *Engine) addModel(path string, opts ModelOptions) error {
	if e.closed {
		return errors.New("engine is closed")
	}
	m, err := newModel(path, opts)
	if err != nil {
		return err
	}
	if _, exists := e.models[m.name]; exists {
		_ = m.close()
		return fmt.Errorf("model %q is already loaded", m.name)
	}
	e.models[m.name] = m
	return nil
}
