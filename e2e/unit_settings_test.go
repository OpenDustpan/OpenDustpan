//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// unitSettingsAgent is the User-Agent of the unit settings API requests. The
// route coverage check counts requests that do not carry the harness agent.
const unitSettingsAgent = "opendustpan-e2e-unit-settings"

var errUnitSettingsDoc = errors.New("the unit settings document is not the expected one")

// unitSettingsDoc is the part of a unit settings document that the steps read.
type unitSettingsDoc struct {
	UnitID  string `json:"unitID"`
	Version int64  `json:"version"`
	Effect  map[string]struct {
		Value  any    `json:"value"`
		Origin string `json:"origin"`
	} `json:"effective"`
}

// TestUnitSettingsAPI drives the unit settings routes on a server of each store,
// with workspaces that the real OpenTofu CLI and the public API made. It proves
// that a read shows the effective value of each field with its origin, that a
// write needs the version that the caller read, the refusals of a viewer and of
// a person with no grant, the unit defaults of the namespace, the bulk call, the
// reset and the audit entries.
func TestUnitSettingsAPI(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.OpenTofu().Versions()
	if len(versions) == 0 {
		t.Fatal("the OpenTofu tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.OpenTofu(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runUnitSettingsOnStore(t, cli, db)
		})
	}
}

func runUnitSettingsOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)
	env.CreateNamespace(ctx, t, refusalNamespace)

	admin := issueRefusalPerson(ctx, t, env, "ann", "admin")
	viewer := issueRefusalPerson(ctx, t, env, "vic", "viewer")
	outside := newTriggerPerson(ctx, t, env, "eve", "")

	// The workspace web comes from the real CLI, with the token of the admin.
	writeRefusalTofuProject(t, env)
	env.WriteFile(t, filepath.Join(env.Home(), "tofurc-refuse-"+admin.name), fmt.Sprintf(
		"credentials %q {\n  token = %q\n}\n", env.Host(), admin.token.Secret,
	))

	for _, args := range [][]string{{"init", "-input=false"}, {"apply", "-input=false", "-auto-approve"}} {
		if res := runRefusalCLI(t, env, cli, admin, args...); res.ExitCode != 0 {
			t.Fatalf("tofu %s as the admin exits %d:\n%s", args[0], res.ExitCode, res.Stderr)
		}
	}

	// The second workspace and the runner pool come from the public API.
	env.APIStep(ctx, t, admin.token.Secret, e2e.APIStepSpec{
		UserAgent: unitSettingsAgent, Name: "create the workspace db", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + refusalNamespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"db"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})

	var pool struct {
		ID string `json:"id"`
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: unitSettingsAgent, Name: "create the runner pool", Method: http.MethodPost,
		Path: "/api/runners/" + refusalNamespace + "/pools", Body: `{"name":"ci","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token", Check: func(body string) error {
			if err := json.Unmarshal([]byte(body), &pool); err != nil || pool.ID == "" {
				return fmt.Errorf("%w: pool %s", errUnitSettingsDoc, body)
			}

			return nil
		},
	})

	fx := &unitSettingsFixture{
		env: env, admin: admin.token.Secret, viewer: viewer.token.Secret, outside: outside.token.Secret,
		webID: unitIDOf(ctx, t, env, admin.token.Secret, "web"), dbID: unitIDOf(ctx, t, env, admin.token.Secret, "db"),
		pool: pool.ID,
	}

	fx.readsADefaultView(ctx, t)
	fx.writesWithTheVersionThatWasRead(ctx, t)
	fx.refusesWhatItMust(ctx, t)
	fx.setsNamespaceDefaults(ctx, t)
	fx.linksManyUnits(ctx, t)
	fx.resetsAndAudits(ctx, t)
}

// unitSettingsFixture holds the tokens, the unit IDs and the pool of the
// scenario.
type unitSettingsFixture struct {
	env               *e2e.Env
	admin, viewer     string
	outside           string
	webID, dbID, pool string
}

func (fx *unitSettingsFixture) settings(unitID string) string {
	return "/api/dustpan/v1/namespaces/" + refusalNamespace + "/units/" + unitID + "/settings"
}

const (
	unitDefaultsPath = "/api/dustpan/v1/namespaces/" + refusalNamespace + "/settings/unit-defaults"
	unitBulkPath     = "/api/dustpan/v1/namespaces/" + refusalNamespace + "/unit-settings/bulk-put"
)

func (fx *unitSettingsFixture) step(
	ctx context.Context, t *testing.T, token string, spec e2e.APIStepSpec, headers map[string]string,
) {
	t.Helper()

	spec.UserAgent = unitSettingsAgent
	spec.Scheme = "token"

	fx.env.APIStepWithHeadersAs(ctx, t, token, spec, headers)
}

func ifMatchHeader(version string) map[string]string { return map[string]string{"If-Match": version} }

// checkOrigins checks the version and the origin of the fields of a document.
func checkOrigins(version int64, origins map[string]string) func(string) error {
	return func(body string) error {
		var doc unitSettingsDoc
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			return fmt.Errorf("%w: %s", errUnitSettingsDoc, body)
		}

		if doc.Version != version {
			return fmt.Errorf("%w: version %d, want %d: %s", errUnitSettingsDoc, doc.Version, version, body)
		}

		for field, want := range origins {
			if got := doc.Effect[field].Origin; got != want {
				return fmt.Errorf("%w: the origin of %s is %q, want %q: %s", errUnitSettingsDoc, field, got, want, body)
			}
		}

		return nil
	}
}

// readsADefaultView reads the settings of a unit that has no record: version 0,
// and every field from the namespace or the built-in value.
func (fx *unitSettingsFixture) readsADefaultView(ctx context.Context, t *testing.T) {
	t.Helper()

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "read the settings of a unit with no record", Method: http.MethodGet, Path: fx.settings(fx.webID),
		Want: http.StatusOK, Check: checkOrigins(0, map[string]string{
			"executor": "built-in-default", "directory": "built-in-default", "pool": "built-in-default",
		}),
	}, nil)
}

// writesWithTheVersionThatWasRead stores the settings, reads them back with the
// origin of each field, and refuses a second write with the old version.
func (fx *unitSettingsFixture) writesWithTheVersionThatWasRead(ctx context.Context, t *testing.T) {
	t.Helper()

	body := `{"pool":"` + fx.pool + `","codeLocation":{"repository":"acme/infra","ref":"main","directory":"stacks/web"}}`

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "store the settings with the version that was read", Method: http.MethodPut, Path: fx.settings(fx.webID),
		Body: body, Want: http.StatusOK, Check: checkOrigins(1, map[string]string{
			"pool": "unit", "repository": "unit", "ref": "unit", "directory": "unit", "executor": "built-in-default",
		}),
	}, ifMatchHeader(`"0"`))

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "read the stored settings", Method: http.MethodGet, Path: fx.settings(fx.webID), Want: http.StatusOK,
		Check: checkOrigins(1, map[string]string{"pool": "unit", "repository": "unit"}),
	}, nil)

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "refuse a write with a stale version", Method: http.MethodPut, Path: fx.settings(fx.webID),
		Body: body, Want: http.StatusPreconditionFailed, Check: inDoc("request.precondition-failed"),
	}, ifMatchHeader(`"0"`))

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "refuse a write with no version", Method: http.MethodPut, Path: fx.settings(fx.webID),
		Body: body, Want: http.StatusPreconditionRequired, Check: inDoc("request.precondition-required"),
	}, nil)

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "answer the suggestions and set no field", Method: http.MethodGet,
		Path: fx.settings(fx.webID) + "/suggestions", Want: http.StatusOK, Check: inDoc(`"suggestions"`),
	}, nil)

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "read the settings again after the suggestions", Method: http.MethodGet, Path: fx.settings(fx.webID),
		Want: http.StatusOK, Check: checkOrigins(1, nil),
	}, nil)
}

// refusesWhatItMust sends the refusals of a viewer, of a person with no grant
// and of hostile values.
func (fx *unitSettingsFixture) refusesWhatItMust(ctx context.Context, t *testing.T) {
	t.Helper()

	fx.step(ctx, t, fx.viewer, e2e.APIStepSpec{
		Name: "let a viewer read the settings", Method: http.MethodGet, Path: fx.settings(fx.webID), Want: http.StatusOK,
	}, nil)

	fx.step(ctx, t, fx.viewer, e2e.APIStepSpec{
		Name: "refuse a write by a viewer", Method: http.MethodPut, Path: fx.settings(fx.webID), Body: `{}`,
		Want: http.StatusForbidden,
	}, ifMatchHeader(`"1"`))

	fx.step(ctx, t, fx.outside, e2e.APIStepSpec{
		Name: "answer not found to a person with no grant", Method: http.MethodGet, Path: fx.settings(fx.webID),
		Want: http.StatusNotFound, Check: inDoc("unit-settings.not-found"),
	}, nil)

	for name, body := range map[string]string{
		"a directory outside the repository": `{"codeLocation":{"directory":"../x"}}`,
		"a directory in CI configuration":    `{"codeLocation":{"directory":"a/.github/b"}}`,
		"a ref with a leading dash":          `{"codeLocation":{"ref":"--upload-pack=x"}}`,
		"a file outside the directory":       `{"writeBack":{"files":["../secrets.yaml"]}}`,
	} {
		fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
			Name: "refuse " + name, Method: http.MethodPut, Path: fx.settings(fx.webID), Body: body,
			Want: http.StatusBadRequest, Check: inDoc("unit-settings.invalid"),
		}, ifMatchHeader(`"1"`))
	}

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "refuse a connection that the namespace does not have", Method: http.MethodPut,
		Path: fx.settings(fx.webID), Body: `{"codeLocation":{"connection":"gitconn-0123456789abcdef0123456789abcdef"}}`,
		Want: http.StatusUnprocessableEntity, Check: inDoc("unit-settings.connection-unknown"),
	}, ifMatchHeader(`"1"`))
}

// setsNamespaceDefaults sets the default pool and reads it as the origin of the
// pool of a unit that sets none.
func (fx *unitSettingsFixture) setsNamespaceDefaults(ctx context.Context, t *testing.T) {
	t.Helper()

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "read the unit defaults", Method: http.MethodGet, Path: unitDefaultsPath, Want: http.StatusOK,
		Check: inDoc(`"version":0`),
	}, nil)

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "set the default pool", Method: http.MethodPut, Path: unitDefaultsPath,
		Body: `{"pool":"` + fx.pool + `"}`, Want: http.StatusOK, Check: inDoc(fx.pool, `"version":1`),
	}, ifMatchHeader(`"0"`))

	fx.step(ctx, t, fx.viewer, e2e.APIStepSpec{
		Name: "refuse a change of the defaults by a viewer", Method: http.MethodPut,
		Path: unitDefaultsPath, Body: `{"pool":""}`, Want: http.StatusForbidden,
	}, ifMatchHeader(`"1"`))

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "show the default pool as the origin of the pool of a unit", Method: http.MethodGet,
		Path: fx.settings(fx.dbID), Want: http.StatusOK,
		Check: checkOrigins(0, map[string]string{"pool": "namespace-default"}),
	}, nil)
}

// linksManyUnits applies one body to two units with the bulk call. One item
// fails on its own and the other succeeds.
func (fx *unitSettingsFixture) linksManyUnits(ctx context.Context, t *testing.T) {
	t.Helper()

	body := fmt.Sprintf(
		`{"units":[{"id":%q,"version":0},{"id":%q,"version":0}],"settings":{"codeLocation":{"repository":"acme/infra","ref":"main"}}}`,
		fx.dbID, fx.webID,
	)

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "link two units with the bulk call", Method: http.MethodPost, Path: unitBulkPath, Body: body,
		Want: http.StatusOK, Check: func(res string) error {
			var out struct {
				Results []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				} `json:"results"`
			}

			if err := json.Unmarshal([]byte(res), &out); err != nil || len(out.Results) != 2 {
				return fmt.Errorf("%w: %s", errUnitSettingsDoc, res)
			}

			if out.Results[0].ID != fx.dbID || out.Results[0].Status != "ok" ||
				out.Results[1].ID != fx.webID || out.Results[1].Status != "error" {
				return fmt.Errorf("%w: want db ok and web stale: %s", errUnitSettingsDoc, res)
			}

			return nil
		},
	}, nil)

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "refuse a bulk call with no unit", Method: http.MethodPost, Path: unitBulkPath,
		Body: `{"units":[],"settings":{}}`, Want: http.StatusBadRequest, Check: inDoc("unit-settings.bulk-size"),
	}, nil)
}

// resetsAndAudits resets a unit to the defaults and reads the audit entries of
// the namespace.
func (fx *unitSettingsFixture) resetsAndAudits(ctx context.Context, t *testing.T) {
	t.Helper()

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "reset the settings of a unit", Method: http.MethodDelete, Path: fx.settings(fx.webID),
		Want: http.StatusNoContent,
	}, ifMatchHeader(`"1"`))

	fx.step(ctx, t, fx.admin, e2e.APIStepSpec{
		Name: "read the reset settings", Method: http.MethodGet, Path: fx.settings(fx.webID), Want: http.StatusOK,
		Check: checkOrigins(0, map[string]string{"repository": "built-in-default"}),
	}, nil)

	var changed, reset, defaults int

	for _, e := range readAuditEntries(ctx, t, fx.env, refusalNamespace) {
		switch e.Action {
		case "unit-settings-changed":
			changed++
		case "unit-settings-reset":
			reset++
		case "unit-defaults-changed":
			defaults++
		default:
		}
	}

	if changed < 2 || reset != 1 || defaults != 1 {
		t.Fatalf("the audit log holds %d changes, %d resets and %d default changes, want at least 2, 1 and 1",
			changed, reset, defaults)
	}
}
