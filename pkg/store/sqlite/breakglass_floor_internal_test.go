package sqlite

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

// workingBreakGlassQueryCase is one case of TestWorkingBreakGlassQuerySchema.
type workingBreakGlassQueryCase struct {
	name     string
	schema   breakGlassSchema
	contains []string
	excludes []string
	wantErr  bool
}

// TestWorkingBreakGlassQuerySchema proves that the count query names only the
// schema that it is given, and that a schema it does not know is unavailable.
func TestWorkingBreakGlassQuerySchema(t *testing.T) {
	t.Parallel()

	for _, tc := range []workingBreakGlassQueryCase{
		{
			name:     "main",
			schema:   breakGlassSchemaMain,
			contains: []string{"main.principals", "main.passkeys"},
			excludes: []string{"live."},
			wantErr:  false,
		},
		{
			name:     "live",
			schema:   breakGlassSchemaLive,
			contains: []string{"live.principals", "live.grants"},
			excludes: []string{"main."},
			wantErr:  false,
		},
		{
			name:     "unknown schema",
			schema:   breakGlassSchema("temp"),
			contains: nil,
			excludes: nil,
			wantErr:  true,
		},
		{
			name:     "empty schema",
			schema:   breakGlassSchema(""),
			contains: nil,
			excludes: nil,
			wantErr:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			query, err := workingBreakGlassQuery(tc.schema)
			checkWorkingBreakGlassQuery(t, tc, query, err)
		})
	}
}

// checkWorkingBreakGlassQuery checks one result of workingBreakGlassQuery.
func checkWorkingBreakGlassQuery(t *testing.T, tc workingBreakGlassQueryCase, query string, err error) {
	t.Helper()

	if tc.wantErr {
		if !errors.Is(err, principal.ErrBreakGlassUnavailable) {
			t.Fatalf("workingBreakGlassQuery(%q) error = %v, want ErrBreakGlassUnavailable", tc.schema, err)
		}

		if query != "" {
			t.Fatalf("workingBreakGlassQuery(%q) = %q, want no text", tc.schema, query)
		}

		return
	}

	if err != nil {
		t.Fatalf("workingBreakGlassQuery(%q) error = %v, want nil", tc.schema, err)
	}

	for _, want := range tc.contains {
		if !strings.Contains(query, want) {
			t.Errorf("workingBreakGlassQuery(%q) = %q, want it to contain %q", tc.schema, query, want)
		}
	}

	for _, unwanted := range tc.excludes {
		if strings.Contains(query, unwanted) {
			t.Errorf("workingBreakGlassQuery(%q) = %q, want it not to contain %q", tc.schema, query, unwanted)
		}
	}
}
