package postgres

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// placementRuleInsert is the raw insert of one placement rule row. Only the
// columns that differ between the rows of these tests stay placeholders.
const placementRuleInsert = `INSERT INTO placement_rules
	(id, namespace, "position", tool, project, stack, collection_id, created_by, created_at_ms)
	VALUES ($1, $2, 1, 'opentofu', 'web', 'web', $3, 'admin', $4)`

// TestPlacementRuleRawRowIsTiedToTheNamespaceOfItsCollection checks that the
// schema alone refuses a raw placement_rules row whose namespace differs from
// the namespace of the collection it names. A restored backup can hold such a
// row, and only this constraint stops it when every Go layer is missing. One
// valid row passes first, to prove that the refused row differs only in its
// namespace.
func TestPlacementRuleRawRowIsTiedToTheNamespaceOfItsCollection(t *testing.T) {
	t.Parallel()

	seed := newPlacementSeed(t)

	_, err := seed.s.pool.Exec(t.Context(), placementRuleInsert,
		ident.NewID("plr-"), seed.beta.Name.String(), seed.colID, 1_700_000_000_000,
	)
	if err != nil {
		t.Fatalf("insert a rule of beta that names the collection of beta: %v", err)
	}

	_, err = seed.s.pool.Exec(t.Context(), placementRuleInsert,
		ident.NewID("plr-"), seed.alpha.Name.String(), seed.colID, 1_700_000_000_001,
	)
	if !isForeignKeyViolation(err) {
		t.Fatalf("insert a rule of alpha that names the collection of beta = %v, want a foreign key error", err)
	}
}

// placementSeed holds the store of the placement rule tests with its two
// namespaces and the collection of beta.
type placementSeed struct {
	s     *Store
	alpha namespace.Namespace
	beta  namespace.Namespace
	colID string
}

// newPlacementSeed returns a store in a fresh schema, with the namespaces
// alpha and beta and the raw collection row of beta. The collection is
// written raw, because these tests prove the schema alone, with no Go layer
// in the way.
func newPlacementSeed(t *testing.T) placementSeed {
	t.Helper()

	ctx := t.Context()
	at := time.UnixMilli(1_700_000_000_000).UTC()

	s := openKeepLockStore(t, testPostgresURL(t))

	seed := placementSeed{
		s:     s,
		alpha: placementNamespace(t, s, "alpha", at),
		beta:  placementNamespace(t, s, "beta", at),
		colID: ident.NewID("col-"),
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO collections (id, namespace, name_key, name, created_at_ms)
		VALUES ($1, $2, 'beta-one', 'beta-one', $3)`,
		seed.colID, seed.beta.Name.String(), at.UnixMilli(),
	)
	if err != nil {
		t.Fatalf("insert the collection of beta: %v", err)
	}

	return seed
}

// placementNamespace creates the namespace name in s and returns it.
func placementNamespace(
	t *testing.T, s *Store, name string, at time.Time,
) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: at, Name: namespace.Name(name),
		PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}

	return ns
}
