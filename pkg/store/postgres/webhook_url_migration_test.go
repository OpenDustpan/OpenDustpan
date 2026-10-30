package postgres_test

import (
	"net/url"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
)

// webhookURLVersion is the version of the migration that moves the URL of a
// webhook sink out of its target.
const webhookURLVersion = 133

// TestWebhookURLMigration applies the migrations before webhook URLs became
// secret items, plants a webhook sink with a plain text URL as its target,
// then opens the store, which applies the rest. The migration of the scoped
// sink tables makes the sink tables again with no row, because no tag shipped
// the old shape. So no sink is left, and no table may hold the URL.
func TestWebhookURLMigration(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL webhook URL migration test", envURL)
	}

	ctx := t.Context()

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	before := slices.DeleteFunc(all, func(m migrate.Migration) bool { return m.Version >= webhookURLVersion })
	schemaURL := emptySchemaURL(t, base)

	// The migrations after 0103 name tables of the control schema without a
	// schema name, so the search path of the setup holds both schemas.
	applyTagMigrations(ctx, t, withControlSchema(t, schemaURL), before)
	plantLegacyWebhook(t, schemaURL)

	s := openURL(t, schemaURL)

	ns, err := s.GetNamespace(ctx, "shop")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	if got, sinksErr := s.NotificationSinks().Sinks(ctx, ns.Name); sinksErr != nil || len(got) != 0 {
		t.Fatalf("Sinks after the scoped tables = %v (%v), want none", got, sinksErr)
	}

	checkNoPlainURL(t, schemaURL)
}

// withControlSchema returns schemaURL with the control schema of its live
// schema added to the search path, as the store sets it.
func withControlSchema(t *testing.T, schemaURL string) string {
	t.Helper()

	u, err := url.Parse(schemaURL)
	if err != nil {
		t.Fatalf("parse schema URL: %v", err)
	}

	q := u.Query()
	live := q.Get("search_path")
	q.Set("search_path", live+","+live+"_control")
	u.RawQuery = q.Encode()

	return u.String()
}

// plantLegacyWebhook inserts the namespace shop and a webhook sink in the
// shape before the migration: the whole URL as the target, and the two
// sealed signing keys.
func plantLegacyWebhook(t *testing.T, schemaURL string) {
	t.Helper()

	ctx := t.Context()

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	nsID, other := namespace.NewID().String(), namespace.NewID().String()
	userinfo := "https://" + "user:" + "pw" + "@hooks.example.com/x"

	stmts := make([]struct {
		query string
		args  []any
	}, 0, 4+len(notifytest.LegacyShapes()))

	stmts = append(stmts, []struct {
		query string
		args  []any
	}{
		{
			`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
				VALUES ('shop', 'eu-west', 1, 0, $1)`,
			[]any{nsID},
		},
		{
			`INSERT INTO notification_sinks (id, namespace_id, kind, name, platform, target, created_by, version)
				VALUES ($1, $2, 'webhook', 'Old hook', '', $3, 'alice', 1)`,
			[]any{notifytest.LegacyWebhookID, nsID, notifytest.LegacyWebhookURL()},
		},
		{
			`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
				VALUES ('other', 'eu-west', 1, 0, $1)`,
			[]any{other},
		},
		{
			`INSERT INTO notification_sinks (id, namespace_id, kind, name, platform, target, created_by, version)
				VALUES ('snk-userinfo', $1, 'webhook', 'Odd hook', '', $2, 'alice', 1)`,
			[]any{other, userinfo},
		},
	}...)

	for _, sh := range notifytest.LegacyShapes() {
		stmts = append(stmts, struct {
			query string
			args  []any
		}{
			`INSERT INTO notification_sinks (id, namespace_id, kind, name, platform, target, created_by, version)
				VALUES ($1, $2, 'webhook', 'Shape hook', '', $3, 'alice', 1)`,
			[]any{sh.ID, other, sh.Target},
		})
	}

	for _, st := range stmts {
		if _, execErr := conn.Exec(ctx, st.query, st.args...); execErr != nil {
			t.Fatalf("plant legacy rows: %v", execErr)
		}
	}

	refs := notifytest.LegacyKeyRefs()

	for i, item := range notifytest.LegacyKeyItems(ctx, "shop") {
		_, execErr := conn.Exec(ctx,
			`INSERT INTO notification_sink_secrets (namespace_id, ref, sink_id, position, ciphertext)
				VALUES ($1, $2, $3, $4, $5)`, nsID, refs[i], notifytest.LegacyWebhookID, i, item.Ciphertext())
		if execErr != nil {
			t.Fatalf("plant legacy secret: %v", execErr)
		}

		_, execErr = conn.Exec(
			ctx,
			`INSERT INTO notification_sink_refs (namespace_id, ref) VALUES ($1, $2)`,
			nsID,
			refs[i],
		)
		if execErr != nil {
			t.Fatalf("plant legacy ref: %v", execErr)
		}
	}
}

// checkNoPlainURL fails if a sink table still holds a row. The migration of the
// scoped sink tables makes them again with no row, so no part of the URL that
// the legacy sink had stays in the database.
func checkNoPlainURL(t *testing.T, schemaURL string) {
	t.Helper()

	ctx := t.Context()

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	for _, table := range []string{"notification_sinks", "notification_sink_secrets", "notification_sink_refs"} {
		var count int

		err = conn.QueryRow(ctx, scopedCountStatement(table)).Scan(&count)
		if err != nil || count != 0 {
			t.Fatalf("rows of %s after the migration = %d (%v), want 0", table, count, err)
		}
	}
}

// scopedCountStatement returns the statement that counts the rows of a sink
// table.
func scopedCountStatement(table string) string {
	return map[string]string{
		"notification_sinks":        `SELECT count(*) FROM notification_sinks`,
		"notification_sink_secrets": `SELECT count(*) FROM notification_sink_secrets`,
		"notification_sink_refs":    `SELECT count(*) FROM notification_sink_refs`,
	}[table]
}
