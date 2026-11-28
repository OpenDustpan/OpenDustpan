package sqlite

import (
	"testing"
	"time"
)

// TestWorkingBreakGlassCountIgnoresGrantsOfAnotherNamespace proves that the
// count of a namespace does not count an administration grant that the row of
// another namespace holds. The store API refuses such a grant, so the row is
// written with SQL: the test shows the grant filter of the count, which is a
// layer of its own. Without the filter, the account below counts as working.
func TestWorkingBreakGlassCountIgnoresGrantsOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	s := openSeededKeepStore(t)
	ctx := t.Context()

	const (
		home = "grant-home"
		away = "grant-away"
		id   = "prn-grant-home"
	)

	stmts := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name: "the person",
			query: `INSERT INTO principals (id, subject, display_name, kind, source, created_at_ms, updated_at_ms,
				namespace, break_glass, last_active_at_ms)
				VALUES (?, 'grant-home', 'Home', 'person', 'local', 1000, 1000, ?, 1, 0)`,
			args: []any{id, home},
		},
		{
			name: "the passkey",
			query: `INSERT INTO passkeys (id, principal_id, name, credential_id, public_key, aaguid, created_at_ms)
				VALUES ('pky-grant-home', ?, 'key', X'01', X'02', zeroblob(16), 1000)`,
			args: []any{id},
		},
		{
			name: "the administration grant in the other namespace",
			query: `INSERT INTO grants (id, subject, subject_kind, subject_id, role, object, target, object_kind,
				namespace, created_at_ms)
				VALUES ('grt-grant-away', ?, 'principal', ?, 'admin', 'namespace:grant-away', '', 'namespace', ?, 1000)`,
			args: []any{id, id, away},
		},
	}

	for _, st := range stmts {
		if _, err := s.conn.ExecContext(ctx, st.query, st.args...); err != nil {
			t.Fatalf("insert %s: %v", st.name, err)
		}
	}

	got, err := s.CountWorkingBreakGlassInNamespace(ctx, home, time.UnixMilli(2000).UTC())
	if err != nil {
		t.Fatalf("count of %q: %v", home, err)
	}

	if got != 0 {
		t.Fatalf("working count of %q = %d, want 0: the grant is in %q", home, got, away)
	}
}
