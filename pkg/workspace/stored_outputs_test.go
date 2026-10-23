package workspace_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestParseStoredOutputs proves the two forms of the outputs document, and a
// flat output named outputs.
func TestParseStoredOutputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{
			name: "flat", raw: `{"a":{"sensitive":false,"value":"x","type":"string"}}`,
			want: map[string]string{"a": `"x"`},
		},
		{
			name: "wrapped", raw: `{"outputs":{"a":{"sensitive":true,"value":1,"type":"number"}}}`,
			want: map[string]string{"a": `1`},
		},
		{
			name: "a flat output named outputs", raw: `{"outputs":{"sensitive":false,"value":"x","type":"string"}}`,
			want: map[string]string{"outputs": `"x"`},
		},
		{name: "null", raw: `null`, want: map[string]string{}},
		{name: "empty object", raw: `{}`, want: map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := workspace.ParseStoredOutputs([]byte(tt.raw))
			if err != nil {
				t.Fatalf("ParseStoredOutputs: %v", err)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("outputs = %v, want %v", got, tt.want)
			}

			for name, value := range tt.want {
				if string(got[name].Value) != value {
					t.Errorf("output %q value = %s, want %s", name, got[name].Value, value)
				}
			}
		})
	}
}

// TestParseStoredOutputsRefusesWhatIsNotAnOutputsDocument proves that a
// document that is not a JSON object of outputs fails.
func TestParseStoredOutputsRefusesWhatIsNotAnOutputsDocument(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{`{`, `[1]`, `"x"`, `{"a":1}`} {
		if _, err := workspace.ParseStoredOutputs([]byte(raw)); err == nil {
			t.Errorf("ParseStoredOutputs(%q) = nil, want an error", raw)
		}
	}
}
