package unitimport_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/unitimport"
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
