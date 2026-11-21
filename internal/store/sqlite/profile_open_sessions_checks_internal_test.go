package sqlite

import (
	"fmt"
	"strings"
	"testing"
)

// openSessionRow is the column set of one profile_open_sessions row. The valid
// row passes every CHECK of the table. Each bad case changes one column.
type openSessionRow struct {
	id, ownerKind, ownerID, consumer, paths string
	sealed                                  []byte
	createdMS, expiresMS                    int64
}

func validOpenSessionRow() openSessionRow {
	return openSessionRow{
		id: "pos-valid", ownerKind: "principal", ownerID: "prn-1", consumer: "api", paths: `["a"]`,
		sealed: []byte{1}, createdMS: 1000, expiresMS: 2000,
	}
}

// TestProfileOpenSessionChecksRefuseABadRow sends a row that breaks one CHECK
// of profile_open_sessions straight to the table, with no service in front. The
// schema is the last layer. Each case differs from the control row in one
// column, so the case fails only when its own CHECK is gone.
func TestProfileOpenSessionChecksRefuseABadRow(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	ctx := t.Context()

	for _, stmt := range []string{
		`INSERT INTO namespaces (name, primary_installation, failover_version, created_at_ms, id)
			VALUES ('acme', 'eu-west', 1, 0, 'ns-pos')`,
		`INSERT INTO profiles (namespace_id, id, name, latest, created_by, created_at_ms, updated_at_ms, deleted_at_ms)
			VALUES ('ns-pos', 'prf-1', 'base', 1, 'prn-1', 0, 0, 0)`,
	} {
		if _, err := s.conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	insert := func(r openSessionRow) error {
		_, err := s.conn.ExecContext(ctx,
			`INSERT INTO profile_open_sessions
				(namespace_id, id, root_profile_id, owner_kind, owner_id, consumer, paths, sealed,
				 created_at_ms, expires_at_ms)
			VALUES ('ns-pos', ?, 'prf-1', ?, ?, ?, ?, ?, ?, ?)`,
			r.id, r.ownerKind, r.ownerID, r.consumer, r.paths, r.sealed, r.createdMS, r.expiresMS)
		if err != nil {
			return fmt.Errorf("insert %s: %w", r.id, err)
		}

		return nil
	}

	// The control: a valid row goes in, so a refusal below is the CHECK and not the seed.
	if err := insert(validOpenSessionRow()); err != nil {
		t.Fatalf("insert the valid row: %v", err)
	}

	tests := []struct {
		name   string
		change func(*openSessionRow)
	}{
		{"id with another prefix", func(r *openSessionRow) { r.id = "xyz-1" }},
		{"id that is only the prefix", func(r *openSessionRow) { r.id = "pos-" }},
		{"owner kind other than principal", func(r *openSessionRow) { r.ownerKind = "group" }},
		{"empty owner id", func(r *openSessionRow) { r.ownerID = "" }},
		{"unknown consumer", func(r *openSessionRow) { r.consumer = "terraform" }},
		{"paths null", func(r *openSessionRow) { r.paths = "null" }},
		{"paths object", func(r *openSessionRow) { r.paths = "{}" }},
		{"paths string", func(r *openSessionRow) { r.paths = `"x"` }},
		{"paths number", func(r *openSessionRow) { r.paths = "1" }},
		{"paths invalid JSON", func(r *openSessionRow) { r.paths = "[" }},
		// json_type() reads JSON5, and json_valid() without flags reads only RFC 8259. These
		// two arrays give json_type = array and json_valid = 0, so only json_valid refuses them.
		{"paths JSON5 with a trailing comma", func(r *openSessionRow) { r.paths = `["a",]` }},
		{"paths JSON5 with single quotes", func(r *openSessionRow) { r.paths = `['a']` }},
		{"empty sealed", func(r *openSessionRow) { r.sealed = []byte{} }},
		{"expiry equal to creation", func(r *openSessionRow) { r.expiresMS = r.createdMS }},
		{"expiry before creation", func(r *openSessionRow) { r.expiresMS = r.createdMS - 1 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := validOpenSessionRow()
			// A new id for each case, except the cases that set the id themselves.
			r.id = "pos-" + strings.ReplaceAll(tc.name, " ", "-")
			tc.change(&r)

			err := insert(r)
			if err == nil {
				t.Fatalf("insert of %q succeeded, want a refusal", tc.name)
			}

			// Invalid JSON may fail as a malformed-JSON error and not as a CHECK, so
			// the text names the CHECK for the other cases only.
			if tc.name != "paths invalid JSON" && !strings.Contains(err.Error(), "CHECK constraint failed") {
				t.Fatalf("insert of %q = %v, want a CHECK constraint failure", tc.name, err)
			}
		})
	}
}
