package unitimport_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/pkg/history"
)

func TestValidateUnitsStartState(t *testing.T) {
	t.Parallel()

	unit := func(state unitimport.UnitState, code string) unitimport.ImportUnit {
		return unitimport.ImportUnit{
			ID:         "u-1",
			ImportID:   "",
			Source:     unitimport.SourceUnit{Tool: "opentofu", Name: "acme/web"},
			TargetName: "web",
			Target:     history.Unit{Namespace: "", Tool: "", ID: ""},
			State:      state,
			ErrorCode:  code,
			Versions:   0,
			Committed:  0,
			Bytes:      0,
			Encryption: "",
			Findings:   nil,
		}
	}

	tests := []struct {
		name string
		unit unitimport.ImportUnit
		ok   bool
	}{
		{"planned", unit(unitimport.UnitPlanned, ""), true},
		{"skipped with a code", unit(unitimport.UnitSkipped, "import.target-exists"), true},
		{"failed with a code", unit(unitimport.UnitFailed, "import.source-decrypt-refused"), true},
		{"skipped with no code", unit(unitimport.UnitSkipped, ""), false},
		{"failed with no code", unit(unitimport.UnitFailed, ""), false},
		{"running", unit(unitimport.UnitRunning, ""), false},
		{"imported", unit(unitimport.UnitImported, "x"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := unitimport.ValidateUnits("imp-1", []unitimport.ImportUnit{tc.unit})

			switch {
			case tc.ok && err != nil:
				t.Errorf("ValidateUnits = %v, want nil", err)
			case !tc.ok && !errors.Is(err, unitimport.ErrInvalid):
				t.Errorf("ValidateUnits = %v, want ErrInvalid", err)
			default:
			}
		})
	}
}

// TestSourceUnitValidateRefusesHiddenRunes checks the text rules of a source
// unit through SourceUnit.Validate: a hidden character in the name is refused
// like a control character is.
func TestSourceUnitValidateRefusesHiddenRunes(t *testing.T) {
	t.Parallel()

	for name, tool := range map[string]unitimport.SourceUnit{
		"plain": {Tool: "opentofu", Name: "acme/web"},
	} {
		if err := tool.Validate(); err != nil {
			t.Errorf("%s: Validate = %v, want nil", name, err)
		}
	}

	for name, unit := range map[string]unitimport.SourceUnit{
		"control character":         {Tool: "opentofu", Name: "acme/we\x00b"},
		"right-to-left override":    {Tool: "opentofu", Name: "acme/we" + string(rune(0x202e)) + "b"},
		"line separator":            {Tool: "opentofu", Name: "acme/we" + string(rune(0x2028)) + "b"},
		"paragraph separator":       {Tool: "opentofu", Name: "acme/we" + string(rune(0x2029)) + "b"},
		"control character in tool": {Tool: "open\x00tofu", Name: "acme/web"},
	} {
		if err := unit.Validate(); !errors.Is(err, unitimport.ErrInvalid) {
			t.Errorf("%s: Validate = %v, want ErrInvalid", name, err)
		}
	}
}
