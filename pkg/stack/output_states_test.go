package stack_test

import (
	"encoding/json"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/stack"
)

// sealedOutput is the deployment form of a secret value that a stack saves.
const sealedOutput = `{"` + stateSigKey + `":"` + stateSecretValue + `","ciphertext":"c2VjcmV0"}`

// rootDeployment returns a deployment whose stack resource holds the outputs,
// and a resource with a secret output that is not a root output.
func rootDeployment(outputs string) []byte {
	return []byte(`{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},` +
		`"secrets_providers":{"type":"service"},"resources":[` +
		`{"urn":"urn:pulumi:prod::web::pulumi:pulumi:Stack::web-prod","type":"pulumi:pulumi:Stack","outputs":` +
		outputs + `},` +
		`{"urn":"urn:pulumi:prod::web::aws:s3/bucket:Bucket::b","type":"aws:s3/bucket:Bucket","custom":true,` +
		`"outputs":{"private":` + sealedOutput + `}}]}`)
}

// TestRootOutputsMarkSecrets proves the parse of the root outputs of a
// checkpoint: the names in order, a plain value, a secret with no value, and a
// secret inside an object that stays the marker of the masked read, with no
// ciphertext in any value.
func TestRootOutputsMarkSecrets(t *testing.T) {
	t.Parallel()

	got, err := stack.ParseRootOutputs(rootDeployment(
		`{"url":"https://x","token":` + sealedOutput + `,"nested":{"k":` + sealedOutput + `}}`,
	))
	if err != nil {
		t.Fatalf("ParseRootOutputs: %v", err)
	}

	if len(got) != 3 || got[0].Name != "nested" || got[1].Name != "token" || got[2].Name != "url" {
		t.Fatalf("outputs = %+v, want nested, token and url in order", got)
	}

	if got[1].Value != nil || !got[1].Secret {
		t.Errorf("token = %+v, want a secret with no value", got[1])
	}

	if string(got[2].Value) != `"https://x"` || got[2].Secret {
		t.Errorf("url = %+v, want the plain value", got[2])
	}

	wantMarker(t, got[0])
	wantNoCiphertext(t, got)
}

// secretMarker is the marker that the masked read puts in place of a secret
// value.
type secretMarker struct {
	Secret bool   `json:"secret"`
	Path   string `json:"path"`
}

// wantMarker proves that output is a plain object whose member k is the
// marker of a secret: secret true and a path.
func wantMarker(t *testing.T, output outputshare.Output) {
	t.Helper()

	var nested map[string]secretMarker

	if err := json.Unmarshal(output.Value, &nested); err != nil || output.Secret {
		t.Fatalf("nested = %+v (%s), %v; want a plain object", output, output.Value, err)
	}

	if marker := nested["k"]; !marker.Secret || marker.Path == "" {
		t.Errorf("nested = %s, want the member k as the secret marker", output.Value)
	}
}

// wantNoCiphertext proves that no output value holds ciphertext of a secret.
func wantNoCiphertext(t *testing.T, outputs []outputshare.Output) {
	t.Helper()

	for _, output := range outputs {
		if strings.Contains(string(output.Value), "c2VjcmV0") || strings.Contains(string(output.Value), "ciphertext") {
			t.Errorf("output %q holds ciphertext: %s", output.Name, output.Value)
		}
	}
}

// TestRootOutputsHandleOddDeployments proves the edge cases of the parse: no
// stack resource, no outputs, a plain output that has the shape of a plain
// object with a secret member, and a deployment that is not JSON.
func TestRootOutputsHandleOddDeployments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		deployment string
		want       int
	}{
		{"no resources", `{"manifest":{},"resources":[]}`, 0},
		{"no outputs", `{"manifest":{},"resources":[{"urn":"u","type":"pulumi:pulumi:Stack"}]}`, 0},
		{
			"a plain object output",
			`{"manifest":{},"resources":[{"urn":"u","type":"pulumi:pulumi:Stack","outputs":{"a":{"secret":true}}}]}`,
			1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := stack.ParseRootOutputs([]byte(tt.deployment))
			if err != nil || got == nil || len(got) != tt.want {
				t.Fatalf("ParseRootOutputs = %+v, %v; want %d outputs", got, err, tt.want)
			}

			for _, output := range got {
				if output.Secret || output.Value == nil {
					t.Errorf("output %+v: an object with a secret member and no path is a plain output", output)
				}
			}
		})
	}

	if _, err := stack.ParseRootOutputs([]byte(`{`)); err == nil {
		t.Error("ParseRootOutputs of broken JSON = nil, want an error")
	}
}
