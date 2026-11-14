package apidesc_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

func TestPinFrozen(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(pinFile)
	if err != nil {
		t.Fatalf("read the pin: %v", err)
	}

	p, err := parsePin(raw)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		version string
	}{
		{"DocumentVersion", apidesc.DocumentVersion},
		{"info.version", member[string](t, decoded(t), "info", "version")},
		{"openapi.pin version", p.version},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.version != "0.36.0" {
				t.Error("The first release sets info.version and openapi.pin; until then they stay 0.36.0.")
			}
		})
	}
}
