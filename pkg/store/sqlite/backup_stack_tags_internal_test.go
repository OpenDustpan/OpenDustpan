package sqlite

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestCheckStagedStackTags proves the restore check of the tag values: it
// accepts the rows that the store wrote, and it refuses each value that a new
// tag could not have, with backup.ErrCorrupt and no value in the error text.
// The value that is not UTF-8 is here because the portable format, which is
// JSON, cannot carry it.
func TestCheckStagedStackTags(t *testing.T) {
	t.Parallel()

	t.Run("a row that the store wrote passes", func(t *testing.T) {
		t.Parallel()

		s := openInternalTemplated(t)
		seedStackTag(t, s, "SELECT 'a' || char(10) || 'b' || char(9) || 'c'")

		if err := checkStagedStackTags(t.Context(), s.conn); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	hostile := map[string]string{
		"a value that is not UTF-8": "SELECT CAST(x'61ff62' AS TEXT)",
		"a value with a NUL":        "SELECT 'a' || char(0) || 'b'",
		"a value with U+202E":       "SELECT 'a' || char(8238) || 'b'",
		"a value with an escape":    "SELECT 'a' || char(27) || 'b'",
		"a value with a CR":         "SELECT 'a' || char(13) || 'b'",
		"a value of 257 bytes":      "SELECT '" + strings.Repeat("v", 257) + "'",
		"a value of 100 KiB":        "SELECT '" + strings.Repeat("v", 100<<10) + "'",
	}

	for name, valueSQL := range hostile {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedStackTag(t, s, valueSQL)

			err := checkStagedStackTags(t.Context(), s.conn)
			if !errors.Is(err, backup.ErrCorrupt) || !errors.Is(err, stack.ErrInvalidTags) {
				t.Fatalf("check = %v, want ErrCorrupt and ErrInvalidTags", err)
			}

			if strings.Contains(err.Error(), "vvvv") {
				t.Fatalf("error text %q holds the value", err)
			}
		})
	}
}

// seedStackTag makes the namespace acme, a stack in it and one tag row whose
// value is the single result of valueSQL, written by SQL as a changed backup
// would write it.
func seedStackTag(t *testing.T, s *Store, valueSQL string) {
	t.Helper()

	seedDriftNamespace(t, s)

	const stackSQL = `INSERT INTO stacks (namespace, project, name, id, version, created_at_ms, updated_at_ms)
VALUES ('acme', 'web', 'prod', 'stk-1', 0, 1, 1)`

	if _, err := s.conn.ExecContext(t.Context(), stackSQL); err != nil {
		t.Fatalf("insert the stack: %v", err)
	}

	tagSQL := `INSERT INTO stack_tags (stack_id, name, value) SELECT 'stk-1', 'k', (` + valueSQL + `)`

	if _, err := s.conn.ExecContext(t.Context(), tagSQL); err != nil {
		t.Fatalf("insert the tag: %v", err)
	}
}
