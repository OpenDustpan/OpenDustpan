package outputsharesvc_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// recordingSource returns the outputs that the test set and counts the reads.
type recordingSource struct {
	outputs []outputsharesvc.Output
	err     error
	reads   int
	gotID   string
}

func (s *recordingSource) Outputs(
	_ context.Context,
	_ identity.Object,
	stateID string,
) ([]outputsharesvc.Output, error) {
	s.reads++
	s.gotID = stateID

	return s.outputs, s.err
}

func newReaderRig(t *testing.T) (*outputsharesvc.Reader, *checkerRig, *recordingSource) {
	t.Helper()

	rig := newCheckerRig(t)
	source := &recordingSource{outputs: []outputsharesvc.Output{
		{Name: "a", Secret: false, Value: json.RawMessage(`1`)},
		{Name: "b", Secret: true, Value: json.RawMessage(`"leak"`)},
	}, err: nil, reads: 0, gotID: ""}

	reader, err := outputsharesvc.NewReader(outputsharesvc.ReaderConfig{
		States: rig.states, Checker: rig.checker, Sources: map[string]outputsharesvc.Source{"opentofu": source},
	})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	return reader, rig, source
}

// TestReaderReadsTheStateThatTheCheckerDecidedOn proves the happy path: the
// checker gets the ID of the latest state, the source reads that ID, and the
// value of a secret output never leaves the reader.
func TestReaderReadsTheStateThatTheCheckerDecidedOn(t *testing.T) {
	t.Parallel()

	reader, _, source := newReaderRig(t)

	res, err := reader.Read(t.Context(), personRequest(latestID, false).Caller, producerAcme().Unit)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if res.StateID != latestID || source.gotID != latestID || len(res.Outputs) != 2 {
		t.Fatalf("result = %+v, source saw %q", res, source.gotID)
	}

	if string(res.Outputs[0].Value) != `1` || res.Outputs[1].Value != nil || !res.Outputs[1].Secret {
		t.Errorf("outputs = %+v, want the plain value and a secret with no value", res.Outputs)
	}
}

// refusalCase is one row of TestReaderRefusals.
type refusalCase struct {
	name  string
	setup func(*checkerRig)
	unit  identity.Object
	want  error
}

// refusalRow returns a case for the OpenTofu unit of the rig and lets edit
// change it.
func refusalRow(name string, want error, edit func(*refusalCase)) refusalCase {
	c := refusalCase{name: name, setup: func(*checkerRig) {}, unit: producerAcme().Unit, want: want}
	edit(&c)

	return c
}

func refusalCases() []refusalCase {
	stack, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		panic(err)
	}

	return []refusalCase{
		refusalRow("no grant", identity.ErrDenied, func(c *refusalCase) {
			c.setup = func(r *checkerRig) {
				r.authz.errs = map[identity.Action]error{identity.ActionUnitReadOutputs: identity.ErrDenied}
			}
		}),
		refusalRow("an unknown producer", outputshare.ErrUnknownProducer, func(c *refusalCase) {
			c.setup = func(r *checkerRig) { r.states.err = outputshare.ErrUnknownProducer }
		}),
		refusalRow("a failing states port", identity.ErrDenied, func(c *refusalCase) {
			c.setup = func(r *checkerRig) { r.states.err = errPort }
		}),
		refusalRow("a client-encrypted state, outputs only", outputshare.ErrClientEncrypted, func(c *refusalCase) {
			c.setup = func(r *checkerRig) {
				r.states.state.ClientEncrypted = true
				r.authz.errs = map[identity.Action]error{identity.ActionUnitRead: identity.ErrDenied}
			}
		}),
		refusalRow("a unit with no state", outputsharesvc.ErrNoState, func(c *refusalCase) {
			c.setup = func(r *checkerRig) { r.states.state = outputshare.State{ID: "", ClientEncrypted: false} }
		}),
		refusalRow("a tool with no source", outputshare.ErrInvalid, func(c *refusalCase) {
			c.unit = stack.WithUnitID("stk-1")
		}),
	}
}

// TestReaderRefusals proves that no refusal reaches the source and that each
// one keeps its sentinel.
func TestReaderRefusals(t *testing.T) {
	t.Parallel()

	for _, tt := range refusalCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader, rig, source := newReaderRig(t)
			tt.setup(rig)

			_, err := reader.Read(t.Context(), personRequest(latestID, false).Caller, tt.unit)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Read = %v, want %v", err, tt.want)
			}

			if source.reads != 0 {
				t.Errorf("the source was read %d times after a refusal", source.reads)
			}
		})
	}
}

// TestReaderPassesAMovedState proves that a state that moves between the check
// and the read is reported and answers no output.
func TestReaderPassesAMovedState(t *testing.T) {
	t.Parallel()

	reader, _, source := newReaderRig(t)
	source.err = outputsharesvc.ErrStateMoved

	res, err := reader.Read(t.Context(), personRequest(latestID, false).Caller, producerAcme().Unit)
	if !errors.Is(err, outputsharesvc.ErrStateMoved) || len(res.Outputs) != 0 {
		t.Fatalf("Read = %+v, %v; want ErrStateMoved and no output", res, err)
	}
}

// TestReaderStripsASecretValueFromASource proves the defense in depth: a source
// that answers a value for a secret output gets it removed.
func TestReaderStripsASecretValueFromASource(t *testing.T) {
	t.Parallel()

	reader, _, _ := newReaderRig(t)

	res, err := reader.Read(t.Context(), personRequest(latestID, false).Caller, producerAcme().Unit)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	for _, output := range res.Outputs {
		if output.Secret && output.Value != nil {
			t.Errorf("secret output %q kept its value", output.Name)
		}
	}
}

// TestNewReaderNeedsItsPorts proves that a missing port fails the build.
func TestNewReaderNeedsItsPorts(t *testing.T) {
	t.Parallel()

	rig := newCheckerRig(t)
	source := &recordingSource{outputs: nil, err: nil, reads: 0, gotID: ""}

	tests := map[string]outputsharesvc.ReaderConfig{
		"states":  {States: nil, Checker: rig.checker, Sources: map[string]outputsharesvc.Source{"x": source}},
		"checker": {States: rig.states, Checker: nil, Sources: map[string]outputsharesvc.Source{"x": source}},
		"sources": {States: rig.states, Checker: rig.checker, Sources: nil},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := outputsharesvc.NewReader(cfg); !errors.Is(err, outputsharesvc.ErrMissingDependency) {
				t.Fatalf("NewReader without %s = %v, want ErrMissingDependency", name, err)
			}
		})
	}
}

// movingStates answers the state of the inner port, and then moves the inner
// port to a new state, as a save of a state between two reads does.
type movingStates struct {
	inner *scriptedStates
}

func (m movingStates) Latest(ctx context.Context, u identity.Object) (outputshare.State, error) {
	state, err := m.inner.Latest(ctx, u)

	m.inner.mu.Lock()
	m.inner.state.ID = "state-after-the-race"
	m.inner.mu.Unlock()

	return state, err
}

// TestReaderAnswersARaceOfTheLatestCheckAsAMovedState proves that a state that
// comes in between the read of the reader and the read of the checker answers
// ErrStateMoved and not a denial, and that the source reads nothing.
func TestReaderAnswersARaceOfTheLatestCheckAsAMovedState(t *testing.T) {
	t.Parallel()

	_, rig, source := newReaderRig(t)

	reader, err := outputsharesvc.NewReader(outputsharesvc.ReaderConfig{
		States: movingStates{inner: rig.states}, Checker: rig.checker,
		Sources: map[string]outputsharesvc.Source{"opentofu": source},
	})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	res, err := reader.Read(t.Context(), personRequest(latestID, false).Caller, producerAcme().Unit)
	if !errors.Is(err, outputsharesvc.ErrStateMoved) || errors.Is(err, identity.ErrDenied) ||
		len(res.Outputs) != 0 || source.reads != 0 {
		t.Fatalf("Read = %+v, %v, %d source reads; want ErrStateMoved, no denial, no read", res, err, source.reads)
	}
}
