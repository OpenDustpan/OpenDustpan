package apidesc_test

import (
	"encoding/json"
	"os"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// frozenVersion is the version of the description until the first tag. The
// pull request of the first tag sets info.version and openapi.pin, and removes
// this test.
const frozenVersion = "0.36.0"

// descriptionInfo is the info object of the description.
type descriptionInfo struct {
	Version string `json:"version"`
}

// descriptionHead is the part of the description that the test reads.
type descriptionHead struct {
	Info descriptionInfo `json:"info"`
}

// TestPinFrozen fails when DocumentVersion, info.version of the embedded
// description or the version line of openapi.pin changes before the first
// tag. It reads only the files of its own package.
func TestPinFrozen(t *testing.T) {
	t.Parallel()

	var doc descriptionHead
	if err := json.Unmarshal(apidesc.Document(), &doc); err != nil {
		t.Fatalf("read info.version of the description: %v", err)
	}

	raw, err := os.ReadFile(pinFile)
	if err != nil {
		t.Fatalf("read the pin: %v", err)
	}

	p, err := parsePin(raw)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		got  string
	}{
		{"DocumentVersion in document.go", apidesc.DocumentVersion},
		{"info.version of openapi.json", doc.Info.Version},
		{"version line of openapi.pin", p.version},
	}

	for _, tt := range tests {
		if tt.got != frozenVersion {
			t.Errorf("%s is %q. The first release sets info.version and openapi.pin; until then they stay %s.",
				tt.name, tt.got, frozenVersion)
		}
	}
}
