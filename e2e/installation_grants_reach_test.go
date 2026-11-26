//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// reachSettingKey is the key of the setting that decides whether a grant on
// the installation reaches the actions inside each namespace.
const reachSettingKey = "access.installationGrantsReachNamespaces"

// reachNamespace is the namespace of the steps of the reach setting.
const reachNamespace = "reach-setting"

// TestInstallationGrantsReach runs the reach setting on each store, with the
// real binary: the default true lets the grant of a service principal on the
// installation do a namespace action; the value false refuses the same
// action with 403, keeps the operator read of the settings of a person with
// an operator role on the installation, and keeps a grant on the namespace.
// The change of the setting needs a console session with a fresh sign-in.
func TestInstallationGrantsReach(t *testing.T) {
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

			runInstallationReachOnStore(t, env)
		})
	}
}

// runInstallationReachOnStore runs the steps of the reach setting on one
// store.
func runInstallationReachOnStore(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	// The change of the setting needs a fresh console session. Make its
	// person first: with the binary server a restart moves the revision of
	// the settings, so a step that reads a revision makes the person before
	// the read.
	env.PrepareFreshAdmin(ctx, t)

	seed := seedInstallationReach(ctx, t, env)

	// The route of the namespace action that the grant reaches: the read of
	// the proxy rule set of the namespace, namespace:read.
	probeRoute := "/api/dustpan/v1/namespaces/" + reachNamespace + "/proxy-rules"

	// reachWorks does the namespace action with the token of the service
	// principal and checks the status of the answer and, for a refusal, the
	// parts of the body.
	reachWorks := func(want int, parts ...string) {
		t.Helper()

		status, body, err := env.ServerAPIStatusAs(ctx, seed.token.Secret, http.MethodGet, probeRoute, "")
		if err != nil {
			t.Fatalf("GET %s: %v", probeRoute, err)
		}

		if status != want {
			t.Fatalf("GET %s answers %d (%s), want %d", probeRoute, status, body, want)
		}

		for _, part := range parts {
			if !strings.Contains(body, part) {
				t.Fatalf("answer %q does not name %q", body, part)
			}
		}
	}

	// The default reaches namespaces: the read of the settings shows true
	// with its source, and the namespace action works.
	reachSettingShowsDefault(t, env)
	reachWorks(http.StatusOK)

	// false, set from a console session with a fresh sign-in.
	patchReachSetting(ctx, t, env, "false")
	reachWorks(http.StatusForbidden, "namespace:read", "on this namespace")

	// The operator read of the settings stays allowed with false.
	operatorReadsSettings(ctx, t, env, seed.operator.ID)

	// The export of the installation shows the grant, so a person can ask
	// why the action was denied.
	reviewShowsGrant(ctx, t, env, seed.probe.ID)

	// The value goes back to true, and the action works again.
	patchReachSetting(ctx, t, env, "true")
	reachWorks(http.StatusOK)

	// false again: a grant on the namespace works with false. The operator
	// person holds admin on the namespace, so its console session grants the
	// role to the service principal there.
	patchReachSetting(ctx, t, env, "false")
	grantInNamespace(ctx, t, env, seed.operator.ID, seed.probe.ID)
	reachWorks(http.StatusOK)
}

// installationReachSeed holds the records that the steps of the reach
// setting use.
type installationReachSeed struct {
	token    e2eToken
	probe    e2ePrincipal
	operator e2ePrincipal
}

// seedInstallationReach stores the records of the steps: the namespace, the
// service principal with its grant of viewer on the installation, the person
// with the operator role on the installation and its grant of admin on the
// namespace, and the token of the service principal.
func seedInstallationReach(ctx context.Context, t *testing.T, env *e2e.Env) installationReachSeed {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"` + reachNamespace + `"}`, http.StatusCreated,
	}, nil)

	var probe e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"reach-probe","kind":"service"}`, http.StatusCreated,
	}, &probe)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/grants",
		`{"subject":"principal:` + probe.ID + `","role":"viewer"}`, http.StatusCreated,
	}, nil)

	var operator e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("reach-operator@example.com") + `","kind":"person"}`, http.StatusCreated,
	}, &operator)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/grants",
		`{"subject":"principal:` + operator.ID + `","role":"installation-operator"}`, http.StatusCreated,
	}, nil)

	// A grant on the namespace survives false. The operator person holds
	// admin on the namespace, so it can grant inside it while the setting is
	// false: the setting limits only grants on the installation.
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + reachNamespace + "/grants",
		`{"subject":"principal:` + operator.ID + `","role":"admin"}`, http.StatusCreated,
	}, nil)

	// The token of the service principal does the namespace action.
	var token e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens",
		`{"principalID":"` + probe.ID + `","name":"probe"}`, http.StatusCreated,
	}, &token)

	return installationReachSeed{token: token, probe: probe, operator: operator}
}

// reachSettingShowsDefault reads the settings and checks that the reach
// setting shows the value true, its default source, a database layer and a
// live apply.
func reachSettingShowsDefault(t *testing.T, env *e2e.Env) {
	t.Helper()

	_, doc := settingsRead(t.Context(), t, env, settingsRoute)

	reach := settingFor(t, doc, reachSettingKey)
	if value, isBool := reach.Value.(bool); !isBool || !value {
		t.Fatalf("%s = %+v, want the value true", reachSettingKey, reach)
	}

	if reach.Source != "default" || !reach.DatabaseLayer || reach.Apply != "live" {
		t.Fatalf("%s = %+v, want the source default, a database layer and a live apply", reachSettingKey, reach)
	}
}

// patchReachSetting sets the reach setting to value with a console session
// that has a fresh sign-in, which the change needs in both directions.
func patchReachSetting(ctx context.Context, t *testing.T, env *e2e.Env, value string) {
	t.Helper()

	tag := settingsETag(ctx, t, env)
	body := `{"` + reachSettingKey + `":` + value + `}`

	status, answer := env.FreshAdminMergePatch(ctx, t, settingsRoute, tag, body)
	if status != http.StatusOK {
		t.Fatalf("PATCH %s with a fresh console session answers %d (%s), want 200", body, status, answer)
	}
}

// operatorReadsSettings reads the settings with the console session of the
// person principalID, which holds an operator role on the installation, and
// checks that the read stays allowed.
func operatorReadsSettings(ctx context.Context, t *testing.T, env *e2e.Env, principalID string) {
	t.Helper()

	status, body := env.SessionAPIStatus(ctx, t, principalID,
		e2e.SessionRequest{Method: http.MethodGet, Path: settingsRoute, Body: ""})
	if status != http.StatusOK {
		t.Fatalf("the operator role reads the settings = %d (%s), want 200", status, body)
	}
}

// reviewShowsGrant reads the export of the installation and checks that it
// shows the grant of viewer on @server of the service principal.
func reviewShowsGrant(ctx context.Context, t *testing.T, env *e2e.Env, principalID string) {
	t.Helper()

	rows := reviewOf(ctx, t, env, "/api/server/access-review", url.Values{})

	found := slices.ContainsFunc(rows, func(r e2eReviewRow) bool {
		return r.Subject == "principal:"+principalID && r.Role == "viewer" && r.Object == "@server"
	})
	if !found {
		t.Fatalf("the export of the installation = %+v, want the grant of the service principal on @server", rows)
	}
}

// grantInNamespace grants the role viewer on the namespace of the steps to
// the service principal, from the console session of the person that holds
// admin on that namespace.
func grantInNamespace(ctx context.Context, t *testing.T, env *e2e.Env, grantor, subject string) {
	t.Helper()

	status, body := env.SessionAPIStatus(ctx, t, grantor, e2e.SessionRequest{
		Method: http.MethodPost, Path: "/api/server/namespaces/" + reachNamespace + "/grants",
		Body: `{"subject":"principal:` + subject + `","role":"viewer"}`,
	})
	if status != http.StatusCreated {
		t.Fatalf("the grant on the namespace answers %d (%s), want 201", status, body)
	}
}

// settingsETag reads the settings and returns the ETag revision of the
// resource.
func settingsETag(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	resp, _ := settingsRead(ctx, t, env, settingsRoute)

	return resp.Header.Get("ETag")
}
