package postgres

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
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

	s := openForSavedViews(t)
	ctx := t.Context()

	for _, stmt := range []string{
		`INSERT INTO namespaces (name, primary_installation, failover_version, created_at_ms, id)
			VALUES ('acme', 'eu-west', 1, 0, 'ns-pos')`,
		`INSERT INTO profiles (namespace_id, id, name, latest, created_by, created_at_ms, updated_at_ms, deleted_at_ms)
			VALUES ('ns-pos', 'prf-1', 'base', 1, 'prn-1', 0, 0, 0)`,
	} {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	insert := func(r openSessionRow) error {
		_, err := s.pool.Exec(ctx,
			`INSERT INTO profile_open_sessions
				(namespace_id, id, root_profile_id, owner_kind, owner_id, consumer, paths, sealed,
				 created_at_ms, expires_at_ms)
			VALUES ('ns-pos', $1, 'prf-1', $2, $3, $4, $5, $6, $7, $8)`,
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

	const (
		checkViolation = "23514"
		pfx            = "profile_open_sessions_"
	)

	tests := []struct {
		name       string
		change     func(*openSessionRow)
		wantCode   string
		constraint string
	}{
		{"id with another prefix", func(r *openSessionRow) { r.id = "xyz-1" }, checkViolation, pfx + "id_check"},
		{"id that is only the prefix", func(r *openSessionRow) { r.id = "pos-" }, checkViolation, pfx + "id_check"},
		{
			"owner kind other than principal",
			func(r *openSessionRow) { r.ownerKind = "group" },
			checkViolation,
			pfx + "owner_kind_check",
		},
		{"empty owner id", func(r *openSessionRow) { r.ownerID = "" }, checkViolation, pfx + "owner_id_check"},
		{
			"unknown consumer",
			func(r *openSessionRow) { r.consumer = "terraform" },
			checkViolation,
			pfx + "consumer_check",
		},
		{"paths null", func(r *openSessionRow) { r.paths = "null" }, checkViolation, pfx + "paths_check"},
		{"paths object", func(r *openSessionRow) { r.paths = "{}" }, checkViolation, pfx + "paths_check"},
		{"paths string", func(r *openSessionRow) { r.paths = `"x"` }, checkViolation, pfx + "paths_check"},
		{"paths number", func(r *openSessionRow) { r.paths = "1" }, checkViolation, pfx + "paths_check"},
		// The cast to jsonb in the CHECK raises invalid_text_representation for text that is not JSON.
		{"paths invalid JSON", func(r *openSessionRow) { r.paths = "[" }, "22P02", ""},
		{"empty sealed", func(r *openSessionRow) { r.sealed = []byte{} }, checkViolation, pfx + "sealed_check"},
		{
			"expiry equal to creation",
			func(r *openSessionRow) { r.expiresMS = r.createdMS },
			checkViolation,
			pfx + "check",
		},
		{
			"expiry before creation",
			func(r *openSessionRow) { r.expiresMS = r.createdMS - 1 },
			checkViolation,
			pfx + "check",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := validOpenSessionRow()
			r.id = "pos-" + strings.ReplaceAll(tc.name, " ", "-")
			tc.change(&r)

			err := insert(r)

			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("insert of %q = %v, want a PostgreSQL error", tc.name, err)
			}

			if pgErr.Code != tc.wantCode || pgErr.ConstraintName != tc.constraint {
				t.Fatalf("insert of %q = code %s constraint %q, want code %s constraint %q",
					tc.name, pgErr.Code, pgErr.ConstraintName, tc.wantCode, tc.constraint)
			}
		})
	}
}
