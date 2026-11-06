package unitsettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
)

// storedStep is the stored form of a job.Step. It has its own JSON names, so
// that a change to job.Step never changes the stored record by accident.
type storedStep struct {
	Name         string             `json:"name"`
	Role         job.StepRole       `json:"role"`
	Phase        job.PhaseKind      `json:"phase"`
	Args         []string           `json:"args"`
	Env          map[string]string  `json:"env,omitempty"`
	Dir          string             `json:"dir,omitempty"`
	TimeoutMs    int64              `json:"timeoutMs,omitempty"`
	AllowFailure bool               `json:"allowFailure,omitempty"`
	Outputs      []storedStepOutput `json:"outputs,omitempty"`
}

type storedStepOutput struct {
	Kind job.OutputKind `json:"kind"`
	Path string         `json:"path"`
}

// storedBody is the JSON document of a record. The unit, the version and the
// time are columns of the record, not part of the document.
type storedBody struct {
	Settings

	Steps []storedStep `json:"steps,omitempty"`
}

// Marshal returns the stored document of s: every field except the unit, the
// version and the time. It validates the fields first and returns an error
// that wraps ErrInvalid for settings that fail.
func Marshal(s *Settings) ([]byte, error) {
	if err := s.validateFields(); err != nil {
		return nil, err
	}

	b := storedBody{Settings: *s, Steps: encodeSteps(s.Steps)}

	doc, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("encode unit settings: %w", err)
	}

	return doc, nil
}

// Parse reads the stored document doc into settings and validates every
// field. It fails closed: a document with an unknown field, a second value or
// a field that fails Validate gives an error that wraps ErrInvalid, so a row
// from another version or a restore never gives a value that failed a check.
// The caller sets the unit, the version and the time.
func Parse(doc []byte) (Settings, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()

	var b storedBody
	if err := dec.Decode(&b); err != nil {
		return zero(), fmt.Errorf("%w: stored document: %w", ErrInvalid, err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return zero(), fmt.Errorf("%w: stored document has more than one value", ErrInvalid)
	}

	s := b.Settings
	s.Steps = nil

	for i := range b.Steps {
		st := &b.Steps[i]
		step := job.Step{
			Name: st.Name, Role: st.Role, Phase: st.Phase, Args: st.Args, Env: st.Env, Dir: st.Dir,
			Timeout: time.Duration(st.TimeoutMs) * time.Millisecond, AllowFailure: st.AllowFailure,
			ChangesResources: false, Outputs: nil,
		}

		for _, o := range st.Outputs {
			step.Outputs = append(step.Outputs, job.Output{Kind: o.Kind, Path: o.Path})
		}

		s.Steps = append(s.Steps, step)
	}

	if err := s.validateFields(); err != nil {
		return zero(), err
	}

	return s, nil
}

func zero() Settings {
	var s Settings

	return s
}

// encodeSteps returns the stored form of steps.
func encodeSteps(steps []job.Step) []storedStep {
	out := make([]storedStep, 0, len(steps))

	for i := range steps {
		st := &steps[i]
		enc := storedStep{
			Name: st.Name, Role: st.Role, Phase: st.Phase, Args: st.Args, Env: st.Env, Dir: st.Dir,
			TimeoutMs: st.Timeout.Milliseconds(), AllowFailure: st.AllowFailure,
			Outputs: make([]storedStepOutput, 0, len(st.Outputs)),
		}

		for _, o := range st.Outputs {
			enc.Outputs = append(enc.Outputs, storedStepOutput{Kind: o.Kind, Path: o.Path})
		}

		out = append(out, enc)
	}

	return out
}
