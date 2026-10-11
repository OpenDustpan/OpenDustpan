package sqlite_test

import (
	"database/sql"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/secret/secrettest"
	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/sqlite"
	"opendustpan.org/opendustpan/store/storetest"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	storetest.Run(t, func(t *testing.T) storetest.Store {
		t.Helper()

		return open(t)
	})
}

func TestSecretsConformance(t *testing.T) {
	t.Parallel()

	secrettest.Run(t, func(t *testing.T) secrettest.Store {
		t.Helper()

		return open(t)
	})
}

func TestUpdatesConformance(t *testing.T) {
	t.Parallel()

	storetest.RunUpdates(t, func(t *testing.T) storetest.UpdatesStore {
		t.Helper()

		return open(t)
	})
}

// open returns a fresh store in a temporary directory.
func open(t *testing.T) *sqlite.Store {
	t.Helper()

	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return s
}

// TestRenameKeepsTags checks that tags follow the stack ID through a rename:
// the renamed stack keeps its tags and ID, and the old name is gone.
func TestRenameKeepsTags(t *testing.T) {
	t.Parallel()

	s := open(t)
	ctx := t.Context()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

	ns := namespace.Namespace{CreatedAt: now, Name: "acme", HomeRegion: "eu-west", FailoverVersion: 1}
	if err := s.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	ref, parseErr := stack.ParseRef("acme", "web", "prod")
	if parseErr != nil {
		t.Fatalf("parse ref: %v", parseErr)
	}

	st := stack.Stack{
		CreatedAt: now,
		UpdatedAt: now,
		Tags:      map[string]string{"team": "platform"},
		Ref:       ref,
		ID:        stack.ID(ident.NewID("stk-")),
		Version:   0,
	}

	if err := s.CreateStack(ctx, st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	next := map[string]string{"team": "e2e"}
	if err := s.UpdateStackTags(ctx, ref, next, now, ns.Fence()); err != nil {
		t.Fatalf("update tags: %v", err)
	}

	if err := s.RenameStack(ctx, ref, "renamed", ns.Fence()); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	renamed, err := stack.ParseRef("acme", "web", "renamed")
	if err != nil {
		t.Fatalf("parse renamed ref: %v", err)
	}

	got, err := s.GetStack(ctx, renamed)
	if err != nil {
		t.Fatalf("get renamed stack: %v", err)
	}

	if !maps.Equal(got.Tags, next) {
		t.Fatalf("renamed tags = %v, want %v", got.Tags, next)
	}

	if got.ID != st.ID {
		t.Fatalf("renamed ID = %q, want %q", got.ID, st.ID)
	}

	if _, err := s.GetStack(ctx, ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get old name: err = %v, want ErrNotFound", err)
	}
}

func TestMigration0003FillsIDs(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	seedAt0002(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	list, err := s.ListStacks(t.Context(), stack.Filter{Namespace: "acme", Project: ""})
	if err != nil {
		t.Fatalf("list stacks: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("migrated stacks = %d, want 2", len(list))
	}

	seen := map[stack.ID]bool{}

	for _, st := range list {
		if st.ID == "" || !strings.HasPrefix(st.ID.String(), "stk-") {
			t.Fatalf("migrated stack %s ID = %q, want stk- prefix", st.Ref, st.ID)
		}

		if seen[st.ID] {
			t.Fatalf("duplicate migrated ID %q", st.ID)
		}

		seen[st.ID] = true
	}
}

func TestMigration0005KeepsHistory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	seedAt0003WithHistory(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	checkMigratedStack(t, s, ref)
	checkMigratedUpdate(t, s, ref)
	checkMigratedHistory(t, s, ref)
	checkMigratedCheckpoint(t, s, ref)
	checkMigratedEvents(t, s, ref)
}

func checkMigratedStack(t *testing.T, s *sqlite.Store, ref stack.Ref) {
	t.Helper()

	gotStack, err := s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if gotStack.ID.String() != "stk-aaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("migrated stack ID = %q, want stk-aaaaaaaaaaaaaaaaaaaa", gotStack.ID)
	}
}

func checkMigratedUpdate(t *testing.T, s *sqlite.Store, ref stack.Ref) {
	t.Helper()

	gotUpdate, err := s.GetUpdate(t.Context(), ref, "u1")
	if err != nil {
		t.Fatalf("get update: %v", err)
	}

	if gotUpdate.Version != 1 || gotUpdate.Status != stack.StatusSucceeded {
		t.Fatalf("migrated update = version %d status %q, want 1 succeeded", gotUpdate.Version, gotUpdate.Status)
	}
}

func checkMigratedHistory(t *testing.T, s *sqlite.Store, ref stack.Ref) {
	t.Helper()

	history, err := s.ListUpdates(t.Context(), ref, stack.Page{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list updates: %v", err)
	}

	if len(history) != 1 || history[0].ID != "u1" {
		t.Fatalf("migrated history = %+v, want one update u1", history)
	}
}

func checkMigratedCheckpoint(t *testing.T, s *sqlite.Store, ref stack.Ref) {
	t.Helper()

	cp, err := s.LatestCheckpoint(t.Context(), ref)
	if err != nil {
		t.Fatalf("latest checkpoint: %v", err)
	}

	if string(cp.Deployment) != "{}" || cp.Update != "u1" {
		t.Fatalf("migrated checkpoint = deployment %q update %q, want {} u1", cp.Deployment, cp.Update)
	}
}

func checkMigratedEvents(t *testing.T, s *sqlite.Store, ref stack.Ref) {
	t.Helper()

	events, err := s.ListEvents(t.Context(), ref, "u1", -1, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("migrated events = %d, want 2", len(events))
	}
}

func TestMigration0007KeepsTags(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	seedAt0006WithTags(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	got, err := s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if got.Tags["team"] != "platform" || len(got.Tags) != 1 {
		t.Fatalf("migrated tags = %v, want map[team:platform]", got.Tags)
	}
}

// seedAt0006WithTags creates a database at migration 0006 with a stack whose
// tags still live in the stacks row.
func seedAt0006WithTags(t *testing.T, path string) {
	t.Helper()

	conn, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	for _, name := range []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
	} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.ExecContext(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		name    TEXT    NOT NULL
	) STRICT`

	if _, err := conn.ExecContext(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
	}

	for i, name := range names {
		if _, err := conn.ExecContext(t.Context(), record, i+1, name); err != nil {
			t.Fatalf("record migration: %v", err)
		}
	}

	const seedNamespace = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	if _, err := conn.ExecContext(t.Context(), seedNamespace); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	const seedStack = `INSERT INTO stacks (namespace, project, name, id, tags_json, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', 'prod', 'stk-aaaaaaaaaaaaaaaaaaaa', '{"team":"platform"}', 0, 0, 0)`

	if _, err := conn.ExecContext(t.Context(), seedStack); err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

func TestMigration0006KeepsOneRowData(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	seedAt0005WithOneRow(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	cp, err := s.LatestCheckpoint(t.Context(), ref)
	if err != nil {
		t.Fatalf("latest checkpoint: %v", err)
	}

	if string(cp.Deployment) != `{"v":1}` || cp.Update != "u1" {
		t.Fatalf("migrated checkpoint = deployment %q update %q, want {\"v\":1} u1", cp.Deployment, cp.Update)
	}

	byVersion, err := s.CheckpointByVersion(t.Context(), ref, 1)
	if err != nil {
		t.Fatalf("checkpoint of version 1: %v", err)
	}

	if string(byVersion.Deployment) != `{"v":1}` || byVersion.Update != "u1" {
		t.Fatalf(
			"migrated checkpoint of version 1 = deployment %q update %q, want {\"v\":1} u1",
			byVersion.Deployment,
			byVersion.Update,
		)
	}

	if _, err := s.CheckpointByVersion(t.Context(), ref, 2); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("checkpoint of missing version 2: err = %v, want ErrNoState", err)
	}
}

// seedAt0005WithOneRow creates a database at migration 0005 with a stack, one
// finished update and its checkpoint: the one-row-per-stack layout.
func seedAt0005WithOneRow(t *testing.T, path string) {
	t.Helper()

	conn, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	applyMigrations0005(t, conn)
	recordMigrations0005(t, conn)
	seedOneRowNamespace(t, conn)
	seedOneRowStack(t, conn)
	seedOneRowUpdate(t, conn)
	seedOneRowCheckpoint(t, conn)
}

func applyMigrations0005(t *testing.T, conn *sql.DB) {
	t.Helper()

	for _, name := range []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql",
	} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.ExecContext(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

func recordMigrations0005(t *testing.T, conn *sql.DB) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		name    TEXT    NOT NULL
	) STRICT`

	if _, err := conn.ExecContext(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql",
	}

	for i, name := range names {
		if _, err := conn.ExecContext(t.Context(), record, i+1, name); err != nil {
			t.Fatalf("record migration: %v", err)
		}
	}
}

func seedOneRowNamespace(t *testing.T, conn *sql.DB) {
	t.Helper()

	const seed = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	if _, err := conn.ExecContext(t.Context(), seed); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
}

func seedOneRowStack(t *testing.T, conn *sql.DB) {
	t.Helper()

	const seed = `INSERT INTO stacks (namespace, project, name, id, tags_json, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', 'prod', 'stk-aaaaaaaaaaaaaaaaaaaa', '{}', 1, 0, 0)`

	if _, err := conn.ExecContext(t.Context(), seed); err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

func seedOneRowUpdate(t *testing.T, conn *sql.DB) {
	t.Helper()

	const seed = `INSERT INTO updates (stack_id, id, kind, dry_run, status, version, revision,
		lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms)
		VALUES ('stk-aaaaaaaaaaaaaaaaaaaa', 'u1', 'update', 0, 'succeeded', 1, 3, X'', 0, 1000, 2000, 3000)`

	if _, err := conn.ExecContext(t.Context(), seed); err != nil {
		t.Fatalf("seed update: %v", err)
	}
}

func seedOneRowCheckpoint(t *testing.T, conn *sql.DB) {
	t.Helper()

	const seed = `INSERT INTO checkpoints (stack_id, update_id, lease_digest, schema_version, sequence,
		digest, invalid, features_json, deployment, saved_at_ms)
		VALUES ('stk-aaaaaaaaaaaaaaaaaaaa', 'u1', X'', 3, 0,
		X'0000000000000000000000000000000000000000000000000000000000000000', 0, '[]', X'7b2276223a317d', 4000)`

	if _, err := conn.ExecContext(t.Context(), seed); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
}

// seedAt0003WithHistory creates a database at migration 0003 with a stack,
// one update, its checkpoint and two events, all keyed by name.
func seedAt0003WithHistory(t *testing.T, path string) {
	t.Helper()

	conn, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	applyMigrations0003(t, conn)
	recordMigrations0003(t, conn)
	seedHistoryNamespace(t, conn)
	seedHistoryStack(t, conn)
	seedHistoryUpdate(t, conn)
	seedHistoryCheckpoint(t, conn)
	seedHistoryEvents(t, conn)
}

func applyMigrations0003(t *testing.T, conn *sql.DB) {
	t.Helper()

	for _, name := range []string{"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql"} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		_, execErr := conn.ExecContext(t.Context(), string(data))
		if execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

func recordMigrations0003(t *testing.T, conn *sql.DB) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		name    TEXT    NOT NULL
	) STRICT`

	_, err := conn.ExecContext(t.Context(), createMigrations)
	if err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`

	for _, m := range [][2]any{{1, "0001_init.sql"}, {2, "0002_updates.sql"}, {3, "0003_stack_id.sql"}} {
		_, err := conn.ExecContext(t.Context(), record, m[0], m[1])
		if err != nil {
			t.Fatalf("record migration: %v", err)
		}
	}
}

func seedHistoryNamespace(t *testing.T, conn *sql.DB) {
	t.Helper()

	const seed = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	_, err := conn.ExecContext(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
}

func seedHistoryStack(t *testing.T, conn *sql.DB) {
	t.Helper()

	const seed = `INSERT INTO stacks (namespace, project, name, id, tags_json, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', 'prod', 'stk-aaaaaaaaaaaaaaaaaaaa', '{}', 1, 0, 0)`

	_, err := conn.ExecContext(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

func seedHistoryUpdate(t *testing.T, conn *sql.DB) {
	t.Helper()

	const seed = `INSERT INTO updates (namespace, project, stack, id, kind, dry_run, status, version, revision,
		lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms)
		VALUES ('acme', 'web', 'prod', 'u1', 'update', 0, 'succeeded', 1, 3, X'', 0, 1000, 2000, 3000)`

	_, err := conn.ExecContext(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed update: %v", err)
	}
}

func seedHistoryCheckpoint(t *testing.T, conn *sql.DB) {
	t.Helper()

	const seed = `INSERT INTO checkpoints (namespace, project, stack, update_id, lease_digest, schema_version, sequence,
		digest, invalid, features_json, deployment, saved_at_ms)
		VALUES ('acme', 'web', 'prod', 'u1', X'', 3, 0,
		X'0000000000000000000000000000000000000000000000000000000000000000', 0, '[]', X'7b7d', 4000)`

	_, err := conn.ExecContext(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
}

func seedHistoryEvents(t *testing.T, conn *sql.DB) {
	t.Helper()

	const seed = `INSERT INTO update_events (namespace, project, stack, update_id, sequence, timestamp_ms, payload)
		VALUES ('acme', 'web', 'prod', 'u1', ?, ?, X'7b7d')`

	_, err := conn.ExecContext(t.Context(), seed, 0, 5000)
	if err != nil {
		t.Fatalf("seed event 0: %v", err)
	}

	_, err = conn.ExecContext(t.Context(), seed, 1, 5001)
	if err != nil {
		t.Fatalf("seed event 1: %v", err)
	}
}

// seedAt0002 creates a database at migration 0002 with two stacks and no IDs.
func seedAt0002(t *testing.T, path string) {
	t.Helper()

	conn, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	applyBaseMigrations(t, conn)
	recordBaseMigrations(t, conn)
	seedBaseStacks(t, conn)
}

func applyBaseMigrations(t *testing.T, conn *sql.DB) {
	t.Helper()

	for _, name := range []string{"0001_init.sql", "0002_updates.sql"} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.ExecContext(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

func recordBaseMigrations(t *testing.T, conn *sql.DB) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		name    TEXT    NOT NULL
	) STRICT`

	if _, err := conn.ExecContext(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`

	for _, m := range [][2]any{{1, "0001_init.sql"}, {2, "0002_updates.sql"}} {
		if _, err := conn.ExecContext(t.Context(), record, m[0], m[1]); err != nil {
			t.Fatalf("record migration: %v", err)
		}
	}
}

func seedBaseStacks(t *testing.T, conn *sql.DB) {
	t.Helper()

	_, err := conn.ExecContext(
		t.Context(),
		`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms) VALUES ('acme', 'eu-west', 1, 0)`,
	)
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	const seedStack = `INSERT INTO stacks
		(namespace, project, name, tags_json, version, created_at_ms, updated_at_ms)
		VALUES ('acme', ?, ?, '{}', 0, 0, 0)`

	for _, s := range [][2]string{{"web", "prod"}, {"web", "dev"}} {
		if _, err := conn.ExecContext(t.Context(), seedStack, s[0], s[1]); err != nil {
			t.Fatalf("seed stack %s: %v", s[1], err)
		}
	}
}
