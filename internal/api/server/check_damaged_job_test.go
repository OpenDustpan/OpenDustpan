package server_test

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestBackupCheckOfAHostileDamagedJobHasCleanText is the hostile table of the
// HTTP layer of backup check. The backup holds a damaged job row whose ID or
// unit has CR, LF and ESC, or has 5000 bytes. The answer must name the job
// and hold no control character, and its text must have a bound. The test
// fails when the cleaning of the text of a damaged job is removed.
func TestBackupCheckOfAHostileDamagedJobHasCleanText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
		unit string
		want string
	}{
		{
			name: "CR, LF and ESC in the job ID", id: "job-1\r\nlevel=ERROR forged\x1b[31m", unit: "ws-1",
			want: "damaged job job-1??level=ERROR forged?[31m of unit ws-1",
		},
		{
			name: "CR, LF and ESC in the unit", id: "job-2", unit: "ws\r\n\x1b[2J-1",
			want: "damaged job job-2 of unit ws???[2J-1",
		},
		{
			name: "a unit of 5000 bytes", id: "job-3", unit: strings.Repeat("u", 5000),
			want: "damaged job job-3 of unit " + strings.Repeat("u", 160) + "...",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			held := openCheckFixture(t)
			insertHostileJob(t, held.stores, tc.id, tc.unit)

			id := startBackup(t, held.fixture.handler)
			pollBackupSucceeded(t, held.fixture.handler, id)

			status, doc := readBackupCheck(t, held.fixture, id)
			if status != http.StatusOK || doc.Complete {
				t.Fatalf("check answers %d %+v, want 200 with complete false", status, doc)
			}

			if !strings.Contains(doc.Error, tc.want) {
				t.Fatalf("check error = %q, want it to hold %q", doc.Error, tc.want)
			}

			if strings.ContainsFunc(doc.Error, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				t.Fatalf("check error = %q holds a control character", doc.Error)
			}

			if len(doc.Error) > 1024 {
				t.Fatalf("check error holds %d bytes, want a bound", len(doc.Error))
			}
		})
	}
}

// insertHostileJob makes the namespace acme and writes one job row with a
// body that does not decode, with the job ID id and the unit ID unit, through
// a second handle of the database.
func insertHostileJob(t *testing.T, stores backupStores, id, unit string) {
	t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme",
		PrimaryInstallation: "local", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := stores.registry.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	conn, err := sql.Open("sqlite", "file:"+stores.path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close the second handle: %v", closeErr)
		}
	}()

	_, err = conn.ExecContext(t.Context(), `INSERT INTO jobs (id, namespace_id, unit_id, seq, state, wait,
current_phase, current_changes, current_operation, idempotency_key, version, body, created_at_ms, updated_at_ms,
state_changed_at_ms) VALUES (?, ?, ?, 1, 'queued', '', 0, 0, '', '', 1, 'not json', 1700000000000, 1700000000000,
1700000000000)`, id, string(ns.ID), unit)
	if err != nil {
		t.Fatalf("insert the damaged job: %v", err)
	}
}
