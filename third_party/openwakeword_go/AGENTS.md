# AGENTS.md

Guidance for coding agents working in this repository.

## Project Shape

`openwakeword_go` is a pre-1.0 native Go implementation of the openWakeWord
inference pipeline. The main module is the library. The microphone demo lives in
`examples/microphone` as a separate Go module so the library does not depend on
PortAudio.

Main library files:

- `shared.go`: public audio constants and `Samples`.
- `audio_features.go`: mel-spectrogram and embedding feature extraction.
- `model.go`: wake-word model options and internal model state.
- `engine.go`: streaming engine and prediction orchestration.
- `vad.go`: stateful Silero VAD wrapper.
- `onnx.go`: private ONNX Runtime adapter.
- `wav.go`: WAV decoding and clip prediction helpers.

Local model and runtime assets live under `models/` and `runtime/`. They are
ignored by git and must not be committed.

## Commands

Run these from the repository root:

```bash
go test ./...
go vet ./...
```

The microphone example has its own module:

```bash
cd examples/microphone
go test ./...
go vet ./...
```

In sandboxed environments where the default Go cache is not writable, use a
local cache and remove it afterwards:

```bash
mkdir -p .gocache
GOCACHE="$PWD/.gocache" go test ./...
GOCACHE="$PWD/.gocache" go vet ./...
rm -rf .gocache
```

Run the opt-in integration test only when local runtime and model files are
available:

```bash
ONNXRUNTIME_SHARED_LIBRARY_PATH=runtime/libonnxruntime.dylib \
OPENWAKEWORD_MODEL_DIR=models \
go test -run TestOfficialONNXModels -v
```

Use the platform-specific ONNX Runtime filename when not on macOS.

## API And Design Conventions

- Public audio input is `Samples`, a mono `[]float32` PCM stream normalized to
  roughly `[-1, 1]`.
- `SampleRate` is fixed at 16 kHz and `FrameSamples` is fixed at 1280 samples.
- Keep the public API small for v0.x. Do not reintroduce audio filters,
  verifiers, or multiclass mappings unless there is a concrete use case.
- `AudioFeatures`, `VAD`, and `Engine` own ONNX sessions and must be closed.
- VAD is stateful. Use separate VAD instances for unrelated streams or
  workflows.
- Keep ONNX-specific tensor lifecycle code inside `onnx.go` where possible.
- The main module should not depend on PortAudio. Keep microphone-only code in
  `examples/microphone`.

## Go File Layout

Keep Go declarations ordered consistently:

1. `package`
2. `import`
3. `const`: exported constants first, then unexported constants.
4. `var`: `Err...` values first, then `Default...` values, then other package
   variables.
5. `type`: aliases, enum-like types, structs, then interfaces.
6. Constructors: `New...`, then `NewFrom...`.
7. Public methods and public functions.
8. Private methods and private functions.
9. Small helper functions.

Do not mix private methods between public methods. For private types, prefer
unexported method names unless there is a concrete reason to expose the method
inside package documentation.

## Error And Documentation Style

- Use lower-case error messages unless they begin with an identifier or acronym
  that is normally capitalized.
- Add context when wrapping errors, for example `load wake-word model %q: %w`.
- Keep comments short and technical. Add GoDoc for exported API; avoid comments
  that merely repeat the function name.

## Release Hygiene

- Do not commit generated caches, ONNX model files, native runtime binaries, or
  IDE files.
- Keep `README.md` and `CHANGELOG.md` aligned with public API changes.
- Before release, run root tests/vet, example tests/vet, and the opt-in
  integration test when local assets are available.
