package outputsharesvc

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// ErrNoState reports that the unit exists and has no state yet, for example a
// stack or a workspace that no update or upload has written to. The read
// answers this before the checker runs, so it writes no denial entry.
var ErrNoState = errors.New("unit has no state yet")

// Result is the outputs of the latest state of one unit.
type Result struct {
	// StateID names the state that the outputs come from, as
	// outputshare.State.ID does.
	StateID string
	// Outputs holds the root outputs, ordered by name.
	Outputs []outputshare.Output
}

// ReaderConfig holds the dependencies of a Reader.
type ReaderConfig struct {
	// States tells the ID of the latest state of a unit.
	States outputshare.States
	// Checker decides the read.
	Checker outputshare.Checker
	// Sources reads the outputs, by the tool of the unit.
	Sources map[string]outputshare.Outputs
}

// Reader reads the outputs of the latest state of a unit for a principal, for
// any tool. It asks the checker before it reads a value, and it reads the state
// that the checker decided on.
type Reader struct {
	states  outputshare.States
	checker outputshare.Checker
	sources map[string]outputshare.Outputs
}

// NewReader returns a Reader. It returns ErrMissingDependency when a port is
// missing.
func NewReader(cfg ReaderConfig) (*Reader, error) {
	if cfg.States == nil || cfg.Checker == nil || len(cfg.Sources) == 0 {
		return nil, fmt.Errorf("%w: states, checker and sources", ErrMissingDependency)
	}

	return &Reader{states: cfg.States, checker: cfg.Checker, sources: cfg.Sources}, nil
}

// Read returns the outputs of the latest state of the unit for the principal
// p. The unit carries its stable ID. The principal needs the actions that the
// checker needs: unit:read-outputs on the unit and, when the client encrypted
// the state, unit:read too. The answer holds no secret value, for any
// principal.
//
// It returns the *outputshare.DeniedError of the checker for a denied read,
// ErrNoState when the unit has no state yet, outputshare.ErrStateMoved when a
// new state came in between, outputshare.ErrUnknownProducer when the unit is
// gone, and an error that wraps outputshare.ErrInvalid for a unit that has no
// source for its tool. The errors of the source pass through, wrapped.
func (r *Reader) Read(ctx context.Context, p identity.Principal, unit identity.Object) (Result, error) {
	source, ok := r.sources[unit.Tool()]
	if !ok {
		return Result{}, fmt.Errorf("%w: no output source for %q", outputshare.ErrInvalid, unit)
	}

	// A states port that fails gives an empty ID. The checker reads the state
	// again and denies, with its own audit entry.
	latest := ""

	if state, err := r.states.Latest(ctx, unit); err == nil {
		// The unit exists and the port answered, so the caller learns nothing
		// new from the answer. Normal condition, not a denial.
		if state.ID == "" {
			return Result{}, fmt.Errorf("read the outputs of %s: %w", unit, ErrNoState)
		}

		latest = state.ID
	}

	req := outputshare.ReadRequest{
		Caller:     p,
		Producer:   outputshare.Placement{Unit: unit, Collection: identity.Object{}},
		Attachment: outputshare.AttachmentRef{Attachment: "", Profile: "", Block: "", Revision: 0},
		Latest:     latest,
		Secrets:    false,
	}

	if _, err := r.checker.CheckRead(ctx, req); err != nil {
		// A denial of the latest-state check, when this read saw a state,
		// means that a new state came in between the two reads of it. It
		// answers as a moved state and not as a denial.
		var denied *outputshare.DeniedError
		if latest != "" && errors.As(err, &denied) && denied.Check == outputshare.CheckLatest {
			return Result{}, fmt.Errorf("check the read of outputs: %w", outputshare.ErrStateMoved)
		}

		return Result{}, fmt.Errorf("check the read of outputs: %w", err)
	}

	outputs, err := source.Outputs(ctx, unit, latest)
	if err != nil {
		return Result{}, fmt.Errorf("read the outputs of %s: %w", unit, err)
	}

	// Defense in depth: the source leaves out the value of a secret output.
	// This loop stays so that a new source cannot answer one.
	for i := range outputs {
		if outputs[i].Secret {
			outputs[i].Value = nil
		}
	}

	return Result{StateID: latest, Outputs: outputs}, nil
}
