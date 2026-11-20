package postgres_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestGitHostRegistrationOldSecretCheck sends rows with a part of the old event
// secret directly to the schema, as another version of the server or a hand
// edit could. The CHECK constraint must refuse each one, whatever the code
// above it does.
func TestGitHostRegistrationOldSecretCheck(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL schema checks", envURL)
	}

	conn, err := pgx.Connect(t.Context(), isolatedURL(t, base))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	const insert = `INSERT INTO git_host_app_registrations
(kind, app_id, base_url, hook_id, sealed_key, key_set_id, key_created_at_ms, sealed_event_secret,
 event_secret_set_id, event_secret_created_at_ms, address_digest, version, created_at_ms, updated_at_ms,
 old_event_secret_ends_at_ms, old_event_secret_set_id, old_sealed_event_secret)
VALUES ($1, '1', 'https://api.github.com', $2, '\x01', 's', 1, '\x02', 'e', 1, 'd', 1, 1, 1, $3, $4, $5)`

	if _, execErr := conn.Exec(t.Context(), insert, "none", "h-none", nil, nil, nil); execErr != nil {
		t.Fatalf("insert a row with no old secret: %v", execErr)
	}

	if _, execErr := conn.Exec(t.Context(), insert, "whole", "h-whole", int64(5), "o", []byte{3}); execErr != nil {
		t.Fatalf("insert a row with the whole old secret: %v", execErr)
	}

	for name, args := range map[string][]any{
		"only the end":          {int64(5), nil, nil},
		"only the set ID":       {nil, "o", nil},
		"only the sealed value": {nil, nil, []byte{3}},
		"no end":                {nil, "o", []byte{3}},
		"no set ID":             {int64(5), nil, []byte{3}},
		"no sealed value":       {int64(5), "o", nil},
	} {
		_, execErr := conn.Exec(t.Context(), insert, append([]any{"k-" + name, "h-" + name}, args...)...)
		requireCheckFailure(t, "insert with "+name, execErr)
	}

	for name, update := range map[string]string{
		"clear only the end":    "UPDATE git_host_app_registrations SET old_event_secret_ends_at_ms = NULL WHERE kind = 'whole'",
		"clear only the set ID": "UPDATE git_host_app_registrations SET old_event_secret_set_id = NULL WHERE kind = 'whole'",
		"clear only the value":  "UPDATE git_host_app_registrations SET old_sealed_event_secret = NULL WHERE kind = 'whole'",
		"set only the end":      "UPDATE git_host_app_registrations SET old_event_secret_ends_at_ms = 9 WHERE kind = 'none'",
	} {
		_, execErr := conn.Exec(t.Context(), update)
		requireCheckFailure(t, "update with "+name, execErr)
	}
}

// requireCheckFailure fails the test unless err is the failure of the CHECK
// constraint of the old event secret.
func requireCheckFailure(t *testing.T, what string, err error) {
	t.Helper()

	if err == nil || !strings.Contains(err.Error(), "git_host_app_registrations_old_event_secret_check") {
		t.Errorf("%s = %v, want the CHECK constraint failure", what, err)
	}
}
