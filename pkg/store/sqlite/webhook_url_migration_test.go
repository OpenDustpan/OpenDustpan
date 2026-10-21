package sqlite_test

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// webhookURLVersion is the version of the migration that moves the URL of a
// webhook sink out of its target.
const webhookURLVersion = 133

// TestWebhookURLMigration applies the migrations before webhook URLs became
// secret items, plants a webhook sink with a plain text URL as its target,
// then opens the store, which applies the rest. No table may hold the URL
// after that, and the sink fails closed until a person sets the URL again.
func TestWebhookURLMigration(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	before := slices.DeleteFunc(all, func(m migrate.Migration) bool { return m.Version >= webhookURLVersion })
	path := filepath.Join(t.TempDir(), "webhook.db")

	applyTagMigrations(ctx, t, path, before)

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	plantLegacyWebhook(t, conn)

	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}

	s, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	ns, err := s.GetNamespace(ctx, "shop")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	notifytest.CheckMigratedWebhook(t, s.NotificationSinks(), ns.Fence())
	checkNoPlainURL(t, path)
}

// plantLegacyWebhook inserts the namespace shop and a webhook sink in the
// shape before the migration: the whole URL as the target, and the two
// sealed signing keys.
func plantLegacyWebhook(t *testing.T, conn *sql.DB) {
	t.Helper()

	ctx := t.Context()
	nsID := namespace.NewID().String()

	stmts := []struct {
		query string
		args  []any
	}{
		{
			`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
				VALUES ('shop', 'eu-west', 1, 0, ?)`,
			[]any{nsID},
		},
		{
			`INSERT INTO notification_sinks (id, namespace_id, kind, name, platform, target, created_by, version)
				VALUES (?, ?, 'webhook', 'Old hook', '', ?, 'alice', 1)`,
			[]any{notifytest.LegacyWebhookID, nsID, notifytest.LegacyWebhookURL()},
		},
	}

	other := namespace.NewID().String()
	userinfo := "https://" + strings.Join([]string{"user", "pw"}, ":") + "@hooks.example.com/x"

	stmts = append(stmts, []struct {
		query string
		args  []any
	}{
		{
			`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
				VALUES ('other', 'eu-west', 1, 0, ?)`,
			[]any{other},
		},
		{
			`INSERT INTO notification_sinks (id, namespace_id, kind, name, platform, target, created_by, version)
				VALUES ('snk-userinfo', ?, 'webhook', 'Odd hook', '', ?, 'alice', 1)`,
			[]any{other, userinfo},
		},
	}...)

	for _, st := range stmts {
		if _, err := conn.ExecContext(ctx, st.query, st.args...); err != nil {
			t.Fatalf("plant legacy rows: %v", err)
		}
	}

	refs := notifytest.LegacyKeyRefs()

	for i, item := range notifytest.LegacyKeyItems(ctx, "shop") {
		_, err := conn.ExecContext(ctx,
			`INSERT INTO notification_sink_secrets (namespace_id, ref, sink_id, position, ciphertext)
				VALUES (?, ?, ?, ?, ?)`, nsID, refs[i], notifytest.LegacyWebhookID, i, item.Ciphertext())
		if err != nil {
			t.Fatalf("plant legacy secret: %v", err)
		}

		if _, err = conn.ExecContext(ctx,
			`INSERT INTO notification_sink_refs (namespace_id, ref) VALUES (?, ?)`, nsID, refs[i]); err != nil {
			t.Fatalf("plant legacy ref: %v", err)
		}
	}
}

// checkNoPlainURL fails if a text column of a sink table still holds a part of
// the URL that the legacy sink had.
func checkNoPlainURL(t *testing.T, path string) {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	var count int

	row := conn.QueryRowContext(t.Context(),
		`SELECT count(*) FROM notification_sinks WHERE instr(target, ?) > 0 OR instr(target, '/') > 0 OR instr(name, ?) > 0`,
		notifytest.LegacyToken(), notifytest.LegacyToken())
	if err = row.Scan(&count); err != nil || count != 0 {
		t.Fatalf("sinks that hold the URL after the migration = %d (%v), want 0", count, err)
	}

	// A target with user information never passed the egress rules, but the
	// migration still scrubs it: the target becomes empty.
	var target string

	row = conn.QueryRowContext(t.Context(), `SELECT target FROM notification_sinks WHERE id = 'snk-userinfo'`)
	if err = row.Scan(&target); err != nil || target != "" {
		t.Fatalf("target of the sink with user information = %q (%v), want empty", target, err)
	}
}
