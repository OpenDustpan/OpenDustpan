//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// retentionKey is the key of the retention period of the decision records.
const retentionKey = "decisionRecords.retentionHours"

// TestDecisionRecordRetentionSetting reads the retention setting, changes it
// and checks the refusals, on a server of each store. No CLI reads the
// setting, so the steps call the settings route with the token of the
// administrator, as the docs page does.
func TestDecisionRecordRetentionSetting(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			retentionScenario(t.Context(), t, env)
		})
	}
}

// retentionScenario runs the steps of the docs page.
func retentionScenario(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	// The refusals of an access token below and in retentionShorter need
	// freshAuthEnabled on. It goes on before the first read of the revision.
	env.EnableFreshAuth(ctx, t)

	// The first read shows keep-forever from the default.
	read, doc := settingsRead(ctx, t, env, settingsRoute)

	item := settingFor(t, doc, retentionKey)
	if item.Value != float64(0) || item.Source != "default" || item.Locked || !item.DatabaseLayer ||
		item.Apply != "live" {
		t.Fatalf("%s = %+v, want 0 from the default, unlocked, with a database layer, applied live", retentionKey, item)
	}

	tag := read.Header.Get("ETag")

	// A period that ends records sooner than keep-forever needs a fresh
	// authentication. An access token never gives it.
	resp, refused := settingsPatch(ctx, t, env, settingsReq{
		path: settingsRoute, ifMatch: tag, body: `{"` + retentionKey + `":8760}`,
	})
	if resp.Status != http.StatusForbidden || refused.ErrorCode != "mfa.fresh-step-required" {
		t.Fatalf("PATCH 8760 with an access token answers %d %q (%s), want 403 mfa.fresh-step-required",
			resp.Status, refused.ErrorCode, resp.Body)
	}

	status, body := env.FreshAdminMergePatch(ctx, t, settingsRoute, tag, `{"`+retentionKey+`":8760}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH 8760 with a fresh console session answers %d %s, want 200", status, body)
	}

	read, doc = settingsRead(ctx, t, env, settingsRoute)

	item = settingFor(t, doc, retentionKey)
	if item.Value != float64(8760) || item.Source != "database" {
		t.Fatalf("%s = %+v, want 8760 from the database", retentionKey, item)
	}

	tag = read.Header.Get("ETag")

	retentionShorter(ctx, t, env, tag)

	retentionRefusals(ctx, t, env, tag)

	retentionStays(ctx, t, env)

	retentionLonger(ctx, t, env, tag)
}

// retentionShorter proves that a shorter period needs a fresh authentication.
func retentionShorter(ctx context.Context, t *testing.T, env *e2e.Env, tag string) {
	t.Helper()

	// A shorter period deletes evidence sooner. An access token never gives
	// the fresh authentication, so the change is refused and 8760 stays.
	resp, refused := settingsPatch(ctx, t, env, settingsReq{
		path: settingsRoute, ifMatch: tag, body: `{"` + retentionKey + `":720}`,
	})
	if resp.Status != http.StatusForbidden || refused.ErrorCode != "mfa.fresh-step-required" {
		t.Fatalf("PATCH 720 with an access token answers %d %q (%s), want 403 mfa.fresh-step-required",
			resp.Status, refused.ErrorCode, resp.Body)
	}

	retentionStays(ctx, t, env)
}

// retentionStays proves that the stored period is still 8760.
func retentionStays(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	_, doc := settingsRead(ctx, t, env, settingsRoute)
	if item := settingFor(t, doc, retentionKey); item.Value != float64(8760) || item.Source != "database" {
		t.Fatalf("%s = %+v, want 8760 from the database", retentionKey, item)
	}
}

// retentionRefusals proves that a value that a typo can cause gets 400.
func retentionRefusals(ctx context.Context, t *testing.T, env *e2e.Env, tag string) {
	t.Helper()

	// A value that a typo can cause gets 400 and the stored value stays.
	for _, value := range []string{`23`, `87601`, `-1`, `1e3`, `24.0`, `"8760"`, `"abc"`, `""`} {
		settingsWant(
			ctx,
			t,
			env,
			settingsReq{path: settingsRoute, ifMatch: tag, body: `{"` + retentionKey + `":` + value + `}`},
			settingsAnswer{status: http.StatusBadRequest, code: "settings.value-invalid"},
		)
	}
}

// retentionLonger proves that a longer period needs no fresh authentication.
func retentionLonger(ctx context.Context, t *testing.T, env *e2e.Env, tag string) {
	t.Helper()

	// A longer period needs no fresh authentication.
	settingsWant(ctx, t, env, settingsReq{path: settingsRoute, ifMatch: tag, body: `{"` + retentionKey + `":17520}`},
		settingsAnswer{status: http.StatusOK, code: ""})

	_, doc := settingsRead(ctx, t, env, settingsRoute)
	if item := settingFor(t, doc, retentionKey); item.Value != float64(17520) || item.Source != "database" {
		t.Fatalf("%s = %+v, want 17520 from the database", retentionKey, item)
	}
}
