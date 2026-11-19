package pulumi_test

import (
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

// writeCase is one state write of the Pulumi API. target builds the path from
// the paths of the running update and of an update that is not started. lease
// says that the write presents the lease token. journaled says that the
// running update uses the journal. recorded says that the route has a
// principal, so the refusal also writes an entry to the audit log.
type writeCase struct {
	name   string
	method string
	target func(running, pending updatePaths) string
	body   string
	// seed is what the test does before the stack leaves its collection.
	seed seedKind
	// lease, journaled and recorded are the traits of the route.
	lease     bool
	journaled bool
	recorded  bool
}

// seedKind names the state that a case needs before the stack leaves its
// collection.
type seedKind int

const (
	// seedNone needs nothing but the running update.
	seedNone seedKind = iota
	// seedCheckpoint saves a checkpoint, so that a delta has a base.
	seedCheckpoint
	// seedState imports a state and starts no update, so that a restore has a
	// version to copy.
	seedState
)

func fixedTarget(path string) func(running, pending updatePaths) string {
	return func(updatePaths, updatePaths) string { return path }
}

func stateWrites() []writeCase {
	const base = "/api/stacks/acme/web/prod"

	return []writeCase{
		{
			name: "create update", method: http.MethodPost, target: fixedTarget(base + "/update"), body: `{}`,
			lease: false, journaled: false, recorded: true, seed: seedNone,
		},
		{
			name: "create preview", method: http.MethodPost, target: fixedTarget(base + "/preview"), body: `{}`,
			lease: false, journaled: false, recorded: true, seed: seedNone,
		},
		{
			name: "start update", method: http.MethodPost, body: `{}`,
			target: func(_, pending updatePaths) string { return pending.Start },
			lease:  false, journaled: false, recorded: true, seed: seedNone,
		},
		{
			name: "checkpoint", method: http.MethodPatch,
			target: func(running, _ updatePaths) string { return running.Checkpoint },
			body:   `{"version":3,"deployment":{"resources":[]}}`,
			lease:  true, journaled: false, recorded: false, seed: seedNone,
		},
		{
			name: "checkpoint verbatim", method: http.MethodPatch,
			target: func(running, _ updatePaths) string { return running.Verbatim },
			body:   `{"version":3,"sequenceNumber":1,"untypedDeployment":{"deployment":{}}}`,
			lease:  true, journaled: false, recorded: false, seed: seedNone,
		},
		{
			name: "checkpoint delta", method: http.MethodPatch,
			target: func(running, _ updatePaths) string { return running.Delta },
			body: `{"version":3,"checkpointHash":` + strconv.Quote(deltaHash(`{"deployment":{"a":1,"b":3}}`)) +
				`,"sequenceNumber":2,"deploymentDelta":[` + deltaEditJSON(25, 26, "3") + `]}`,
			lease: true, journaled: false, recorded: false, seed: seedCheckpoint,
		},
		{
			name:   "journal entries",
			method: http.MethodPatch,
			target: func(running, _ updatePaths) string {
				return strings.Replace(running.Events, "/events/batch", "/journalentries", 1)
			},
			body:      `{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
			lease:     true,
			journaled: true,
			recorded:  false,
			seed:      seedNone,
		},
		{
			name: "import", method: http.MethodPost, target: fixedTarget(base + "/import"),
			body:  `{"version":3,"deployment":{}}`,
			lease: false, journaled: false, recorded: true, seed: seedNone,
		},
		{
			name: "restore", method: http.MethodPost, target: fixedTarget(base + "/restore/1"), body: ``,
			lease: false, journaled: false, recorded: true, seed: seedState,
		},
	}
}

// refusedWrite sends tc to a stack that no collection holds any more. The
// stack was in a collection when the update started, as a stack is that lost
// its collection while an update runs.
func refusedWrite(t *testing.T, tc writeCase) refusedAnswer {
	t.Helper()

	placement := &placementSwitch{unplaced: atomic.Bool{}}

	f := newUpdateFixtureWithAuthenticator(t, allowAll(), tokenAuthenticator{}, fixtureOptions{
		manual: nil, checker: nil, limit: 0, cutoff: 0, replayer: nil, guard: nil, placer: nil,
		placement: placement,
	})

	pending, err := f.updates.Create(
		t.Context(), f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create pending update: %v", err)
	}

	var runningID stack.UpdateID

	lease := ""

	if tc.seed == seedState {
		if _, err = f.updates.Import(t.Context(), f.ref, "seed", seedInput()); err != nil {
			t.Fatalf("seed state: %v", err)
		}
	} else {
		runningID, lease = startedUpdate(t, f, tc.journalVersion())
	}

	if tc.seed == seedCheckpoint {
		seeded := `{"version":3,"untypedDeployment":{"deployment":{"a":1,"b":2}},"sequenceNumber":1}`
		status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, mintedPaths(runningID).Verbatim, lease, seeded))

		if status != http.StatusOK {
			t.Fatalf("seed checkpoint answers %d (%s)", status, body)
		}
	}

	before := historyLen(t, f)

	placement.Unplace()

	target := tc.target(mintedPaths(runningID), mintedPaths(pending.ID))
	req := accessRequest(tc.method, target, tc.body)

	if tc.lease {
		req = leaseRequest(tc.method, target, lease, tc.body)
	}

	status, body := f.doUpdate(t, req)

	return refusedAnswer{
		status: status, body: body, denied: noCollectionDenials(f), grown: historyLen(t, f) - before,
	}
}

// refusedAnswer is what a write to a stack in no collection got.
type refusedAnswer struct {
	body   string
	denied []audit.Entry
	status int
	// grown is the number of updates that the write added to history.
	grown int
}

// journalVersion is the journal version of the running update of the case.
func (tc writeCase) journalVersion() int64 {
	if tc.journaled {
		return 1
	}

	return 0
}

func startedUpdate(t *testing.T, f updateFixture, version int64) (stack.UpdateID, string) {
	t.Helper()

	u, err := f.updates.Create(
		t.Context(), f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.StartWithJournalVersion(t.Context(), f.ref, u.ID, version)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return u.ID, started.Token.Reveal()
}

func noCollectionDenials(f updateFixture) []audit.Entry {
	f.audit.mu.Lock()
	defer f.audit.mu.Unlock()

	var out []audit.Entry

	for i := range f.audit.entries {
		if f.audit.entries[i].Outcome == audit.OutcomeDenied {
			out = append(out, f.audit.entries[i])
		}
	}

	return out
}

func historyLen(t *testing.T, f updateFixture) int {
	t.Helper()

	history, err := f.updates.History(t.Context(), f.ref, stack.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("history: %v", err)
	}

	return len(history)
}

// TestWritesToAStackInNoCollectionAreRefused sends every state write of the
// Pulumi API to a stack that no collection holds. Each write must answer 409
// with the code unit.no-collection and the command that helps, change nothing,
// and, on a route with a principal, write an audit entry that names the
// reason.
func TestWritesToAStackInNoCollectionAreRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range stateWrites() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := refusedWrite(t, tc)
			if got.status != http.StatusConflict {
				t.Fatalf("answers %d, want %d (%s)", got.status, http.StatusConflict, got.body)
			}

			if !strings.Contains(got.body, "unit.no-collection") ||
				!strings.Contains(got.body, "PUT /api/inventory/unit-collection") {
				t.Fatalf("body %q lacks the code or the command that helps", got.body)
			}

			if got.grown != 0 {
				t.Fatalf("refused write added %d updates to history", got.grown)
			}

			if tc.recorded {
				requireNoCollectionEntry(t, got.denied)
			}
		})
	}
}

func requireNoCollectionEntry(t *testing.T, denied []audit.Entry) {
	t.Helper()

	for i := range denied {
		if denied[i].Action == string(identity.ActionUnitWrite) &&
			strings.Contains(denied[i].Reason, "stack in no collection") {
			return
		}
	}

	t.Fatalf(
		"no denied audit entry for %s with the reason %q: %+v",
		identity.ActionUnitWrite,
		"stack in no collection",
		denied,
	)
}

// TestReadsOfAStackInNoCollectionStayAllowed proves that the check refuses
// writes only.
func TestReadsOfAStackInNoCollectionStayAllowed(t *testing.T) {
	t.Parallel()

	placement := &placementSwitch{unplaced: atomic.Bool{}}
	f := newUpdateFixtureWithAuthenticator(t, allowAll(), tokenAuthenticator{}, fixtureOptions{
		manual: nil, checker: nil, limit: 0, cutoff: 0, replayer: nil, guard: nil, placer: nil,
		placement: placement,
	})
	id, _ := f.mintStartedMutating(t)

	placement.Unplace()

	for _, target := range []string{
		"/api/stacks/acme/web/prod/export",
		"/api/stacks/acme/web/prod/updates",
		mintedPaths(id).Get,
	} {
		status, body := f.doUpdate(t, accessRequest(http.MethodGet, target, ""))
		if status == http.StatusConflict || status >= http.StatusInternalServerError {
			t.Errorf("GET %s answers %d (%s), want a read", target, status, body)
		}
	}
}

// TestWriteRefusalWritesNoEntryForAGuest keeps the audit entry tied to a
// principal: the refusal of a request with no sign-in is a 401 and writes no
// no-collection entry.
func TestWriteRefusalWritesNoEntryForAGuest(t *testing.T) {
	t.Parallel()

	placement := &placementSwitch{unplaced: atomic.Bool{}}
	f := newUpdateFixtureWithAuthenticator(t, allowAll(), tokenAuthenticator{}, fixtureOptions{
		manual: nil, checker: nil, limit: 0, cutoff: 0, replayer: nil, guard: nil, placer: nil,
		placement: placement,
	})

	placement.Unplace()

	status, _ := f.doUpdate(t, anonymousRequest(http.MethodPost, "/api/stacks/acme/web/prod/update", `{}`))
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous create answers %d, want %d", status, http.StatusUnauthorized)
	}

	for _, e := range noCollectionDenials(f) {
		if strings.Contains(e.Reason, "stack in no collection") {
			t.Fatalf("a guest wrote a no-collection entry: %+v", e)
		}
	}
}

func seedInput() stack.CheckpointInput {
	return stack.CheckpointInput{
		Deployment: []byte(`{"resources":[]}`), SchemaVersion: 3, Sequence: 0, Invalid: false, Features: nil,
	}
}
