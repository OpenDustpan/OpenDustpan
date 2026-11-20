package postgres

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/store/outputsharestore"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// TestRuleInsertMapsATableRefusalToErrInvalid proves that a refusal of a CHECK
// or of the foreign key of the rule table reaches the caller as
// outputshare.ErrInvalid, and never as a raw driver error. The rows go to the
// insert with no Go check before it, as a new caller would send them.
func TestRuleInsertMapsATableRefusalToErrInvalid(t *testing.T) {
	t.Parallel()

	seed := newPlacementSeed(t)
	good := outputsharestore.Insert{
		Namespace: seed.alpha.Name.String(), ID: "rule-001", ProducerKind: "namespace", ProducerID: "",
		ConsumerKind: "unit", ConsumerNamespace: "globex", ConsumerID: "", ConsumerTool: "opentofu",
		ConsumerName: "web", CreatedBy: "tester", CreatedAtMs: 1,
	}

	noCreator := good
	noCreator.ID, noCreator.CreatedBy = "rule-002", ""

	noNamespace := good
	noNamespace.ID, noNamespace.Namespace = "rule-003", "ghost"

	tx := ruleTx{q: seed.s.queries}

	for name, row := range map[string]outputsharestore.Insert{"a CHECK": noCreator, "the foreign key": noNamespace} {
		if _, err := tx.Insert(t.Context(), row); !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("insert refused by %s = %v, want outputshare.ErrInvalid", name, err)
		}
	}

	if n, err := tx.Insert(t.Context(), good); err != nil || n != 1 {
		t.Fatalf("insert of the good row = %d, %v, want 1 row", n, err)
	}
}
