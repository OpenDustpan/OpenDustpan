package workspace_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// nestedState builds a state whose one resource holds a leaf at the given
// depth in its attributes.
func nestedState(depth int) []byte {
	doc := strings.Repeat(`{"a":`, depth) + `1` + strings.Repeat(`}`, depth)

	return []byte(`{"version":4,"resources":[{"mode":"managed","type":"terraform_data","name":"x",` +
		`"instances":[{"attributes":` + doc + `}]}]}`)
}

func TestReadStateResourceDeepDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		depth   int
		marks   workspace.SensitiveMarks
		wantErr bool
	}{
		{name: "shallow document reads", depth: 10, marks: nil, wantErr: false},
		{name: "depth at the bound reads", depth: 55, marks: nil, wantErr: false},
		{name: "depth past the bound is refused", depth: 200, marks: nil, wantErr: true},
		{name: "decoder limit depth is refused", depth: 9000, marks: nil, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := workspace.ReadStateResource(nestedState(tt.depth), tt.marks, "terraform_data.x")
			if got := errors.Is(err, workspace.ErrStateTooDeep); got != tt.wantErr {
				t.Fatalf("depth %d: ErrStateTooDeep = %v, want %v (err %v)", tt.depth, got, tt.wantErr, err)
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("depth %d: unexpected error %v", tt.depth, err)
			}
		})
	}
}
