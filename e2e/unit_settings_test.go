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

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	version := e2e.OpenTofu().Single()
	if version == "" {
		t.Fatal("the OpenTofu tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.OpenTofu(), version)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, version, err)
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
	t.Run("job_uses_location", func(t *testing.T) { fx.jobUsesTheLocation(ctx, t) })
	fx.refusesWhatItMust(ctx, t)
	fx.setsNamespaceDefaults(ctx, t)
	fx.linksManyUnits(ctx, t)
	fx.repeatsAKeyedWrite(ctx, t)
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

// unitStep is one request of the scenario.
type unitStep struct {
	name, method, path, body string
	want                     int
	check                    func(string) error
	version                  string
}

// readStep is a GET.
func readStep(name, path string, want int, check func(string) error) unitStep {
	return unitStep{name: name, method: http.MethodGet, path: path, body: "", want: want, check: check, version: ""}
}

// sending returns the step as a request with a body and the version that the
// caller read. An empty version sends no If-Match header.
func (s unitStep) sending(method, body, version string) unitStep {
	s.method = method
	s.body = body
	s.version = version

	return s
}

// run sends the step with the token of a person.
func (fx *unitSettingsFixture) run(ctx context.Context, t *testing.T, token string, s unitStep) {
	t.Helper()

	var headers map[string]string
	if s.version != "" {
		headers = map[string]string{"If-Match": s.version}
	}

	fx.env.APIStepWithHeadersAs(ctx, t, token, e2e.APIStepSpec{
		UserAgent: unitSettingsAgent, Name: s.name, Method: s.method, Path: s.path, Body: s.body, Want: s.want,
		Scheme: "token", Check: s.check,
	}, headers)
}

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

	fx.run(ctx, t, fx.admin, readStep("read the settings of a unit with no record", fx.settings(fx.webID),
		http.StatusOK, checkOrigins(0, map[string]string{
			"executor": "built-in-default", "directory": "built-in-default", "pool": "built-in-default",
		})))
}

// writesWithTheVersionThatWasRead stores the settings, reads them back with the
// origin of each field, and refuses a second write with the old version.
func (fx *unitSettingsFixture) writesWithTheVersionThatWasRead(ctx context.Context, t *testing.T) {
	t.Helper()

	path := fx.settings(fx.webID)
	body := `{"pool":"` + fx.pool + `","codeLocation":{"repository":"acme/infra","ref":"main","directory":"stacks/web"}}`

	fx.run(
		ctx,
		t,
		fx.admin,
		readStep(
			"store the settings with the version that was read",
			path,
			http.StatusOK,
			checkOrigins(1, map[string]string{
				"pool":       "unit",
				"repository": "unit",
				"ref":        "unit",
				"directory":  "unit",
				"executor":   "built-in-default",
			}),
		).sending(http.MethodPut, body, `"0"`),
	)
	fx.run(ctx, t, fx.admin, readStep("read the stored settings", path, http.StatusOK,
		checkOrigins(1, map[string]string{"pool": "unit", "repository": "unit"})))
	fx.run(
		ctx,
		t,
		fx.admin,
		readStep(
			"refuse a write with a stale version",
			path,
			http.StatusPreconditionFailed,
			inDoc("request.precondition-failed"),
		).sending(http.MethodPut, body, `"0"`),
	)
	fx.run(
		ctx,
		t,
		fx.admin,
		readStep(
			"refuse a write with no version",
			path,
			http.StatusPreconditionRequired,
			inDoc("request.precondition-required"),
		).sending(http.MethodPut, body, ""),
	)
	fx.run(ctx, t, fx.admin, readStep("answer the suggestions and set no field", path+"/suggestions",
		http.StatusOK, inDoc(`"suggestions"`)))
	fx.run(ctx, t, fx.admin, readStep("read the settings again after the suggestions", path, http.StatusOK,
		checkOrigins(1, nil)))
}

// jobUsesTheLocation starts a job that names no code location and no pool, and
// reads it back: the job holds the code location and the pool of the settings
// of the unit.
func (fx *unitSettingsFixture) jobUsesTheLocation(ctx context.Context, t *testing.T) {
	t.Helper()

	var jobID string

	jobs := "/api/dustpan/v1/namespaces/" + refusalNamespace + "/jobs"

	fx.run(ctx, t, fx.admin, unitStep{
		name: "start a job with no location", method: http.MethodPost, path: jobs,
		body: `{"unit":{"tool":"opentofu","name":"web"},"operation":"plan","reason":"e2e"}`,
		want: http.StatusCreated, version: "",
		check: func(body string) error {
			var doc jobDoc
			if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.ID == "" {
				return fmt.Errorf("%w: %s", errUnitSettingsDoc, body)
			}

			jobID = doc.ID

			return nil
		},
	})

	// The bootstrap token reads the job, as in the other job steps.
	fx.env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: unitSettingsAgent, Name: "read the job", Method: http.MethodGet, Path: jobs + "/" + jobID, Body: "",
		Want: http.StatusOK, Scheme: "token",
		Check: inDoc(`"location":{"repository":"acme/infra","ref":"main","directory":"stacks/web"}`,
			`"poolID":"`+fx.pool+`"`, `"defaulted":["pool","location"]`),
	})
}

// refusesWhatItMust sends the refusals of a viewer, of a person with no grant
// and of hostile values.
func (fx *unitSettingsFixture) refusesWhatItMust(ctx context.Context, t *testing.T) {
	t.Helper()

	path := fx.settings(fx.webID)

	fx.run(ctx, t, fx.viewer, readStep("let a viewer read the settings", path, http.StatusOK, nil))
	fx.run(
		ctx,
		t,
		fx.viewer,
		readStep("refuse a write by a viewer", path, http.StatusForbidden, nil).sending(http.MethodPut, `{}`, `"1"`),
	)
	fx.run(ctx, t, fx.outside, readStep("answer not found to a person with no grant", path, http.StatusNotFound,
		inDoc("unit-settings.not-found")))

	for name, body := range map[string]string{
		"a directory outside the repository": `{"codeLocation":{"directory":"../x"}}`,
		"a directory in CI configuration":    `{"codeLocation":{"directory":"a/.github/b"}}`,
		"a ref with a leading dash":          `{"codeLocation":{"ref":"--upload-pack=x"}}`,
		"a file outside the directory":       `{"writeBack":{"files":["../other.yaml"]}}`,
	} {
		fx.run(
			ctx,
			t,
			fx.admin,
			readStep(
				"refuse "+name,
				path,
				http.StatusBadRequest,
				inDoc("unit-settings.invalid"),
			).sending(http.MethodPut, body, `"1"`),
		)
	}

	fx.run(
		ctx,
		t,
		fx.admin,
		readStep(
			"refuse a connection that the namespace does not have",
			path,
			http.StatusUnprocessableEntity,
			inDoc("unit-settings.connection-unknown"),
		).sending(http.MethodPut, `{"codeLocation":{"connection":"gitconn-0123456789abcdef0123456789abcdef"}}`, `"1"`),
	)
}

// setsNamespaceDefaults sets the default pool and reads it as the origin of the
// pool of a unit that sets none.
func (fx *unitSettingsFixture) setsNamespaceDefaults(ctx context.Context, t *testing.T) {
	t.Helper()

	fx.run(ctx, t, fx.admin, readStep("read the unit defaults", unitDefaultsPath, http.StatusOK,
		inDoc(`"version":0`)))
	fx.run(
		ctx,
		t,
		fx.admin,
		readStep(
			"set the default pool",
			unitDefaultsPath,
			http.StatusOK,
			inDoc(fx.pool, `"version":1`),
		).sending(http.MethodPut, `{"pool":"`+fx.pool+`"}`, `"0"`),
	)
	fx.run(
		ctx,
		t,
		fx.viewer,
		readStep(
			"refuse a change of the defaults by a viewer",
			unitDefaultsPath,
			http.StatusForbidden,
			nil,
		).sending(http.MethodPut, `{"pool":""}`, `"1"`),
	)
	fx.run(ctx, t, fx.admin, readStep("show the default pool as the origin of the pool of a unit",
		fx.settings(fx.dbID), http.StatusOK, checkOrigins(0, map[string]string{"pool": "namespace-default"})))
}

// bulkResultOf is one result of a bulk answer.
type bulkResultOf struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// bulkAnswerOf is the answer of a bulk call.
type bulkAnswerOf struct {
	Results []bulkResultOf `json:"results"`
}

// linksManyUnits applies one body to two units with the bulk call. One item
// fails on its own and the other succeeds.
func (fx *unitSettingsFixture) linksManyUnits(ctx context.Context, t *testing.T) {
	t.Helper()

	body := fmt.Sprintf(
		`{"units":[{"id":%q,"version":0},{"id":%q,"version":0}],"settings":{"codeLocation":{"repository":"acme/infra","ref":"main"}}}`,
		fx.dbID,
		fx.webID,
	)

	check := func(res string) error {
		var out bulkAnswerOf
		if err := json.Unmarshal([]byte(res), &out); err != nil || len(out.Results) != 2 {
			return fmt.Errorf("%w: %s", errUnitSettingsDoc, res)
		}

		if out.Results[0].ID != fx.dbID || out.Results[0].Status != "ok" ||
			out.Results[1].ID != fx.webID || out.Results[1].Status != "error" {
			return fmt.Errorf("%w: want db ok and web stale: %s", errUnitSettingsDoc, res)
		}

		return nil
	}

	fx.run(
		ctx,
		t,
		fx.admin,
		readStep(
			"link two units with the bulk call",
			unitBulkPath,
			http.StatusOK,
			check,
		).sending(http.MethodPost, body, ""),
	)
	fx.run(
		ctx,
		t,
		fx.admin,
		readStep(
			"refuse a bulk call with no unit",
			unitBulkPath,
			http.StatusBadRequest,
			inDoc("unit-settings.bulk-size"),
		).sending(http.MethodPost, `{"units":[],"settings":{}}`, ""),
	)
}

// repeatsAKeyedWrite sends one write twice with one Idempotency-Key. The second
// request would answer 412 if it ran, because it names the version that the
// first one replaced. It answers the first answer, and the version moves once.
func (fx *unitSettingsFixture) repeatsAKeyedWrite(ctx context.Context, t *testing.T) {
	t.Helper()

	path := fx.settings(fx.dbID)
	body := `{"codeLocation":{"repository":"acme/infra","ref":"release"}}`
	headers := map[string]string{"If-Match": `"1"`, "Idempotency-Key": "e2e-unit-settings-1"}

	for _, name := range []string{"write with an Idempotency-Key", "repeat the write with the same key"} {
		fx.env.APIStepWithHeadersAs(ctx, t, fx.admin, e2e.APIStepSpec{
			UserAgent: unitSettingsAgent, Name: name, Method: http.MethodPut, Path: path, Body: body,
			Want: http.StatusOK, Scheme: "token", Check: checkOrigins(2, map[string]string{"ref": "unit"}),
		}, headers)
	}

	fx.run(ctx, t, fx.admin, readStep("read the settings after the repeated write", path, http.StatusOK,
		checkOrigins(2, map[string]string{"ref": "unit"})))

	other := map[string]string{"If-Match": `"2"`, "Idempotency-Key": "e2e-unit-settings-1"}

	fx.env.APIStepWithHeadersAs(ctx, t, fx.admin, e2e.APIStepSpec{
		UserAgent: unitSettingsAgent, Name: "refuse the same key with another request", Method: http.MethodPut,
		Path: path, Body: `{"codeLocation":{"repository":"acme/infra","ref":"main"}}`,
		Want: http.StatusUnprocessableEntity, Scheme: "token", Check: inDoc("request.idempotency-key-reused"),
	}, other)
}

// resetsAndAudits resets a unit to the defaults and reads the audit entries of
// the namespace.
func (fx *unitSettingsFixture) resetsAndAudits(ctx context.Context, t *testing.T) {
	t.Helper()

	path := fx.settings(fx.webID)

	fx.run(
		ctx,
		t,
		fx.admin,
		readStep("reset the settings of a unit", path, http.StatusNoContent, nil).sending(http.MethodDelete, "", `"1"`),
	)
	fx.run(ctx, t, fx.admin, readStep("read the reset settings", path, http.StatusOK,
		checkOrigins(0, map[string]string{"repository": "built-in-default"})))

	var changed, reset, defaults int

	entries := readAuditEntries(ctx, t, fx.env, refusalNamespace)
	for i := range entries {
		switch entries[i].Action {
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
