package openwakeword

import (
	"errors"
	"fmt"
	"strings"

	ort "github.com/yalue/onnxruntime_go"
)

type (
	onnxValues struct {
		values []ort.Value
	}

	onnxSession struct {
		session *ort.DynamicAdvancedSession
		inputs  []ort.InputOutputInfo
		outputs []ort.InputOutputInfo
	}
)

func newONNXValues(n int) *onnxValues {
	return &onnxValues{values: make([]ort.Value, n)}
}

func newONNXSession(path string) (*onnxSession, error) {
	inputs, outputs, err := ort.GetInputOutputInfo(path)
	if err != nil {
		return nil, fmt.Errorf("inspect ONNX model %q: %w", path, err)
	}

	inputNames := make([]string, len(inputs))
	outputNames := make([]string, len(outputs))
	for i := range inputs {
		inputNames[i] = inputs[i].Name
	}
	for i := range outputs {
		outputNames[i] = outputs[i].Name
	}

	session, err := ort.NewDynamicAdvancedSession(path, inputNames, outputNames, nil)
	if err != nil {
		return nil, fmt.Errorf("load ONNX model %q: %w", path, err)
	}

	return &onnxSession{session: session, inputs: inputs, outputs: outputs}, nil
}

func (v *onnxValues) set(i int, value ort.Value) {
	v.values[i] = value
}

func (v *onnxValues) close() {
	for _, value := range v.values {
		if value != nil {
			_ = value.Destroy()
		}
	}
}

func (s *onnxSession) run(inputs *onnxValues, outputs *onnxValues) error {
	return s.session.Run(inputs.values, outputs.values)
}

func (s *onnxSession) runFloat(shape []int64, data []float32) (_ []float32, _ []int64, err error) {
	if len(s.inputs) != 1 || len(s.outputs) == 0 {
		return nil, nil, fmt.Errorf("expected one input and at least one output, got %d and %d", len(s.inputs), len(s.outputs))
	}
	input, err := ort.NewTensor(ort.NewShape(shape...), data)
	if err != nil {
		return nil, nil, err
	}
	defer func(input *ort.Tensor[float32]) {
		if deferErr := input.Destroy(); deferErr != nil {
			err = errors.Join(err, deferErr)
		}
	}(input)
	outputs := newONNXValues(len(s.outputs))
	if err := s.session.Run([]ort.Value{input}, outputs.values); err != nil {
		outputs.close()
		return nil, nil, err
	}
	defer outputs.close()

	output, ok := outputs.values[0].(*ort.Tensor[float32])
	if !ok {
		return nil, nil, fmt.Errorf("expected float32 output, got %T", outputs.values[0])
	}
	return append([]float32(nil), output.GetData()...), append([]int64(nil), output.GetShape()...), nil
}

// runVAD executes Silero VAD and returns the current speech score plus copied
// recurrent state tensors.
func (s *onnxSession) runVAD(audio, h, c []float32, sampleRate int) (float32, []float32, []float32, error) {
	inputs := newONNXValues(len(s.inputs))
	defer inputs.close()

	for i, input := range s.inputs {
		var (
			value ort.Value
			err   error
		)
		switch strings.ToLower(input.Name) {
		case "input":
			value, err = ort.NewTensor(ort.NewShape(1, int64(len(audio))), audio)
		case "h":
			value, err = ort.NewTensor(ort.NewShape(2, 1, 64), h)
		case "c":
			value, err = ort.NewTensor(ort.NewShape(2, 1, 64), c)
		case "sr":
			value, err = ort.NewTensor(ort.NewShape(1), []int64{int64(sampleRate)})
		default:
			return 0, nil, nil, fmt.Errorf("unsupported vad input %q", input.Name)
		}
		if err != nil {
			return 0, nil, nil, fmt.Errorf("create vad input %q: %w", input.Name, err)
		}
		inputs.set(i, value)
	}

	outputs := newONNXValues(len(s.outputs))
	defer outputs.close()

	if err := s.run(inputs, outputs); err != nil {
		return 0, nil, nil, fmt.Errorf("run vad model: %w", err)
	}

	var (
		score *float32
		nextH []float32
		nextC []float32
	)
	for i, output := range outputs.values {
		lower := strings.ToLower(s.outputs[i].Name)
		tensor, ok := output.(*ort.Tensor[float32])
		if !ok {
			return 0, nil, nil, fmt.Errorf("unsupported vad output %q type %T", s.outputs[i].Name, output)
		}
		data := tensor.GetData()
		switch {
		case lower == "hn" || lower == "h":
			nextH = append([]float32(nil), data...)
		case lower == "cn" || lower == "c":
			nextC = append([]float32(nil), data...)
		case len(data) > 0:
			value := data[0]
			score = &value
		}
	}
	if score == nil {
		return 0, nil, nil, errors.New("vad model did not return a score")
	}
	if nextH == nil || nextC == nil {
		return 0, nil, nil, errors.New("vad model did not return recurrent state")
	}
	return *score, nextH, nextC, nil
}

func (s *onnxSession) close() error {
	if s == nil || s.session == nil {
		return nil
	}
	err := s.session.Destroy()
	s.session = nil
	return err
}
