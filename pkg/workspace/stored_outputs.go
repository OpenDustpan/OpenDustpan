package workspace

import (
	"encoding/json"
	"fmt"
)

// storedOutputs is the wrapped form of the stored outputs document: an
// "outputs" key over the map of outputs.
type storedOutputs struct {
	Outputs map[string]StoredOutput `json:"outputs"`
}

// StoredOutput is one entry of the outputs document of a state version: the
// JSON rendering of one root output that the client sent at create. It holds
// the cty value and its cty type in JSON form, and whether the client marked the
// output sensitive. A sensitive output holds its value in plain text.
type StoredOutput struct {
	Sensitive bool            `json:"sensitive"`
	Value     json.RawMessage `json:"value"`
	Type      json.RawMessage `json:"type"`
}

// ParseStoredOutputs reads the outputs document of a state version into the
// map of outputs by name. It accepts the flat map that the CLI sends and the
// wrapped form with an "outputs" key, which versions stored before the flat
// shape was served hold. Anything else fails.
func ParseStoredOutputs(raw []byte) (map[string]StoredOutput, error) {
	var wrapped storedOutputs

	// A flat document with an output named "outputs" does not decode as the
	// wrapped form: its entries are not outputs. It is read as flat below.
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Outputs != nil {
		return wrapped.Outputs, nil
	}

	var flat map[string]StoredOutput

	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, fmt.Errorf("decode stored outputs: %w", err)
	}

	if flat == nil {
		return map[string]StoredOutput{}, nil
	}

	return flat, nil
}
