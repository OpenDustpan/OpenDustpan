package postgres

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// TestRunnerAppliesAFixtureChain proves that the numbered-migration runner
// works while the binary embeds the baseline alone: it applies two fixture
// migrations after the baseline in order, records the SHA-256 of each, applies
// nothing on a second run, and refuses an edit of an applied migration.
func TestRunnerAppliesAFixtureChain(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := openAt(t, membershipURL(t), "dev")

	base, err := migrate.Load(migrations, "migrations")
	if err != nil {
		t.Fatalf("load the baseline: %v", err)
	}

	chain, err := migrate.Load(os.DirFS("testdata"), "runner-chain")
	if err != nil || len(chain) != 2 {
		t.Fatalf("load the fixture chain = %d migrations, %v; want 2", len(chain), err)
	}

	all := slices.Concat(base, chain)
	rules := migrateRules{binaryVersion: "dev", kind: "", noMigrate: false}

	run := func(migrations []migrate.Migration) error {
		return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			return migrateTx(ctx, tx, migrations, rules)
		})
	}

	if err := run(all); err != nil {
		t.Fatalf("apply the fixture chain: %v", err)
	}

	var recorded []schema.AppliedMigration

	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var readErr error

		recorded, readErr = recordedMigrations(ctx, tx)

		return readErr
	})
	if err != nil || len(recorded) != len(all) {
		t.Fatalf("recorded %d migrations, %v; want %d", len(recorded), err, len(all))
	}

	for i, m := range all {
		if recorded[i].Version != m.Version || recorded[i].Name != m.Name || recorded[i].SHA256 != m.SHA256 {
			t.Fatalf("recorded %+v, want version %d, name %s and SHA-256 %s", recorded[i], m.Version, m.Name, m.SHA256)
		}
	}

	var note string
	if err := s.pool.QueryRow(ctx, `SELECT note FROM runner_fixture WHERE id = 1`).Scan(&note); err != nil {
		t.Fatalf("read the fixture row: %v", err)
	}

	if note != "applied" {
		t.Fatalf("fixture row = %q, want applied", note)
	}

	if err := run(all); err != nil {
		t.Fatalf("second run of the fixture chain: %v", err)
	}

	edited := slices.Clone(all)
	last := len(edited) - 1
	edited[last].SQL += "\n-- An edit of an applied migration.\n"
	edited[last].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"

	if err := run(edited); !errors.Is(err, schema.ErrSchemaDiverged) {
		t.Fatalf("run with an edited applied migration = %v, want %v", err, schema.ErrSchemaDiverged)
	}
}
