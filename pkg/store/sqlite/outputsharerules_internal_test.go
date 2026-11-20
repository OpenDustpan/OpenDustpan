package sqlite

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/store/internal/outputsharestore"
)

// TestRuleInsertMapsATableRefusalToErrInvalid proves that a refusal of a CHECK
// or of the foreign key of the rule table reaches the caller as
// outputshare.ErrInvalid, and never as a raw driver error. The rows go to the
// insert with no Go check before it, as a new caller would send them.
func TestRuleInsertMapsATableRefusalToErrInvalid(t *testing.T) {
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
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme",
		PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err = s.CreateNamespace(t.Context(), seed); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	good := outputsharestore.Insert{
		Namespace: "acme", ID: "rule-001", ProducerKind: "namespace", ProducerID: "",
		ConsumerKind: "unit", ConsumerNamespace: "globex", ConsumerID: "", ConsumerTool: "opentofu",
		ConsumerName: "web", CreatedBy: "tester", CreatedAtMs: 1,
	}

	noCreator := good
	noCreator.ID, noCreator.CreatedBy = "rule-002", ""

	noNamespace := good
	noNamespace.ID, noNamespace.Namespace = "rule-003", "ghost"

	tx := ruleTx{q: s.queries}

	for name, row := range map[string]outputsharestore.Insert{"a CHECK": noCreator, "the foreign key": noNamespace} {
		if _, insertErr := tx.Insert(t.Context(), row); !errors.Is(insertErr, outputshare.ErrInvalid) {
			t.Fatalf("insert refused by %s = %v, want outputshare.ErrInvalid", name, insertErr)
		}
	}

	if n, insertErr := tx.Insert(t.Context(), good); insertErr != nil || n != 1 {
		t.Fatalf("insert of the good row = %d, %v, want 1 row", n, insertErr)
	}
}
