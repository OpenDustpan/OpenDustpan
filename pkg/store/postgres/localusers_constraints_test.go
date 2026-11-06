package postgres_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/localuserstore"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// TestLocalUserConstraints is the last layer of the local user table: a row
// that skips every check of the code still fails at the database.
func TestLocalUserConstraints(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL tests", envURL)
	}

	schemaURL := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	at := time.UnixMilli(1_700_000_000_000).UTC()
	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-1", Subject: "local:lus-1",
		DisplayName: "Alice", Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}

	e := audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "server:manage-principals", Object: principal.Ref(p.ID), Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	if createErr := s.CreatePrincipal(t.Context(), p, e); createErr != nil {
		t.Fatalf("create principal: %v", createErr)
	}

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	insert := func(r localuserstore.HostileRow) error {
		_, execErr := conn.Exec(t.Context(), `INSERT INTO local_users
			(id, principal_id, user_name, password_hash, password_set_at_ms, change_required, demo, created_by, created_at_ms)
			VALUES ('lus-1', 'prn-1', $1, $2, $3, $4, false, 'admin', 1)`,
			r.UserName, r.PasswordHash, r.SetAtMs, r.Change)

		if execErr != nil {
			return fmt.Errorf("insert local user row: %w", execErr)
		}

		return nil
	}

	for _, r := range localuserstore.HostileRows() {
		if err := insert(r); err == nil {
			t.Errorf("insert %s: err = nil, want a constraint error", r.Name)
		}
	}

	if err := insert(localuserstore.ValidRow()); err != nil {
		t.Fatalf("insert a valid row: %v", err)
	}
}
