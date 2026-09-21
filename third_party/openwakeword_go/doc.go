// Package openwakeword provides a native Go inference pipeline for
// openWakeWord-compatible ONNX models.
//
// The package consumes mono 16 kHz float32 PCM samples, computes openWakeWord
// audio features, runs one or more binary wake-word models, and can optionally
// suppress detections with a stateful Silero VAD model.
//
// Callers own the global github.com/yalue/onnxruntime_go environment. Set the
// ONNX Runtime shared library path and initialize the environment before
// constructing feature extractors, VAD instances, or engines.
package openwakeword
