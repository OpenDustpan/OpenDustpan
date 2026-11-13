//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// lockAgent is the User-Agent of the manual lock requests. The route coverage
// check counts requests that do not carry the harness agent.
const lockAgent = "opendustpan-e2e-manual-locks"

// lockReasonText is the reason that the scenarios give. The CLI output and the
// API answers must hold it.
const lockReasonText = "change window closed"

// lockedCollection is the collection that the cross-tool test locks.
const lockedCollection = "locked-set"

// tofuUnitRef returns the unit reference of the e2e workspace.
func tofuUnitRef() string {
	return "opentofu:" + e2e.Namespace + "/" + e2e.Workspace
}

// pulumiUnitRef returns the unit reference of the e2e stack.
func pulumiUnitRef() string {
	return "pulumi:" + e2e.Namespace + "/" + e2e.Project + "/" + stackName
}

// unitLockPath returns the unit lock route for the unit reference ref.
func unitLockPath(ref string) string {
	return "/api/inventory/unit-lock?unit=" + url.QueryEscape(ref)
}

// unitForcePath returns the unit force-unlock route for the unit reference ref.
func unitForcePath(ref string) string {
	return "/api/inventory/unit-lock/force-unlock?unit=" + url.QueryEscape(ref)
}

// lockBody returns the JSON body of a lock request.
func lockBody(reason string) string {
	return `{"reason":"` + reason + `"}`
}

// lockActor names the principal that sends a lock request.
type lockActor int

const (
	// actorA is principal A, the admin of the scenarios.
	actorA lockActor = iota
	// actorB is principal B, a second admin.
	actorB
)

// lockCall runs one lock request as the actor.
func lockCall(t *testing.T, env *e2e.Env, actor lockActor, c collectionCall) {
	t.Helper()

	spec := e2e.APIStepSpec{
		UserAgent: lockAgent, Name: c.name, Method: c.method, Path: c.path, Body: c.body, Want: c.want,
		Check: c.check, Scheme: "",
	}

	if actor == actorB {
		env.APIStepB(t.Context(), t, spec)

		return
	}

	env.APIStepA(t.Context(), t, spec)
}

// lockLifecycle drives the lock routes of one object: path is its lock route and
// force its force-unlock route. Principal A takes the lock, B cannot take it again
// or remove it, A removes it, and B force-unlocks a second lock of A.
func lockLifecycle(t *testing.T, env *e2e.Env, object, path, force string) {
	t.Helper()

	steps := []struct {
		actor lockActor
		call  collectionCall
	}{
		{actorA, collectionCall{
			name: object + ": lock without a reason", method: http.MethodPut, path: path,
			body: `{}`, want: http.StatusBadRequest, check: nil,
		}},
		{actorA, collectionCall{
			name: object + ": lock", method: http.MethodPut, path: path, body: lockBody(lockReasonText),
			want: http.StatusOK, check: collectionHolds(`"holder":"` + e2e.Subject + `"`),
		}},
		{actorB, collectionCall{
			name: object + ": a second lock is refused", method: http.MethodPut, path: path,
			body: lockBody("other"), want: http.StatusConflict, check: collectionHolds(e2e.Subject),
		}},
		{actorB, collectionCall{
			name: object + ": read the lock", method: http.MethodGet, path: path, body: "",
			want: http.StatusOK, check: collectionHolds(`"reason":"` + lockReasonText + `"`),
		}},
		{actorB, collectionCall{
			name: object + ": another principal cannot remove the lock", method: http.MethodDelete, path: path,
			body: "", want: http.StatusConflict, check: nil,
		}},
		{actorA, collectionCall{
			name: object + ": the holder removes the lock", method: http.MethodDelete, path: path,
			body: "", want: http.StatusNoContent, check: nil,
		}},
		{actorA, collectionCall{
			name: object + ": a removed lock is not found", method: http.MethodGet, path: path,
			body: "", want: http.StatusNotFound, check: nil,
		}},
		{actorA, collectionCall{
			name: object + ": lock again", method: http.MethodPut, path: path, body: lockBody(lockReasonText),
			want: http.StatusOK, check: nil,
		}},
		{actorB, collectionCall{
			name: object + ": force-unlock", method: http.MethodPost, path: force, body: "",
			want: http.StatusNoContent, check: nil,
		}},
		{actorA, collectionCall{
			name: object + ": a force-unlocked lock is not found", method: http.MethodGet, path: path,
			body: "", want: http.StatusNotFound, check: nil,
		}},
	}

	for _, s := range steps {
		lockCall(t, env, s.actor, s.call)
	}
}

// manualLockRoutesScenario drives the manual lock routes of the e2e workspace
// and of its collection, and proves that an OpenTofu plan fails while a lock
// covers the workspace and works after the lock goes. It runs after the
// collection scenario, so the workspace is in a collection.
func manualLockRoutesScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	collectionPath := collectionsBase + "/platform/lock"

	lockLifecycle(t, env, "unit", unitLockPath(tofuUnitRef()), unitForcePath(tofuUnitRef()))
	lockLifecycle(t, env, "collection", collectionPath, collectionPath+"/force-unlock")

	for _, object := range []struct {
		name, path, remove string
	}{
		{"unit", unitLockPath(tofuUnitRef()), unitLockPath(tofuUnitRef())},
		{"collection", collectionPath, collectionPath},
	} {
		lockCall(t, env, actorA, collectionCall{
			name: object.name + " lock before a plan", method: http.MethodPut, path: object.path,
			body: lockBody(lockReasonText), want: http.StatusOK, check: nil,
		})

		stepName := "plan under the " + object.name + " lock"
		env.Step(ctx, t, e2e.StepSpec{
			Check:  checkPlanRefusedByManualLock(env, stepName),
			Name:   stepName,
			Args:   []string{"plan", "-input=false", "-no-color", "-lock-timeout=0s"},
			Env:    nil,
			Stdin:  "",
			Fail:   true,
			Redact: false,
		})

		lockCall(t, env, actorA, collectionCall{
			name: object.name + " remove after a plan", method: http.MethodDelete, path: object.remove,
			body: "", want: http.StatusNoContent, check: nil,
		})
	}

	env.Step(ctx, t, step("plan after the manual locks", nil, "plan", "-input=false", "-no-color"))
}

// checkPlanRefusedByManualLock checks a plan that the manual lock stopped: the CLI
// failed on the lock, and the lock request of the plan answered 409 with a detail
// that names the holder and the reason. go-tfe discards the detail of a 409 on a
// lock request, so the CLI prints its own line and the API answer is the proof.
func checkPlanRefusedByManualLock(env *e2e.Env, stepName string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if out := r.Stdout + r.Stderr; !strings.Contains(out, "workspace already locked") {
			return fmt.Errorf("%w: plan output lacks the lock failure: %s", errUnexpected, out)
		}

		entries := env.Recorder.Entries()
		for i := range entries {
			if isLockRequestOfStep(&entries[i], stepName) {
				return checkLockRefusal(&entries[i])
			}
		}

		return fmt.Errorf("%w: plan made no lock request", errUnexpected)
	}
}

// isLockRequestOfStep reports whether e is the lock request of the step.
func isLockRequestOfStep(e *e2e.Entry, stepName string) bool {
	return e.Step == stepName && e.Method == http.MethodPost && strings.HasSuffix(e.Path, "/actions/lock")
}

// checkLockRefusal checks that the lock request e answered 409 with a detail that
// names the holder, the reason and the manual lock.
func checkLockRefusal(e *e2e.Entry) error {
	if e.Status != http.StatusConflict {
		return fmt.Errorf("%w: lock during plan answers %d, want %d", errUnexpected, e.Status, http.StatusConflict)
	}

	for _, want := range []string{e2e.Subject, lockReasonText, "manual lock"} {
		if !strings.Contains(e.ResponseBody, want) {
			return fmt.Errorf("%w: lock detail lacks %q: %s", errUnexpected, want, e.ResponseBody)
		}
	}

	return nil
}

// outputNamesTheLock checks that the output of a refused CLI names the holder
// and the reason of the manual lock.
func outputNamesTheLock(r e2e.Result) error {
	out := r.Stdout + r.Stderr
	for _, want := range []string{e2e.Subject, lockReasonText, "manual lock"} {
		if !strings.Contains(out, want) {
			return fmt.Errorf("%w: output lacks %q: %s", errUnexpected, want, out)
		}
	}

	return nil
}

// TestManualLockStopsBothTools proves, on one server and on each store, that a
// manual lock on a collection stops OpenTofu and the Pulumi CLI for the units in
// it: `tofu plan` takes its remote lock and fails, and `pulumi up` fails with a
// message that names the holder and the reason. After the lock goes, both work.
func TestManualLockStopsBothTools(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// Both CLIs must trust the test server's certificate, and Go
		// honors SSL_CERT_FILE only on Linux.
		t.Skipf("the combined suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runManualLockOnStore(t, fetcher, db)
		})
	}
}

// lockE2E is a server with an OpenTofu workspace and a Pulumi stack that both
// work, and the two CLIs that drive them.
type lockE2E struct {
	env       *e2e.Env
	pulumiCLI e2e.Install
	tofuCLI   e2e.Install
	qualified string
}

// newLockE2E installs both CLIs, starts the server on store, and proves that
// both units work before any lock.
func newLockE2E(t *testing.T, fetcher *e2e.Fetcher, store e2e.Store) lockE2E {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	pulumiVersion, tofuVersion := e2e.Pulumi().Single(), e2e.OpenTofu().Single()
	if pulumiVersion == "" || tofuVersion == "" {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	pulumiCLI, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersion)
	if err != nil {
		t.Fatalf("install pulumi: %v", err)
	}

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersion)
	if err != nil {
		t.Fatalf("install tofu: %v", err)
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, tofuCLI, store)
	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	// Both units exist and work before any lock.
	env.Step(ctx, t, step("tofu init", nil, "init", "-input=false", "-no-color"))
	env.Step(ctx, t, step("tofu apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"))

	env.SetCLI(pulumiCLI)
	env.Step(ctx, t, step("pulumi login", contains(env.URL), "login"))
	env.Step(ctx, t, step("pulumi stack init", nil, "stack", "init", qualified))
	env.Step(ctx, t, step("pulumi stack init other", nil, "stack", "init", "other"))
	env.Step(ctx, t, step("pulumi stack select", nil, "stack", "select", qualified))
	env.Step(ctx, t, step("pulumi up before the lock", nil, "up", "--yes", "--non-interactive"))

	return lockE2E{env: env, pulumiCLI: pulumiCLI, tofuCLI: tofuCLI, qualified: qualified}
}

func runManualLockOnStore(t *testing.T, fetcher *e2e.Fetcher, store e2e.Store) {
	t.Helper()

	lab := newLockE2E(t, fetcher, store)
	ctx, env, pulumiCLI, tofuCLI := t.Context(), lab.env, lab.pulumiCLI, lab.tofuCLI

	// Both units go into one collection, and the collection gets the lock. The
	// workspace holds a tool lock first, so the test can force-unlock it later.
	collectionPath := collectionsBase + "/" + lockedCollection + "/lock"
	toolLock := "/api/v2/workspaces/" + env.WorkspaceID(ctx, t) + "/actions/lock"
	toolForce := "/api/v2/workspaces/" + env.WorkspaceID(ctx, t) + "/actions/force-unlock"

	for _, c := range []collectionCall{
		{
			name: "make the collection", method: http.MethodPost, path: collectionsBase,
			body: collectionBody(t, lockedCollection), want: http.StatusCreated, check: nil,
		},
		{
			name: "move the workspace into the collection", method: http.MethodPut,
			path: "/api/inventory/unit-collection?unit=" + url.QueryEscape(tofuUnitRef()),
			body: moveBody(t, lockedCollection), want: http.StatusOK, check: nil,
		},
		{
			name: "move the stack into the collection", method: http.MethodPut,
			path: "/api/inventory/unit-collection?unit=" + url.QueryEscape(pulumiUnitRef()),
			body: moveBody(t, lockedCollection), want: http.StatusOK, check: nil,
		},
		{
			name: "take the tool lock of the workspace", method: http.MethodPost, path: toolLock,
			body: `{"data":{}}`, want: http.StatusOK, check: nil,
		},
		{
			name: "lock the collection", method: http.MethodPut, path: collectionPath,
			body: lockBody(lockReasonText), want: http.StatusOK, check: nil,
		},
		{
			name: "a tool force-unlock ends the tool lock", method: http.MethodPost, path: toolForce,
			body: "", want: http.StatusOK, check: nil,
		},
		{
			name: "the manual lock stays after the tool force-unlock", method: http.MethodGet, path: collectionPath,
			body: "", want: http.StatusOK, check: collectionHolds(lockReasonText),
		},
	} {
		lockCall(t, env, actorA, c)
	}

	// The inventory shows the flag on both units.
	lockCall(t, env, actorA, collectionCall{
		name: "the inventory flags both units", method: http.MethodGet,
		path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/units", body: "", want: http.StatusOK,
		check: func(body string) error {
			if got := strings.Count(body, `"manualLock"`); got != 2 {
				return fmt.Errorf("%w: %d manualLock flags in %s, want 2", errAnswerLacksFragment, got, body)
			}

			if got := strings.Count(body, `"state":"locked"`); got != 2 {
				return fmt.Errorf("%w: %d locked states in %s, want 2", errAnswerLacksFragment, got, body)
			}

			return nil
		},
	})

	// The Pulumi CLI fails and names the holder and the reason.
	env.Step(ctx, t, failStep("pulumi up under the lock", outputNamesTheLock, "up", "--yes", "--non-interactive"))

	// OpenTofu fails on its remote lock.
	env.SetCLI(tofuCLI)

	stepName := "tofu plan under the lock"
	env.Step(ctx, t, e2e.StepSpec{
		Check: checkPlanRefusedByManualLock(env, stepName), Name: stepName,
		Args: []string{"plan", "-input=false", "-no-color", "-lock-timeout=0s"},
		Env:  nil, Stdin: "", Fail: true, Redact: false,
	})

	lockCall(t, env, actorA, collectionCall{
		name: "the lock stays after the failed plan", method: http.MethodGet, path: collectionPath, body: "",
		want: http.StatusOK, check: collectionHolds(lockReasonText),
	})

	// After the unlock, both tools work.
	lockCall(t, env, actorA, collectionCall{
		name: "unlock the collection", method: http.MethodDelete, path: collectionPath, body: "",
		want: http.StatusNoContent, check: nil,
	})

	env.Step(ctx, t, step("tofu plan after the unlock", nil, "plan", "-input=false", "-no-color"))

	env.SetCLI(pulumiCLI)
	env.Step(ctx, t, step("pulumi up after the unlock", nil, "up", "--yes", "--non-interactive"))
}

// TestManualLockOnNamespaceStopsTheChangeRoutes proves, on each store, that a
// manual lock on a namespace stops every unit in it: the lock life cycle works
// on the namespace route, `tofu plan` takes its remote lock and fails,
// and the Pulumi CLI fails to update, import, rename and remove the stack, with
// a message that names the holder, the reason and the namespace. The stack and
// the workspace are in no collection. After the unlock, both tools work.
func TestManualLockOnNamespaceStopsTheChangeRoutes(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the combined suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runNamespaceLockOnStore(t, fetcher, db)
		})
	}
}

// outputNamesTheNamespaceLock checks that the output of a refused CLI names the
// holder, the reason and the locked namespace.
func outputNamesTheNamespaceLock(r e2e.Result) error {
	if err := outputNamesTheLock(r); err != nil {
		return err
	}

	if out := r.Stdout + r.Stderr; !strings.Contains(out, "in namespace "+e2e.Namespace) {
		return fmt.Errorf("%w: output lacks the namespace: %s", errUnexpected, out)
	}

	return nil
}

func runNamespaceLockOnStore(t *testing.T, fetcher *e2e.Fetcher, store e2e.Store) {
	t.Helper()

	lab := newLockE2E(t, fetcher, store)
	ctx, env := t.Context(), lab.env
	namespacePath := "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/lock"

	// The export that the import step sends back, taken before the lock.
	exported := env.Step(ctx, t, step("stack export before the namespace lock", nil, "stack", "export"))
	importPath := writeImportFile(t, exported.Stdout)

	lockLifecycle(t, env, "namespace", namespacePath, namespacePath+"/force-unlock")

	lockCall(t, env, actorA, collectionCall{
		name: "lock the namespace", method: http.MethodPut, path: namespacePath, body: lockBody(lockReasonText),
		want: http.StatusOK, check: collectionHolds(`"kind":"namespace"`),
	})

	// The inventory shows the flag on both units, though neither is in a collection.
	lockCall(t, env, actorA, collectionCall{
		name: "the inventory flags both units", method: http.MethodGet,
		path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/units", body: "", want: http.StatusOK,
		check: func(body string) error {
			if got := strings.Count(body, `"kind":"namespace"`); got < 2 {
				return fmt.Errorf("%w: %d namespace flags in %s, want at least 2", errAnswerLacksFragment, got, body)
			}

			return nil
		},
	})

	env.Step(ctx, t, failStep("pulumi up under the namespace lock", outputNamesTheNamespaceLock,
		"up", "--yes", "--non-interactive"))
	env.Step(ctx, t, failStep("pulumi stack import under the namespace lock", outputNamesTheNamespaceLock,
		"stack", "import", "--file", importPath))
	env.Step(ctx, t, failStep("pulumi stack rename under the namespace lock", outputNamesTheNamespaceLock,
		"stack", "rename", lab.qualified+"-renamed", "--stack", lab.qualified))
	env.Step(ctx, t, failStep("pulumi stack rm under the namespace lock", outputNamesTheNamespaceLock,
		"stack", "rm", "--yes", "--force", "--stack", lab.qualified))

	// Preview still works: it changes nothing.
	env.Step(ctx, t, step("pulumi preview under the namespace lock", nil, "preview", "--non-interactive"))

	env.SetCLI(lab.tofuCLI)

	stepName := "tofu plan under the namespace lock"
	env.Step(ctx, t, e2e.StepSpec{
		Check: checkPlanRefusedByManualLock(env, stepName), Name: stepName,
		Args: []string{"plan", "-input=false", "-no-color", "-lock-timeout=0s"},
		Env:  nil, Stdin: "", Fail: true, Redact: false,
	})

	lockCall(t, env, actorA, collectionCall{
		name: "unlock the namespace", method: http.MethodDelete, path: namespacePath, body: "",
		want: http.StatusNoContent, check: nil,
	})

	env.Step(ctx, t, step("tofu plan after the unlock", nil, "plan", "-input=false", "-no-color"))

	env.SetCLI(lab.pulumiCLI)
	env.Step(ctx, t, step("pulumi stack import after the unlock", nil, "stack", "import", "--file", importPath))
	env.Step(ctx, t, step("pulumi up after the unlock", nil, "up", "--yes", "--non-interactive"))
}
