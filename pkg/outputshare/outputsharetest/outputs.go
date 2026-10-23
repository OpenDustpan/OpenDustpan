package outputsharetest

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// The root outputs of the state that the SaveOutputs hook saves. A harness
// writes them in the form of its tool.
const (
	// OutputPlainName is a plain output: the JSON string OutputPlainValue.
	OutputPlainName = "plain"
	// OutputPlainValue is the value of the plain output.
	OutputPlainValue = "a"
	// OutputSecretName is an output that the tool marks secret or sensitive.
	// Its value is the JSON string OutputHiddenText.
	OutputSecretName = "secret"
	// OutputNestedName is an object output with two members: "name", the
	// plain JSON string "web", and "inner", the JSON string OutputHiddenText.
	// A tool that marks a value inside an object marks "inner" secret. A tool
	// that marks whole outputs only marks the whole output sensitive.
	OutputNestedName = "nested"
	// OutputHiddenText is the plain text of each secret value. A tool that
	// stores a secret as ciphertext stores the base64 of this text as the
	// ciphertext. No answer holds the text, its base64 or a ciphertext member.
	OutputHiddenText = "hidden-value"
)

// outputsCases are the behaviors of the Outputs port.
func outputsCases() []suiteCase {
	return withOutputs([]suiteCase{
		{
			testOutputsRead,
			"outputs: the latest state answers its root outputs ordered by name, a plain output with its value",
		},
		{
			testOutputsSecret,
			"outputs: a secret output has no value, and no value holds a secret, its ciphertext or its plain text",
		},
		{testOutputsMoved, "outputs: a state ID that is not the latest answers ErrStateMoved, also with no state"},
		{testOutputsOtherUnit, "outputs: a stable ID of another unit answers ErrUnknownProducer"},
		{testOutputsDeleted, "outputs: a unit that is deleted after the check answers ErrUnknownProducer"},
		{testOutputsNotAUnit, "outputs: an object that is not a unit answers ErrInvalid"},
		{
			testOutputsEmpty,
			"outputs: a state with no root output, and a state that the client encrypted, answer an empty list",
		},
	})
}

// withOutputs wraps each case so that it skips when the harness has no unit,
// no Outputs port or no hook of the outputs.
func withOutputs(cases []suiteCase) []suiteCase {
	out := make([]suiteCase, 0, len(cases))

	for _, tc := range cases {
		run := tc.test

		out = append(out, suiteCase{name: tc.name, test: func(t *testing.T, h Harness) {
			t.Helper()

			if h.Unit == nil || h.Outputs == nil || h.SaveOutputs == nil || h.SaveState == nil ||
				h.DeleteUnit == nil {
				t.Skip("the harness has no Outputs port of the tools")
			}

			run(t, h)
		}})
	}

	return out
}

// readRoot returns the root outputs of the state id of unit, and fails the
// test on an error.
func readRoot(t *testing.T, h Harness, unit outputshare.Placement, id string) []outputshare.Output {
	t.Helper()

	got, err := h.Outputs.Outputs(t.Context(), unit.Unit, id)
	if err != nil {
		t.Fatalf("Outputs of %s at %q: %v", unit.Unit, id, err)
	}

	return got
}

// wantOutputsErr fails the test unless the read of unit at id fails with want
// and answers no output.
func wantOutputsErr(t *testing.T, h Harness, unit identity.Object, id string, want error) {
	t.Helper()

	got, err := h.Outputs.Outputs(t.Context(), unit, id)
	if !errors.Is(err, want) || len(got) != 0 {
		t.Fatalf("Outputs of %s at %q = %+v, %v; want %v and no output", unit, id, got, err, want)
	}
}

// testOutputsRead proves that the latest state answers its three root outputs
// in the order of their names, and the value of the plain output.
func testOutputsRead(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		got := readRoot(t, h, unit, h.SaveOutputs(t, unit))

		if len(got) != 3 || got[0].Name != OutputNestedName || got[1].Name != OutputPlainName ||
			got[2].Name != OutputSecretName {
			t.Fatalf("outputs = %+v, want %s, %s and %s in order", got, OutputNestedName, OutputPlainName,
				OutputSecretName)
		}

		want, err := json.Marshal(OutputPlainValue)
		if err != nil {
			t.Fatalf("encode the plain value: %v", err)
		}

		if got[1].Secret || string(got[1].Value) != string(want) {
			t.Errorf("plain output = %+v, want the value %s", got[1], want)
		}
	})
}

// testOutputsSecret proves that a secret output has no value, and that no
// value holds the plain text of a secret, its base64 or a ciphertext member,
// also for a secret inside an object.
func testOutputsSecret(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		for _, output := range readRoot(t, h, unit, h.SaveOutputs(t, unit)) {
			wantHidden(t, output)
		}
	})
}

// wantHidden proves that output holds no secret: a secret output has no
// value, the secret output of the suite is secret, no value holds the plain
// text of a secret, its base64 or a ciphertext member, and a plain nested
// output keeps its plain member.
func wantHidden(t *testing.T, output outputshare.Output) {
	t.Helper()

	if output.Secret && output.Value != nil {
		t.Errorf("secret output %q has the value %s, want none", output.Name, output.Value)
	}

	if output.Name == OutputSecretName && !output.Secret {
		t.Errorf("output %q is not secret", output.Name)
	}

	for _, text := range []string{
		OutputHiddenText, base64.StdEncoding.EncodeToString([]byte(OutputHiddenText)), "ciphertext",
	} {
		if strings.Contains(string(output.Value), text) {
			t.Errorf("output %q holds %q: %s", output.Name, text, output.Value)
		}
	}

	if output.Name != OutputNestedName || output.Secret {
		return
	}

	var nested map[string]json.RawMessage
	if err := json.Unmarshal(output.Value, &nested); err != nil || string(nested["name"]) != `"web"` {
		t.Errorf("nested output = %s, %v; want an object with the plain member name", output.Value, err)
	}
}

// testOutputsMoved proves that the ID of an older state, and any ID for a unit
// with no state, answer ErrStateMoved.
func testOutputsMoved(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, tool string, unit outputshare.Placement) {
		t.Helper()

		empty := h.Unit(t, tool, acme, "staging", "")
		wantOutputsErr(t, h, empty.Unit, "no-state", outputshare.ErrStateMoved)

		older := h.SaveOutputs(t, unit)
		latest := h.SaveOutputs(t, unit)

		wantOutputsErr(t, h, unit.Unit, older, outputshare.ErrStateMoved)
		readRoot(t, h, unit, latest)
	})
}

// testOutputsOtherUnit proves that the reference of a unit with the stable ID
// of another unit is an unknown producer, and that the state ID of another
// unit is a state that moved.
func testOutputsOtherUnit(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, tool string, unit outputshare.Placement) {
		t.Helper()

		other := h.Unit(t, tool, acme, "staging", "")
		otherID := h.SaveOutputs(t, other)
		id := h.SaveOutputs(t, unit)

		wantOutputsErr(t, h, unit.Unit.WithUnitID(other.Unit.UnitID()), id, outputshare.ErrUnknownProducer)
		wantOutputsErr(t, h, unit.Unit, otherID, outputshare.ErrStateMoved)
	})
}

// testOutputsDeleted proves that a unit that a delete removed after the check
// of the read answers ErrUnknownProducer, as a unit that never existed.
func testOutputsDeleted(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveOutputs(t, unit)
		h.DeleteUnit(t, unit)

		wantOutputsErr(t, h, unit.Unit, id, outputshare.ErrUnknownProducer)
	})
}

// testOutputsNotAUnit proves that an object that is not a unit is refused.
func testOutputsNotAUnit(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, acme.String())
	wantOutputsErr(t, h, identity.NamespaceObject(acme), "any", outputshare.ErrInvalid)
}

// testOutputsEmpty proves that a state with no root output answers an empty
// list, and that a state that the client encrypted answers an empty list too:
// the server stores no output of it.
func testOutputsEmpty(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, tool string, unit outputshare.Placement) {
		t.Helper()

		got, err := h.Outputs.Outputs(t.Context(), unit.Unit, h.SaveState(t, unit, false))
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("Outputs of a state with no output = %+v, %v; want an empty list", got, err)
		}

		if tool != toolOpenTofu {
			return
		}

		got, err = h.Outputs.Outputs(t.Context(), unit.Unit, h.SaveState(t, unit, true))
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("Outputs of a client-encrypted state = %+v, %v; want an empty list", got, err)
		}
	})
}
