package sqlite

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// TestConfirmGitHostRotationQueryRefusesAClosedWindow sends the query a window
// that ended, directly, as if the Go check before it did not exist. The query
// is the only layer under test: it must write nothing at or after the end of
// the window, and it must write before the end. The test fails when the end
// condition is removed from the query.
func TestConfirmGitHostRotationQueryRefusesAClosedWindow(t *testing.T) {
	t.Parallel()

	const endsMs = 5_000

	for _, tc := range []struct {
		name   string
		nowMs  int64
		writes int64
	}{
		{"before the end", endsMs - 1, 1},
		{"at the end", endsMs, 0},
		{"after the end", endsMs + 1, 0},
		{"a week after the end", endsMs + 7*24*60*60*1000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)

			_, err := s.conn.ExecContext(t.Context(), `INSERT INTO git_host_app_registrations
(kind, app_id, base_url, hook_id, sealed_key, key_set_id, key_created_at_ms, sealed_event_secret,
 event_secret_set_id, event_secret_created_at_ms, address_digest, version, created_at_ms, updated_at_ms,
 old_event_secret_ends_at_ms, old_event_secret_set_id, old_sealed_event_secret)
VALUES ('github', '1', 'https://api.github.com', 'h', x'01', 's', 1, x'02', 'e', 1, 'd', 1, 1, 1, ?, 'o', x'03')`,
				endsMs)
			if err != nil {
				t.Fatalf("insert the registration: %v", err)
			}

			n, err := s.queries.ConfirmGitHostRotation(t.Context(), db.ConfirmGitHostRotationParams{
				UpdatedAtMs: tc.nowMs, Kind: "github", IfVersion: 0,
			})
			if err != nil {
				t.Fatalf("query: %v", err)
			}

			if n != tc.writes {
				t.Fatalf("the query wrote %d rows at %d ms, want %d", n, tc.nowMs, tc.writes)
			}
		})
	}
}
