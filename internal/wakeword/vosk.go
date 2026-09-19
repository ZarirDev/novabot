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
*/
import "C"

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ZarirDev/novabot/internal/audio"
)

// 500 RMS ≈ gentle speech floor; keeps inference off during silence/fans.
const voskVADThreshold = 500.0

// 32 frames * 32 ms ≈ 1 s — clears recognizer state during long silences.
const voskSilenceFramesToReset = 32

type VoskDetector struct {
	wakeWord string
	model    *C.VoskModel
	rec      *C.VoskRecognizer

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

	// Build the grammar BEFORE creating the recognizer.
	grammar := fmt.Sprintf(`["%s", "hey %s", "[unk]"]`, wakeWord, wakeWord)
	cGrammar := C.CString(grammar)
	defer C.free(unsafe.Pointer(cGrammar))

	rec := C.vosk_recognizer_new_grm(model, C.float(audio.SampleRate), cGrammar)
	if rec == nil {
		C.vosk_model_free(model)
		return nil, fmt.Errorf("vosk: failed to create recognizer with grammar")
	}

	log.Printf("[WAKEWORD] Vosk model loaded (%s), grammar locked to '%s'", modelPath, wakeWord)

	return &VoskDetector{
		wakeWord: wakeWord,
		model:    model,
		rec:      rec,
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
				renderMeter(dbfs(rms), rms >= voskVADThreshold)
			}

			if time.Now().Before(cooldownUntil) {
				continue
			}

			// Cheap VAD gate: never touch the model during silence.
			if rms < voskVADThreshold {
				silentFrames++
				if silentFrames == voskSilenceFramesToReset {
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

			if !strings.Contains(jsonOut, d.wakeWord) {
				continue
			}

			if meterOn {
				fmt.Fprintln(os.Stdout) // break the meter line before logging
			}
			log.Printf("[WAKEWORD] match: %s", jsonOut)
			cooldownUntil = time.Now().Add(3 * time.Second)
			C.vosk_recognizer_reset(d.rec)
			go onDetected()
		}
	}
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

// Free releases the native model. Optional — useful if you ever want to unload
// the model on mode switch to reclaim ~200 MB.
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

// dbfs converts int16 RMS to dBFS: 0 dB = full scale, -100 dB = silence floor.
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

// renderMeter overwrites a single terminal line. 40 chars ≈ -80..0 dBFS.
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
	// trailing spaces scrub any residue from prior log lines
	fmt.Fprintf(os.Stdout, "\r[MIC] %6.1f dBFS |%s| %s            ",
		db, bar, marker)
}
