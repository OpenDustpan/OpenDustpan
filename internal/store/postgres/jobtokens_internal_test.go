package postgres

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
)

// issueTestURL names the variable that holds the PostgreSQL URL of the
// issue tests of the job token store. The tests skip when it is empty.
const issueTestURL = "DUSTPAN_TEST_POSTGRES_URL"

// issueURL returns the URL of the test database, or skips the test when the
// environment names no database.
func issueURL(t *testing.T) string {
	t.Helper()

	base := os.Getenv(issueTestURL)
	if base == "" {
		t.Skipf("set %s to run the job token store test", issueTestURL)
	}

	return base
}

// openJobTokenInternalStore opens a store in a migrated schema of a database
// of its own, cloned from the template database.
func openJobTokenInternalStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(t.Context(), pgtest.MigratedURL(t, issueURL(t)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

// newIssueToken returns a valid token of the issue-world table. The time of
// the table is at, and the token expires after it.
func newIssueToken(t *testing.T, at time.Time) jobtoken.Token {
	t.Helper()

	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	unit, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatalf("unit reference: %v", err)
	}

	var tok jobtoken.Token

	tok.Digest = jobtoken.DigestOf(secret)
	tok.StarterID = "prn-starter"
	tok.JobID = "job-issue"
	tok.Namespace = "acme"
	tok.Unit = unit.WithUnitID("unt-issue")
	tok.ClaimOperation = "op-issue"
	tok.ClaimToken = 1
	tok.ExpiresAt = at.Add(10 * time.Minute)
	tok.CreatedAt = at

	if err := tok.Validate(); err != nil {
		t.Fatalf("the token of the table is not valid: %v", err)
	}

	return tok
}

// newIssueWorld returns an issue world whose starter, claim and job are live,
// so that every check of checkIssueLive before the namespace guards passes.
// The namespace fields hold namespaceID, unitNamespaceID and jobNamespaceID.
func newIssueWorld(tok jobtoken.Token, namespaceID, unitNamespaceID, jobNamespaceID string) issueWorld {
	var starter db.GetPrincipalRow

	starter.ID = tok.StarterID

	var claim db.DurableOperation

	claim.State = string(durable.StateRunning)
	claim.ClaimToken = tok.ClaimToken
	claim.ClaimExpiresAtMs = pgtype.Int8{Int64: tok.ExpiresAt.UnixMilli(), Valid: true}

	var jobRow db.GetJobRow

	jobRow.ID = string(tok.JobID)
	jobRow.NamespaceID = jobNamespaceID
	jobRow.State = string(job.StateRunning)
	jobRow.CurrentOperation = string(tok.ClaimOperation)
	jobRow.UnitID = tok.Unit.UnitID()

	return issueWorld{
		job:             jobRow,
		claim:           claim,
		starter:         starter,
		jobStarterID:    tok.StarterID,
		namespaceID:     namespaceID,
		unitNamespaceID: unitNamespaceID,
	}
}

// TestCheckIssueLiveRefusesEmptyAndForeignNamespaceIDs sends issue worlds
// straight to checkIssueLive, with the starter, the claim and the job live,
// so that only the namespace guards decide. Each hostile case wants
// ErrNotLive. The last case proves that the world is otherwise valid: it
// passes.
func TestCheckIssueLiveRefusesEmptyAndForeignNamespaceIDs(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	tok := newIssueToken(t, at)

	for _, tc := range []struct {
		name          string
		namespaceID   string
		unitNamespace string
		jobNamespace  string
		refused       bool
	}{
		{
			"an empty namespace ID on both sides of the unit comparison, with a live job namespace ID",
			"", "", "ns-acme", true,
		},
		{
			"an empty namespace ID on both sides of the unit comparison, with an empty job namespace ID",
			"", "", "", true,
		},
		{
			"an empty namespace ID on both sides of the job comparison, with a live unit namespace ID",
			"", "ns-acme", "", true,
		},
		{
			"an empty namespace ID with a live unit row and a live job in one namespace",
			"", "ns-acme", "ns-acme", true,
		},
		{
			"a live namespace ID and a live job with no live unit row",
			"ns-acme", "", "ns-acme", true,
		},
		{"a unit row in another namespace", "ns-acme", "ns-globex", "ns-acme", true},
		{"a job in another namespace", "ns-acme", "ns-acme", "ns-globex", true},
		{"a live unit row and a live job in the namespace of the token", "ns-acme", "ns-acme", "ns-acme", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newIssueWorld(tok, tc.namespaceID, tc.unitNamespace, tc.jobNamespace)
			err := checkIssueLive(tok, &w, at)

			if tc.refused {
				if !errors.Is(err, jobtoken.ErrNotLive) {
					t.Fatalf("%s: checkIssueLive = %v, want ErrNotLive", tc.name, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("%s: checkIssueLive = %v, want nil", tc.name, err)
			}
		})
	}
}

// TestLoadIssueNamespacesRefusesAMissingNamespaceRow proves that the read
// alone refuses a token whose namespace has no row: the refusal is
// ErrNotLive and holds no error of the driver.
func TestLoadIssueNamespacesRefusesAMissingNamespaceRow(t *testing.T) {
	t.Parallel()

	s := openJobTokenInternalStore(t)
	at := time.UnixMilli(1_700_000_000_000).UTC()
	tok := newIssueToken(t, at)

	var w issueWorld

	err := loadIssueNamespaces(t.Context(), s.queries, tok, &w)
	if !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("loadIssueNamespaces with a namespace that has no row = %v, want ErrNotLive", err)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("loadIssueNamespaces returned the error of the driver: %v", err)
	}
}

// TestLoadIssueNamespacesToleratesAMissingUnitRow proves that the read alone
// gives no error for a token whose unit ID names no live unit row: the read
// leaves the unit namespace ID empty, and the world of that read refuses the
// issue with ErrNotLive and no error of the driver.
func TestLoadIssueNamespacesToleratesAMissingUnitRow(t *testing.T) {
	t.Parallel()

	s := openJobTokenInternalStore(t)
	at := time.UnixMilli(1_700_000_000_000).UTC()
	tok := newIssueToken(t, at)

	ns := namespace.Namespace{
		ID:                  "ns-acme",
		CreatedAt:           at,
		Name:                tok.Namespace,
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %s: %v", tok.Namespace, err)
	}

	// The world holds a live job in the namespace of the row, so that only
	// the unit guard decides. loadIssueNamespaces writes the two namespace
	// fields of the world, as the issue path does.
	w := newIssueWorld(tok, "", "", "ns-acme")

	if err := loadIssueNamespaces(t.Context(), s.queries, tok, &w); err != nil {
		t.Fatalf("loadIssueNamespaces with a unit ID that has no row = %v, want nil", err)
	}

	if w.namespaceID != "ns-acme" {
		t.Fatalf("loaded namespace ID = %q, want %q", w.namespaceID, "ns-acme")
	}

	if w.unitNamespaceID != "" {
		t.Fatalf("loaded unit namespace ID = %q, want empty", w.unitNamespaceID)
	}

	err := checkIssueLive(tok, &w, at)
	if !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("checkIssueLive with no live unit row = %v, want ErrNotLive", err)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("checkIssueLive returned the error of the driver: %v", err)
	}
}
