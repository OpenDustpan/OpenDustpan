package backup

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/outputshare"
)

// TestCheckOutputSharingRuleValueRefusesHostileColumns proves the layer of the
// backup alone: a restored row with a rule ID, a kind or a creator that no
// store write could make fails before it reaches the database.
func TestCheckOutputSharingRuleValueRefusesHostileColumns(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		column string
		value  any
		ok     bool
	}{
		{"a good ID", "id", "rule-001", true},
		{"an ID with a capital", "id", "Rule", false},
		{"an empty ID", "id", "", false},
		{"an ID that is not text", "id", int64(1), false},
		{"an ID of 65 characters", "id", strings.Repeat("a", 65), false},
		{"a unit producer", "producer_kind", "unit", true},
		{"a principal producer", "producer_kind", "principal", false},
		{"a principal consumer", "consumer_kind", "principal", false},
		{"an empty consumer kind", "consumer_kind", "", false},
		{"a creator", "created_by", "tester", true},
		{"no creator", "created_by", "", false},
		{"a consumer namespace", "consumer_namespace", "globex", true},
		{"a consumer namespace that no namespace can have", "consumer_namespace", "bad name!", false},
		{"an empty consumer namespace", "consumer_namespace", "", false},
		{"a consumer stable ID", "consumer_id", "stk-abc123", true},
		{"no consumer ID for a namespace consumer", "consumer_id", "", true},
		{"a consumer ID with a capital", "consumer_id", "Stk", false},
		{"a consumer ID with a slash", "consumer_id", "a/b", false},
		{"a consumer ID of 65 characters", "consumer_id", strings.Repeat("a", 65), false},
		{"a consumer ID that is not text", "consumer_id", int64(1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkPortableValue(TableOutputSharingRules, tc.column, tc.value)

			switch {
			case tc.ok && err != nil:
				t.Fatalf("check %s = %v, want no error", tc.column, err)
			case !tc.ok && (!errors.Is(err, ErrCorrupt) || !errors.Is(err, outputshare.ErrInvalid)):
				t.Fatalf("check %s = %v, want ErrCorrupt with outputshare.ErrInvalid", tc.column, err)
			default:
				// The value is accepted or refused as the case says.
			}
		})
	}
}
