package tfe

import (
	"encoding/json"
	"testing"
)

// TestParseStoredOutputsReadsAFlatOutputNamedOutputs proves a flat outputs
// document with an output named "outputs" is read as that output, not as the
// wrapped form. The wrapped form is an "outputs" key over the map of outputs.
func TestParseStoredOutputsReadsAFlatOutputNamedOutputs(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"outputs":{"sensitive":false,"value":"x","type":"string"}}`)

	got, err := parseStoredOutputs(raw)
	if err != nil {
		t.Fatalf("parse a flat document with an output named outputs: %v", err)
	}

	output, ok := got["outputs"]
	if !ok {
		t.Fatalf("the output named outputs is missing: %v", got)
	}

	if !json.Valid(output.Value) || string(output.Value) != `"x"` {
		t.Fatalf("value of the output named outputs = %s, want \"x\"", output.Value)
	}
}

// TestParseStoredOutputsReadsTheWrappedForm keeps the wrapped form working.
func TestParseStoredOutputsReadsTheWrappedForm(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"outputs":{"greeting":{"sensitive":false,"value":"hello","type":"string"}}}`)

	got, err := parseStoredOutputs(raw)
	if err != nil {
		t.Fatalf("parse the wrapped form: %v", err)
	}

	if _, ok := got["greeting"]; !ok || len(got) != 1 {
		t.Fatalf("wrapped outputs = %v, want the greeting output only", got)
	}
}
