package workspace_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// deepRevealPath is a reveal path whose attribute route holds more tokens than
// MaxStateDepth.
func deepRevealPath(base string) string {
	return base + strings.Repeat("/a", workspace.MaxStateDepth+1)
}

// TestRevealStreamedBranchesRefuseDeepPath proves that each streamed branch of
// the reveal refuses a path deeper than the bound with ErrStateBound, whatever
// the state holds: the branch with no marks and the branch with marks that
// answers from the stream.
func TestRevealStreamedBranchesRefuseDeepPath(t *testing.T) {
	t.Parallel()

	state := []byte(revealState())
	deep := deepRevealPath("/resources/0/instances/0/attributes/input")

	tests := []struct {
		name  string
		marks workspace.SensitiveMarks
	}{
		{name: "no marks", marks: nil},
		{name: "marks, streamed answer", marks: revealMarks()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := workspace.RevealValue(state, tt.marks, deep)
			if !errors.Is(err, workspace.ErrStateBound) {
				t.Fatalf("err = %v, want ErrStateBound", err)
			}
		})
	}
}

// cancelOnClose is a stream whose Close ends the context of the reveal, so the
// context ends at the end of one pass, after the pass has read its state.
type cancelOnClose struct {
	io.Reader

	cancel context.CancelFunc
	fire   bool
}

func (c *cancelOnClose) Close() error {
	if c.fire {
		c.cancel()
	}

	return nil
}

// TestRevealStreamedBranchesStopWhenContextEnds proves that a context that
// ends at the end of any pass of a streamed branch gives an error and no
// value. The last case ends the context at the end of the last pass, after
// which only the check at the end of the reveal can stop the answer.
func TestRevealStreamedBranchesStopWhenContextEnds(t *testing.T) {
	t.Parallel()

	state := []byte(revealState())
	path := "/resources/0/instances/0/attributes/input"

	tests := []struct {
		name   string
		marks  workspace.SensitiveMarks
		passes int
	}{
		{name: "no marks", marks: nil, passes: 2},
		{name: "marks, streamed answer", marks: revealMarks(), passes: 3},
	}

	for _, tt := range tests {
		for end := 1; end <= tt.passes; end++ {
			t.Run(tt.name+", ends at pass "+strconv.Itoa(end), func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				opens := 0
				open := func() (io.ReadCloser, error) {
					opens++

					return &cancelOnClose{Reader: bytes.NewReader(state), cancel: cancel, fire: opens == end}, nil
				}

				value, _, err := workspace.RevealValueFromOpenerContext(ctx, open, tt.marks, path)
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("value = %v, err = %v, want context.Canceled", value, err)
				}

				// The reveal stops at the end of the pass that the context cut
				// short, and opens no later pass.
				if opens != end {
					t.Fatalf("passes = %d, want %d", opens, end)
				}
			})
		}
	}
}

// TestRevealRefusesDeepInstanceOfFullDecode proves the depth bound of the
// reveal that decodes the whole instance: a field outside the attributes that
// nests deeper than MaxStateDepth ends the reveal with ErrStateBound.
func TestRevealRefusesDeepInstanceOfFullDecode(t *testing.T) {
	t.Parallel()

	deep := strings.Repeat(`{"a":`, workspace.MaxStateDepth+20) + `1` + strings.Repeat(`}`, workspace.MaxStateDepth+20)
	state := []byte(`{"version":4,"resources":[{"mode":"managed","type":"terraform_data","name":"x",` +
		`"instances":[{"attributes":{"input":{"value":{"k":"s"},"type":"dynamic"}},` +
		`"private":` + deep + `,` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"input"},{"type":"get_attr","value":"k"}]]}]}]}`)

	_, _, err := workspace.RevealValue(state, revealMarks(), "/resources/0/instances/0/attributes/input/k")
	if !errors.Is(err, workspace.ErrStateBound) {
		t.Fatalf("err = %v, want ErrStateBound", err)
	}
}
