package sqlite

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/outputshare/outputsharetest"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestPortableBackupKeepsSharingRules proves that the export writes the rule
// table and that the stage of the export loads the rows back under the CHECK
// constraints of the table.
func TestPortableBackupKeepsSharingRules(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	seed := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if createErr := s.CreateNamespace(t.Context(), seed); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	rule := outputshare.Rule{
		CreatedAt: time.Time{},
		ID:        "rule-001",
		CreatedBy: "tester",
		Producer:  mustUnit(t, "acme", "a").WithUnitID("ws-a"),
		Consumer:  outputshare.Consumer{Kind: outputshare.ConsumerUnit, Namespace: "globex", ID: "ws-web"},
	}

	_, err = s.conn.ExecContext(t.Context(), outputsharetest.RawRuleInsertSQLite(), outputsharetest.RawRuleRow(rule)...)
	if err != nil {
		t.Fatalf("insert the rule: %v", err)
	}

	sink := &keepMemSink{files: map[string]*bytes.Buffer{}}

	sum, files, err := s.ExportPortable(t.Context(), sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	var rows int64

	for _, f := range files {
		if f.Table == backup.TableOutputSharingRules {
			rows = f.Rows
		}
	}

	if rows != 1 {
		t.Fatalf("the export holds %d sharing rules, want 1", rows)
	}

	if _, err = s.StagePortable(t.Context(), sink.read, sum, files, storetest.StageAs(t, s)); err != nil {
		t.Fatalf("stage portable: %v", err)
	}
}

// mustUnit returns the unit reference of the OpenTofu tool.
func mustUnit(t *testing.T, ns namespace.Name, name string) identity.Object {
	t.Helper()

	o, err := identity.UnitObject("opentofu", ns, name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return o
}
