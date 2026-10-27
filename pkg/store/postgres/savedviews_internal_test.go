package postgres

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
)

const testOwner = "prn-aaaaaaaaaaaaaaaaaaaa"

// seedSavedView makes the namespace acme and one valid view in it.
func seedSavedView(t *testing.T, s *Store) {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	v := savedview.View{
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), ID: savedview.NewID(), Namespace: "acme", Name: "Payments",
		Owner: testOwner,
		Query: savedview.Query{
			Labels: []savedview.Label{{Key: "team", Value: "payments"}}, References: []string{"pulumi:acme/web/prod"},
		},
	}

	if err := s.SavedViews().Create(t.Context(), v, ns.Fence(), viewEntry()); err != nil {
		t.Fatalf("create view: %v", err)
	}
}

// TestCheckStagedSavedViews proves the restore check: it accepts the rows that
// the store wrote, and it refuses each row that a new view would not pass. A
// row that fails is also not served by a read.
func TestCheckStagedSavedViews(t *testing.T) {
	t.Parallel()

	t.Run("a row that the store wrote passes", func(t *testing.T) {
		t.Parallel()

		s := openForSavedViews(t)
		seedSavedView(t, s)

		if err := checkStagedSavedViews(t.Context(), s.pool, currentSchema(t, s)); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for _, tc := range tamperedRows() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkTamperedRow(t, tc.update)
		})
	}
}

type tamperedRow struct {
	name   string
	update string
}

// tamperedRows lists the changes that make a stored row fail the rules of a
// new view.
func tamperedRows() []tamperedRow {
	good := `{"labels":[{"key":"team","value":"payments"}],"references":["pulumi:acme/web/prod"]}`

	return []tamperedRow{
		{"owner is a subject", `UPDATE saved_views SET owner = 'alice'`},
		{"empty owner", `UPDATE saved_views SET owner = ''`},
		{"owner with a pattern", `UPDATE saved_views SET owner = 'prn-%'`},
		{"empty name", `UPDATE saved_views SET name = ''`},
		{"name with a space at the start", `UPDATE saved_views SET name = ' Payments'`},
		{"name with a hidden character", `UPDATE saved_views SET name = 'Pay' || chr(8203) || 'ments'`},
		{"name over 128 characters", `UPDATE saved_views SET name = repeat('a', 129)`},
		{"ID that is not a view ID", `UPDATE saved_views SET id = 'view-0'`},
		{"query that is not JSON", `UPDATE saved_views SET query_json = 'not json'`},
		{"empty query text", `UPDATE saved_views SET query_json = ''`},
		{
			"query with an unknown field",
			`UPDATE saved_views SET query_json = '{"labels":[],"references":[],"sort":"name"}'`,
		},
		{"text after the query", `UPDATE saved_views SET query_json = '` + good + ` {}'`},
		{
			"label key with a space",
			`UPDATE saved_views SET query_json = '{"labels":[{"key":"a b","value":"c"}],"references":[]}'`,
		},
		{
			"reference of another namespace",
			`UPDATE saved_views SET query_json = '{"labels":[],"references":["pulumi:other/web/prod"]}'`,
		},
		{"reference that is text", `UPDATE saved_views SET query_json = '{"labels":[],"references":["nonsense"]}'`},
	}
}

// checkTamperedRow changes the only row with update, and checks that the
// restore check refuses it and that a read does not serve it.
func checkTamperedRow(t *testing.T, update string) {
	t.Helper()

	s := openForSavedViews(t)
	seedSavedView(t, s)

	if _, err := s.pool.Exec(t.Context(), update); err != nil {
		t.Fatalf("change the row: %v", err)
	}

	err := checkStagedSavedViews(t.Context(), s.pool, currentSchema(t, s))
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	// The read path checks the row again, so a bad row is not served. The row
	// may have a new owner and a new ID, so read as the stored ones.
	var owner, id string

	if err = s.pool.QueryRow(t.Context(), `SELECT owner, id FROM saved_views`).Scan(&owner, &id); err != nil {
		t.Fatalf("read the row: %v", err)
	}

	_, err = s.SavedViews().Get(t.Context(), "acme", savedview.ID(id), owner)
	if !errors.Is(err, savedview.ErrNotFound) {
		t.Fatalf("get of a bad row = %v, want ErrNotFound", err)
	}

	views, err := s.SavedViews().List(t.Context(), "acme", owner)
	if err != nil || len(views) != 0 {
		t.Fatalf("list of a bad row = %v, %v; want none", views, err)
	}
}

// TestCheckStagedSavedViewsRefusesAMissingNamespace proves that a view whose
// namespace the backup does not hold is corrupt. An inner join would skip such
// a row, and the restore would bring it in.
func TestCheckStagedSavedViewsRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	seedSavedView(t, s)

	// The foreign key refuses this change, so the test drops the key in its
	// own schema first.
	for _, stmt := range []string{
		`ALTER TABLE saved_views DROP CONSTRAINT saved_views_namespace_id_fkey`,
		`UPDATE saved_views SET namespace_id = 'ns-gone'`,
	} {
		if _, execErr := s.pool.Exec(t.Context(), stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}

	err := checkStagedSavedViews(t.Context(), s.pool, currentSchema(t, s))
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(fmt.Sprint(err), "has no namespace") {
		t.Fatalf("check = %v, want the refusal of a view with no namespace", err)
	}
}

// viewEntry returns a valid audit entry for the create of a view in acme.
func viewEntry() audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1), Namespace: "acme", Subject: "ana", IDPSubject: "", TokenID: "", Action: "view:create",
		Object: "acme", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "",
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

func openForSavedViews(t *testing.T) *Store {
	t.Helper()

	s, err := Open(t.Context(), membershipURL(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

func currentSchema(t *testing.T, s *Store) string {
	t.Helper()

	var name string
	if err := s.pool.QueryRow(t.Context(), `SELECT current_schema()`).Scan(&name); err != nil {
		t.Fatalf("read the schema: %v", err)
	}

	return name
}
