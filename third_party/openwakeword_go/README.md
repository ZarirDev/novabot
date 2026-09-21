# openwakeword_go

![Go Version](https://img.shields.io/badge/go-1.22+-00ADD8)
![License](https://img.shields.io/badge/license-Apache--2.0-blue)
![Status](https://img.shields.io/badge/status-v0.1.0_preview-orange)

Native Go inference pipeline for openWakeWord: streaming audio features,
wake-word scoring, optional Silero VAD, and WAV helpers without Python.

> This project is pre-1.0; public APIs may change between minor versions.

## ✨ Features

- Streaming 16 kHz, mono, `float32` PCM input
- Arbitrary input chunk sizes with 1280-sample internal framing
- Multiple binary wake-word ONNX models
- Python-compatible startup suppression, patience, debounce, and VAD behavior
- WAV processing helpers
- Direct integration with `github.com/yalue/onnxruntime_go`

## 📦 Requirements

The ONNX backend uses
[`github.com/yalue/onnxruntime_go`](https://github.com/yalue/onnxruntime_go).
Callers own the global ONNX Runtime environment: initialize it before creating
an `Engine`, and destroy it after all engines and VAD instances are closed.

Your application must provide a compatible native ONNX Runtime shared library:

- macOS: `libonnxruntime.dylib`
- Linux: `libonnxruntime.so`
- Windows: `onnxruntime.dll`

Official openWakeWord model weights are licensed separately under
CC BY-NC-SA 4.0. The Go source in this repository is Apache-2.0.

## 🧰 Setup Scripts

The repository includes small helper scripts for local development and the
microphone demo:

```bash
./scripts/download-models.sh
./scripts/download-runtime.sh
```

`download-models.sh` downloads the default openWakeWord ONNX files into
`./models`. `download-runtime.sh` detects the current platform, downloads ONNX
Runtime, and copies the shared library into `./runtime`.

Both scripts accept an optional output directory:

```bash
./scripts/download-models.sh /path/to/models
./scripts/download-runtime.sh /path/to/runtime
```

## 🚀 Quick Start

```go
package main

import (
	"log"

	openwakeword "github.com/stanislaw-glogowski/openwakeword_go"
	ort "github.com/yalue/onnxruntime_go"
)

func main() {
	ort.SetSharedLibraryPath("runtime/libonnxruntime.dylib")
	if err := ort.InitializeEnvironment(); err != nil {
		log.Fatal(err)
	}
	defer ort.DestroyEnvironment()

	features, err := openwakeword.NewAudioFeatures(
		"models/melspectrogram.onnx",
		"models/embedding_model.onnx",
	)
	if err != nil {
		log.Fatal(err)
	}

	vad, err := openwakeword.NewVAD(
		"models/silero_vad.onnx",
		openwakeword.WithVADThreshold(0.5),
	)
	if err != nil {
		log.Fatal(err)
	}

	engine, err := openwakeword.New(features, vad)
	if err != nil {
		log.Fatal(err)
	}
	defer engine.Close()

	err = engine.AddModel(
		"models/hey_jarvis_v0.1.onnx",
		openwakeword.WithModelName("hey_jarvis_v0.1"),
		openwakeword.WithModelThreshold(0.5),
		openwakeword.WithModelPredictionHistory(30),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Feed successive microphone frames. 1280 samples is 80 ms at 16 kHz.
	scores, err := engine.Predict(make(openwakeword.Samples, openwakeword.FrameSamples))
	if err != nil {
		log.Fatal(err)
	}
	if scores["hey_jarvis_v0.1"] >= 0.5 {
		log.Println("wake word detected")
	}
}
```

For boolean detections using the model threshold:

```go
detections, err := engine.Detect(make(openwakeword.Samples, openwakeword.FrameSamples))
if err != nil {
	log.Fatal(err)
}
if detections["hey_jarvis_v0.1"] {
	log.Println("wake word detected")
}
```

For a WAV file:

```go
predictions, err := engine.PredictWAV(
	"recording.wav", openwakeword.FrameSamples, time.Second,
)

detections, err := engine.DetectWAV(
	"recording.wav", openwakeword.FrameSamples, time.Second,
)
```

The WAV must be mono 16-bit PCM at 16 kHz.

## 🎯 Models

`AddModel` loads a binary wake-word ONNX model. `WithModelName` controls the key
returned by `Predict`; if it is omitted, the filename without `.onnx` is used.
Model options store per-model detection settings such as threshold, prediction
history, patience, and debounce time.

## 🗣️ Using VAD Directly

`VAD` can be used independently from the wake-word engine. Use this when you
need speech/silence detection for a conversation stream with separate state and
thresholds.

```go
vad, err := openwakeword.NewVAD(
	"models/silero_vad.onnx",
	openwakeword.WithVADThreshold(0.5),
	openwakeword.WithVADFrameSize(640),
	openwakeword.WithVADContextWindow(7, 4),
)
if err != nil {
	log.Fatal(err)
}
defer vad.Close()

speech, err := vad.Detect(samples)
if err != nil {
	log.Fatal(err)
}
if !speech {
	log.Println("silence detected")
}
```

Use `ContextScore` to inspect the delayed speech context used by
`DetectContext` and the engine's VAD suppression path. `WithVADContextWindow`
sets that history window as offsets counted back from the newest VAD score.

Use a separate `VAD` instance for unrelated streams. The model keeps recurrent
state and score history, so sharing one instance between wake-word suppression
and conversation silence detection will couple those workflows.

## 🧪 Tests

```bash
go test ./...
go vet ./...
```

The end-to-end test is opt-in because it needs native ONNX Runtime and model
files. The test initializes `github.com/yalue/onnxruntime_go` directly:

```bash
ONNXRUNTIME_SHARED_LIBRARY_PATH=/path/to/libonnxruntime.so \
OPENWAKEWORD_MODEL_DIR=/path/to/models \
OPENWAKEWORD_TEST_WAV=/path/to/alexa_test.wav \
go test -run TestOfficialONNXModels -v
```

`OPENWAKEWORD_MODEL_DIR` should contain:

- `melspectrogram.onnx`
- `embedding_model.onnx`
- `silero_vad.onnx`
- one or more wake-word models, for example `alexa_v0.1.onnx`

## 🎙️ Microphone Example

The example in `examples/microphone` listens to the default PortAudio input. It
is a separate Go module so the main library does not depend on PortAudio. The
example reads configuration from a working directory, using `--cwd` or the
current directory by default.

Use the setup scripts above to download the default model files and ONNX
Runtime library.

Expected layout:

```text
.
├── models
│   └── hey_jarvis_v0.1.onnx
│   ├── alexa_v0.1.onnx
│   ├── embedding_model.onnx
│   ├── melspectrogram.onnx
│   ├── silero_vad.onnx
└── runtime
    └── libonnxruntime.dylib
```

On Linux the runtime file is `runtime/libonnxruntime.so`; on Windows it is
`runtime/onnxruntime.dll`.

Every additional `models/*.onnx` file is treated as a wake-word model unless it
is one of the feature/VAD files above. The result name is the filename without
`.onnx`, for example `alexa_v0.1.onnx` becomes `alexa_v0.1`.

On macOS, install the native PortAudio library first:

```bash
brew install portaudio
```

Run the demo:

```bash
cd examples/microphone
go run .
```

Useful flags:

```bash
go run . \
  --cwd /path/to/openwakeword_assets \
  --threshold 0.9 \
  --vad-threshold 0.5 \
  --warmup 3s \
  --debounce 1500ms
```

Set `--vad-threshold 0` to disable VAD. The default microphone must support
mono capture at 16 kHz. macOS will ask for microphone permission the first time
the program starts recording.

## 🗺️ Roadmap

- GitHub Actions CI for `go test ./...` and `go vet ./...`.
- WAV-file example that accepts a local file path instead of bundling sample
  audio in the repository.
- Optional resampling example for common 44.1/48 kHz input sources. Keep this
  outside the core library unless there is a strong reason to add a dependency.
- More package examples for `Engine.Detect`, direct `VAD` usage, and clip
  prediction.
- Additional opt-in benchmarks around local ONNX model assets.

## 📄 License

The Go source code in this repository is licensed under Apache-2.0. Official
openWakeWord model weights are licensed separately under CC BY-NC-SA 4.0.
