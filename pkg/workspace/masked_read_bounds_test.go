package workspace_test

// Hostile tests for the work bound of the masked read of a workspace, the
// shapes that the read cannot read, the deadline after the parse and the walk,
// and the read of a version by the workspace that the grant names. Each case
// sends its input directly to one layer, as if the other layers were absent,
// and each guard has a case that fails when the guard is removed.

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// stateWithAttributes returns a state with one resource instance that has the
// attributes JSON text attrs.
func stateWithAttributes(attrs string) string {
	return `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"lin-w","outputs":{},` +
		`"resources":[{"mode":"managed","type":"t","name":"n","provider":"p",` +
		`"instances":[{"schema_version":0,"attributes":` + attrs + `}]}]}`
}

// nestedAttributes returns attributes that nest depth objects below the
// attributes object, with one leaf at the bottom.
func nestedAttributes(depth int) string {
	return strings.Repeat(`{"a":`, depth) + `0` + strings.Repeat(`}`, depth)
}

// leafAttributes returns attributes with one array of n leaves below a key of
// keyBytes bytes.
func leafAttributes(keyBytes, n int) string {
	return `{"` + strings.Repeat("k", keyBytes) + `":[` + strings.TrimSuffix(strings.Repeat("0,", n), ",") + `]}`
}

// TestMaskedPathsNestingBound proves the depth bound: a document that nests
// 100 levels is read, one that nests 101 is refused with ErrTooLarge before it
// is decoded, and the sum of the nesting of the document and of the attributes
// is what counts.
func TestMaskedPathsNestingBound(t *testing.T) {
	t.Parallel()

	// The state document holds 5 levels above the attributes object: the state,
	// the resources array, the resource, the instances array and the instance.
	const above = 5

	tests := []struct {
		name    string
		depth   int
		wantErr bool
	}{
		{name: "at the bound", depth: workspace.MaskedMaxDepth - above, wantErr: false},
		{name: "one level over", depth: workspace.MaskedMaxDepth - above + 1, wantErr: true},
		{name: "far over", depth: 8000, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := workspace.MaskedPaths([]byte(stateWithAttributes(nestedAttributes(tc.depth))), nil)

			if tc.wantErr {
				if !errors.Is(err, workspace.ErrTooLarge) {
					t.Fatalf("err = %v, want ErrTooLarge", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("err = %v, want none", err)
			}
		})
	}
}

// TestMaskedPathsDepthIgnoresBracketsInStrings proves that the depth count
// skips strings: a key full of brackets and escaped quotes does not count as
// nesting.
func TestMaskedPathsDepthIgnoresBracketsInStrings(t *testing.T) {
	t.Parallel()

	key := strings.Repeat(`[{\"`, 400)

	if _, err := workspace.MaskedPaths([]byte(stateWithAttributes(`{"`+key+`":1}`)), nil); err != nil {
		t.Fatalf("err = %v, want none: brackets in a string are not nesting", err)
	}
}

// TestMaskedPathsMarkerBound proves the bound on the markers of a version:
// 10000 are read, 10001 are refused with ErrTooLarge, for a version with no
// kept marks and for one with kept marks.
func TestMaskedPathsMarkerBound(t *testing.T) {
	t.Parallel()

	t.Run("no kept marks at the bound", func(t *testing.T) {
		t.Parallel()

		state := stateWithAttributes(leafAttributes(1, workspace.MaskedMaxMarkers))

		paths, err := workspace.MaskedPaths([]byte(state), nil)
		if err != nil || len(paths) != workspace.MaskedMaxMarkers {
			t.Fatalf("paths = %d, err = %v, want %d paths", len(paths), err, workspace.MaskedMaxMarkers)
		}
	})

	t.Run("no kept marks over the bound", func(t *testing.T) {
		t.Parallel()

		_, err := workspace.MaskedPaths(
			[]byte(stateWithAttributes(leafAttributes(1, workspace.MaskedMaxMarkers+1))), nil,
		)
		if !errors.Is(err, workspace.ErrTooLarge) {
			t.Fatalf("err = %v, want ErrTooLarge", err)
		}
	})

	t.Run("kept marks over the bound", func(t *testing.T) {
		t.Parallel()

		marks := workspace.SensitiveMarks{"t.n": make([]string, workspace.MaskedMaxMarkers+1)}
		for i := range marks["t.n"] {
			marks["t.n"][i] = fmt.Sprintf("/k/%d", i)
		}

		_, err := workspace.MaskedPaths([]byte(stateWithAttributes(`{"k":[0]}`)), marks)
		if !errors.Is(err, workspace.ErrTooLarge) {
			t.Fatalf("err = %v, want ErrTooLarge", err)
		}
	})
}

// TestMaskedPathsDuplicateMarkersBound proves the bound on the markers that the
// copy of a secret adds: one kept mark names one value, and 10001 copies of it
// answer masked too, so the count after the walk is the second layer of the
// bound on the pointers.
func TestMaskedPathsDuplicateMarkersBound(t *testing.T) {
	t.Parallel()

	copies := strings.TrimSuffix(strings.Repeat(`"v",`, workspace.MaskedMaxMarkers+1), ",")
	state := stateWithAttributes(`{"s":"v","d":[` + copies + `]}`)

	_, err := workspace.MaskedPaths([]byte(state), workspace.SensitiveMarks{"t.n": {"/s"}})
	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// TestMaskedStatePathsOutputsCountAsMarkers proves the bound counts the
// sensitive outputs with the resource markers: 10000 markers in the resources
// and one sensitive output are over the bound.
func TestMaskedStatePathsOutputsCountAsMarkers(t *testing.T) {
	t.Parallel()

	state := strings.Replace(
		stateWithAttributes(leafAttributes(1, workspace.MaskedMaxMarkers)),
		`"outputs":{}`, `"outputs":{"o":{"value":"x","type":"string","sensitive":true}}`, 1,
	)

	_, err := workspace.MaskedStateAndOutputPaths(t.Context(), []byte(state), nil)
	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// TestMaskedPathsPathBytesBound proves the bound on the bytes that the paths
// need: a long key above many values multiplies in each path, so the read
// refuses it with ErrTooLarge although the document is small and has few
// markers.
func TestMaskedPathsPathBytesBound(t *testing.T) {
	t.Parallel()

	_, err := workspace.MaskedPaths([]byte(stateWithAttributes(leafAttributes(1<<20, 40))), nil)
	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// refusedAllocation returns the bytes that one refused masked set allocates.
func refusedAllocation(t *testing.T, state string) uint64 {
	t.Helper()

	data := []byte(state)

	debug.FreeOSMemory()

	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)

	_, err := workspace.MaskedPaths(data, nil)

	runtime.ReadMemStats(&after)

	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}

	return after.TotalAlloc - before.TotalAlloc
}

// TestMaskedPathsRefusalBuildsNoPath proves that a refusal of the work bound
// builds no path: the memory that the refusal allocates stays far below what
// the paths of the same document would need.
func TestMaskedPathsRefusalBuildsNoPath(t *testing.T) {
	// The test counts the allocation of the process, so no other test may run
	// at the same time. t.Setenv forbids t.Parallel, so the test runs alone.
	t.Setenv("DUSTPAN_TEST_ALLOC", "1")

	const limit = 8 << 20

	tests := []struct {
		name  string
		state string
	}{
		{name: "deep nesting", state: stateWithAttributes(nestedAttributes(8000))},
		// 10001 leaves below a key of 2 KiB: the paths would need 20 MB.
		{name: "too many markers", state: stateWithAttributes(leafAttributes(2<<10, workspace.MaskedMaxMarkers+1))},
		// 40 leaves below a key of 1 MiB: the paths would need 40 MB.
		{name: "path bytes", state: stateWithAttributes(leafAttributes(1<<20, 40))},
	}

	for _, tc := range tests {
		if got := refusedAllocation(t, tc.state); got > limit {
			t.Fatalf("%s: a refusal allocated %d bytes, want at most %d", tc.name, got, limit)
		}
	}
}

// checkOverBoundRefusal reads the version that h holds and proves the answer is
// ErrTooLarge with the refusal reason too-large and no page.
func checkOverBoundRefusal(t *testing.T, h *maskedHostile) {
	t.Helper()

	page, err := h.read(t.Context(), latest(), "")
	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}

	if len(page.Markers) != 0 || page.NextCursor != "" {
		t.Fatalf("page = %+v, want an empty page", page)
	}

	if got := h.log.got(); len(got) != 1 || got[0] != workspace.RefusalTooLarge {
		t.Errorf("reasons = %q, want [%q]", got, workspace.RefusalTooLarge)
	}
}

// TestMaskedReadDeepNestingRefused proves the read refuses a version under the
// size limit with a nesting depth above 100: ErrTooLarge, the reason too-large,
// no page. TestMaskedPathsRefusalBuildsNoPath measures the allocation.
func TestMaskedReadDeepNestingRefused(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, stateWithAttributes(nestedAttributes(300)), 4<<20)

	checkOverBoundRefusal(t, h)
}

// TestMaskedReadTooManyMarkersRefused proves the read refuses a version under the
// size limit with more than 10000 markers: ErrTooLarge, the reason too-large,
// no page.
func TestMaskedReadTooManyMarkersRefused(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, stateWithAttributes(leafAttributes(1, workspace.MaskedMaxMarkers+1)), 4<<20)

	checkOverBoundRefusal(t, h)
}

// TestMaskedPathsUnknownShapeFailsClosed proves that a state that the server
// cannot read is refused with ErrUnreadableState and no page, and never read as
// a state with no secrets.
func TestMaskedPathsUnknownShapeFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state string
	}{
		{
			name: "client-encrypted envelope",
			state: `{"serial":1,"lineage":"l","meta":{"key_provider.pbkdf2.k":"AAAA"},` +
				`"encrypted_data":"c2VjcmV0","encryption_version":"v0"}`,
		},
		{name: "resources is not an array", state: `{"version":4,"serial":1,"lineage":"l","resources":{"a":1}}`},
		{name: "resources is a string", state: `{"version":4,"serial":1,"lineage":"l","resources":"x"}`},
		{
			name:  "resource with no name",
			state: `{"version":4,"serial":1,"lineage":"l","resources":[{"mode":"managed","type":"t","instances":[]}]}`,
		},
		{
			name:  "resource with no type",
			state: `{"version":4,"serial":1,"lineage":"l","resources":[{"mode":"managed","name":"n","instances":[]}]}`,
		},
		{name: "resource that is not an object", state: `{"version":4,"serial":1,"lineage":"l","resources":[1]}`},
		{name: "document that is an array", state: `[{"resources":[]}]`},
		{name: "document that is a string", state: `"secret-text-in-error"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			paths, err := workspace.MaskedPaths([]byte(tc.state), nil)
			if !errors.Is(err, workspace.ErrUnreadableState) {
				t.Fatalf("paths = %q, err = %v, want ErrUnreadableState", paths, err)
			}

			if strings.Contains(err.Error(), "secret-text-in-error") || strings.Contains(err.Error(), "c2VjcmV0") {
				t.Errorf("error %q holds bytes of the state", err)
			}
		})
	}
}

// TestMaskedPathsEmptyStateIsNotUnreadable proves the fail-closed check does not
// refuse a state that has no resources: that state has no secrets.
func TestMaskedPathsEmptyStateIsNotUnreadable(t *testing.T) {
	t.Parallel()

	for _, state := range []string{
		`{"version":4,"serial":1,"lineage":"l","outputs":{},"resources":[]}`,
		`{"version":4,"serial":1,"lineage":"l","outputs":{}}`,
	} {
		paths, err := workspace.MaskedPaths([]byte(state), nil)
		if err != nil || len(paths) != 0 {
			t.Fatalf("paths = %q, err = %v, want no paths and no error", paths, err)
		}
	}
}

// TestMaskedReadUnknownShapeFailsClosed proves the read refuses a version whose
// shape it cannot read with ErrUnreadableState, no page, and no refusal count:
// the caller did nothing wrong. TestMaskedPathsUnknownShapeFailsClosed holds the
// table of shapes.
func TestMaskedReadUnknownShapeFailsClosed(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, `{"version":4,"serial":1,"lineage":"l","resources":{"a":1}}`, 4<<20)

	page, err := h.read(t.Context(), latest(), "")
	if !errors.Is(err, workspace.ErrUnreadableState) {
		t.Fatalf("err = %v, want ErrUnreadableState", err)
	}

	if len(page.Markers) != 0 {
		t.Fatalf("page = %+v, want an empty page", page)
	}

	if got := h.log.got(); len(got) != 0 {
		t.Errorf("reasons = %q, want none", got)
	}
}

// singleShotCtx is a context whose Err answers the deadline on the call number
// at and nil on every other call. It counts the calls.
type singleShotCtx struct {
	calls *atomic.Int64
	at    int64
}

// Deadline implements context.Context: the context has no deadline of its own.
func (c singleShotCtx) Deadline() (time.Time, bool) { return time.Time{}, false }

// Done implements context.Context: the context is never canceled by itself.
func (c singleShotCtx) Done() <-chan struct{} { return nil }

// Value implements context.Context: the context holds no value.
func (c singleShotCtx) Value(any) any { return nil }

// Err implements context.Context.
func (c singleShotCtx) Err() error {
	if c.calls.Add(1) == c.at {
		return context.DeadlineExceeded
	}

	return nil
}

// maskedReadContextChecks is the number of calls to Err of the context in one
// read of the test state.
const maskedReadContextChecks = 5

// TestMaskedReadNeverAnswersPastTheDeadline proves the read checks the deadline
// after the parse and after the walk. A context that expires at any one check
// of the read gives no page: removing the check after the parse or the check
// after the walk makes the read answer 200 for the call that the check owns.
func TestMaskedReadNeverAnswersPastTheDeadline(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, stateWithAttributes(`{"k":[0,1]}`), 4<<20)

	count := &atomic.Int64{}

	if _, err := h.read(singleShotCtx{calls: count, at: 0}, latest(), ""); err != nil {
		t.Fatalf("read with no deadline: %v", err)
	}

	// The checks of one read: the start, the chunk loop of the load, the load
	// itself, the parse, and the walk. A change of this number must be
	// deliberate: the sweep below proves each check by its own position.
	total := count.Load()
	if total != maskedReadContextChecks {
		t.Fatalf("the read checked the context %d times, want %d", total, maskedReadContextChecks)
	}

	for k := int64(1); k <= total; k++ {
		page, err := h.read(singleShotCtx{calls: &atomic.Int64{}, at: k}, latest(), "")
		if err == nil {
			t.Errorf("deadline at check %d of %d: read answered a page with %d markers", k, total, len(page.Markers))
		}
	}
}

// TestMaskedReadDeadlineAfterTheWalkReportsTheDeadline proves the refusal after
// the walk is the deadline refusal with its reason, not a plain error.
func TestMaskedReadDeadlineAfterTheWalkReportsTheDeadline(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, stateWithAttributes(`{"k":[0,1]}`), 4<<20)

	count := &atomic.Int64{}
	if _, err := h.read(singleShotCtx{calls: count, at: 0}, latest(), ""); err != nil {
		t.Fatalf("read with no deadline: %v", err)
	}

	// The last check of the read is the one after the walk.
	_, err := h.read(singleShotCtx{calls: &atomic.Int64{}, at: count.Load()}, latest(), "")
	if !errors.Is(err, workspace.ErrReadDeadline) {
		t.Fatalf("err = %v, want ErrReadDeadline", err)
	}

	if got := h.log.got(); len(got) != 1 || got[0] != workspace.RefusalDeadline {
		t.Errorf("reasons = %q, want [%q]", got, workspace.RefusalDeadline)
	}
}

// naiveStates is a state store that ignores the workspace in the lookup of one
// version, as a store without the second layer would. It lets a test reach the
// check of the service alone.
type naiveStates struct {
	*memStore
}

// GetWorkspaceStateVersion answers the version of any workspace by its ID.
func (n naiveStates) GetWorkspaceStateVersion(
	ctx context.Context, _ namespace.Name, _ workspace.ID, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	return n.GetStateVersion(ctx, id)
}

// serviceOverStore returns a service over the stores of f, with states as the
// state store.
func serviceOverStore(t *testing.T, f fixture, states workspace.StateStore) *workspace.Service {
	t.Helper()

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard: nil, OnDeleteWait: nil, ManualLocks: nil,
		Workspaces: f.store, States: states, Blobs: f.blobs, Namespaces: f.namespaces,
		OutOfScope: f.refusals, Sealer: f.secrets, Logger: nil,
		Now:           func() time.Time { return *f.now },
		MaxStateBytes: 4 << 20, MaxPendingVersions: 0, Retention: 0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// writeStateOf writes state as the next version of the workspace w of f, and
// returns its ID.
func writeStateOf(t *testing.T, f fixture, w workspace.Workspace, state string) workspace.StateVersionID {
	t.Helper()

	if _, err := f.svc.LockWithID(t.Context(), w.ID, "alice", "apply", "lock-"+w.ID.String()); err != nil {
		t.Fatalf("lock: %v", err)
	}

	sv, err := f.svc.WriteState(
		t.Context(), w.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-" + w.ID.String(), MD5: ""},
		streamAuthority(noOverrides(), nil), strings.NewReader(state),
	)
	if err != nil {
		t.Fatalf("write state: %v", err)
	}

	return sv.ID
}

// siblings holds the versions of the other workspaces of a test: one in the
// same namespace, one in another namespace, and the ID of the second.
type siblings struct {
	sameNS    workspace.StateVersionID
	otherNS   workspace.StateVersionID
	foreignID workspace.ID
}

// otherWorkspaces makes a second workspace in the namespace of f and a third
// in another namespace, writes a state in each, and returns their versions.
func otherWorkspaces(t *testing.T, f fixture) siblings {
	t.Helper()

	name, err := workspace.ParseName("sibling")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	sibling, err := f.svc.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name}, memPlacement("acme"))
	if err != nil {
		t.Fatalf("create sibling: %v", err)
	}

	beta := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: *f.now, Name: "beta", PrimaryInstallation: "local",
		FailoverVersion: 1, Overrides: noOverrides(),
	}

	if err = f.store.CreateNamespace(t.Context(), beta); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	foreign, err := f.svc.Create(t.Context(), workspace.Ref{Namespace: "beta", Name: name}, memPlacement("beta"))
	if err != nil {
		t.Fatalf("create foreign: %v", err)
	}

	secretState := stateWithAttributes(`{"other-workspace-secret":"x"}`)

	return siblings{
		sameNS: writeStateOf(t, f, sibling, secretState), otherNS: writeStateOf(t, f, foreign, secretState),
		foreignID: foreign.ID,
	}
}

// TestMaskedReadRefusesAVersionOfAnotherWorkspace proves a pinned version is
// read only through the workspace that owns it. A caller with unit:read on one
// workspace names the version ID of another workspace, in the same namespace
// and in another one, and gets ErrStateVersionNotFound and no marker path. Each
// layer is tested alone: the service with a store that ignores the workspace,
// and the service over the store lookup that takes the workspace.
func TestMaskedReadRefusesAVersionOfAnotherWorkspace(t *testing.T) {
	t.Parallel()

	for _, layer := range []string{"service check alone", "store lookup alone"} {
		t.Run(layer, func(t *testing.T) {
			t.Parallel()

			h, _ := newMaskedHostile(t, stateWithAttributes(`{"own":"x"}`), 4<<20)
			others := otherWorkspaces(t, h.f)

			if layer == "service check alone" {
				h.f.svc = serviceOverStore(t, h.f, naiveStates{memStore: h.f.store})
			}

			checkNotReadable(t, h, "same namespace", others.sameNS)
			checkNotReadable(t, h, "other namespace", others.otherNS)

			// The version of the workspace itself still reads.
			if _, err := h.read(t.Context(), workspace.VersionSelector{Latest: false, ID: h.id}, ""); err != nil {
				t.Errorf("own version: %v", err)
			}
		})
	}
}

// checkNotReadable proves the pinned version id answers ErrStateVersionNotFound
// and no marker through the workspace of h.
func checkNotReadable(t *testing.T, h *maskedHostile, name string, id workspace.StateVersionID) {
	t.Helper()

	page, err := h.read(t.Context(), workspace.VersionSelector{Latest: false, ID: id}, "")
	if !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Errorf("%s: err = %v, want ErrStateVersionNotFound", name, err)
	}

	if len(page.Markers) != 0 || len(page.ValuePaths) != 0 {
		t.Errorf("%s: page = %+v, want no marker of the other workspace", name, page)
	}
}

// TestMaskedReadRefusesAVersionThatIsNotFinalized proves a pinned version that
// is not finalized is not read, although it belongs to the workspace.
func TestMaskedReadRefusesAVersionThatIsNotFinalized(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, stateWithAttributes(`{"own":"x"}`), 4<<20)

	pending := func() workspace.StateVersionID {
		h.f.store.mu.Lock()
		defer h.f.store.mu.Unlock()

		sv := h.f.store.versions[h.id]
		sv.ID = "sv-pending-copy"
		// The size names a content over the limit. The status check of the read
		// is the first layer that sees the version; the check of the content
		// stream would refuse it as not finalized too, but it comes later, so a
		// read with no status check answers ErrTooLarge here.
		sv.Size = workspace.MaskedMaxVersionBytes + 1
		sv.Status = workspace.StatusPending
		h.f.store.versions[sv.ID] = sv

		return sv.ID
	}()

	page, err := h.read(t.Context(), workspace.VersionSelector{Latest: false, ID: pending}, "")
	if !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("err = %v, want ErrStateVersionNotFound", err)
	}

	if len(page.Markers) != 0 {
		t.Fatalf("page = %+v, want an empty page", page)
	}
}

// swapAuthorizer runs hook inside the authorization of the read, between the
// lookup of the handler and the read of the state, and then answers as grants.
type swapAuthorizer struct {
	grantAuthorizer

	once sync.Once
	hook func()
}

// Authorize implements identity.Authorizer.
func (s *swapAuthorizer) Authorize(
	ctx context.Context, p identity.Principal, a identity.Action, o identity.Object,
) error {
	s.once.Do(s.hook)

	return s.grantAuthorizer.Authorize(ctx, p, a, o)
}

// TestMaskedReadReadsByTheAuthorizedID proves the read follows the stable ID
// that the grant named, not the name. If the workspace is deleted and made again
// with the same name between the check and the read, the new workspace has
// another ID, and the read refuses it with ErrNotFound: the state of the new
// workspace is not read under the check of the old one.
func TestMaskedReadReadsByTheAuthorizedID(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, stateWithAttributes(`{"old":"x"}`), 4<<20)

	swap := &swapAuthorizer{grantAuthorizer: h.authz, once: sync.Once{}, hook: func() { replaceWorkspace(t, h) }}

	page, err := h.f.svc.ReadMasked(t.Context(), workspace.ReadMaskedArgs{
		Ref: h.f.ws.Ref, Object: h.obj, Selector: latest(), Principal: person("pat"),
		Authorizer: swap, Key: testCursorKey(), Sink: h.log.sink(), Cursor: "",
	})
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	if len(page.Markers) != 0 {
		t.Fatalf("page = %+v, want no marker of the new workspace", page)
	}
}

// replaceWorkspace deletes the workspace of h and makes it again with the same
// name and another state.
func replaceWorkspace(t *testing.T, h *maskedHostile) {
	t.Helper()

	if _, err := h.f.svc.Unlock(t.Context(), h.f.ws.ID, "alice"); err != nil {
		t.Errorf("unlock: %v", err)
	}

	if err := h.f.svc.Delete(t.Context(), h.f.ws.Ref, retentionEntry()); err != nil {
		t.Errorf("delete: %v", err)
	}

	again, err := h.f.svc.Create(t.Context(), h.f.ws.Ref, memPlacement("acme"))
	if err != nil {
		t.Errorf("create again: %v", err)

		return
	}

	writeStateOf(t, h.f, again, stateWithAttributes(`{"new-unit-secret":"x"}`))
}

// TestMaskedReadRefusesAnObjectWithNoUnitID proves a read with no stable ID in
// the object is refused with ErrNotFound.
func TestMaskedReadRefusesAnObjectWithNoUnitID(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, stateWithAttributes(`{"old":"x"}`), 4<<20)

	bare, err := identity.UnitObject("opentofu", h.f.ws.Ref.Namespace, h.f.ws.Ref.Name.String())
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	h.authz = grantAuthorizer{grants: map[string][]identity.Action{bare.String(): {identity.ActionUnitRead}}}
	h.obj = bare

	if _, err = h.read(t.Context(), latest(), ""); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestMaskedReadRefusesAnIDOfAnotherNamespace proves the namespace of the unit
// that the ID names must be the namespace of the object: the grant names a unit
// of "acme" with the ID of a workspace of "beta".
func TestMaskedReadRefusesAnIDOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, stateWithAttributes(`{"old":"x"}`), 4<<20)
	foreign := otherWorkspaces(t, h.f).foreignID

	other := h.obj.WithUnitID(foreign.String())
	h.obj = other
	h.authz = grantAuthorizer{grants: map[string][]identity.Action{other.String(): {identity.ActionUnitRead}}}

	if _, err := h.read(t.Context(), latest(), ""); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
