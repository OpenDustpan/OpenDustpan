package stack_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// nestedDeployment builds a deployment whose one resource holds a secret
// object at the given depth in its inputs.
func nestedDeployment(depth int) []byte {
	//nolint:gosec // A fake secret signature of a test document, not a credential.
	secret := `{"4dabf18193072939515e22adb298388d":"1b47061264138c4ac30d75fd1eb44270","ciphertext":"x"}`
	doc := strings.Repeat(`{"a":`, depth) + secret + strings.Repeat(`}`, depth)

	return []byte(`{"resources":[{"urn":"urn:pulumi:dev::web::t:m:T::r","type":"t:m:T","inputs":` + doc + `}]}`)
}

func TestReadStateResourceDeepDocument(t *testing.T) {
	t.Parallel()

	const urn = "urn:pulumi:dev::web::t:m:T::r"

	tests := []struct {
		name    string
		depth   int
		wantErr bool
	}{
		{name: "shallow document reads", depth: 10, wantErr: false},
		{name: "depth at the bound reads", depth: 60, wantErr: false},
		{name: "depth past the bound is refused", depth: 200, wantErr: true},
		{name: "decoder limit depth is refused", depth: 9000, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := stack.ReadStateResource(nestedDeployment(tt.depth), urn)
			if got := errors.Is(err, stack.ErrStateTooDeep); got != tt.wantErr {
				t.Fatalf("depth %d: ErrStateTooDeep = %v, want %v (err %v)", tt.depth, got, tt.wantErr, err)
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("depth %d: unexpected error %v", tt.depth, err)
			}
		})
	}
}

func TestReadStateOutputsDeepDocument(t *testing.T) {
	t.Parallel()

	deep := strings.Repeat(`{"a":`, 9000) + `1` + strings.Repeat(`}`, 9000)
	doc := []byte(`{"resources":[{"urn":"u","type":"pulumi:pulumi:Stack","outputs":` + deep + `}]}`)

	if _, err := stack.ReadStateOutputs(doc); !errors.Is(err, stack.ErrStateTooDeep) {
		t.Fatalf("err = %v, want ErrStateTooDeep", err)
	}
}
