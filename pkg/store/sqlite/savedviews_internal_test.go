package sqlite

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
)

const testOwner = "prn-aaaaaaaaaaaaaaaaaaaa"

// seedSavedView makes the namespace acme and one valid view in it.
func seedSavedView(t *testing.T, s *Store) savedview.View {
	t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme", HomeRegion: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
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

	e := audit.Entry{
		At: time.UnixMilli(1), Namespace: "acme", Subject: "ana", Action: "view:create", Object: "acme",
		SourceIP: "192.0.2.1", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
	}

	if err := s.SavedViews().Create(t.Context(), v, ns.Fence(), e); err != nil {
		t.Fatalf("create view: %v", err)
	}

	return v
}

// TestCheckStagedSavedViews proves the restore check: it accepts the rows that
// the store wrote, and it refuses each row that a new view would not pass. A
// row that fails is also not served by a read.
func TestCheckStagedSavedViews(t *testing.T) {
	t.Parallel()

	good := `{"labels":[{"key":"team","value":"payments"}],"references":["pulumi:acme/web/prod"]}`

	tests := []struct {
		name   string
		update string
	}{
		{"owner is a subject", `UPDATE saved_views SET owner = 'alice'`},
		{"empty owner", `UPDATE saved_views SET owner = ''`},
		{"owner with a pattern", `UPDATE saved_views SET owner = 'prn-%'`},
		{"empty name", `UPDATE saved_views SET name = ''`},
		{"name with a space at the start", `UPDATE saved_views SET name = ' Payments'`},
		{"name with a hidden character", "UPDATE saved_views SET name = 'Pay' || char(8203) || 'ments'"},
		{"name over 128 characters", `UPDATE saved_views SET name = printf('%0129d', 0)`},
		{"ID that is not a view ID", `UPDATE saved_views SET id = 'view-0'`},
		{"query that is not JSON", `UPDATE saved_views SET query_json = 'not json'`},
		{"empty query text", `UPDATE saved_views SET query_json = ''`},
		{"query with an unknown field", `UPDATE saved_views SET query_json = '{"labels":[],"references":[],"sort":"name"}'`},
		{"text after the query", `UPDATE saved_views SET query_json = '` + good + ` {}'`},
		{"label key with a space", `UPDATE saved_views SET query_json = '{"labels":[{"key":"a b","value":"c"}],"references":[]}'`},
		{"reference of another namespace", `UPDATE saved_views SET query_json = '{"labels":[],"references":["pulumi:other/web/prod"]}'`},
		{"reference that is text", `UPDATE saved_views SET query_json = '{"labels":[],"references":["nonsense"]}'`},
	}

	t.Run("a row that the store wrote passes", func(t *testing.T) {
		t.Parallel()

		s := openInternalTemplated(t)
		seedSavedView(t, s)

		if err := checkStagedSavedViews(t.Context(), s.conn); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			v := seedSavedView(t, s)

			if _, err := s.conn.ExecContext(t.Context(), tc.update); err != nil {
				t.Fatalf("change the row: %v", err)
			}

			if err := checkStagedSavedViews(t.Context(), s.conn); !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}

			// The read path checks the row again, so a bad row is not served.
			// The owner of a row may have changed, so read as the stored owner.
			var owner string
			if err := s.conn.QueryRowContext(t.Context(), `SELECT owner FROM saved_views`).Scan(&owner); err != nil {
				t.Fatalf("read the owner: %v", err)
			}

			var id string
			if err := s.conn.QueryRowContext(t.Context(), `SELECT id FROM saved_views`).Scan(&id); err != nil {
				t.Fatalf("read the ID: %v", err)
			}

			if _, err := s.SavedViews().Get(t.Context(), "acme", savedview.ID(id), owner); !errors.Is(err, savedview.ErrNotFound) {
				t.Fatalf("get of a bad row = %v, want ErrNotFound", err)
			}

			if views, err := s.SavedViews().List(t.Context(), "acme", owner); err != nil || len(views) != 0 {
				t.Fatalf("list of a bad row = %v, %v; want none", views, err)
			}

			_ = v
		})
	}
}
