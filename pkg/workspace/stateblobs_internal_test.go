package workspace

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

const (
	acmeID  = namespace.ID("ns-acmeacmeacmeacmeacme")
	otherID = namespace.ID("ns-otherotherotherother")
)

var (
	errNotUsed   = errors.New("not used")
	errAuditDown = errors.New("audit log down")
)

// scopeNamespace returns a namespace of the tests.
func scopeNamespace(id namespace.ID, name namespace.Name) namespace.Namespace {
	return namespace.Namespace{
		CreatedAt: time.Time{}, ID: id, Name: name, HomeRegion: "local",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	}
}

// scopeNamespaces is a namespace store with a fixed set of namespaces.
type scopeNamespaces struct {
	byName map[namespace.Name]namespace.Namespace
}

func (s scopeNamespaces) CreateNamespace(context.Context, namespace.Namespace) error {
	return errNotUsed
}

func (s scopeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	ns, ok := s.byName[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (s scopeNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return errNotUsed
}

func (s scopeNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	return nil, errNotUsed
}

func (s scopeNamespaces) Ping(context.Context) error { return nil }

// callCountingStore is a blob store in memory that counts the calls.
type callCountingStore struct {
	mu      sync.Mutex
	objects map[blob.Key][]byte
	calls   int
}

func (s *callCountingStore) Put(_ context.Context, key blob.Key, r io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	data, err := io.ReadAll(r)
	if err != nil {
		return err //nolint:wrapcheck // A test double.
	}

	s.objects[key] = data

	return nil
}

func (s *callCountingStore) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	data, ok := s.objects[key]
	if !ok {
		return nil, blob.ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *callCountingStore) Delete(_ context.Context, key blob.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++
	delete(s.objects, key)

	return nil
}

func (s *callCountingStore) List(_ context.Context, prefix string) ([]blob.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	var out []blob.Object

	for key := range s.objects {
		if strings.HasPrefix(key.String(), prefix) {
			out = append(out, blob.Object{Key: key, ModifiedAt: time.Time{}})
		}
	}

	return out, nil
}

func (s *callCountingStore) Ping(context.Context) error { return nil }

func (s *callCountingStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

// refusalLog is an OutOfScopeRecorder that keeps the refusals.
type refusalLog struct {
	mu  sync.Mutex
	got []string
	err error
}

func (l *refusalLog) RecordOutOfScope(_ context.Context, ns namespace.ID, op string, _ secret.OutOfScopeWhere) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.got = append(l.got, ns.String()+" "+op)

	return l.err
}

func (l *refusalLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string(nil), l.got...)
}

func newScopeBlobs(t *testing.T) (*stateBlobs, *callCountingStore, *refusalLog) {
	t.Helper()

	nsStore := scopeNamespaces{byName: map[namespace.Name]namespace.Namespace{
		"acme":  scopeNamespace(acmeID, "acme"),
		"other": scopeNamespace(otherID, "other"),
	}}

	namespaces, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	store := &callCountingStore{mu: sync.Mutex{}, objects: map[blob.Key][]byte{}, calls: 0}
	refusals := &refusalLog{mu: sync.Mutex{}, got: nil, err: nil}

	return &stateBlobs{store: store, namespaces: namespaces, refusals: refusals}, store, refusals
}

// hostileKey is one key that the use-time check must refuse. want is the error
// of the refusal; recorded reports that the refusal reaches the recorder.
type hostileKey struct {
	name     string
	key      string
	want     error
	recorded bool
}

// hostileOp is one operation of the state blobs that takes a key.
type hostileOp struct {
	name string
	op   string
	call func(b *stateBlobs, r io.Reader, key blob.Key) error
}

// TestStateBlobsRefuseHostileKeys sends hostile keys straight to the use-time
// check of the state blobs, as if the parser and the handler had passed them:
// each call is refused before the store, and a key that crosses a namespace
// boundary is recorded.
func TestStateBlobsRefuseHostileKeys(t *testing.T) {
	t.Parallel()

	own := "namespaces/" + acmeID.String() + "/tofu/ws-1/sv-1/"

	cases := []hostileKey{
		{
			"a key of another namespace",
			"namespaces/" + otherID.String() + "/tofu/ws-1/sv-1/a1",
			blob.ErrOutOfScope,
			true,
		},
		{
			"a key of another directory",
			"namespaces/" + acmeID.String() + "/pulumi/st-1/up-1/1",
			blob.ErrOutOfScope,
			true,
		},
		{"the item key directory", "namespaces/" + acmeID.String() + "/item-keys/ik-1/v1", blob.ErrOutOfScope, true},
		{"the old layout", "tofu/ws-1/sv-1/a1", blob.ErrOutOfScope, true},
		{
			"an ID that only starts like ours",
			"namespaces/" + acmeID.String() + "x/tofu/ws-1/sv-1/a1",
			blob.ErrOutOfScope,
			true,
		},
		{"the parent of the scope", "namespaces/", blob.ErrOutOfScope, true},
		{"an empty key", "", blob.ErrOutOfScope, true},
		{"a dot segment", "namespaces/" + acmeID.String() + "/tofu/ws-1/../a1", blob.ErrOutOfScope, true},
		{
			"a dot segment for the workspace",
			"namespaces/" + acmeID.String() + "/tofu/../sv-1/a1",
			blob.ErrOutOfScope,
			true,
		},
		{"a control character", own + "a\x00", blob.ErrOutOfScope, true},
		{"a line break", own + "a\nb", blob.ErrOutOfScope, true},
		{"a backslash", own + `a\..\b`, blob.ErrOutOfScope, true},
	}

	ops := []hostileOp{
		{"put", opPut, func(b *stateBlobs, r io.Reader, key blob.Key) error {
			return b.put(context.Background(), "acme", key, r)
		}},
		{"get", opGet, func(b *stateBlobs, _ io.Reader, key blob.Key) error {
			rc, err := b.get(context.Background(), "acme", key)
			if rc != nil {
				err = errors.Join(err, rc.Close())
			}

			return err
		}},
		{"delete", opDelete, func(b *stateBlobs, _ io.Reader, key blob.Key) error {
			return b.delete(context.Background(), "acme", key)
		}},
		{"deleteRetried", opDelete, func(b *stateBlobs, _ io.Reader, key blob.Key) error {
			return b.deleteRetried(context.Background(), "acme", key)
		}},
	}

	for _, tc := range cases {
		for _, op := range ops {
			t.Run(op.name+" "+tc.name, func(t *testing.T) {
				t.Parallel()

				checkHostileKey(t, tc, op)
			})
		}
	}
}

// checkHostileKey calls the operation with the key and checks the refusal: the
// error, no call to the store, no byte read from the body, and the record of
// the refusal when it is due.
func checkHostileKey(t *testing.T, tc hostileKey, op hostileOp) {
	t.Helper()

	b, store, refusals := newScopeBlobs(t)
	body := &readCounter{r: strings.NewReader("state"), n: 0}

	err := op.call(b, body, blob.Key(tc.key))
	if !errors.Is(err, tc.want) {
		t.Fatalf("err = %v, want %v", err, tc.want)
	}

	if n := store.callCount(); n != 0 {
		t.Errorf("calls that reached the store = %d, want 0", n)
	}

	if body.n != 0 {
		t.Errorf("bytes read from the body = %d, want 0", body.n)
	}

	wantRecords := []string(nil)
	if tc.recorded {
		wantRecords = []string{acmeID.String() + " " + op.op}
	}

	if got := refusals.all(); !slices.Equal(got, wantRecords) {
		t.Errorf("refusals = %v, want %v", got, wantRecords)
	}

	if strings.Contains(err.Error(), otherID.String()) {
		t.Errorf("error text %q names the other namespace", err)
	}
}

// readCounter counts the bytes that a reader gave.
type readCounter struct {
	r io.Reader
	n int
}

func (c *readCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n

	return n, err //nolint:wrapcheck // A test double.
}

// TestStateBlobsListRefusesHostileWorkspaces proves the list of the blobs of a
// workspace stays inside the state directory of the namespace.
func TestStateBlobsListRefusesHostileWorkspaces(t *testing.T) {
	t.Parallel()

	for _, ws := range []ID{"../ws-2", "ws-1/../../other", "", "ws-1\x00", "a/b/../.."} {
		t.Run(string(ws), func(t *testing.T) {
			t.Parallel()

			b, store, refusals := newScopeBlobs(t)

			_, err := b.list(context.Background(), "acme", ws)
			if !errors.Is(err, blob.ErrOutOfScope) {
				t.Fatalf("err = %v, want blob.ErrOutOfScope", err)
			}

			if n := store.callCount(); n != 0 {
				t.Errorf("calls that reached the store = %d, want 0", n)
			}

			if got, want := refusals.all(), []string{acmeID.String() + " " + opList}; !slices.Equal(got, want) {
				t.Errorf("refusals = %v, want %v", got, want)
			}
		})
	}
}

// TestStateBlobsInScope proves that a call inside the scope reaches the store
// and records nothing, and that two namespaces keep their state apart.
func TestStateBlobsInScope(t *testing.T) {
	t.Parallel()

	b, store, refusals := newScopeBlobs(t)
	ctx := context.Background()

	key := scopedAttemptKey(t, b, "acme", acmeID)
	otherKey := scopedAttemptKey(t, b, "other", otherID)

	if err := b.put(ctx, "acme", key, strings.NewReader("state")); err != nil {
		t.Fatalf("put: %v", err)
	}

	listed, err := b.list(ctx, "acme", "ws-1")
	if err != nil || len(listed) != 1 || listed[0].Key != key {
		t.Fatalf("list = %v, %v, want only %q", listed, err, key)
	}

	if other, listErr := b.list(ctx, "other", "ws-1"); listErr != nil || len(other) != 0 {
		t.Fatalf("list of other = %v, %v, want none", other, listErr)
	}

	if key == otherKey {
		t.Fatal("two namespaces made the same key")
	}

	checkGetAndDelete(t, b, key)

	if store.callCount() == 0 {
		t.Fatal("no call reached the store")
	}

	if got := refusals.all(); len(got) != 0 {
		t.Fatalf("refusals = %v, want none", got)
	}
}

// scopedAttemptKey returns the attempt key of the namespace name and checks
// that it starts with the directory of the namespace ID id.
func scopedAttemptKey(t *testing.T, b *stateBlobs, name namespace.Name, id namespace.ID) blob.Key {
	t.Helper()

	key, err := b.attemptKey(context.Background(), name, "ws-1", "sv-1")
	if err != nil {
		t.Fatalf("attempt key of %s: %v", name, err)
	}

	if want := "namespaces/" + id.String() + "/tofu/ws-1/sv-1/"; !strings.HasPrefix(key.String(), want) {
		t.Fatalf("key = %q, want prefix %q", key, want)
	}

	return key
}

// checkGetAndDelete reads the blob under key, deletes it, and reads it again.
func checkGetAndDelete(t *testing.T, b *stateBlobs, key blob.Key) {
	t.Helper()

	ctx := context.Background()

	rc, err := b.get(ctx, "acme", key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	data, err := io.ReadAll(rc)
	if closeErr := rc.Close(); err != nil || closeErr != nil || string(data) != "state" {
		t.Fatalf("read = %q, %v, %v, want state", data, err, closeErr)
	}

	if err = b.deleteRetried(ctx, "acme", key); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err = b.get(ctx, "acme", key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
}

// TestStateBlobsUnknownNamespace proves that a namespace the server does not
// know gets no view and no store call.
func TestStateBlobsUnknownNamespace(t *testing.T) {
	t.Parallel()

	b, store, refusals := newScopeBlobs(t)

	_, err := b.get(context.Background(), "ghost", blob.Key("namespaces/"+acmeID.String()+"/tofu/ws-1/sv-1/a1"))
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("err = %v, want namespace.ErrNotFound", err)
	}

	if n := store.callCount(); n != 0 {
		t.Errorf("calls that reached the store = %d, want 0", n)
	}

	if got := refusals.all(); len(got) != 0 {
		t.Errorf("refusals = %v, want none", got)
	}
}

// TestStateBlobsRecorderFailure proves a refusal stands when the recorder
// fails: the caller sees blob.ErrOutOfScope and the failure of the record.
func TestStateBlobsRecorderFailure(t *testing.T) {
	t.Parallel()

	b, _, refusals := newScopeBlobs(t)
	refusals.err = errAuditDown

	_, err := b.get(context.Background(), "acme", blob.Key("namespaces/"+otherID.String()+"/tofu/ws-1/sv-1/a1"))
	if !errors.Is(err, blob.ErrOutOfScope) || !errors.Is(err, errAuditDown) {
		t.Fatalf("err = %v, want blob.ErrOutOfScope joined with the record failure", err)
	}
}

// serverEntries is an audit.ServerStore that keeps the entries.
type serverEntries struct {
	got []audit.Entry
	err error
}

func (s *serverEntries) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if s.err != nil {
		return audit.Entry{}, s.err
	}

	s.got = append(s.got, e)

	return e, nil
}

// TestAuditOutOfScope proves the audit entry of a refusal of a state blob,
// through the shared recorder with the family of the state blobs: kind
// security, outcome denied, scope @server, the namespace of the call as the
// object, a reason that names the state blobs and the place, and no blob key.
func TestAuditOutOfScope(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 20, 12, 0, 0, 0, time.FixedZone("x", 3600))
	entries := &serverEntries{got: nil, err: nil}

	rec, err := secret.NewAuditOutOfScope(entries, audit.ChannelAPI, StateBlobFamily(), func() time.Time { return at })
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}

	for _, op := range []string{opPut, opGet, opDelete, opList} {
		recordErr := rec.RecordOutOfScope(context.Background(), acmeID, op, secret.OutsideNamespace)
		if recordErr != nil {
			t.Fatalf("record %s: %v", op, recordErr)
		}
	}

	recordErr := rec.RecordOutOfScope(context.Background(), acmeID, opGet, secret.OutsideDirectory)
	if recordErr != nil {
		t.Fatalf("record outside the directory: %v", recordErr)
	}

	if len(entries.got) != 5 {
		t.Fatalf("entries = %d, want 5", len(entries.got))
	}

	for i, e := range entries.got {
		checkSecurityEntry(t, i, e, at)
	}

	if got, want := entries.got[1].Reason, "state blob get refused: key outside the namespace"; got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}

	want := "state blob get refused: key outside the tofu directory of the namespace"
	if got := entries.got[4].Reason; got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
}

// checkSecurityEntry checks that e is the denied security entry of a refusal in
// the namespace acmeID, stamped with the time at, in UTC, with no key in the reason.
func checkSecurityEntry(t *testing.T, i int, e audit.Entry, at time.Time) {
	t.Helper()

	if err := e.Validate(); err != nil {
		t.Errorf("entry %d does not validate: %v", i, err)
	}

	if e.Kind != audit.KindSecurity || e.Outcome != audit.OutcomeDenied || e.Namespace != audit.ServerScope ||
		e.Action != secret.ActionBlobOutOfScope || e.Object != acmeID.String() || e.Subject != "server" ||
		!e.At.Equal(at) || e.At.Location() != time.UTC {
		t.Errorf("entry %d = %+v, want a denied security entry in @server for the namespace", i, e)
	}

	if strings.Contains(e.Reason, "namespaces/") || strings.Contains(e.Reason, "tofu/") {
		t.Errorf("reason %q holds a key", e.Reason)
	}
}

// TestStateBlobsDeleteEveryShapeInTheDirectory proves that a delete reaches each
// blob of the state directory of the namespace, also one of an unexpected
// shape, so the purge of a workspace leaves no blob. The view still refuses a
// key that is not valid.
func TestStateBlobsDeleteEveryShapeInTheDirectory(t *testing.T) {
	t.Parallel()

	b, store, refusals := newScopeBlobs(t)
	ctx := context.Background()
	dir := "namespaces/" + acmeID.String() + "/tofu/ws-1/"

	for _, rest := range []string{"first", "sv-1/a1", "sv-1/a1/extra", "sv-1/a1/extra/more"} {
		key := blob.Key(dir + rest)

		if err := store.Put(ctx, key, strings.NewReader("x")); err != nil {
			t.Fatalf("put %q: %v", key, err)
		}
	}

	listed, err := b.list(ctx, "acme", "ws-1")
	if err != nil || len(listed) != 4 {
		t.Fatalf("list = %d, %v, want 4", len(listed), err)
	}

	for _, obj := range listed {
		if deleteErr := b.deleteRetried(ctx, "acme", obj.Key); deleteErr != nil {
			t.Fatalf("delete %q: %v", obj.Key, deleteErr)
		}
	}

	if left, listErr := b.list(ctx, "acme", "ws-1"); listErr != nil || len(left) != 0 {
		t.Fatalf("list after the delete = %d, %v, want none", len(left), listErr)
	}

	if got := refusals.all(); len(got) != 0 {
		t.Fatalf("refusals = %v, want none", got)
	}
}
