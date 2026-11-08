package workspace_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// spyBlobs counts the calls that reach the blob store.
type spyBlobs struct {
	blob.Store

	mu    sync.Mutex
	calls int
}

func newSpyBlobs(store blob.Store) *spyBlobs {
	return &spyBlobs{Store: store, mu: sync.Mutex{}, calls: 0}
}

func (s *spyBlobs) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	s.count()

	return s.Store.Put(ctx, key, r)
}

func (s *spyBlobs) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	s.count()

	return s.Store.Get(ctx, key)
}

func (s *spyBlobs) Delete(ctx context.Context, key blob.Key) error {
	s.count()

	return s.Store.Delete(ctx, key)
}

func (s *spyBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	s.count()

	return s.Store.ListPage(ctx, req)
}

// total returns the calls that reached the store.
func (s *spyBlobs) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

func (s *spyBlobs) count() {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
}

// scopeEnv is a server with two namespaces, acme and other, and one finalized
// state version of the workspace in acme.
type scopeEnv struct {
	fixture

	spy   *spyBlobs
	acme  namespace.Namespace
	other namespace.Namespace
	sv    workspace.StateVersion
}

const scopeState = `{"version":4,"serial":1,"lineage":"lin-1","outputs":{}}`

func newScopeEnv(t *testing.T) *scopeEnv {
	t.Helper()

	spy := newSpyBlobs(openFixtureBlobs(t))
	f := newFixtureWithBlobs(t, spy)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lin-1", 1, scopeState), scopeState); err != nil {
		t.Fatalf("push state: %v", err)
	}

	sv, err := f.store.CurrentStateVersion(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current state version: %v", err)
	}

	acme, err := f.namespaces.Get(ctx, "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	other := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           acme.CreatedAt,
		Name:                "other",
		PrimaryInstallation: "local",
		FailoverVersion:     1,
		Overrides:           noOverrides(),
	}

	if createErr := f.store.CreateNamespace(ctx, other); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	return &scopeEnv{fixture: f, spy: spy, acme: acme, other: other, sv: sv}
}

// foreign returns the content key of the version with the namespace ID of
// the other namespace.
func (e *scopeEnv) foreign() blob.Key {
	return blob.Key(strings.Replace(e.sv.Content.String(), e.acme.ID.String(), e.other.ID.String(), 1))
}

// TestStateContentLivesUnderTheNamespacePrefix proves the layout of the key of
// a state version, and that no blob lands under the old layout.
func TestStateContentLivesUnderTheNamespacePrefix(t *testing.T) {
	t.Parallel()

	e := newScopeEnv(t)

	want := "namespaces/" + e.acme.ID.String() + "/tofu/" + e.ws.ID.String() + "/" + e.sv.ID.String() + "/"
	if !strings.HasPrefix(e.sv.Content.String(), want) {
		t.Fatalf("content key = %q, want prefix %q", e.sv.Content, want)
	}

	if attempt := strings.TrimPrefix(e.sv.Content.String(), want); attempt == "" || strings.Contains(attempt, "/") {
		t.Fatalf("attempt segment = %q, want one non-empty segment", attempt)
	}

	if listed, err := blobtest.ListAll(t.Context(), e.blobs, "tofu/"); err != nil || len(listed) != 0 {
		t.Fatalf("blobs under the old layout = %d, %v, want none", len(listed), err)
	}

	listed, err := blobtest.ListAll(t.Context(), e.blobs, "namespaces/")
	if err != nil || len(listed) != 1 || listed[0].Key != e.sv.Content {
		t.Fatalf("blobs under namespaces/ = %v, %v, want only %q", listed, err, e.sv.Content)
	}

	elsewhere := blob.Key("namespaces/" + e.other.ID.String() + "/" +
		strings.TrimPrefix(e.sv.Content.String(), "namespaces/"+e.acme.ID.String()+"/"))

	if _, err := e.blobs.Get(t.Context(), elsewhere); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("content under the other namespace: err = %v, want ErrNotFound", err)
	}
}

// TestStateBlobReadsRefuseOtherNamespaces is the blob scope test: each read
// of state content with a key outside the namespace of the workspace gives
// blob.ErrOutOfScope, no call reaches the blob store, and the refusal reaches
// the recorder once, in the namespace of the call.
func TestStateBlobReadsRefuseOtherNamespaces(t *testing.T) {
	t.Parallel()

	ws := func(e *scopeEnv) string { return e.ws.ID.String() }
	rest := func(e *scopeEnv) string {
		return strings.TrimPrefix(e.sv.Content.String(), "namespaces/"+e.acme.ID.String()+"/tofu/")
	}

	cases := []struct {
		name string
		call func(t *testing.T, e *scopeEnv) error
	}{
		{"OpenStateBlob, key of another namespace", func(t *testing.T, e *scopeEnv) error {
			t.Helper()

			_, err := e.svc.OpenStateBlob(t.Context(), e.foreign())

			return err
		}},
		{"Open, version names a key of another namespace", func(t *testing.T, e *scopeEnv) error {
			t.Helper()

			sv := e.sv
			sv.Content = e.foreign()
			_, err := e.svc.Open(t.Context(), sv)

			return err
		}},
		{"Open, version names a key of another directory of the namespace", func(t *testing.T, e *scopeEnv) error {
			t.Helper()

			sv := e.sv
			sv.Content = blob.Key("namespaces/" + e.acme.ID.String() + "/item-keys/ik-1/v1")
			_, err := e.svc.Open(t.Context(), sv)

			return err
		}},
		{"Open, version names a key of the old layout", func(t *testing.T, e *scopeEnv) error {
			t.Helper()

			sv := e.sv
			sv.Content = blob.Key("tofu/" + rest(e))
			_, err := e.svc.Open(t.Context(), sv)

			return err
		}},
		{"Open, version names a key whose namespace ID only starts like ours", func(t *testing.T, e *scopeEnv) error {
			t.Helper()

			sv := e.sv
			sv.Content = blob.Key("namespaces/" + e.acme.ID.String() + "x/tofu/" + rest(e))
			_, err := e.svc.Open(t.Context(), sv)

			return err
		}},
		{"Open, version names a key with a dot segment", func(t *testing.T, e *scopeEnv) error {
			t.Helper()

			sv := e.sv
			sv.Content = blob.Key("namespaces/" + e.acme.ID.String() + "/tofu/" + ws(e) + "/../x")
			_, err := e.svc.Open(t.Context(), sv)

			return err
		}},
		{"Open, version names a key with an upper-case namespace ID", func(t *testing.T, e *scopeEnv) error {
			t.Helper()

			sv := e.sv
			sv.Content = blob.Key(
				strings.Replace(sv.Content.String(), e.acme.ID.String(), strings.ToUpper(e.acme.ID.String()), 1),
			)
			_, err := e.svc.Open(t.Context(), sv)

			return err
		}},
		{"Restore, the source version names a key of another namespace", func(t *testing.T, e *scopeEnv) error {
			t.Helper()

			svc := serviceWith(t, e.fixture, rewriteStates{StateStore: e.store, content: e.foreign(), superseded: ""})

			_, err := svc.Restore(t.Context(), e.ws.ID, e.sv.ID, "alice")

			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newScopeEnv(t)
			before := e.spy.total()

			err := tc.call(t, e)
			if !errors.Is(err, blob.ErrOutOfScope) {
				t.Fatalf("err = %v, want blob.ErrOutOfScope", err)
			}

			if calls := e.spy.total() - before; calls != 0 {
				t.Errorf("calls that reached the blob store = %d, want 0", calls)
			}

			checkOneRefusal(t, e, "get")
			checkErrorHidesKeys(t, e, err)
		})
	}
}

// TestStateBlobDeleteRefusesOtherNamespaces proves the delete of a key that a
// row names: a superseded attempt in another namespace is refused with
// blob.ErrOutOfScope, nothing reaches the blob store, and the refusal reaches
// the recorder.
func TestStateBlobDeleteRefusesOtherNamespaces(t *testing.T) {
	t.Parallel()

	e := newScopeEnv(t)

	pending, err := e.svc.BeginState(
		t.Context(), e.ws.ID, "alice", newState("lin-1", 2, scopeState), authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	svc := serviceWith(t, e.fixture, rewriteStates{StateStore: e.store, content: "", superseded: e.foreign()})
	before := e.spy.total()

	_, err = svc.Upload(t.Context(), pending.Version.ID, pending.Upload, strings.NewReader(scopeState))
	if !errors.Is(err, blob.ErrOutOfScope) {
		t.Fatalf("upload: err = %v, want blob.ErrOutOfScope", err)
	}

	if calls := e.spy.total() - before; calls != 0 {
		t.Errorf("calls that reached the blob store = %d, want 0", calls)
	}

	checkOneRefusal(t, e, "delete")
	checkErrorHidesKeys(t, e, err)

	if _, err := e.blobs.Get(t.Context(), e.sv.Content); err != nil {
		t.Errorf("current content after the refused delete: %v", err)
	}
}

// TestDeletePurgedBlobsStaysInTheNamespace proves that the blob delete of a
// purged workspace reads only the directory of the namespace ID that the caller
// gives, and that an empty ID fails instead of falling back.
func TestDeletePurgedBlobsStaysInTheNamespace(t *testing.T) {
	t.Parallel()

	e := newScopeEnv(t)

	if err := e.svc.DeletePurgedBlobs(t.Context(), e.other.ID, e.ws.ID); err != nil {
		t.Fatalf("delete purged blobs in the other namespace: %v", err)
	}

	if _, err := e.blobs.Get(t.Context(), e.sv.Content); err != nil {
		t.Fatalf("content of acme after a delete in the other namespace: %v", err)
	}

	if err := e.svc.DeletePurgedBlobs(t.Context(), "", e.ws.ID); !errors.Is(err, blob.ErrInvalidKey) {
		t.Fatalf("delete purged blobs with no namespace ID: err = %v, want blob.ErrInvalidKey", err)
	}

	if err := e.svc.DeletePurgedBlobs(t.Context(), e.acme.ID, e.ws.ID); err != nil {
		t.Fatalf("delete purged blobs in acme: %v", err)
	}

	if left := listStateBlobs(t, e.blobs, e.ws.ID); len(left) != 0 {
		t.Fatalf("state blobs after the delete in acme = %d, want 0", len(left))
	}

	if refusals := e.refusals.All(); len(refusals) != 0 {
		t.Fatalf("refusals = %v, want none for a call in scope", refusals)
	}
}

// TestParseStateBlobKey is the boundary check of a state content key.
func TestParseStateBlobKey(t *testing.T) {
	t.Parallel()

	const ns = "ns-abcdefghijklmnopqrst"

	var zero workspace.StateBlobKey

	good := workspace.StateBlobKey{Namespace: ns, Workspace: "ws-1", Version: "sv-1", Attempt: "a1"}

	cases := []struct {
		name string
		key  string
		want workspace.StateBlobKey
		ok   bool
	}{
		{"a key of the layout", "namespaces/" + ns + "/tofu/ws-1/sv-1/a1", good, true},
		{"the old layout", "tofu/ws-1/sv-1/a1", zero, false},
		{"no attempt", "namespaces/" + ns + "/tofu/ws-1/sv-1", zero, false},
		{"a part too many", "namespaces/" + ns + "/tofu/ws-1/sv-1/a1/b", zero, false},
		{"another family", "namespaces/" + ns + "/pulumi/ws-1/sv-1/a1", zero, false},
		{"a family that only starts like ours", "namespaces/" + ns + "/tofux/ws-1/sv-1/a1", zero, false},
		{"the item key directory", "namespaces/" + ns + "/item-keys/ik-1/v1", zero, false},
		{"no namespaces directory", "spaces/" + ns + "/tofu/ws-1/sv-1/a1", zero, false},
		{"an upper-case namespace ID", "namespaces/NS-ABCDEFGHIJKLMNOPQRST/tofu/ws-1/sv-1/a1", zero, false},
		{"a namespace ID with a bad character", "namespaces/ns-abc$def/tofu/ws-1/sv-1/a1", zero, false},
		{"a namespace name for the ID", "namespaces/acme/tofu/ws-1/sv-1/a1", zero, false},
		{"an empty workspace segment", "namespaces/" + ns + "/tofu//sv-1/a1", zero, false},
		{"an empty attempt segment", "namespaces/" + ns + "/tofu/ws-1/sv-1/", zero, false},
		{"an empty key", "", zero, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := workspace.ParseStateBlobKey(blob.Key(tc.key))
			if !tc.ok {
				if !errors.Is(err, workspace.ErrStateVersionNotFound) {
					t.Fatalf("err = %v, want ErrStateVersionNotFound", err)
				}

				return
			}

			if err != nil || got != tc.want {
				t.Fatalf("got %+v, %v, want %+v", got, err, tc.want)
			}
		})
	}
}

// TestOpenStateBlobRefusesTheOldLayout proves that the key of the old layout
// opens nothing, also when it names a real version: the server keeps no
// reader for it.
func TestOpenStateBlobRefusesTheOldLayout(t *testing.T) {
	t.Parallel()

	e := newScopeEnv(t)
	old := blob.Key("tofu/" + strings.TrimPrefix(e.sv.Content.String(), "namespaces/"+e.acme.ID.String()+"/tofu/"))
	before := e.spy.total()

	if _, err := e.svc.OpenStateBlob(t.Context(), old); !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("open the old layout: err = %v, want ErrStateVersionNotFound", err)
	}

	if calls := e.spy.total() - before; calls != 0 {
		t.Errorf("calls that reached the blob store = %d, want 0", calls)
	}

	r, err := e.svc.OpenStateBlob(t.Context(), e.sv.Content)
	if err != nil {
		t.Fatalf("open the key of the version: %v", err)
	}

	if closeErr := r.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
}

// TestOpenStateBlobRecordsAKeyOfAnotherWorkspace proves that a key which names
// another workspace than the row of its version is refused with
// ErrStateVersionNotFound, reaches the blob store never, and writes one
// security refusal in the namespace of the row, in the place "unit directory".
func TestOpenStateBlobRecordsAKeyOfAnotherWorkspace(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"another workspace ID":     "ws-other",
		"a look-alike of ours":     "x",
		"the ID of ours, extended": "ws-extended-",
	}

	for name, prefix := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newScopeEnv(t)
			own := "/tofu/" + e.ws.ID.String() + "/"
			key := blob.Key(strings.Replace(e.sv.Content.String(), own, "/tofu/"+prefix+e.ws.ID.String()+"/", 1))
			before := e.spy.total()

			_, err := e.svc.OpenStateBlob(t.Context(), key)
			if !errors.Is(err, workspace.ErrStateVersionNotFound) {
				t.Fatalf("err = %v, want ErrStateVersionNotFound", err)
			}

			checkErrorHidesKey(t, key, err)

			if calls := e.spy.total() - before; calls != 0 {
				t.Errorf("calls that reached the blob store = %d, want 0", calls)
			}

			got := e.refusals.All()
			if len(got) != 1 || got[0].Namespace != e.acme.ID || got[0].Op != "get" ||
				got[0].Where != scopeaudit.OutsideUnitDirectory {
				t.Fatalf("refusals = %+v, want one get refusal in %s outside the unit directory", got, e.acme.ID)
			}
		})
	}
}

// TestOpenStateBlobErrorsHideTheKey proves that a refusal names the operation
// and, when it is known, the version or the workspace, never the key that the
// caller supplied.
func TestOpenStateBlobErrorsHideTheKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		key   func(e *scopeEnv) blob.Key
		want  error
		holds []string
	}{
		{
			name: "a malformed key",
			key: func(e *scopeEnv) blob.Key {
				return blob.Key("namespaces/" + e.acme.ID.String() + "/tofu/" + e.ws.ID.String())
			},
			want:  workspace.ErrStateVersionNotFound,
			holds: nil,
		},
		{
			name: "a version that does not exist",
			key: func(e *scopeEnv) blob.Key {
				return blob.Key("namespaces/" + e.acme.ID.String() + "/tofu/" + e.ws.ID.String() + "/sv-ghost/a1")
			},
			want:  workspace.ErrStateVersionNotFound,
			holds: []string{"sv-ghost"},
		},
		{
			name: "a key of another workspace",
			key: func(e *scopeEnv) blob.Key {
				return blob.Key(strings.Replace(
					e.sv.Content.String(), "/tofu/"+e.ws.ID.String()+"/", "/tofu/ws-other/", 1,
				))
			},
			want:  workspace.ErrStateVersionNotFound,
			holds: []string{"ws-other"},
		},
		{
			name: "a key of another version",
			key: func(e *scopeEnv) blob.Key {
				return blob.Key(strings.Replace(
					e.sv.Content.String(), "/"+e.sv.ID.String()+"/", "/sv-other/", 1,
				))
			},
			want:  workspace.ErrStateVersionNotFound,
			holds: []string{"sv-other"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newScopeEnv(t)
			key := tc.key(e)

			_, err := e.svc.OpenStateBlob(t.Context(), key)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}

			if !strings.Contains(err.Error(), "open state blob") {
				t.Errorf("error text %q does not name the operation", err)
			}

			for _, want := range tc.holds {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error text %q does not hold %q", err, want)
				}
			}

			checkErrorHidesKey(t, key, err)
		})
	}
}

// TestRefusalRecordFailureStillRefuses proves the refusal stands when the
// recorder fails: the caller still gets blob.ErrOutOfScope, with the record
// failure beside it.
func TestRefusalRecordFailureStillRefuses(t *testing.T) {
	t.Parallel()

	e := newScopeEnv(t)
	e.refusals.Err = errRecordDown

	_, err := e.svc.OpenStateBlob(t.Context(), e.foreign())
	if !errors.Is(err, blob.ErrOutOfScope) || !errors.Is(err, errRecordDown) {
		t.Fatalf("err = %v, want blob.ErrOutOfScope joined with the record failure", err)
	}
}

// errRecordDown is the failure of a recorder in the tests.
var errRecordDown = errors.New("audit log down")

// checkOneRefusal checks that the recorder holds exactly one refusal, in the
// namespace of the call, for op, with a known place.
func checkOneRefusal(t *testing.T, e *scopeEnv, op string) {
	t.Helper()

	got := e.refusals.All()
	if len(got) != 1 || got[0].Namespace != e.acme.ID || got[0].Op != op ||
		(got[0].Where != scopeaudit.OutsideNamespace && got[0].Where != scopeaudit.OutsideDirectory) {
		t.Fatalf("refusals = %+v, want one %s refusal in %s", got, op, e.acme.ID)
	}
}

// checkErrorHidesKeys checks that the text of err names neither the ID of the
// other namespace nor a key.
func checkErrorHidesKeys(t *testing.T, e *scopeEnv, err error) {
	t.Helper()

	text := err.Error()
	if strings.Contains(text, e.other.ID.String()) || strings.Contains(text, "namespaces/") {
		t.Errorf("error text %q names a namespace ID or a key", text)
	}
}

// checkErrorHidesKey checks that the text of err does not hold key.
func checkErrorHidesKey(t *testing.T, key blob.Key, err error) {
	t.Helper()

	if strings.Contains(err.Error(), key.String()) {
		t.Errorf("error text %q names the key %q", err, key)
	}
}

// rewriteStates wraps a state store so that a test plants a key in a row that
// the service reads: content replaces the content key of every version that
// GetStateVersion returns, and superseded is what RecordUploadAttempt reports
// as the attempt that it replaced.
type rewriteStates struct {
	workspace.StateStore

	content    blob.Key
	superseded blob.Key
}

func (r rewriteStates) GetStateVersion(
	ctx context.Context,
	id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	sv, err := r.StateStore.GetStateVersion(ctx, id)
	if err != nil {
		return workspace.StateVersion{}, err
	}

	if r.content != "" && sv.Status == workspace.StatusFinalized {
		sv.Content = r.content
	}

	return sv, nil
}

func (r rewriteStates) RecordUploadAttempt(
	ctx context.Context, id workspace.StateVersionID, key blob.Key, fence namespace.Fence,
) (blob.Key, error) {
	if r.superseded == "" {
		return r.StateStore.RecordUploadAttempt(ctx, id, key, fence)
	}

	if _, err := r.StateStore.RecordUploadAttempt(ctx, id, key, fence); err != nil {
		return "", err
	}

	return r.superseded, nil
}

// serviceWith returns a second service over the stores of f, with states as
// its state store.
func serviceWith(t *testing.T, f fixture, states workspace.StateStore) *workspace.Service {
	t.Helper()

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.store,
		States:             states,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         f.refusals,
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}
