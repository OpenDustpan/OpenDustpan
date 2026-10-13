package backup_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestCheckColumnsCoverage proves the comparison matches the live columns in
// any order, and that a missing or an unknown column fails with ErrCoverage
// and names the table and the column (ADR 42, decision 5).
func TestCheckColumnsCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		listed []string
		live   []string
		want   string
	}{
		{
			name:   "the same columns in another order match",
			listed: []string{"id", "created_by"},
			live:   []string{"created_by", "id"},
			want:   "",
		},
		{
			name:   "a column the list omits fails",
			listed: []string{"id"},
			live:   []string{"id", "created_by"},
			want:   `"created_by"`,
		},
		{
			name:   "a listed column the schema does not hold fails",
			listed: []string{"id", "nope"},
			live:   []string{"id"},
			want:   `"nope"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := backup.CheckColumns("updates", tt.listed, tt.live)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("CheckColumns = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, backup.ErrCoverage) {
				t.Fatalf("CheckColumns = %v, want ErrCoverage", err)
			}

			if !strings.Contains(err.Error(), `"updates"`) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("CheckColumns = %v, want the table %q and the column %s", err, "updates", tt.want)
			}
		})
	}
}
