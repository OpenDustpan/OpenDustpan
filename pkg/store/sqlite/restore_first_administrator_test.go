package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/firstadmin"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// The fixed times of the restore tests: the offer, the sign-in and the redeem,
// inside the 10-minute life of the code.
func faRestoreNow() time.Time { return time.UnixMilli(1_700_000_000_000).UTC() }

func faRestoreSignInAt() time.Time { return faRestoreNow().Add(time.Minute) }

func faRestoreRedeemAt() time.Time { return faRestoreNow().Add(2 * time.Minute) }

// faRestoreSink holds the bytes of each portable file in memory, by path.
type faRestoreSink struct {
	files map[string]*bytes.Buffer
}

type faRestoreWriter struct{ *bytes.Buffer }

// Close does nothing: the buffer stays in the sink.
func (faRestoreWriter) Close() error { return nil }

func (s *faRestoreSink) open(path string) (io.WriteCloser, error) {
	buf := &bytes.Buffer{}
	s.files[path] = buf

	return faRestoreWriter{Buffer: buf}, nil
}

func (s *faRestoreSink) read(path string) (io.ReadCloser, error) {
	buf, ok := s.files[path]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}

	return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

const (
	faRestoreBackupPerson = "prn-in-the-backup"
	faRestoreLivePerson   = "prn-live-now"
)

// faRestoreDigest is the digest of the one code that the live database holds
// when the restore runs.
func faRestoreDigest() string {
	return firstadmin.Code("R" + strings.Repeat("r", firstadmin.CodeLength-1)).Digest()
}

func faRestoreExec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()

	if _, err := conn.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func faRestoreMarker(t *testing.T, conn *sql.DB, principal string) {
	t.Helper()

	faRestoreExec(t, conn, `DELETE FROM first_administrator`)
	faRestoreExec(t, conn, `INSERT INTO first_administrator
(id, principal_id, grant_id, session_id, created_by, made_at_ms) VALUES (1, ?, 'grt-x', 'ses-x', 'host', 1000)`, principal)
}

func faRestoreCode(t *testing.T, conn *sql.DB) {
	t.Helper()

	nowMs := faRestoreNow().UnixMilli()
	faRestoreExec(t, conn, `INSERT INTO first_administrator_codes
(id, principal_id, digest, created_by, reason, created_at_ms, expires_at_ms) VALUES (1, ?, ?, 'host', 'reason', ?, ?)`,
		faRestoreLivePerson, faRestoreDigest(), nowMs, nowMs+firstadmin.LifetimeMs)
}

func faRestoreCount(t *testing.T, conn *sql.DB, table string) int {
	t.Helper()

	// The table name is one of two constants of this file.
	query := map[string]string{
		"first_administrator":       "SELECT COUNT(*) FROM first_administrator",
		"first_administrator_codes": "SELECT COUNT(*) FROM first_administrator_codes",
	}[table]

	var n int
	if err := conn.QueryRowContext(t.Context(), query).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}

	return n
}

// faRestoreApply applies the staged restore with or without the access tables
// kept.
type faRestoreApply func(ctx context.Context, s *sqlite.Store) error

// faApplyAll applies the restore as a restore with server:restore-access does.
func faApplyAll(ctx context.Context, s *sqlite.Store) error {
	if err := s.ApplyStaging(ctx); err != nil {
		return fmt.Errorf("apply: %w", err)
	}

	return nil
}

// faApplyKeepingAccess applies the restore as a restore without
// server:restore-access does.
func faApplyKeepingAccess(ctx context.Context, s *sqlite.Store) error {
	if _, err := s.ApplyStagingKeepingAccess(ctx); err != nil {
		return fmt.Errorf("apply keeping access: %w", err)
	}

	return nil
}

// restoreFirstAdministrator exports the live store, changes the live rows with
// change, stages the export and applies it with apply.
func restoreFirstAdministrator(t *testing.T, s *sqlite.Store, apply faRestoreApply, change func()) {
	t.Helper()

	sink := &faRestoreSink{files: map[string]*bytes.Buffer{}}

	sum, files, err := s.ExportPortable(t.Context(), sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	change()

	if _, err = s.StagePortable(t.Context(), sink.read, sum, files, storetest.StageAs(t, s)); err != nil {
		t.Fatalf("stage portable: %v", err)
	}

	if err = apply(t.Context(), s); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

// faRestoreStore opens the store of a restore test, with its direct handle.
func faRestoreStore(t *testing.T) (*sqlite.Store, *sql.DB) {
	t.Helper()

	s := open(t)

	return s, sqlite.DB(s)
}

// faCheckMarker fails unless the state of s holds the marker of want and no
// open code, and the code table is empty.
func faCheckMarker(t *testing.T, s *sqlite.Store, want string) {
	t.Helper()

	st, err := s.State(context.Background(), faRestoreRedeemAt())
	if err != nil {
		t.Fatalf("state: %v", err)
	}

	if st.Marker == nil || st.Marker.PrincipalID != want {
		t.Fatalf("marker = %+v, want the marker of %q", st.Marker, want)
	}

	if st.Offer != nil {
		t.Fatalf("offer = %+v, want none after a restore", *st.Offer)
	}

	if n := faRestoreCount(t, sqlite.DB(s), "first_administrator_codes"); n != 0 {
		t.Fatalf("the code table holds %d rows after the restore, want 0", n)
	}
}

// TestRestoreFirstAdministratorMarker proves how a restore treats the marker. A
// restore with server:restore-access brings back the marker of the backup. A
// restore without it keeps the live marker. After each restore the code table
// is empty.
func TestRestoreFirstAdministratorMarker(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		apply faRestoreApply
		name  string
		want  string
	}{
		{name: "with server:restore-access", apply: faApplyAll, want: faRestoreBackupPerson},
		{name: "without server:restore-access", apply: faApplyKeepingAccess, want: faRestoreLivePerson},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s, conn := faRestoreStore(t)

			faRestoreMarker(t, conn, faRestoreBackupPerson)

			restoreFirstAdministrator(t, s, tc.apply, func() {
				faRestoreMarker(t, conn, faRestoreLivePerson)
				faRestoreCode(t, conn)
			})

			faCheckMarker(t, s, tc.want)
		})
	}
}

// TestRestoreFirstAdministratorEmptiesCode proves that no restore keeps the open
// code: after a restore of a backup with no marker, in each mode, the code
// table is empty and a redeem of the old code is refused as an invalid code.
func TestRestoreFirstAdministratorEmptiesCode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		apply faRestoreApply
		name  string
	}{
		{name: "with server:restore-access", apply: faApplyAll},
		{name: "without server:restore-access", apply: faApplyKeepingAccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s, conn := faRestoreStore(t)

			restoreFirstAdministrator(t, s, tc.apply, func() { faRestoreCode(t, conn) })

			// The restore swaps the database file, so the handle is read again.
			if n := faRestoreCount(t, sqlite.DB(s), "first_administrator_codes"); n != 0 {
				t.Fatalf("the code table holds %d rows after the restore, want 0", n)
			}

			in := firstadmin.RedeemInput{
				Now:         faRestoreRedeemAt(),
				AuthTime:    faRestoreSignInAt(),
				PrincipalID: faRestoreLivePerson,
				SessionID:   "ses-x",
				Digest:      faRestoreDigest(),
				GrantID:     "grt-after",
				Window:      time.Hour,
			}

			_, err := s.Redeem(t.Context(), in, faRestoreEntry())
			if !errors.Is(err, firstadmin.ErrCodeInvalid) {
				t.Fatalf("redeem of the old code: err = %v, want ErrCodeInvalid", err)
			}
		})
	}
}

// faRestoreEntry is the audit entry of a redeem that the store refuses before it
// writes anything.
func faRestoreEntry() audit.Entry {
	return audit.Entry{
		FenceVersion:  0,
		At:            faRestoreRedeemAt(),
		Namespace:     audit.ServerScope,
		Subject:       "host",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "caller:redeem-first-administrator",
		Object:        "@server",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
		Origin:        nil,
	}
}
