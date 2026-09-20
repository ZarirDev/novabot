package wakeword

/*
#cgo LDFLAGS: -lvosk -ldl -lpthread

#include <stdlib.h>

typedef struct VoskModel VoskModel;
typedef struct VoskRecognizer VoskRecognizer;

extern VoskModel*      vosk_model_new(const char *model_path);
extern void            vosk_model_free(VoskModel *model);
extern VoskRecognizer* vosk_recognizer_new_grm(VoskModel *model, float sample_rate, const char *grammar);
extern int             vosk_recognizer_accept_waveform(VoskRecognizer *r, const char *data, int length);
extern const char*     vosk_recognizer_result(VoskRecognizer *r);
extern const char*     vosk_recognizer_partial_result(VoskRecognizer *r);
extern void            vosk_recognizer_reset(VoskRecognizer *r);
extern void            vosk_recognizer_free(VoskRecognizer *r);
extern void            vosk_recognizer_set_words(VoskRecognizer *r, int words);
extern void            vosk_recognizer_set_partial_words(VoskRecognizer *r, int partial_words);
*/
import "C"

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ZarirDev/novabot/internal/audio"
)

const (
	defaultVADThreshold    = 500.0
	defaultConfidence      = 0.75
	defaultCooldownSeconds = 1.5
	silenceFramesToReset   = 32 // ~1s at 32ms/frame
)

type voskWord struct {
	Word string  `json:"word"`
	Conf float64 `json:"conf"`
}

type voskResult struct {
	Text          string     `json:"text"`
	Partial       string     `json:"partial"`
	Result        []voskWord `json:"result"`
	PartialResult []voskWord `json:"partial_result"`
}

type VoskDetector struct {
	wakeWord string
	model    *C.VoskModel
	rec      *C.VoskRecognizer

	confidence float64
	cooldown   time.Duration
	vad        float64
	debug      bool

	mu         sync.Mutex
	cancelFunc context.CancelFunc
	active     bool
}

func NewVoskDetector(wakeWord, modelPath string) (*VoskDetector, error) {
	cPath := C.CString(modelPath)
	defer C.free(unsafe.Pointer(cPath))

	model := C.vosk_model_new(cPath)
	if model == nil {
		return nil, fmt.Errorf("vosk: failed to load model at %q", modelPath)
	}

	// Tight grammar — "hey nova" removed to stop "hey"/"hi" false triggers.
	grammar := fmt.Sprintf(`["%s", "[unk]"]`, wakeWord)
	cGrammar := C.CString(grammar)
	defer C.free(unsafe.Pointer(cGrammar))

	rec := C.vosk_recognizer_new_grm(model, C.float(audio.SampleRate), cGrammar)
	if rec == nil {
		C.vosk_model_free(model)
		return nil, fmt.Errorf("vosk: failed to create recognizer with grammar")
	}

	// Word-level confidence in both final and partial results.
	C.vosk_recognizer_set_words(rec, 1)
	C.vosk_recognizer_set_partial_words(rec, 1)

	conf := envFloat("WAKE_CONFIDENCE", defaultConfidence)
	cool := time.Duration(envFloat("WAKE_COOLDOWN_SECONDS", defaultCooldownSeconds) * float64(time.Second))
	vad := envFloat("WAKE_VAD_THRESHOLD", defaultVADThreshold)
	debug := os.Getenv("WAKE_DEBUG") == "1"

	log.Printf("[WAKEWORD] Vosk loaded (%s) | grammar=%s | conf≥%.2f | cooldown=%.1fs | vad=%.0f",
		modelPath, grammar, conf, cool.Seconds(), vad)

	return &VoskDetector{
		wakeWord:   wakeWord,
		model:      model,
		rec:        rec,
		confidence: conf,
		cooldown:   cool,
		vad:        vad,
		debug:      debug,
	}, nil
}

func (d *VoskDetector) Start(ctx context.Context, onDetected func()) error {
	d.mu.Lock()
	subCtx, cancel := context.WithCancel(ctx)
	d.cancelFunc = cancel
	d.active = true
	d.mu.Unlock()

	log.Printf("[WAKEWORD] Vosk detector active — say '%s'", d.wakeWord)
	go d.loop(subCtx, onDetected)
	return nil
}

func (d *VoskDetector) loop(ctx context.Context, onDetected func()) {
	stream, err := audio.StartMicCapture(ctx)
	if err != nil {
		log.Printf("[WAKEWORD] mic open failed: %v", err)
		return
	}
	defer stream.Close()

	samples := make([]int16, audio.FrameSize)
	pcm := make([]byte, audio.FrameSize*2)

	var cooldownUntil time.Time
	var silentFrames int

	meterOn := os.Getenv("NOVABOT_AUDIO_METER") == "1"

	for {
		select {
		case <-ctx.Done():
			if meterOn {
				fmt.Fprintln(os.Stdout)
			}
			log.Println("[WAKEWORD] Vosk loop stopped, mic closed.")
			return
		default:
			if err := stream.ReadFrame(samples); err != nil {
				log.Printf("[WAKEWORD] ReadFrame error: %v", err)
				time.Sleep(100 * time.Millisecond)
				continue
			}

			rms := audio.CalculateRMS(samples)

			if meterOn {
				renderMeter(dbfs(rms), rms >= d.vad)
			}

			if time.Now().Before(cooldownUntil) {
				continue
			}

			if rms < d.vad {
				silentFrames++
				if silentFrames == silenceFramesToReset {
					C.vosk_recognizer_reset(d.rec)
				}
				continue
			}
			silentFrames = 0

			for i, s := range samples {
				binary.LittleEndian.PutUint16(pcm[i*2:], uint16(s))
			}

			accepted := C.vosk_recognizer_accept_waveform(
				d.rec,
				(*C.char)(unsafe.Pointer(&pcm[0])),
				C.int(len(pcm)),
			)

			var jsonOut string
			if accepted == 1 {
				jsonOut = C.GoString(C.vosk_recognizer_result(d.rec))
			} else {
				jsonOut = C.GoString(C.vosk_recognizer_partial_result(d.rec))
			}

			var res voskResult
			if err := json.Unmarshal([]byte(jsonOut), &res); err != nil {
				if d.debug {
					log.Printf("[WAKEWORD-DBG] json parse error: %v | raw=%s", err, jsonOut)
				}
				continue
			}

			var text string
			var words []voskWord
			if accepted == 1 {
				text = res.Text
				words = res.Result
			} else {
				text = res.Partial
				words = res.PartialResult
			}

			if d.debug {
				log.Printf("[WAKEWORD-DBG] accepted=%d text=%q words=%+v", accepted, text, words)
			}

			if !d.matches(text, words) {
				continue
			}

			if meterOn {
				fmt.Fprintln(os.Stdout)
			}

			conf := d.bestConfidence(words)
			log.Printf("[WAKEWORD] match: %q (conf=%.3f, final=%v)", text, conf, accepted == 1)

			cooldownUntil = time.Now().Add(d.cooldown)
			C.vosk_recognizer_reset(d.rec)
			go onDetected()
		}
	}
}

// matches returns true only if the transcript contains the wake word as a
// standalone token AND the corresponding word's confidence clears the bar.
func (d *VoskDetector) matches(text string, words []voskWord) bool {
	if text == "" {
		return false
	}

	tokens := strings.Fields(strings.ToLower(text))
	found := false
	for _, t := range tokens {
		if t == d.wakeWord {
			found = true
			break
		}
	}
	if !found {
		return false
	}

	// If confidence data is present, require the wake word's conf to clear
	// the threshold. Vosk sometimes drops conf fields — in that case we
	// accept the text match alone.
	for _, w := range words {
		if strings.EqualFold(w.Word, d.wakeWord) {
			return w.Conf >= d.confidence
		}
	}
	return true
}

func (d *VoskDetector) bestConfidence(words []voskWord) float64 {
	for _, w := range words {
		if strings.EqualFold(w.Word, d.wakeWord) {
			return w.Conf
		}
	}
	return 0
}

func (d *VoskDetector) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancelFunc != nil {
		d.cancelFunc()
		d.cancelFunc = nil
	}
	d.active = false
}

func (d *VoskDetector) Free() {
	d.Stop()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rec != nil {
		C.vosk_recognizer_free(d.rec)
		d.rec = nil
	}
	if d.model != nil {
		C.vosk_model_free(d.model)
		d.model = nil
	}
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
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
	fmt.Fprintf(os.Stdout, "\r[MIC] %6.1f dBFS |%s| %s            ",
		db, bar, marker)
}
