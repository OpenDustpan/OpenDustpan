package clientgen_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen"
)

func TestCheckClientVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		description string
		client      string
		ok          bool
	}{
		{"same version", "0.12.0", "0.12.0", true},
		{"own patch", "0.12.0", "0.12.7", true},
		{"description patch differs", "0.12.3", "0.12.0", true},
		{"minor differs", "0.12.0", "0.13.0", false},
		{"major differs", "1.2.0", "2.2.0", false},
		{"client is not semver", "0.12.0", "0.12", false},
		{"description is not semver", "v0.12.0", "0.12.0", false},
		{"leading zero", "0.12.0", "0.012.0", false},
		{"prerelease of the client", "0.12.0", "0.12.1-rc.1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := clientgen.CheckClientVersion(tt.description, tt.client)
			if tt.ok && err != nil {
				t.Fatalf("want no error, got %v", err)
			}

			if !tt.ok && !errors.Is(err, clientgen.ErrVersion) {
				t.Fatalf("want ErrVersion, got %v", err)
			}
		})
	}
}

func TestClientVersion(t *testing.T) {
	t.Parallel()

	got, err := clientgen.ClientVersion("0.12.9", 3)
	if err != nil || got != "0.12.3" {
		t.Fatalf("want 0.12.3, got %q and %v", got, err)
	}

	if _, err = clientgen.ClientVersion("0.12", 0); !errors.Is(err, clientgen.ErrVersion) {
		t.Errorf("a description version that is not semver: want ErrVersion, got %v", err)
	}

	if _, err = clientgen.ClientVersion("0.12.0", -1); !errors.Is(err, clientgen.ErrVersion) {
		t.Errorf("a patch below 0: want ErrVersion, got %v", err)
	}
}
