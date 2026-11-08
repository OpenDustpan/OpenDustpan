//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// triggersAgent is the User-Agent of the trigger API requests. The route
// coverage check counts requests that do not carry the harness agent.
const triggersAgent = "opendustpan-e2e-triggers"

var errTriggerDoc = errors.New("the trigger document is not the expected one")

// triggerDoc is the part of a trigger document that the steps read.
type triggerDoc struct {
	ID           string            `json:"id"`
	Namespace    string            `json:"namespace"`
	Unit         triggerUnitRef    `json:"unit"`
	Kind         string            `json:"kind"`
	Frequency    *triggerFrequency `json:"frequency"`
	Source       *triggerUnitRef   `json:"source"`
	Enabled      bool              `json:"enabled"`
	LastSaver    string            `json:"lastSaver"`
	NeedsNewSave bool              `json:"needsNewSave"`
}

// triggerErrDoc is the part of an error document that the steps read.
type triggerErrDoc struct {
	ErrorCode string           `json:"errorCode"`
	Units     []triggerUnitRef `json:"units"`
}

// triggerUnitRef is a unit reference in a trigger document.
type triggerUnitRef struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Name string `json:"name"`
}

// triggerFrequency is the frequency of a schedule trigger.
type triggerFrequency struct {
	Preset string `json:"preset"`
	Cron   string `json:"cron"`
}

// unitListDoc is the part of the unit list that the steps read.
type unitListDoc struct {
	Units []unitListItem `json:"units"`
}

// unitListItem is one unit of the unit list.
type unitListItem struct {
	UnitID string `json:"unitID"`
	Name   string `json:"name"`
}

// fireDoc is the part of a fire answer that the steps read.
type fireDoc struct {
	Job fireJob `json:"job"`
}

// fireJob is the job in a fire answer.
type fireJob struct {
	ID string `json:"id"`
}

// triggersFixture holds the people, the units and the paths of the scenario.
type triggersFixture struct {
	env     *e2e.Env
	cli     e2e.Install
	admin   *refusalPerson
	viewer  *refusalPerson
	outside e2ePrincipalToken
	second  e2ePrincipalToken
	webID   string
	dbID    string
}

// e2ePrincipalToken is a person with a stored principal, a personal token and
// the ID of its grant.
type e2ePrincipalToken struct {
	principal e2ePrincipal
	token     e2e.PersonalToken
	grant     e2eGrant
}

// TestRunTriggers drives the trigger routes on a server of each store, with a
// workspace that the real OpenTofu CLI made. It proves the routes of a person
// who may change a unit, of a viewer and of a person with no grant, the refusal
// of a loop of units, a fire by hand that starts one plan job, the audit
// entries, the refusal of a start whose last saver lost access, and that the
// namespace holds no trigger of another namespace.
func TestRunTriggers(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

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

			runTriggersOnStore(t, cli, db)
		})
	}
}

func runTriggersOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)
	env.CreateNamespace(ctx, t, refusalNamespace)

	admin := issueRefusalPerson(ctx, t, env, "ann", "admin")
	viewer := issueRefusalPerson(ctx, t, env, "vic", "viewer")
	outside := newTriggerPerson(ctx, t, env, "eve", "")
	second := newTriggerPerson(ctx, t, env, "bob", "admin")

	fx := &triggersFixture{
		env: env, cli: cli, admin: admin, viewer: viewer, outside: outside, second: second, webID: "", dbID: "",
	}

	// The workspace web comes from the real CLI, with the token of the admin.
	writeRefusalTofuProject(t, env)
	env.WriteFile(t, filepath.Join(env.Home(), "tofurc-refuse-"+fx.admin.name), fmt.Sprintf(
		"credentials %q {\n  token = %q\n}\n", env.Host(), fx.admin.token.Secret,
	))

	for _, args := range [][]string{{"init", "-input=false"}, {"apply", "-input=false", "-auto-approve"}} {
		if res := runRefusalCLI(t, env, cli, fx.admin, args...); res.ExitCode != 0 {
			t.Fatalf("tofu %s as the admin exits %d:\n%s", args[0], res.ExitCode, res.Stderr)
		}
	}

	// The second workspace and the runner pool come from the public API.
	env.APIStep(ctx, t, fx.admin.token.Secret, e2e.APIStepSpec{
		UserAgent: triggersAgent, Name: "create the workspace db", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + refusalNamespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"db"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: triggersAgent, Name: "create the runner pool", Method: http.MethodPost,
		Path: "/api/runners/" + refusalNamespace + "/pools", Body: `{"name":"ci","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token", Check: nil,
	})

	fx.webID = unitIDOf(ctx, t, env, fx.admin.token.Secret, "web")
	fx.dbID = unitIDOf(ctx, t, env, fx.admin.token.Secret, "db")

	first := fx.savesAndReads(ctx, t)
	fx.refusesALoop(ctx, t)
	fx.firesATrigger(ctx, t, first)
	fx.refusesASaverWithoutAccess(ctx, t)
	fx.refusesWhatItMust(ctx, t, first)
	fx.writesAuditEntries(ctx, t)
}

// newTriggerPerson makes a person with a principal, a personal token and, when
// role is not empty, a grant of role on the namespace.
func newTriggerPerson(ctx context.Context, t *testing.T, env *e2e.Env, name, role string) e2ePrincipalToken {
	t.Helper()

	var p e2ePrincipalToken

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		fmt.Sprintf(`{"subject":%q,"kind":"person"}`, e2e.SignInSubject(name+"@example.com")), http.StatusCreated,
	}, &p.principal)

	if role != "" {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/namespaces/" + refusalNamespace + "/grants",
			`{"subject":"principal:` + p.principal.ID + `","role":"` + role + `"}`, http.StatusCreated,
		}, &p.grant)
	}

	p.token = env.PersonalToken(ctx, t, p.principal.ID, "trigger-"+name)

	return p
}

// unitIDOf returns the stable ID of the unit name in the namespace of the
// scenario, from the inventory API.
func unitIDOf(ctx context.Context, t *testing.T, env *e2e.Env, token, name string) string {
	t.Helper()

	status, body, err := env.ServerAPIStatusAs(ctx, token, http.MethodGet,
		"/api/dustpan/v1/namespaces/"+refusalNamespace+"/units", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("list the units = %d, %v: %s", status, err, body)
	}

	var list unitListDoc

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode the unit list: %v: %s", err, body)
	}

	for _, u := range list.Units {
		if u.Name == name {
			return u.UnitID
		}
	}

	t.Fatalf("the unit list holds no unit %q: %s", name, body)

	return ""
}

func (fx *triggersFixture) triggers(unitID string) string {
	return "/api/dustpan/v1/namespaces/" + refusalNamespace + "/units/" + unitID + "/triggers"
}

// step runs one request with the personal token of a person.
func (fx *triggersFixture) step(ctx context.Context, t *testing.T, token string, spec e2e.APIStepSpec) {
	t.Helper()

	fx.env.APIStep(ctx, t, token, spec)
}

func (fx *triggersFixture) ann() string { return fx.admin.token.Secret }

// decodeTrigger reads a trigger document that a step answered.
func decodeTrigger(into *triggerDoc) func(string) error {
	return func(body string) error {
		if err := json.Unmarshal([]byte(body), into); err != nil {
			return fmt.Errorf("decode trigger: %w", err)
		}

		return nil
	}
}

func inDoc(fragments ...string) func(string) error {
	return func(body string) error {
		for _, f := range fragments {
			if !strings.Contains(body, f) {
				return fmt.Errorf("%w: body %s, want %s", errTriggerDoc, body, f)
			}
		}

		return nil
	}
}

// savesAndReads saves a schedule trigger and a follows trigger, reads and lists
// them, replaces one and returns the schedule trigger.
func (fx *triggersFixture) savesAndReads(ctx context.Context, t *testing.T) triggerDoc {
	t.Helper()

	var first, follows triggerDoc

	annSubject := e2e.SignInSubject("ann@example.com")

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "save a schedule trigger", Method: http.MethodPost, Path: fx.triggers(fx.webID),
		Body: `{"kind":"schedule","frequency":{"preset":"daily"}}`, Want: http.StatusCreated,
		Check: func(body string) error {
			if err := decodeTrigger(&first)(body); err != nil {
				return err
			}

			return fx.checkFirst(&first, annSubject, body)
		},
	})

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "save a follows trigger", Method: http.MethodPost, Path: fx.triggers(fx.webID),
		Body: `{"kind":"follows","source":{"id":"` + fx.dbID + `"}}`, Want: http.StatusCreated,
		Check: func(body string) error {
			if err := decodeTrigger(&follows)(body); err != nil {
				return err
			}

			if follows.Source == nil || follows.Source.ID != fx.dbID || follows.Source.Name != "db" {
				return fmt.Errorf("%w: %s", errTriggerDoc, body)
			}

			return nil
		},
	})

	fx.readsAndReplaces(ctx, t, first, follows)

	return first
}

// checkFirst checks the schedule trigger that the first save answered.
func (fx *triggersFixture) checkFirst(first *triggerDoc, saver, body string) error {
	if !strings.HasPrefix(first.ID, "trg-") || first.Unit.ID != fx.webID || first.Unit.Tool != "OpenTofu" ||
		first.Unit.Name != "web" || first.LastSaver != saver || first.NeedsNewSave || !first.Enabled {
		return fmt.Errorf("%w: %s", errTriggerDoc, body)
	}

	return nil
}

// readsAndReplaces lists and reads the saved triggers and replaces the
// schedule trigger.
func (fx *triggersFixture) readsAndReplaces(ctx context.Context, t *testing.T, first, follows triggerDoc) {
	t.Helper()

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "list the triggers of the unit", Method: http.MethodGet, Path: fx.triggers(fx.webID), Body: "",
		Want: http.StatusOK, Check: inDoc(`"id":"`+first.ID+`"`, `"id":"`+follows.ID+`"`),
	})

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "read one trigger", Method: http.MethodGet, Path: fx.triggers(fx.webID) + "/" + first.ID, Body: "",
		Want: http.StatusOK, Check: inDoc(`"preset":"daily"`),
	})

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "replace the trigger with a cron expression", Method: http.MethodPut,
		Path: fx.triggers(fx.webID) + "/" + first.ID,
		Body: `{"kind":"schedule","frequency":{"cron":"30 4 * * 1-5"},"enabled":false}`, Want: http.StatusOK,
		Check: inDoc(`"cron":"30 4 * * 1-5"`, `"enabled":false`),
	})

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "a replace cannot change the kind", Method: http.MethodPut,
		Path: fx.triggers(fx.webID) + "/" + first.ID, Body: `{"kind":"review-request-push"}`,
		Want: http.StatusBadRequest, Check: inDoc(`"errorCode":"trigger.invalid"`),
	})

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "switch the trigger on again", Method: http.MethodPut,
		Path: fx.triggers(fx.webID) + "/" + first.ID,
		Body: `{"kind":"schedule","frequency":{"preset":"weekly"}}`, Want: http.StatusOK,
		Check: inDoc(`"enabled":true`),
	})
}

// refusesALoop proves that a loop of units fails with the units named.
func (fx *triggersFixture) refusesALoop(ctx context.Context, t *testing.T) {
	t.Helper()

	// web follows db. A trigger that makes db follow web closes a loop.
	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "refuse a trigger that closes a loop", Method: http.MethodPost, Path: fx.triggers(fx.dbID),
		Body: `{"kind":"follows","source":{"id":"` + fx.webID + `"}}`, Want: http.StatusConflict,
		Check: func(body string) error {
			var doc triggerErrDoc
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				return fmt.Errorf("decode error: %w", err)
			}

			if doc.ErrorCode != "trigger.cycle" || len(doc.Units) != 3 || doc.Units[0] != doc.Units[2] ||
				doc.Units[0].Name != "web" || doc.Units[1].Name != "db" || doc.Units[0].Tool != "OpenTofu" {
				return fmt.Errorf("%w: %s", errTriggerDoc, body)
			}

			return nil
		},
	})

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "a unit that follows itself", Method: http.MethodPost, Path: fx.triggers(fx.webID),
		Body: `{"kind":"follows","source":{"id":"` + fx.webID + `"}}`, Want: http.StatusConflict,
		Check: inDoc(`"errorCode":"trigger.cycle"`),
	})
}

// firesATrigger fires the trigger by hand and reads the job.
func (fx *triggersFixture) firesATrigger(ctx context.Context, t *testing.T, first triggerDoc) {
	t.Helper()

	fire := fx.triggers(fx.webID) + "/" + first.ID + "/fire"
	body := `{"reason":"e2e","location":{"repository":"acme/infra","ref":"main","directory":"net"}}`

	// A plan needs a code location, and a unit has none in its settings yet.
	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "a fire with no code location", Method: http.MethodPost, Path: fire, Body: "",
		Want: http.StatusUnprocessableEntity, Check: inDoc(`"errorCode":"job.unsupported"`),
	})

	// The extra action of a location is outside the actions of a role today, so
	// the bootstrap administrator names the location.
	var jobID string

	startFire := func(name string) {
		fx.env.APIStepA(ctx, t, e2e.APIStepSpec{
			UserAgent: triggersAgent, Name: name, Method: http.MethodPost, Path: fire, Body: body,
			Want: http.StatusCreated, Scheme: "token",
			Check: func(answer string) error {
				var doc fireDoc

				if err := json.Unmarshal([]byte(answer), &doc); err != nil || !strings.HasPrefix(doc.Job.ID, "job-") {
					return fmt.Errorf("%w: %s", errTriggerDoc, answer)
				}

				if jobID != "" && doc.Job.ID != jobID {
					return fmt.Errorf("%w: a double click made job %s, not %s", errTriggerDoc, doc.Job.ID, jobID)
				}

				jobID = doc.Job.ID

				return nil
			},
		})
	}

	startFire("fire the trigger")
	startFire("fire it again: a double click gives the same job")

	fx.env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: triggersAgent, Name: "read the job that the fire started", Method: http.MethodGet,
		Path: "/api/dustpan/v1/namespaces/" + refusalNamespace + "/jobs/" + jobID, Body: "",
		Want: http.StatusOK, Scheme: "token",
		Check: inDoc(`"source":"api"`, `"operation":"plan"`, `"trigger":{"id":"`+first.ID+`","saver":"`+
			e2e.SignInSubject("ann@example.com")+`"}`),
	})

	fx.env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: triggersAgent, Name: "a second fire with another reason in the same minute", Method: http.MethodPost,
		Path: fire, Body: strings.Replace(body, `"e2e"`, `"another"`, 1), Want: http.StatusConflict, Scheme: "token",
		Check: inDoc(`"errorCode":"trigger.fire-conflict"`),
	})

	fx.firesWithAKey(ctx, t, fire, body, jobID)

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "switch the trigger off", Method: http.MethodPut, Path: fx.triggers(fx.webID) + "/" + first.ID,
		Body: `{"kind":"schedule","frequency":{"preset":"weekly"},"enabled":false}`, Want: http.StatusOK, Check: nil,
	})

	fx.env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: triggersAgent, Name: "a trigger that is off is not fired", Method: http.MethodPost, Path: fire,
		Body: body, Want: http.StatusConflict, Scheme: "token", Check: inDoc(`"errorCode":"trigger.disabled"`),
	})
}

// firesWithAKey proves that a caller key gives one job for two fires, that it is
// another job than the one of the fires with no key (previous), and that a key
// that is not valid is refused.
func (fx *triggersFixture) firesWithAKey(ctx context.Context, t *testing.T, fire, body, previous string) {
	t.Helper()

	// A caller key makes a retry safe across a minute boundary: two fires with one
	// key give one job, and it is another job than the one of the fires above.
	var keyed string

	for _, name := range []string{"fire with an Idempotency-Key", "fire again with the same Idempotency-Key"} {
		fx.env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
			UserAgent: triggersAgent, Name: name, Method: http.MethodPost, Path: fire, Body: body,
			Want: http.StatusCreated, Scheme: "token",
			Check: func(answer string) error {
				var doc fireDoc

				if err := json.Unmarshal([]byte(answer), &doc); err != nil || !strings.HasPrefix(doc.Job.ID, "job-") {
					return fmt.Errorf("%w: %s", errTriggerDoc, answer)
				}

				if keyed != "" && doc.Job.ID != keyed {
					return fmt.Errorf("%w: one key made job %s, not %s", errTriggerDoc, doc.Job.ID, keyed)
				}

				if keyed == "" && doc.Job.ID == previous {
					return fmt.Errorf("%w: a key made the job %s of the fires with no key", errTriggerDoc, previous)
				}

				keyed = doc.Job.ID

				return nil
			},
		}, map[string]string{"Idempotency-Key": "triggers-e2e-key-1"})
	}

	fx.env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: triggersAgent, Name: "a fire with an Idempotency-Key that is not valid", Method: http.MethodPost,
		Path: fire, Body: body, Want: http.StatusBadRequest, Scheme: "token",
		Check: inDoc(`"errorCode":"request.idempotency-key-invalid"`),
	}, map[string]string{"Idempotency-Key": strings.Repeat("k", 256)})
}

// refusesASaverWithoutAccess proves that a start refuses a last saver who lost
// access, that the refusal is audited, and that a new save by a person with
// access brings the trigger back.
func (fx *triggersFixture) refusesASaverWithoutAccess(ctx context.Context, t *testing.T) {
	t.Helper()

	var saved triggerDoc

	fx.step(ctx, t, fx.second.token.Secret, e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "a second admin saves a trigger", Method: http.MethodPost, Path: fx.triggers(fx.webID),
		Body: `{"kind":"schedule","frequency":{"preset":"hourly"}}`, Want: http.StatusCreated,
		Check: decodeTrigger(&saved),
	})

	// The second admin loses the grant.
	principalCall(ctx, t, fx.env, principalStep{
		http.MethodDelete, "/api/server/grants/" + fx.second.grant.ID, "", http.StatusNoContent,
	}, nil)

	one := fx.triggers(fx.webID) + "/" + saved.ID

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "the trigger needs a new save", Method: http.MethodGet, Path: one, Body: "", Want: http.StatusOK,
		Check: inDoc(`"needsNewSave":true`),
	})

	fx.env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: triggersAgent, Name: "a start whose last saver lost access is refused", Method: http.MethodPost,
		Path: one + "/fire", Body: "", Want: http.StatusForbidden, Scheme: "token",
		Check: inDoc(`"errorCode":"trigger.saver-denied"`),
	})

	fx.step(ctx, t, fx.second.token.Secret, e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "the saver without access cannot save again", Method: http.MethodPut, Path: one,
		Body: `{"kind":"schedule","frequency":{"preset":"daily"}}`, Want: http.StatusNotFound, Check: nil,
	})

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "a person with access saves it again", Method: http.MethodPut, Path: one,
		Body: `{"kind":"schedule","frequency":{"preset":"hourly"}}`, Want: http.StatusOK,
		Check: inDoc(`"needsNewSave":false`, `"lastSaver":"`+e2e.SignInSubject("ann@example.com")+`"`),
	})

	entries := readAuditEntries(ctx, t, fx.env, refusalNamespace)
	for i := range entries {
		e := &entries[i]
		if e.Outcome == "denied" && e.Action == "unit:run-plan" && strings.Contains(e.Reason, "last saver") {
			return
		}
	}

	t.Fatalf("the namespace log holds no denied entry for the start of %s: %d entries", saved.ID, len(entries))
}

// refusesWhatItMust proves the answers for a viewer, a person with no grant and
// a bad request, and deletes the trigger.
func (fx *triggersFixture) refusesWhatItMust(ctx context.Context, t *testing.T, first triggerDoc) {
	t.Helper()

	web := fx.triggers(fx.webID)
	one := web + "/" + first.ID

	for _, step := range []struct {
		name, method, path, body string
		token                    string
		want                     int
	}{
		{"a viewer lists the triggers", http.MethodGet, web, "", fx.viewer.token.Secret, http.StatusOK},
		{"a viewer reads one trigger", http.MethodGet, one, "", fx.viewer.token.Secret, http.StatusOK},
		{
			"a viewer cannot save", http.MethodPost, web, `{"kind":"review-request-push"}`,
			fx.viewer.token.Secret, http.StatusForbidden,
		},
		{"a viewer cannot delete", http.MethodDelete, one, "", fx.viewer.token.Secret, http.StatusForbidden},
		{"a viewer cannot fire", http.MethodPost, one + "/fire", "", fx.viewer.token.Secret, http.StatusForbidden},
		{"a person with no grant lists nothing", http.MethodGet, web, "", fx.outside.token.Secret, http.StatusNotFound},
		{"a person with no grant cannot delete", http.MethodDelete, one, "", fx.outside.token.Secret, http.StatusNotFound},
		{"an unknown trigger", http.MethodGet, web + "/trg-nope", "", fx.ann(), http.StatusNotFound},
		{"a trigger under another unit", http.MethodGet, fx.triggers(fx.dbID) + "/" + first.ID, "", fx.ann(), http.StatusNotFound},
		{"an unknown unit", http.MethodGet, fx.triggers("ws-nope"), "", fx.ann(), http.StatusNotFound},
		{"a body with an unknown field", http.MethodPost, web, `{"kind":"review-request-push","lastSaver":"user:root"}`, fx.ann(), http.StatusBadRequest},
		{"a body that is no JSON", http.MethodPost, web, `kind=schedule`, fx.ann(), http.StatusBadRequest},
		{"a schedule that runs every minute", http.MethodPost, web, `{"kind":"schedule","frequency":{"cron":"* * * * *"}}`, fx.ann(), http.StatusBadRequest},
		{"a follows trigger on a source that does not exist", http.MethodPost, web, `{"kind":"follows","source":{"id":"ws-nope"}}`, fx.ann(), http.StatusForbidden},
	} {
		fx.step(ctx, t, step.token, e2e.APIStepSpec{
			UserAgent: triggersAgent, Scheme: "token",
			Name: step.name, Method: step.method, Path: step.path, Body: step.body, Want: step.want, Check: nil,
		})
	}

	fx.env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: triggersAgent, Name: "a write with an Idempotency-Key header", Method: http.MethodPost, Path: web,
		Body: `{"kind":"review-request-push"}`, Want: http.StatusBadRequest, Scheme: "token",
		Check: inDoc(`"errorCode":"request.idempotency-key-unsupported"`),
	}, map[string]string{"Idempotency-Key": "k-1"})

	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "delete the trigger", Method: http.MethodDelete, Path: one, Body: "", Want: http.StatusNoContent,
		Check: nil,
	})
	fx.step(ctx, t, fx.ann(), e2e.APIStepSpec{
		UserAgent: triggersAgent, Scheme: "token",
		Name: "the deleted trigger is gone", Method: http.MethodGet, Path: one, Body: "", Want: http.StatusNotFound,
		Check: nil,
	})
}

// writesAuditEntries proves the namespace log names each change of a trigger.
func (fx *triggersFixture) writesAuditEntries(ctx context.Context, t *testing.T) {
	t.Helper()

	var saves, deletes int

	entries := readAuditEntries(ctx, t, fx.env, refusalNamespace)
	for i := range entries {
		e := &entries[i]
		if e.Action != "unit:manage" || e.Outcome != "success" || e.Object != "opentofu:"+refusalNamespace+"/web" {
			continue
		}

		switch {
		case strings.HasPrefix(e.Reason, "save trigger") || strings.HasPrefix(e.Reason, "replace trigger"):
			saves++
		case e.Reason == "delete trigger":
			deletes++
		default:
		}
	}

	if saves < 4 || deletes != 1 {
		t.Fatalf("the namespace log holds %d saves and %d deletes of triggers, want at least 4 and 1", saves, deletes)
	}
}
