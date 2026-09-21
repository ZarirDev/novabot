# Changelog

## v0.1.1 - 2026-06-29

### Added

- Public `WithModelPredictionHistory` configuration for model score history.
- Public `VAD.ContextScore` access and `WithVADContextWindow` configuration for inspecting delayed VAD speech context.

## v0.1.0 - 2026-06-28

Initial preview release of `openwakeword_go`.

### Added

- Streaming openWakeWord inference pipeline in native Go.
- ONNX Runtime integration through `github.com/yalue/onnxruntime_go`.
- Mel-spectrogram and embedding feature extraction.
- Binary wake-word model loading with per-model threshold, patience, and debounce options.
- Optional Silero VAD support for wake-word suppression.
- `Samples` float32 PCM input type for streaming audio.
- WAV decoding plus clip prediction and detection helpers for mono 16-bit PCM at 16 kHz.
- Opt-in integration test for official ONNX models and local runtime assets.
- Microphone example in `examples/microphone` as a separate Go module, so the main library does not depend on PortAudio.
- Helper scripts for downloading model files and ONNX Runtime.

### Changed

- Simplified the public API around explicit `AudioFeatures`, optional `VAD`, and `Engine.AddModel`.
- Kept advanced extension points such as custom audio filters, verifiers, and multiclass class mappings out of the initial public API.

### Notes

- This is a pre-1.0 release. Public APIs may change between minor versions.
- Model weights and ONNX Runtime binaries are not bundled with the Go module.
- Official openWakeWord model weights are licensed separately from this repository.
