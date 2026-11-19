package sqlite_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestGitHostRegistrationOldSecretCheck sends rows with a part of the old event
// secret directly to the schema, as another version of the server or a hand
// edit could. The CHECK constraint must refuse each one, whatever the code
// above it does.
func TestGitHostRegistrationOldSecretCheck(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	})

	const insert = `INSERT INTO git_host_app_registrations
(kind, app_id, base_url, hook_id, sealed_key, key_set_id, key_created_at_ms, sealed_event_secret,
 event_secret_set_id, event_secret_created_at_ms, address_digest, version, created_at_ms, updated_at_ms,
 old_event_secret_ends_at_ms, old_event_secret_set_id, old_sealed_event_secret)
VALUES (?, '1', 'https://api.github.com', ?, x'01', 's', 1, x'02', 'e', 1, 'd', 1, 1, 1, ?, ?, ?)`

	// The rows that the CHECK must allow, so that a refusal below is the CHECK.
	if _, execErr := conn.ExecContext(t.Context(), insert, "none", "h-none", nil, nil, nil); execErr != nil {
		t.Fatalf("insert a row with no old secret: %v", execErr)
	}

	if _, execErr := conn.ExecContext(t.Context(), insert, "whole", "h-whole", 5, "o", []byte{3}); execErr != nil {
		t.Fatalf("insert a row with the whole old secret: %v", execErr)
	}

	for name, args := range map[string][]any{
		"only the end":          {5, nil, nil},
		"only the set ID":       {nil, "o", nil},
		"only the sealed value": {nil, nil, []byte{3}},
		"no end":                {nil, "o", []byte{3}},
		"no set ID":             {5, nil, []byte{3}},
		"no sealed value":       {5, "o", nil},
	} {
		_, execErr := conn.ExecContext(t.Context(), insert, append([]any{"k-" + name, "h-" + name}, args...)...)
		if execErr == nil || !strings.Contains(execErr.Error(), "CHECK") {
			t.Errorf("insert with %s = %v, want a CHECK constraint failure", name, execErr)
		}
	}

	// A partial change of a stored row is refused too.
	for name, update := range map[string]string{
		"clear only the end":    "UPDATE git_host_app_registrations SET old_event_secret_ends_at_ms = NULL WHERE kind = 'whole'",
		"clear only the set ID": "UPDATE git_host_app_registrations SET old_event_secret_set_id = NULL WHERE kind = 'whole'",
		"clear only the value":  "UPDATE git_host_app_registrations SET old_sealed_event_secret = NULL WHERE kind = 'whole'",
		"set only the end":      "UPDATE git_host_app_registrations SET old_event_secret_ends_at_ms = 9 WHERE kind = 'none'",
	} {
		_, execErr := conn.ExecContext(t.Context(), update)
		if execErr == nil || !strings.Contains(execErr.Error(), "CHECK") {
			t.Errorf("update with %s = %v, want a CHECK constraint failure", name, execErr)
		}
	}
}
