package pulumi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// updatePatterns lists every update route as "METHOD path". The enforcement
// tests use it to tell update routes, which need live update state, apart
// from the older routes.
func updatePatterns() []string {
	return []string{
		"POST /api/stacks/{namespace}/{project}/{stack}/update",
		"POST /api/stacks/{namespace}/{project}/{stack}/preview",
		"POST /api/stacks/{namespace}/{project}/{stack}/refresh",
		"POST /api/stacks/{namespace}/{project}/{stack}/destroy",
		"POST /api/stacks/{namespace}/{project}/{stack}/import",
		"POST /api/stacks/{namespace}/{project}/{stack}/restore/{version}",
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}",
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/renew_lease",
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/events/batch",
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpoint",
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpointverbatim",
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpointdelta",
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/complete",
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/cancel",
		"GET /api/stacks/{namespace}/{project}/{stack}/update/{update}",
		"GET /api/stacks/{namespace}/{project}/{stack}/updates",
		"GET /api/stacks/{namespace}/{project}/{stack}/export",
		"GET /api/stacks/{namespace}/{project}/{stack}/export/{version}",
	}
}

// memUpdateStore implements the update, checkpoint and event ports in
// memory, following their documented contracts far enough for the HTTP
// tests: compare-and-swap on revision, one mutating update per stack,
// versions, and lease checks on event writes.
type memUpdateStore struct {
	updates      map[stack.UpdateID]stack.Update
	stackVersion map[stack.Ref]int64
	checkpoints  map[stack.UpdateID]stack.Checkpoint
	events       map[stack.UpdateID][]stack.Event
	journal      map[stack.UpdateID][]stack.JournalBatchRef
	refusals     map[stack.UpdateID][]stack.JournalRefusal
	seqs         map[stack.UpdateID][]int64
	// stacks resolves the stack ID of the item key of a new update, as the
	// Stacks of the update service does.
	stacks stack.CheckpointStacks
	mu     sync.Mutex
}

func newMemUpdateStore() *memUpdateStore {
	return &memUpdateStore{
		updates:      map[stack.UpdateID]stack.Update{},
		stackVersion: map[stack.Ref]int64{},
		checkpoints:  map[stack.UpdateID]stack.Checkpoint{},
		events:       map[stack.UpdateID][]stack.Event{},
		journal:      map[stack.UpdateID][]stack.JournalBatchRef{},
		refusals:     map[stack.UpdateID][]stack.JournalRefusal{},
		seqs:         map[stack.UpdateID][]int64{},
		stacks:       nil,
		mu:           sync.Mutex{},
	}
}

func (m *memUpdateStore) CreateUpdate(
	ctx context.Context, u stack.Update, key stack.UpdateKey, fence namespace.Fence,
) error {
	st, err := m.stacks.GetStack(ctx, u.Ref)
	if err != nil {
		return err
	}

	item, err := key.RefFor(u.Ref.Namespace, st.ID, u.ID)
	if err != nil {
		return err
	}

	// The fake has no transaction: a sealer of the tests needs none.
	if _, err := key.NewKey(ctx, nil, item, fence); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.updates[u.ID] = u

	return nil
}

func (m *memUpdateStore) GetUpdate(_ context.Context, ref stack.Ref, id stack.UpdateID) (stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.updates[id]
	if !ok || u.Ref != ref {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	return u, nil
}

func (m *memUpdateStore) StartUpdate(_ context.Context, u stack.Update, _ namespace.Fence) (stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updates[u.ID].Revision != u.Revision-1 {
		return stack.Update{}, stack.ErrConcurrentModification
	}

	var highest int64

	for id := range m.updates {
		if !m.updates[id].Mutates() || m.updates[id].Ref != u.Ref {
			continue
		}

		if m.updates[id].Status == stack.StatusRunning && u.Mutates() {
			return stack.Update{}, stack.ErrUpdateConflict
		}

		highest = max(highest, m.updates[id].Version)
	}

	u.Version = m.stackVersion[u.Ref]
	if u.Mutates() {
		u.Version = highest + 1
	}

	m.updates[u.ID] = u

	return u, nil
}

func (m *memUpdateStore) SaveUpdate(_ context.Context, u stack.Update, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updates[u.ID].Revision != u.Revision-1 {
		return stack.ErrConcurrentModification
	}

	if u.Mutates() && u.Status.Terminal() && u.Version > 0 {
		m.stackVersion[u.Ref] = u.Version
	}

	m.updates[u.ID] = u

	return nil
}

func (m *memUpdateStore) ActiveUpdate(_ context.Context, ref stack.Ref) (stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.updates {
		if m.updates[id].Ref == ref && m.updates[id].Mutates() && m.updates[id].Status == stack.StatusRunning {
			return m.updates[id], nil
		}
	}

	return stack.Update{}, stack.ErrUpdateNotFound
}

func (m *memUpdateStore) ListUpdates(_ context.Context, ref stack.Ref, _ stack.Page) ([]stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var list []stack.Update

	for id := range m.updates {
		if m.updates[id].Ref == ref && m.updates[id].Mutates() && m.updates[id].Status != stack.StatusNotStarted {
			u := m.updates[id]
			// The SQL stores fill the checkpoint size from the checkpoint
			// row on read; the memory store does the same, so history
			// tests see the size of the checkpoint each update saved.
			if cp, ok := m.checkpoints[id]; ok {
				u.CheckpointBytes = int64(len(cp.Deployment))
			}

			list = append(list, u)
		}
	}

	slices.SortFunc(list, func(a, b stack.Update) int { return int(b.Version - a.Version) })

	return list, nil
}

func (m *memUpdateStore) PutCheckpoint(_ context.Context, cp stack.Checkpoint, _ namespace.Fence) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.checkpoints[cp.Update] = cp

	return false, nil
}

// StateAt implements stack.StateAtReader for the one stack that the fixture
// holds, so it ignores the stack ID. It compares times in milliseconds, as
// the SQL stores do.
func (m *memUpdateStore) StateAt(_ context.Context, _ stack.ID, at time.Time) (stack.StateAt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	atMs := at.UnixMilli()

	var best, first, running stack.Update

	for id := range m.updates {
		u := m.updates[id]
		if !u.Mutates() || u.Status == stack.StatusNotStarted {
			continue
		}

		if _, saved := m.checkpoints[id]; saved && !u.EndedAt.IsZero() {
			first = lowerVersion(first, u)

			if u.EndedAt.UnixMilli() <= atMs {
				best = higherVersion(best, u)
			}
		}

		if u.StartedAt.UnixMilli() <= atMs && (u.EndedAt.IsZero() || u.EndedAt.UnixMilli() > atMs) {
			running = higherVersion(running, u)
		}
	}

	if best.Version == 0 {
		return stack.StateAt{}, &stack.NoStateAtError{First: first.EndedAt}
	}

	return stack.StateAt{
		EndedAt:   best.EndedAt,
		CreatedBy: best.CreatedBy,
		Version:   int(best.Version),
		Running:   runningAbove(running, best),
		Names:     inventory.Names{AtTime: "", Current: ""},
	}, nil
}

// runningAbove returns the version of running when it is above that of best,
// or 0.
func runningAbove(running, best stack.Update) int {
	if running.Version <= best.Version {
		return 0
	}

	return int(running.Version)
}

// lowerVersion returns the update with the lower version; the zero update
// has none yet.
func lowerVersion(a, b stack.Update) stack.Update {
	if a.Version == 0 || b.Version < a.Version {
		return b
	}

	return a
}

// higherVersion returns the update with the higher version.
func higherVersion(a, b stack.Update) stack.Update {
	if b.Version > a.Version {
		return b
	}

	return a
}

func (m *memUpdateStore) LatestCheckpoint(_ context.Context, ref stack.Ref) (stack.Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var (
		best    stack.Checkpoint
		found   bool
		highest int64 = -1
	)

	for id := range m.checkpoints {
		if m.checkpoints[id].Ref != ref {
			continue
		}

		if v := m.updates[id].Version; v > highest {
			highest, best, found = v, m.checkpoints[id], true
		}
	}

	if !found {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	return withTestScope(best), nil
}

// withTestScope fills the namespace ID and the stack ID of a checkpoint that a
// read returns, as the SQL stores do from the query. The IDs are the ones of
// homedNamespaceStore and recordingStackStore.
func withTestScope(cp stack.Checkpoint) stack.Checkpoint {
	cp.NamespaceID = namespace.ID("ns-" + cp.Ref.Namespace.String())
	cp.StackID = "stk-test"

	return cp
}

func (m *memUpdateStore) CheckpointByVersion(
	_ context.Context, ref stack.Ref, version int64,
) (stack.Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.checkpoints {
		if m.checkpoints[id].Ref == ref && m.updates[id].Version == version {
			return withTestScope(m.checkpoints[id]), nil
		}
	}

	return stack.Checkpoint{}, stack.ErrNoState
}

// CheckpointByUpdate implements stack.CheckpointStore.
func (m *memUpdateStore) CheckpointByUpdate(
	_ context.Context, ref stack.Ref, id stack.UpdateID,
) (stack.Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp, ok := m.checkpoints[id]
	if !ok || cp.Ref != ref {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	return withTestScope(cp), nil
}

// ReferencedBlobKeys implements stack.CheckpointStore.
func (m *memUpdateStore) ReferencedBlobKeys(
	_ context.Context, _ stack.ID,
) ([]blob.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []blob.Key

	for id := range m.checkpoints {
		if cp := m.checkpoints[id]; cp.BlobKey != "" {
			out = append(out, cp.BlobKey)
		}
	}

	for _, refs := range m.journal {
		for i := range refs {
			out = append(out, refs[i].Key)
		}
	}

	slices.Sort(out)

	return slices.Compact(out), nil
}

func (m *memUpdateStore) AppendEvents(
	_ context.Context,
	batch stack.EventBatch,
	_ namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u := m.updates[batch.Update]
	if u.Status != stack.StatusRunning || u.Lease.Digest != batch.Lease {
		return stack.ErrWrongUpdateState
	}

	for _, e := range batch.Events {
		if !slices.ContainsFunc(
			m.events[batch.Update],
			func(have stack.Event) bool { return have.Sequence == e.Sequence },
		) {
			m.events[batch.Update] = append(m.events[batch.Update], e)
		}
	}

	return nil
}

func (m *memUpdateStore) ListEvents(
	_ context.Context, _ stack.Ref, id stack.UpdateID, after int64, limit int,
) ([]stack.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.updates[id]; !ok {
		return nil, stack.ErrUpdateNotFound
	}

	var out []stack.Event

	for _, e := range m.events[id] {
		if e.Sequence > after && len(out) < limit {
			out = append(out, e)
		}
	}

	return out, nil
}

// resolving sets the stack resolver of m and returns m.
func (m *memUpdateStore) resolving(stacks stack.CheckpointStacks) *memUpdateStore {
	m.stacks = stacks

	return m
}

// memBlob is an in-memory blob.Store. Writes are write-once, as the port
// requires: Put on an existing key fails with ErrExists.
type memBlob struct {
	objects map[blob.Key][]byte
	mu      sync.Mutex
}

func newMemBlob() *memBlob {
	return &memBlob{objects: map[blob.Key][]byte{}, mu: sync.Mutex{}}
}

func (m *memBlob) Put(_ context.Context, key blob.Key, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read blob: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.objects[key]; ok {
		return blob.ErrExists
	}

	m.objects[key] = data

	return nil
}

func (m *memBlob) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, ok := m.objects[key]
	if !ok {
		return nil, blob.ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memBlob) Delete(_ context.Context, key blob.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.objects, key)

	return nil
}

// ListPage implements blob.Store: it cuts the page that req selects from the
// objects of list.
func (m *memBlob) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	objects, err := m.list(ctx, req.Prefix)
	if err != nil {
		return blob.Page{}, err
	}

	slices.SortFunc(objects, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return blob.PageOf(objects, req), nil
}

func (m *memBlob) Ping(context.Context) error {
	return nil
}

func (m *memBlob) list(_ context.Context, prefix string) ([]blob.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []blob.Object

	for key := range m.objects {
		if strings.HasPrefix(key.String(), prefix) {
			out = append(out, blob.Object{ModifiedAt: time.Time{}, Key: key})
		}
	}

	slices.SortFunc(out, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return out, nil
}

// updateFixture is a Handler backed by real stack and update services over
// in-memory stores, plus the services to mint update state with.
type updateFixture struct {
	handler *pulumi.Handler
	updates *stack.Updates
	ref     stack.Ref
	// mem is the update store of the fixture, or nil for a fixture that
	// builds its own store.
	mem *memUpdateStore
	// sealer and audit are the item sealer and the audit log of the fixture,
	// or nil for a fixture that builds its own.
	sealer *secrettest.MemorySealer
	audit  *testAudit
}

// newUpdateFixture builds a Handler whose update routes work end to end.
// authz decides the authorization policy under test.
func newUpdateFixture(t *testing.T, authz identity.Authorizer) updateFixture {
	t.Helper()

	return newUpdateFixtureWithLimit(t, authz, 0)
}

// newUpdateFixtureWithLimit builds a Handler as newUpdateFixture does, with
// checkpoints bounded by limit bytes (zero means the domain default). Tests
// for the checkpoint size limit use a small bound.
func newUpdateFixtureWithLimit(t *testing.T, authz identity.Authorizer, limit int) updateFixture {
	t.Helper()

	return newUpdateFixtureWithCutoff(t, authz, limit, 0)
}

// newUpdateFixtureWithCutoff builds a Handler as newUpdateFixture does,
// with checkpoints bounded by limit bytes and the delta checkpoint
// capability advertised with cutoff bytes (zero hides the capability).
// Tests for delta uploads use a cutoff.
func newUpdateFixtureWithCutoff(
	t *testing.T, authz identity.Authorizer, limit, cutoff int,
) updateFixture {
	t.Helper()

	return newUpdateFixtureWithAuthenticator(
		t,
		authz,
		tokenAuthenticator{},
		fixtureOptions{manual: nil, checker: nil, limit: limit, cutoff: cutoff, replayer: nil, guard: nil},
	)
}

// fixtureOptions holds the settings of an update fixture beyond its policy and
// authenticator: the checkpoint limit, the delta cutoff, the check of the
// access token that started an update, and the journal replayer (nil builds
// no checkpoint).
type fixtureOptions struct {
	manual   stack.ManualLocks
	checker  stack.TokenChecker
	limit    int
	cutoff   int
	replayer stack.JournalReplayer
	// guard is the state guard of the handler. Nil gives the guard of
	// stategate.Off.
	guard stategate.Guard
}

// newUpdateFixtureWithAuthenticator builds a Handler as newUpdateFixtureWithCutoff
// does, with authn as the authenticator of access tokens.
func newUpdateFixtureWithAuthenticator(
	t *testing.T, authz identity.Authorizer, authn identity.Authenticator, opts fixtureOptions,
) updateFixture {
	t.Helper()

	limit, cutoff, checker := opts.limit, opts.cutoff, opts.checker
	replayer := opts.replayer
	if replayer == nil {
		replayer = pulumijournal.NewReplayer(time.Now, "")
	}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stores := &recordingStackStore{
		placed: nil, placeErr: nil,
		created:   nil,
		renamed:   nil,
		tags:      nil,
		deleted:   nil,
		forced:    nil,
		entries:   nil,
		deleteErr: nil,
	}

	stacks, err := stack.NewService(
		stack.Config{Store: stores, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	mem := newMemUpdateStore()
	blobs := newMemBlob()
	sealer := secrettest.NewMemorySealer()
	auditLog := newTestAudit()

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             sealer,
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        opts.manual,
		Updates:            mem.resolving(stacks),
		Checkpoints:        mem,
		Events:             mem,
		Namespaces:         namespaces,
		Stacks:             stacks,
		Blobs:              blobs,
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: limit,
		Journal:            journalWriterFor(t, blobs, mem, limit),
		Replayer:           replayer,
		Tokens:             checker,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	var guard stategate.Guard = stategate.Off("the test keeps no gates")
	if opts.guard != nil {
		guard = opts.guard
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		Placer:                  nil,
		StateGuard:              guard,
		SessionPolicy:           nil,
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 updates,
		Secrets:                 newTestSecrets(t, namespaces),
		Namespaces:              namespaces,
		Authenticator:           authn,
		Authorizer:              authz,
		Sessions:                nil,
		AddressGate:             nil,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 baseurl.Resolver{},
		MaxBodyBytesInFlight:    0,
		Gate:                    nil,
		Audit:                   auditLog,
		OnAuditFailure:          nil,
		CheckpointDeltaCutoff:   cutoff,
		DenialLimiter:           nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	return updateFixture{handler: handler, updates: updates, ref: ref, mem: mem, sealer: sealer, audit: auditLog}
}

// allowAll authorizes every action.
func allowAll() selectiveAuthorizer {
	return selectiveAuthorizer{deny: map[identity.Action]bool{}}
}

// updateRequest describes one update-route request. Lease is sent as the
// update-token credential when not empty, access as the access token
// otherwise. Either may be empty for the unauthenticated cases.
type updateRequest struct {
	Method    string
	Target    string
	Access    string
	Lease     string
	Body      string
	UserAgent string
}

// accessRequest builds a request that signs in with the test access token.
func accessRequest(method, target, body string) updateRequest {
	return updateRequest{Method: method, Target: target, Access: "test-token", Lease: "", Body: body, UserAgent: ""}
}

// leaseRequest builds a request that presents the update lease token.
func leaseRequest(method, target, lease, body string) updateRequest {
	return updateRequest{Method: method, Target: target, Access: "", Lease: lease, Body: body, UserAgent: ""}
}

// anonymousRequest builds a request with no credential.
func anonymousRequest(method, target, body string) updateRequest {
	return updateRequest{Method: method, Target: target, Access: "", Lease: "", Body: body, UserAgent: ""}
}

// doUpdate sends one update-route request.
func (f updateFixture) doUpdate(t *testing.T, req updateRequest) (int, string) {
	t.Helper()

	var httpReq *http.Request
	if req.Body == "" {
		httpReq = httptest.NewRequestWithContext(t.Context(), req.Method, req.Target, http.NoBody)
	} else {
		httpReq = httptest.NewRequestWithContext(t.Context(), req.Method, req.Target, strings.NewReader(req.Body))
	}

	if req.UserAgent != "" {
		httpReq.Header.Set("User-Agent", req.UserAgent)
	}

	switch {
	case req.Lease != "":
		httpReq.Header.Set("Authorization", "update-token "+req.Lease)
	case req.Access != "":
		httpReq.Header.Set("Authorization", "token "+req.Access)
	default:
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httpReq)

	return rec.Code, rec.Body.String()
}

// mintStarted creates and starts a preview update and returns its ID and
// lease token.
func (f updateFixture) mintStarted(t *testing.T) (stack.UpdateID, string) {
	t.Helper()

	u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{
		Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return u.ID, started.Token.Reveal()
}

// mintStartedMutating creates and starts a mutating update and returns its
// ID and lease token. Only mutating updates save checkpoints.
func (f updateFixture) mintStartedMutating(t *testing.T) (stack.UpdateID, string) {
	t.Helper()

	u, err := f.updates.Create(
		t.Context(),
		f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return u.ID, started.Token.Reveal()
}

// updatePaths holds the lease, checkpoint, cancel and get paths for one update.
type updatePaths struct {
	Start      string
	Renew      string
	Events     string
	Checkpoint string
	Verbatim   string
	Delta      string
	Complete   string
	Cancel     string
	Get        string
}

// mintedPaths returns the start, lease, checkpoint, cancel and get paths for one update.
func mintedPaths(id stack.UpdateID) updatePaths {
	base := "/api/stacks/acme/web/prod/update/" + id.String()

	return updatePaths{
		Start:      base,
		Renew:      base + "/renew_lease",
		Events:     base + "/events/batch",
		Checkpoint: base + "/checkpoint",
		Verbatim:   base + "/checkpointverbatim",
		Delta:      base + "/checkpointdelta",
		Complete:   base + "/complete",
		Cancel:     base + "/cancel",
		Get:        base,
	}
}

// isLeasePattern reports whether pattern is a lease route: the lease token
// stands in for sign-in on these routes only.
func isLeasePattern(pattern string) bool {
	for _, suffix := range []string{
		"/renew_lease",
		"/events/batch",
		"/checkpoint",
		"/checkpointverbatim",
		"/checkpointdelta",
		"/complete",
	} {
		if strings.HasSuffix(pattern, suffix) {
			return true
		}
	}

	return false
}

// wantUpdateAction maps every update route to the action it must declare:
// reads need unit:read, writes need unit:write.
func wantUpdateAction() map[string]identity.Action {
	write := map[string]bool{}
	for _, pattern := range updatePatterns() {
		write[pattern] = true
	}

	for _, pattern := range []string{
		"GET /api/stacks/{namespace}/{project}/{stack}/update/{update}",
		"GET /api/stacks/{namespace}/{project}/{stack}/updates",
		"GET /api/stacks/{namespace}/{project}/{stack}/export",
		"GET /api/stacks/{namespace}/{project}/{stack}/export/{version}",
	} {
		write[pattern] = false
	}

	want := map[string]identity.Action{}

	for pattern, isWrite := range write {
		if isWrite {
			want[pattern] = identity.ActionUnitWrite
		} else {
			want[pattern] = identity.ActionUnitRead
		}
	}

	return want
}

// wantUpdateScope maps every update route to the scope it must declare.
func wantUpdateScope() map[string]pulumi.Scope {
	want := map[string]pulumi.Scope{}

	for _, pattern := range updatePatterns() {
		if isLeasePattern(pattern) {
			want[pattern] = pulumi.ScopeUpdateLease
		} else {
			want[pattern] = pulumi.ScopeAuthorized
		}
	}

	return want
}

// checkUpdateRoute proves one route declares its action with a resolver and
// the expected scope.
func checkUpdateRoute(
	t *testing.T, route pulumi.Route, wantScope map[string]pulumi.Scope, wantAction map[string]identity.Action,
) {
	t.Helper()

	pattern := route.Method + " " + route.Path
	scope, ok := wantScope[pattern]

	if !ok {
		return
	}

	if route.Scope != scope {
		t.Errorf("route %q scope = %v, want %v", pattern, route.Scope, scope)
	}

	if len(route.Actions) != 1 || route.Actions[0] != wantAction[pattern] {
		t.Errorf("route %q actions = %v, want exactly [%s]", pattern, route.Actions, wantAction[pattern])
	}

	if route.Resolve == nil {
		t.Errorf("route %q declares actions but no resolver", pattern)
	}
}

// TestUpdateRoutesDeclareActions proves every update route declares its
// action on the unit, with a resolver and the expected scope: reads declare
// unit:read, writes declare unit:write, and the lease token stands in for
// sign-in on the lease routes only.
func TestUpdateRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	wantScope := wantUpdateScope()
	wantAction := wantUpdateAction()
	seen := map[string]bool{}

	for _, route := range f.handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, ok := wantScope[pattern]; ok {
			seen[pattern] = true
		}

		checkUpdateRoute(t, route, wantScope, wantAction)
	}

	for _, pattern := range updatePatterns() {
		if !seen[pattern] {
			t.Errorf("route %q is not declared", pattern)
		}
	}
}

// createPreviewUpdate creates a preview through HTTP and returns its ID.
func createPreviewUpdate(t *testing.T, f updateFixture) stack.UpdateID {
	t.Helper()

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/preview", `{}`))
	if status != http.StatusOK {
		t.Fatalf("create answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var created struct {
		UpdateID string `json:"updateID"`
	}

	if err := json.Unmarshal([]byte(body), &created); err != nil || created.UpdateID == "" {
		t.Fatalf("create answers %q, want an update ID: %v", body, err)
	}

	return stack.UpdateID(created.UpdateID)
}

// startPreviewUpdate starts a preview through HTTP and returns its lease.
// A preview takes no version: it carries the stack's current version.
func startPreviewUpdate(t *testing.T, f updateFixture, paths updatePaths) string {
	t.Helper()

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Start, `{}`))
	if status != http.StatusOK {
		t.Fatalf("start answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var started struct {
		Token   string `json:"token"`
		Version int    `json:"version"`
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decode start: %v (%s)", err, body)
	}

	if started.Token == "" {
		t.Fatalf("start answers no lease token (%s)", body)
	}

	if started.Version != 0 {
		t.Fatalf("preview version = %d, want the stack's current version 0", started.Version)
	}

	return started.Token
}

// finishPreviewUpdate posts one engine event batch and completes a preview.
func finishPreviewUpdate(t *testing.T, f updateFixture, paths updatePaths, lease string) {
	t.Helper()

	event := `{"sequence":0,"timestamp":0,"diagnosticEvent":{"message":"hi","severity":"info"}}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Events, lease, `{"events":[`+event+`]}`))
	if status != http.StatusOK {
		t.Fatalf("events answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	status, body = f.doUpdate(t, leaseRequest(http.MethodPost, paths.Complete, lease, `{"status":"succeeded"}`))
	if status != http.StatusOK {
		t.Fatalf("complete answers %d, want %d (%s)", status, http.StatusOK, body)
	}
}

// checkPreviewInvisible proves a finished preview is readable but leaves no
// history: a preview changes nothing.
func checkPreviewInvisible(t *testing.T, f updateFixture, paths updatePaths) {
	t.Helper()

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, paths.Get, ""))
	if status != http.StatusOK || !strings.Contains(body, `"status":"succeeded"`) {
		t.Fatalf("get answers %d %q, want 200 with status succeeded", status, body)
	}

	status, body = f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/updates", ""))
	if status != http.StatusOK || strings.TrimSpace(body) != `{"updates":[]}` {
		t.Fatalf("history answers %d %q, want 200 with no updates", status, body)
	}
}

// TestPreviewUpdateCycle drives one preview through HTTP: create, start,
// events, complete, get and history. History stays empty: a preview changes
// nothing.
func TestPreviewUpdateCycle(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	id := createPreviewUpdate(t, f)
	paths := mintedPaths(id)
	lease := startPreviewUpdate(t, f, paths)
	finishPreviewUpdate(t, f, paths, lease)
	checkPreviewInvisible(t, f, paths)
}

// leaseCase is one renewal or completion attempt against a lease route.
type leaseCase struct {
	name   string
	method string
	target string
	body   string
}

// leaseCases returns the renew and complete attempts for paths.
func leaseCases(prefix string, paths updatePaths) []leaseCase {
	return []leaseCase{
		{name: prefix + "renew", method: http.MethodPost, target: paths.Renew, body: `{"duration":60}`},
		{name: prefix + "complete", method: http.MethodPost, target: paths.Complete, body: `{"status":"succeeded"}`},
	}
}

// checkUnauthorizedError proves an error body carries the 401 code shape.
func checkUnauthorizedError(t *testing.T, body string) {
	t.Helper()

	var decoded struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode error: %v (%s)", err, body)
	}

	if decoded.Code != http.StatusUnauthorized || decoded.Message == "" {
		t.Fatalf("error = %+v, want code 401 with a message", decoded)
	}
}

// checkLeaseMissing proves the lease routes answer 401 without a credential.
func checkLeaseMissing(t *testing.T, f updateFixture, paths updatePaths) {
	t.Helper()

	for _, tc := range leaseCases("missing ", paths) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := f.doUpdate(t, anonymousRequest(tc.method, tc.target, tc.body))
			if status != http.StatusUnauthorized {
				t.Fatalf("status %d, want %d (%s)", status, http.StatusUnauthorized, body)
			}
		})
	}
}

// checkLeaseWrong proves the lease routes answer 401 for a foreign token.
func checkLeaseWrong(t *testing.T, f updateFixture, paths updatePaths, foreign string) {
	t.Helper()

	for _, tc := range leaseCases("wrong ", paths) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := f.doUpdate(t, leaseRequest(tc.method, tc.target, foreign, tc.body))
			if status != http.StatusUnauthorized {
				t.Fatalf("status %d, want %d (%s)", status, http.StatusUnauthorized, body)
			}

			checkUnauthorizedError(t, body)
		})
	}
}

// TestUpdateLeaseRejected proves renewal and completion with a wrong lease
// token are rejected, without touching any store.
func TestUpdateLeaseRejected(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, _ := f.mintStarted(t)
	paths := mintedPaths(id)

	foreign, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	checkLeaseMissing(t, f, paths)
	checkLeaseWrong(t, f, paths, foreign.Reveal())
}

// TestUpdateRoutesEnforce proves every update route enforces its row.
// Access-authed routes answer 200 when allowed and 403 when their action is
// denied. Lease routes answer 200 with a valid lease even when the
// Authorizer denies: the lease stands in for the declared unit:write. Both
// answer 401 without a usable credential.
func TestUpdateRoutesEnforce(t *testing.T) {
	t.Parallel()

	for _, pattern := range updatePatterns() {
		t.Run(pattern, func(t *testing.T) {
			t.Parallel()
			checkUpdateRouteEnforcement(t, pattern)
		})
	}
}

// checkUpdateRouteEnforcement proves one update route enforces its row with
// a fresh handler and fresh update state per case.
func checkUpdateRouteEnforcement(t *testing.T, pattern string) {
	t.Helper()

	route := updateRoute(t, newUpdateFixture(t, allowAll()).handler, pattern)

	if route.Scope == pulumi.ScopeUpdateLease {
		checkLeaseRouteEnforcement(t, route, pattern)

		return
	}

	allowed := newUpdateFixture(t, allowAll())
	at := accessRouteTarget(t, allowed, pattern)

	status, resp := allowed.doUpdate(t, accessRequest(route.Method, at.Target, at.Body))
	if status != http.StatusOK {
		t.Fatalf("allowed request answers %d, want %d (%s)", status, http.StatusOK, resp)
	}

	denied := newUpdateFixture(t, selectiveAuthorizer{deny: map[identity.Action]bool{route.Actions[0]: true}})

	status, resp = denied.doUpdate(t, accessRequest(route.Method, at.Target, at.Body))
	if status != http.StatusForbidden {
		t.Fatalf("denied request answers %d, want %d (%s)", status, http.StatusForbidden, resp)
	}

	status, resp = allowed.doUpdate(t, anonymousRequest(route.Method, at.Target, at.Body))
	if status != http.StatusUnauthorized {
		t.Fatalf("unsigned request answers %d, want %d (%s)", status, http.StatusUnauthorized, resp)
	}
}

// updateRoute returns the declared route for pattern.
func updateRoute(t *testing.T, handler *pulumi.Handler, pattern string) pulumi.Route {
	t.Helper()

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == pattern {
			return route
		}
	}

	t.Fatalf("route %q is not declared", pattern)

	return pulumi.Route{Method: "", Path: "", Scope: 0, Actions: nil, Resolve: nil, Serve: nil, Lease: nil}
}

// accessTarget holds the target and body for an access-authed update route.
type accessTarget struct {
	Target string
	Body   string
}

// seedAccessCheckpoint imports an empty deployment as version 1 of the
// fixture stack, so routes that need a checkpoint to copy or export have one.
func seedAccessCheckpoint(t *testing.T, f updateFixture) {
	t.Helper()

	input := stack.CheckpointInput{
		Features:      nil,
		Deployment:    []byte(`{}`),
		SchemaVersion: 3,
		Sequence:      0,
		Invalid:       false,
	}

	if _, err := f.updates.Import(t.Context(), f.ref, "", input); err != nil {
		t.Fatalf("import: %v", err)
	}
}

// accessRouteTarget builds the target and body for an access-authed update
// route, minting the update state the route needs in f: a created update
// for start and get, a started update for cancel, a finished update with a
// checkpoint for versioned export and restore, none for create, history and
// export, and a deployment body for import.
func accessRouteTarget(t *testing.T, f updateFixture, pattern string) accessTarget {
	t.Helper()

	switch {
	case strings.HasSuffix(pattern, "/export/{version}"):
		seedAccessCheckpoint(t, f)

		return accessTarget{Target: "/api/stacks/acme/web/prod/export/1", Body: ""}
	case strings.HasSuffix(pattern, "/restore/{version}"):
		seedAccessCheckpoint(t, f)

		return accessTarget{Target: "/api/stacks/acme/web/prod/restore/1", Body: ""}
	case strings.HasSuffix(pattern, "/updates"), strings.HasSuffix(pattern, "/export"):
		return accessTarget{
			Target: "/api/stacks/acme/web/prod" + strings.TrimPrefix(
				pattern, "GET /api/stacks/{namespace}/{project}/{stack}",
			),
			Body: "",
		}
	case strings.HasSuffix(pattern, "/import"):
		return accessTarget{Target: "/api/stacks/acme/web/prod/import", Body: `{"version":3,"deployment":{}}`}
	case strings.HasSuffix(pattern, "/cancel"):
		u, err := f.updates.Create(
			t.Context(),
			f.ref,
			stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
		)
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		if _, err := f.updates.Start(t.Context(), f.ref, u.ID); err != nil {
			t.Fatalf("start: %v", err)
		}

		return accessTarget{Target: "/api/stacks/acme/web/prod/update/" + u.ID.String() + "/cancel", Body: ""}
	case strings.HasSuffix(pattern, "/update/{update}"):
		u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{
			Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		return accessTarget{Target: "/api/stacks/acme/web/prod/update/" + u.ID.String(), Body: "{}"}
	default:
		kind := strings.TrimPrefix(pattern, "POST /api/stacks/{namespace}/{project}/{stack}/")

		return accessTarget{Target: "/api/stacks/acme/web/prod/" + kind, Body: "{}"}
	}
}

// leaseBodies holds one valid body per lease route.
func leaseBodies() map[string]string {
	return map[string]string{
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/renew_lease":  `{"duration":60}`,
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/events/batch": `{"events":[]}`,
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpoint":  `{"version":3,"deployment":{}}`,
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpointverbatim": `{"version":3,` +
			`"untypedDeployment":{"deployment":{}},"sequenceNumber":1}`,
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpointdelta": deltaLeaseBody(),
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/complete":         `{"status":"succeeded"}`,
	}
}

// deltaLeaseBody returns a valid delta for the seeded save of mintForLease:
// it replaces the seeded envelope with itself under sequence 2.
func deltaLeaseBody() string {
	const seeded = `{"deployment":{}}`

	return `{"version":3,"checkpointHash":` + strconv.Quote(deltaHash(seeded)) +
		`,"sequenceNumber":2,"deploymentDelta":[` + deltaEditJSON(0, len(seeded), seeded) + `]}`
}

// leaseTargets returns the renew, event, checkpoint and complete paths for one update.
func leaseTargets(id stack.UpdateID) map[string]string {
	paths := mintedPaths(id)

	return map[string]string{
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/renew_lease":         paths.Renew,
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/events/batch":        paths.Events,
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpoint":         paths.Checkpoint,
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpointverbatim": paths.Verbatim,
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpointdelta":    paths.Delta,
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/complete":            paths.Complete,
	}
}

// mintForLease creates and starts the update that pattern needs: a mutating
// update for the checkpoint routes, which a preview never saves, and a
// preview otherwise. The delta route also seeds the first save, which the
// delta applies to.
func mintForLease(t *testing.T, f updateFixture, pattern string) (stack.UpdateID, string) {
	t.Helper()

	if strings.HasSuffix(pattern, "/checkpoint") || strings.HasSuffix(pattern, "/checkpointverbatim") {
		return f.mintStartedMutating(t)
	}

	if strings.HasSuffix(pattern, "/checkpointdelta") {
		id, revealed := f.mintStartedMutating(t)

		tok, err := token.Parse(revealed)
		if err != nil {
			t.Fatalf("parse lease: %v", err)
		}

		base := stack.CheckpointInput{
			Features: nil, Deployment: []byte(`{"deployment":{}}`), SchemaVersion: 3, Sequence: 1, Invalid: false,
		}

		if err := f.updates.SaveCheckpoint(t.Context(), f.ref, id, tok, base); err != nil {
			t.Fatalf("seed checkpoint: %v", err)
		}

		return id, revealed
	}

	return f.mintStarted(t)
}

// checkLeaseRouteEnforcement proves one lease route answers 200 with a
// valid lease whatever the Authorizer says, and 401 otherwise.
func checkLeaseRouteEnforcement(t *testing.T, route pulumi.Route, pattern string) {
	t.Helper()

	body, ok := leaseBodies()[pattern]
	if !ok {
		t.Fatalf("no lease body for route %q", pattern)
	}

	// A valid lease answers 200, whether the Authorizer allows or denies.
	for _, authz := range []identity.Authorizer{
		allowAll(),
		selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitWrite: true}},
	} {
		f := newUpdateFixture(t, authz)
		id, lease := mintForLease(t, f, pattern)
		target := leaseTargets(id)[pattern]
		status, resp := f.doUpdate(t, leaseRequest(route.Method, target, lease, body))

		if status != http.StatusOK {
			t.Fatalf("leased request answers %d, want %d (%s)", status, http.StatusOK, resp)
		}
	}

	// Without a usable lease the route answers 401.
	f := newUpdateFixture(t, allowAll())
	id, _ := mintForLease(t, f, pattern)
	target := leaseTargets(id)[pattern]

	foreign, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	requests := []updateRequest{
		anonymousRequest(route.Method, target, body),
		accessRequest(route.Method, target, body),
		leaseRequest(route.Method, target, foreign.Reveal(), body),
	}

	for _, req := range requests {
		status, resp := f.doUpdate(t, req)
		if status != http.StatusUnauthorized {
			t.Fatalf("unleased request answers %d, want %d (%s)", status, http.StatusUnauthorized, resp)
		}
	}
}

// TestUpdateRoutesAuthorizeDeclaredObject proves every authorized update
// route asks for its declared action on the unit its resolver returns, and
// every lease route never calls the Authorizer: the lease stands in for the
// declared unit:write.
func TestUpdateRoutesAuthorizeDeclaredObject(t *testing.T) {
	t.Parallel()

	for _, pattern := range updatePatterns() {
		t.Run(pattern, func(t *testing.T) {
			t.Parallel()
			checkUpdateRouteObject(t, pattern)
		})
	}
}

// checkUpdateRouteObject proves one update route authorizes its declared
// object: lease routes record no call with a valid lease, authorized routes
// record the declared action on the resolved unit and answer 403 once that
// unit is denied.
func checkUpdateRouteObject(t *testing.T, pattern string) {
	t.Helper()

	route := updateRoute(t, newUpdateFixture(t, allowAll()).handler, pattern)

	if route.Scope == pulumi.ScopeUpdateLease {
		checkLeaseRouteObject(t, route, pattern)

		return
	}

	checkAccessRouteObject(t, route, pattern)
}

// checkLeaseRouteObject proves one lease route answers 200 with a valid
// lease without calling the Authorizer.
func checkLeaseRouteObject(t *testing.T, route pulumi.Route, pattern string) {
	t.Helper()

	recorder := &recordingAuthorizer{calls: nil}
	f := newUpdateFixture(t, recorder)

	id, lease := mintForLease(t, f, pattern)
	target := leaseTargets(id)[pattern]
	body := leaseBodies()[pattern]

	status, resp := f.doUpdate(t, leaseRequest(route.Method, target, lease, body))
	if status != http.StatusOK {
		t.Fatalf("leased request answers %d, want %d (%s)", status, http.StatusOK, resp)
	}

	if len(recorder.calls) != 0 {
		t.Fatalf("lease route calls %v, want no Authorizer call", recorder.calls)
	}
}

// checkAccessRouteObject proves one access-authed update route records its
// declared action on the resolved unit and answers 403 once denied on it.
func checkAccessRouteObject(t *testing.T, route pulumi.Route, pattern string) {
	t.Helper()

	authz := &objectToggleAuthorizer{calls: nil, object: "pulumi:acme/web/prod", deny: false}
	f := newUpdateFixture(t, authz)
	at := accessRouteTarget(t, f, pattern)

	status, resp := f.doUpdate(t, accessRequest(route.Method, at.Target, at.Body))
	if status != http.StatusOK {
		t.Fatalf("allowed request answers %d, want %d (%s)", status, http.StatusOK, resp)
	}

	for _, action := range route.Actions {
		if !slices.Contains(authz.calls, recordedCall{action: action, object: authz.object}) {
			t.Fatalf("route never asks %s on %q (calls %v)", action, authz.object, authz.calls)
		}
	}

	authz.deny = true

	status, resp = f.doUpdate(t, accessRequest(route.Method, at.Target, at.Body))
	if status != http.StatusForbidden {
		t.Fatalf("request denied on %q answers %d, want %d (%s)", authz.object, status, http.StatusForbidden, resp)
	}
}

// objectToggleAuthorizer records every call and allows all until deny is
// set, after which it denies object. Tests mint state before they deny.
type objectToggleAuthorizer struct {
	calls  []recordedCall
	object string
	deny   bool
}

func (a *objectToggleAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	a.calls = append(a.calls, recordedCall{action: action, object: obj.String()})

	if a.deny && obj.String() == a.object {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (*objectToggleAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// historyEntry is one entry of the stack history as the tests decode it.
type historyEntry struct {
	Result      string `json:"result"`
	Kind        string `json:"kind"`
	Version     int    `json:"version"`
	RequestedBy string `json:"requestedBy"`
}

// historyPayload is the stack history as the tests decode it.
type historyPayload struct {
	Updates []historyEntry `json:"updates"`
}

// TestHistoryShowsFinishedUpdates proves the history lists a finished
// mutating update with its kind, result and version.
func TestHistoryShowsFinishedUpdates(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	u, err := f.updates.Create(
		t.Context(),
		f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, err := f.updates.Complete(t.Context(), f.ref, u.ID, started.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete: %v", err)
	}

	status, body := f.doUpdate(
		t,
		accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/updates", ""),
	)
	if status != http.StatusOK {
		t.Fatalf("history answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var history historyPayload

	if err := json.Unmarshal([]byte(body), &history); err != nil {
		t.Fatalf("decode history: %v (%s)", err, body)
	}

	if len(history.Updates) != 1 {
		t.Fatalf("history holds %d updates, want 1 (%s)", len(history.Updates), body)
	}

	got := history.Updates[0]
	if got.Kind != "update" || got.Result != "succeeded" || got.Version != 1 {
		t.Fatalf("history entry = %+v, want kind update result succeeded version 1", got)
	}
}

// TestRenewLeaseEchoesToken proves a renewal keeps the token: the answer
// carries the presented token and its new expiry.
func TestRenewLeaseEchoesToken(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, lease := f.mintStarted(t)
	paths := mintedPaths(id)

	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`))
	if status != http.StatusOK {
		t.Fatalf("renew answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var renewed struct {
		Token           string `json:"token"`
		TokenExpiration int64  `json:"tokenExpiration"`
	}

	if err := json.Unmarshal([]byte(body), &renewed); err != nil {
		t.Fatalf("decode renew: %v (%s)", err, body)
	}

	if renewed.Token != lease {
		t.Fatal("renew answers a new token, want the presented one kept")
	}

	if renewed.TokenExpiration <= 0 {
		t.Fatalf("renew answers no expiry (%s)", body)
	}
}

// checkpointPaths starts a mutating update and returns its paths with its
// lease token.
func checkpointPaths(t *testing.T, f updateFixture) (updatePaths, string) {
	t.Helper()

	id, lease := f.mintStartedMutating(t)

	return mintedPaths(id), lease
}

// TestCheckpointCycle saves two checkpoints to a mutating update and proves
// the last write wins: export returns the second deployment.
func TestCheckpointCycle(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	paths, lease := checkpointPaths(t, f)

	first := `{"version":3,"deployment":{"resources":[{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev"}]}}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Checkpoint, lease, first))
	if status != http.StatusOK {
		t.Fatalf("first checkpoint answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	const second = `{"resources":[]}`

	status, body = f.doUpdate(
		t,
		leaseRequest(http.MethodPatch, paths.Checkpoint, lease, `{"version":3,"deployment":`+second+`}`),
	)
	if status != http.StatusOK {
		t.Fatalf("second checkpoint answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	status, body = f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/export", ""))
	if status != http.StatusOK {
		t.Fatalf("export answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var exported struct {
		Deployment json.RawMessage `json:"deployment"`
		Version    int             `json:"version"`
	}

	if err := json.Unmarshal([]byte(body), &exported); err != nil {
		t.Fatalf("decode export: %v (%s)", err, body)
	}

	if exported.Version != 3 || string(exported.Deployment) != second {
		t.Fatalf(
			"export = version %d %s, want version 3 with the second deployment",
			exported.Version,
			exported.Deployment,
		)
	}
}

// TestCheckpointPreviewRejected proves a preview saves no checkpoint.
func TestCheckpointPreviewRejected(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	id, lease := f.mintStarted(t)
	paths := mintedPaths(id)

	status, body := f.doUpdate(
		t,
		leaseRequest(http.MethodPatch, paths.Checkpoint, lease, `{"version":3,"deployment":{}}`),
	)
	if status != http.StatusConflict {
		t.Fatalf("preview checkpoint answers %d, want %d (%s)", status, http.StatusConflict, body)
	}
}

// TestCheckpointBodyLimit proves a checkpoint over the size limit answers
// 413 with the limit in the message.
func TestCheckpointBodyLimit(t *testing.T) {
	t.Parallel()

	const limit = 64

	f := newUpdateFixtureWithLimit(t, allowAll(), limit)
	paths, lease := checkpointPaths(t, f)

	big := strings.Repeat("a", limit+1)
	payload := `{"version":3,"deployment":"` + big + `"}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Checkpoint, lease, payload))

	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want %d (%s)", status, http.StatusRequestEntityTooLarge, body)
	}

	var decoded struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode error: %v (%s)", err, body)
	}

	if decoded.Code != http.StatusRequestEntityTooLarge || !strings.Contains(decoded.Message, strconv.Itoa(limit)) {
		t.Fatalf("error = %+v, want code 413 with limit %d in the message", decoded, limit)
	}
}

// TestEventBatchBodyLimit proves an event batch over the size limit is
// rejected before it reaches any store.
func TestEventBatchBodyLimit(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, lease := f.mintStarted(t)
	paths := mintedPaths(id)

	big := strings.Repeat("a", 8<<20+1)
	payload := `{"events":[{"sequence":0,"payload":"` + big + `"}]}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Events, lease, payload))

	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want %d (%s)", status, http.StatusRequestEntityTooLarge, body)
	}

	var decoded struct {
		Code int `json:"code"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil || decoded.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("error = %q, want code 413", body)
	}
}

// TestImportStackCycle imports a deployment through HTTP: the import answers
// the ID of its update, shows in history as kind import, and becomes the
// latest export.
func TestImportStackCycle(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	status, body := f.doUpdate(t, accessRequest(
		http.MethodPost, "/api/stacks/acme/web/prod/import", `{"version":3,"deployment":{"resources":[]}}`,
	))
	if status != http.StatusOK {
		t.Fatalf("import answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var imported struct {
		UpdateID string `json:"updateId"` //nolint:tagliatelle // The Pulumi CLI's wire name; it must not change.
	}

	if err := json.Unmarshal([]byte(body), &imported); err != nil || imported.UpdateID == "" {
		t.Fatalf("import answers %q, want an update id: %v", body, err)
	}

	status, body = f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/updates", ""))
	if status != http.StatusOK {
		t.Fatalf("history answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var history historyPayload

	if err := json.Unmarshal([]byte(body), &history); err != nil {
		t.Fatalf("decode history: %v (%s)", err, body)
	}

	if len(history.Updates) != 1 {
		t.Fatalf("history holds %d updates, want 1 (%s)", len(history.Updates), body)
	}

	got := history.Updates[0]
	checkImportedHistoryEntry(t, got)

	checkImportCreator(t, f, imported.UpdateID)

	status, body = f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/export", ""))
	if status != http.StatusOK || !strings.Contains(body, `"resources"`) {
		t.Fatalf("export answers %d %q, want 200 with the imported deployment", status, body)
	}
}

// checkImportCreator fails when the import update behind updateID records a
// creator other than the caller of the import route.
func checkImportCreator(t *testing.T, f updateFixture, updateID string) {
	t.Helper()

	id, err := stack.ParseUpdateID(updateID)
	if err != nil {
		t.Fatalf("parse update ID %q: %v", updateID, err)
	}

	u, err := f.updates.Get(t.Context(), f.ref, id)
	if err != nil {
		t.Fatalf("get update: %v", err)
	}

	if u.CreatedBy != "tester" {
		t.Fatalf("CreatedBy = %q, want %q", u.CreatedBy, "tester")
	}
}

// checkImportedHistoryEntry proves an import shows in history as kind import
// with the caller as its requester.
func checkImportedHistoryEntry(t *testing.T, got historyEntry) {
	t.Helper()

	if got.Kind != "import" || got.Result != "succeeded" || got.Version != 1 {
		t.Fatalf("history entry = %+v, want kind import result succeeded version 1", got)
	}

	if got.RequestedBy != "tester" {
		t.Fatalf("history entry requestedBy = %q, want the caller %q", got.RequestedBy, "tester")
	}
}

// TestImportStackConflict proves an import while another update runs is
// rejected, and the running update keeps the stack lock.
func TestImportStackConflict(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, _ := f.mintStartedMutating(t)

	status, body := f.doUpdate(t, accessRequest(
		http.MethodPost, "/api/stacks/acme/web/prod/import", `{"version":3,"deployment":{}}`,
	))
	if status != http.StatusConflict {
		t.Fatalf("import during a running update answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	active, err := f.updates.Active(t.Context(), f.ref)
	if err != nil || active.ID != id {
		t.Fatalf("active update = %q, %v; want the running one %q", active.ID, err, id)
	}
}

// TestImportStackInvalidBody proves an import with a bad body answers 400
// and records no update: validation runs before the import update exists.
func TestImportStackInvalidBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: `{`},
		{name: "no schema version", body: `{"version":0,"deployment":{}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdateFixture(t, allowAll())

			status, body := f.doUpdate(
				t,
				accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/import", tc.body),
			)
			if status != http.StatusBadRequest {
				t.Fatalf("import answers %d, want %d (%s)", status, http.StatusBadRequest, body)
			}

			status, body = f.doUpdate(
				t,
				accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/updates", ""),
			)
			if status != http.StatusOK || strings.TrimSpace(body) != `{"updates":[]}` {
				t.Fatalf("history answers %d %q, want 200 with no updates", status, body)
			}
		})
	}
}

// TestExportStackVersion proves versioned export returns only the named
// version: each version holds its own checkpoint, a missing or empty version
// answers 404, and a malformed version answers 400.
func TestExportStackVersion(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	for _, deployment := range []string{`{"v":1}`, `{"v":2}`} {
		status, body := f.doUpdate(t, accessRequest(
			http.MethodPost, "/api/stacks/acme/web/prod/import",
			`{"version":3,"deployment":`+deployment+`}`,
		))
		if status != http.StatusOK {
			t.Fatalf("import answers %d, want %d (%s)", status, http.StatusOK, body)
		}
	}

	// A finished update without a checkpoint leaves its version empty.
	finishMutatingUpdate(t, f, "", stack.StatusSucceeded)

	cases := []struct {
		name   string
		target string
		check  string
		want   int
	}{
		{name: "first version", target: "/api/stacks/acme/web/prod/export/1", want: http.StatusOK, check: `"v":1`},
		{name: "second version", target: "/api/stacks/acme/web/prod/export/2", want: http.StatusOK, check: `"v":2`},
		{name: "missing version", target: "/api/stacks/acme/web/prod/export/99", want: http.StatusNotFound, check: ""},
		{name: "empty version", target: "/api/stacks/acme/web/prod/export/3", want: http.StatusNotFound, check: ""},
		{name: "not a number", target: "/api/stacks/acme/web/prod/export/abc", want: http.StatusBadRequest, check: ""},
		{name: "negative", target: "/api/stacks/acme/web/prod/export/-1", want: http.StatusBadRequest, check: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := f.doUpdate(t, accessRequest(http.MethodGet, tc.target, ""))
			if status != tc.want {
				t.Fatalf("export answers %d, want %d (%s)", status, tc.want, body)
			}

			if tc.check != "" && !strings.Contains(body, tc.check) {
				t.Fatalf("export answers %q, want it to hold %s", body, tc.check)
			}
		})
	}
}

// TestExportStackVersionNoFallback proves versioned export never falls back:
// version 2 keeps no checkpoint of its own after version 1 saved one, so it
// answers 404 even though an older checkpoint exists.
func TestExportStackVersionNoFallback(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	finishMutatingUpdate(t, f, `{"v":1}`, stack.StatusSucceeded)
	finishMutatingUpdate(t, f, "", stack.StatusSucceeded)

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/export/2", ""))
	if status != http.StatusNotFound {
		t.Fatalf("export of an empty version answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}
}

// finishMutatingUpdate creates, starts, and completes a mutating update,
// saving deployment as its checkpoint first when not empty.
func finishMutatingUpdate(t *testing.T, f updateFixture, deployment string, status stack.UpdateStatus) {
	t.Helper()

	u, createErr := f.updates.Create(
		t.Context(),
		f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if createErr != nil {
		t.Fatalf("create: %v", createErr)
	}

	started, startErr := f.updates.Start(t.Context(), f.ref, u.ID)
	if startErr != nil {
		t.Fatalf("start: %v", startErr)
	}

	if deployment != "" {
		input := stack.CheckpointInput{
			Features:      nil,
			Deployment:    []byte(deployment),
			SchemaVersion: 3,
			Sequence:      0,
			Invalid:       false,
		}

		saveErr := f.updates.SaveCheckpoint(t.Context(), f.ref, u.ID, started.Token, input)
		if saveErr != nil {
			t.Fatalf("save checkpoint: %v", saveErr)
		}
	}

	_, completeErr := f.updates.Complete(t.Context(), f.ref, u.ID, started.Token, status)
	if completeErr != nil {
		t.Fatalf("complete: %v", completeErr)
	}
}

// TestCancelUpdateCycle cancels a running mutating update through HTTP: the
// stack is locked while it runs, cancel frees it, and a new update starts.
func TestCancelUpdateCycle(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, _ := f.mintStartedMutating(t)
	paths := mintedPaths(id)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Cancel, ""))
	if status != http.StatusOK {
		t.Fatalf("cancel answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	// A canceled update keeps its number, so the next one does not reuse it.
	u, err := f.updates.Create(
		t.Context(),
		f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start after cancel: %v", err)
	}

	if started.Update.Version != 2 {
		t.Fatalf("version after cancel = %d, want 2", started.Update.Version)
	}
}

// TestCancelUpdateIdempotent proves a retried cancel with the same final
// status succeeds: the CLI retries cancel when a response is lost.
func TestCancelUpdateIdempotent(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, _ := f.mintStartedMutating(t)
	paths := mintedPaths(id)

	for range 2 {
		status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Cancel, ""))
		if status != http.StatusOK {
			t.Fatalf("cancel answers %d, want %d (%s)", status, http.StatusOK, body)
		}
	}
}

// TestCancelUpdateNeedsAccessToken proves the cancel route serves the access
// token, not the update lease: the CLI sends no lease on cancel.
func TestCancelUpdateNeedsAccessToken(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, lease := f.mintStartedMutating(t)
	paths := mintedPaths(id)

	status, _ := f.doUpdate(t, anonymousRequest(http.MethodPost, paths.Cancel, ""))
	if status != http.StatusUnauthorized {
		t.Fatalf("unsigned cancel answers %d, want %d", status, http.StatusUnauthorized)
	}

	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Cancel, lease, ""))
	if status != http.StatusUnauthorized {
		t.Fatalf("leased cancel answers %d, want %d (%s)", status, http.StatusUnauthorized, body)
	}
}

// TestGetStackShowsActiveUpdate proves the stack response always holds
// activeUpdate as a string: the ID of the running mutating update, or ""
// when none runs. A preview never shows.
func TestGetStackShowsActiveUpdate(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	get := func() (int, string) {
		return f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod", ""))
	}

	check := func(want string) {
		t.Helper()

		status, body := get()
		if status != http.StatusOK {
			t.Fatalf("get stack answers %d, want %d (%s)", status, http.StatusOK, body)
		}

		var raw map[string]any
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("decode stack: %v (%s)", err, body)
		}

		active, ok := raw["activeUpdate"]
		if !ok {
			t.Fatalf("stack response lacks activeUpdate (%s)", body)
		}

		text, ok := active.(string)
		if !ok {
			t.Fatalf("activeUpdate = %v, want a string", active)
		}

		if text != want {
			t.Fatalf("activeUpdate = %q, want %q", text, want)
		}
	}

	check("")

	// A preview alone never shows.
	preview, err := f.updates.Create(t.Context(), f.ref, stack.Plan{
		Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create preview: %v", err)
	}

	if _, err := f.updates.Start(t.Context(), f.ref, preview.ID); err != nil {
		t.Fatalf("start preview: %v", err)
	}

	check("")

	id, _ := f.mintStartedMutating(t)

	// A preview takes no lock, so the mutating update still shows.
	check(id.String())

	paths := mintedPaths(id)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Cancel, ""))
	if status != http.StatusOK {
		t.Fatalf("cancel answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	// The preview still runs, so this stays the preview-only case.
	check("")
}

// historyFullEntry is one entry of the stack history with the ADR 28 fields.
type historyFullEntry struct {
	Result          string `json:"result"`
	Kind            string `json:"kind"`
	Version         int    `json:"version"`
	StartTime       int64  `json:"startTime"`
	EndTime         int64  `json:"endTime"`
	UpdateID        string `json:"updateID"`
	RequestedBy     string `json:"requestedBy"`
	CheckpointBytes int64  `json:"checkpointBytes"`
	LeaseExpiresAt  int64  `json:"leaseExpiresAt"`
	RestoredFrom    int64  `json:"restoredFrom"`
}

// historyFullPayload is the stack history with the ADR 28 fields.
type historyFullPayload struct {
	Updates []historyFullEntry `json:"updates"`
}

// historySeed holds the updates a history-field test seeds and the lease
// tokens that must never appear in a response.
type historySeed struct {
	finished stack.Update
	restored stack.Update
	running  stack.Update
	leases   []string
	expiry   int64
	size     int64
}

// seedHistoryFields finishes an update with a checkpoint, restores its
// version, and holds a running update without one.
func seedHistoryFields(t *testing.T, f updateFixture) historySeed {
	t.Helper()

	const deployment = `{"v":1}`

	u, createErr := f.updates.Create(t.Context(), f.ref, stack.Plan{
		Kind:         stack.KindUpdate,
		DryRun:       false,
		CreatedBy:    "tester",
		RestoredFrom: 0, GitHead: "",
	})
	if createErr != nil {
		t.Fatalf("create: %v", createErr)
	}

	started, startErr := f.updates.Start(t.Context(), f.ref, u.ID)
	if startErr != nil {
		t.Fatalf("start: %v", startErr)
	}

	input := stack.CheckpointInput{
		Features:      nil,
		Deployment:    []byte(deployment),
		SchemaVersion: 3,
		Sequence:      0,
		Invalid:       false,
	}

	if saveErr := f.updates.SaveCheckpoint(t.Context(), f.ref, u.ID, started.Token, input); saveErr != nil {
		t.Fatalf("save checkpoint: %v", saveErr)
	}

	finished, completeErr := f.updates.Complete(t.Context(), f.ref, u.ID, started.Token, stack.StatusSucceeded)
	if completeErr != nil {
		t.Fatalf("complete: %v", completeErr)
	}

	restored, restoreErr := f.updates.Restore(t.Context(), f.ref, 1, "tester")
	if restoreErr != nil {
		t.Fatalf("restore: %v", restoreErr)
	}

	held, heldErr := f.updates.Create(t.Context(), f.ref, stack.Plan{
		Kind:         stack.KindUpdate,
		DryRun:       false,
		CreatedBy:    "tester",
		RestoredFrom: 0, GitHead: "",
	})
	if heldErr != nil {
		t.Fatalf("create running: %v", heldErr)
	}

	running, runningErr := f.updates.Start(t.Context(), f.ref, held.ID)
	if runningErr != nil {
		t.Fatalf("start running: %v", runningErr)
	}

	return historySeed{
		finished: finished,
		restored: restored,
		running:  running.Update,
		leases:   []string{started.Token.Reveal(), running.Token.Reveal()},
		expiry:   running.Update.Lease.ExpiresAt.Unix(),
		size:     int64(len(deployment)),
	}
}

// getHistoryFields reads the stack history with the ADR 28 fields.
func getHistoryFields(t *testing.T, f updateFixture) (string, historyFullPayload) {
	t.Helper()

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/updates", ""))
	if status != http.StatusOK {
		t.Fatalf("history answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var history historyFullPayload

	if err := json.Unmarshal([]byte(body), &history); err != nil {
		t.Fatalf("decode history: %v (%s)", err, body)
	}

	return body, history
}

// historyByVersion returns the entry with version.
func historyByVersion(t *testing.T, history historyFullPayload, version int) historyFullEntry {
	t.Helper()

	for _, entry := range history.Updates {
		if entry.Version == version {
			return entry
		}
	}

	t.Fatalf("history holds no version %d (%+v)", version, history.Updates)

	return historyFullEntry{
		Result:          "",
		Kind:            "",
		Version:         0,
		StartTime:       0,
		EndTime:         0,
		UpdateID:        "",
		RequestedBy:     "",
		CheckpointBytes: 0,
		LeaseExpiresAt:  0,
		RestoredFrom:    0,
	}
}

// checkFinishedHistoryEntry proves a finished update shows its ID, requester
// and checkpoint size, and no lease expiry or restore source.
func checkFinishedHistoryEntry(t *testing.T, id stack.UpdateID, size int64, got historyFullEntry) {
	t.Helper()

	if got.Result != "succeeded" || got.Kind != "update" || got.Version != 1 {
		t.Fatalf("finished entry = %+v, want result succeeded kind update version 1", got)
	}

	if got.StartTime <= 0 || got.EndTime <= 0 {
		t.Fatalf("finished entry = %+v, want start and end times set", got)
	}

	if got.UpdateID != id.String() {
		t.Fatalf("updateID = %q, want %q", got.UpdateID, id.String())
	}

	if got.RequestedBy != "tester" {
		t.Fatalf("requestedBy = %q, want %q", got.RequestedBy, "tester")
	}

	if got.CheckpointBytes != size {
		t.Fatalf("checkpointBytes = %d, want %d", got.CheckpointBytes, size)
	}

	if got.LeaseExpiresAt != 0 {
		t.Fatalf("leaseExpiresAt = %d, want 0 for a finished update", got.LeaseExpiresAt)
	}

	if got.RestoredFrom != 0 {
		t.Fatalf("restoredFrom = %d, want 0 for a plain update", got.RestoredFrom)
	}
}

// checkRestoredHistoryEntry proves a restore shows its source version and the
// copied checkpoint size.
func checkRestoredHistoryEntry(t *testing.T, id stack.UpdateID, size int64, got historyFullEntry) {
	t.Helper()

	if got.Kind != "restore" || got.Result != "succeeded" || got.Version != 2 {
		t.Fatalf("restored entry = %+v, want kind restore result succeeded version 2", got)
	}

	if got.UpdateID != id.String() {
		t.Fatalf("updateID = %q, want %q", got.UpdateID, id.String())
	}

	if got.RequestedBy != "tester" {
		t.Fatalf("requestedBy = %q, want %q", got.RequestedBy, "tester")
	}

	if got.CheckpointBytes != size {
		t.Fatalf("checkpointBytes = %d, want the copied size %d", got.CheckpointBytes, size)
	}

	if got.LeaseExpiresAt != 0 {
		t.Fatalf("leaseExpiresAt = %d, want 0 for a finished update", got.LeaseExpiresAt)
	}

	if got.RestoredFrom != 1 {
		t.Fatalf("restoredFrom = %d, want 1", got.RestoredFrom)
	}
}

// checkRunningHistoryEntry proves the running update shows its requester and
// lease expiry, and no checkpoint size yet.
func checkRunningHistoryEntry(t *testing.T, id stack.UpdateID, expiry int64, got historyFullEntry) {
	t.Helper()

	if got.Result != "in-progress" || got.Kind != "update" || got.Version != 3 {
		t.Fatalf("running entry = %+v, want result in-progress kind update version 3", got)
	}

	if got.UpdateID != id.String() {
		t.Fatalf("updateID = %q, want %q", got.UpdateID, id.String())
	}

	if got.RequestedBy != "tester" {
		t.Fatalf("requestedBy = %q, want %q", got.RequestedBy, "tester")
	}

	if got.CheckpointBytes != 0 {
		t.Fatalf("checkpointBytes = %d, want 0 before any checkpoint", got.CheckpointBytes)
	}

	if got.EndTime != 0 {
		t.Fatalf("endTime = %d, want 0 for a running update", got.EndTime)
	}

	if expiry <= 0 {
		t.Fatalf("seed lease expiry = %d, want above 0", expiry)
	}

	if got.LeaseExpiresAt != expiry {
		t.Fatalf("leaseExpiresAt = %d, want the lease expiry %d", got.LeaseExpiresAt, expiry)
	}

	if got.RestoredFrom != 0 {
		t.Fatalf("restoredFrom = %d, want 0 for a plain update", got.RestoredFrom)
	}
}

// TestHistoryShowsNewFields proves the history carries the update ID, the
// requester, the checkpoint size, the lease expiry and the restore source
// (ADR 28 decision 4), and keeps every old field with its name and shape.
func TestHistoryShowsNewFields(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seed := seedHistoryFields(t, f)

	_, history := getHistoryFields(t, f)

	if len(history.Updates) != 3 {
		t.Fatalf("history holds %d updates, want 3 (%+v)", len(history.Updates), history.Updates)
	}

	checkFinishedHistoryEntry(t, seed.finished.ID, seed.size, historyByVersion(t, history, 1))
	checkRestoredHistoryEntry(t, seed.restored.ID, seed.size, historyByVersion(t, history, 2))
	checkRunningHistoryEntry(t, seed.running.ID, seed.expiry, historyByVersion(t, history, 3))
}

// TestHistoryKeepsOldFieldNames proves every history entry carries the five
// old fields with their names, the five new fields, and nothing else: no
// rename, no removal, no token.
func TestHistoryKeepsOldFieldNames(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedHistoryFields(t, f)

	body, _ := getHistoryFields(t, f)

	var raw struct {
		Updates []map[string]any `json:"updates"`
	}

	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode history: %v (%s)", err, body)
	}

	want := map[string]bool{
		"result": true, "kind": true, "version": true, "startTime": true, "endTime": true,
		"updateID": true, "requestedBy": true, "checkpointBytes": true,
		"leaseExpiresAt": true, "restoredFrom": true,
	}

	for _, entry := range raw.Updates {
		if len(entry) != len(want) {
			t.Fatalf("history entry holds %d keys, want %d (%v)", len(entry), len(want), entry)
		}

		for key := range entry {
			if !want[key] {
				t.Fatalf("history entry holds unexpected key %q (%v)", key, entry)
			}
		}
	}
}

// TestHistoryHidesLeaseSecrets proves the history carries no lease token:
// neither presented token appears in the response body.
func TestHistoryHidesLeaseSecrets(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seed := seedHistoryFields(t, f)

	body, _ := getHistoryFields(t, f)

	for _, lease := range seed.leases {
		if lease == "" {
			t.Fatal("seed holds an empty lease token")
		}

		if strings.Contains(body, lease) {
			t.Fatal("history response holds a lease token")
		}
	}

	if strings.Contains(body, `"token"`) {
		t.Fatalf("history response holds a token field (%s)", body)
	}
}

// TestCreateUpdateRecordsCaller checks that a new update records the subject
// of the caller as its creator (ADR 28 decision 4).
func TestCreateUpdateRecordsCaller(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	code, body := f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/update", `{}`))
	if code != http.StatusOK {
		t.Fatalf("create update: status %d, body %s", code, body)
	}

	var resp struct {
		UpdateID string `json:"updateID"`
	}

	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	id, err := stack.ParseUpdateID(resp.UpdateID)
	if err != nil {
		t.Fatalf("parse update ID %q: %v", resp.UpdateID, err)
	}

	u, err := f.updates.Get(t.Context(), f.ref, id)
	if err != nil {
		t.Fatalf("get update: %v", err)
	}

	if u.CreatedBy != "tester" {
		t.Fatalf("CreatedBy = %q, want %q", u.CreatedBy, "tester")
	}
}

// seedRestoreVersion imports version 1 of the fixture stack, so restore
// tests have a checkpoint to copy.
func seedRestoreVersion(t *testing.T, f updateFixture) {
	t.Helper()

	input := stack.CheckpointInput{
		Features:      nil,
		Deployment:    []byte(`{"v":1}`),
		SchemaVersion: 3,
		Sequence:      0,
		Invalid:       false,
	}

	if _, err := f.updates.Import(t.Context(), f.ref, "tester", input); err != nil {
		t.Fatalf("import: %v", err)
	}
}

// decodeRestoreID decodes a restore answer into its update ID.
func decodeRestoreID(t *testing.T, body string) stack.UpdateID {
	t.Helper()

	var decoded struct {
		UpdateID string `json:"updateId"` //nolint:tagliatelle // The Pulumi CLI's wire name; it must not change.
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil || decoded.UpdateID == "" {
		t.Fatalf("restore answers %q, want an update id: %v", body, err)
	}

	id, err := stack.ParseUpdateID(decoded.UpdateID)
	if err != nil {
		t.Fatalf("parse update ID %q: %v", decoded.UpdateID, err)
	}

	return id
}

// checkErrorShape proves a route error carries the Pulumi shape: the code
// matches the status and the message is not empty.
func checkErrorShape(t *testing.T, status int, body string) {
	t.Helper()

	var decoded struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode error: %v (%s)", err, body)
	}

	if decoded.Code != status || decoded.Message == "" {
		t.Fatalf("error = %+v, want code %d with a message", decoded, status)
	}
}

// TestRestoreStackCycle restores version 1 through HTTP: the restore answers
// the ID of its update, shows in history as kind restore with RestoredFrom
// set, records the caller, and becomes the latest export.
func TestRestoreStackCycle(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedRestoreVersion(t, f)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/restore/1", ""))
	if status != http.StatusOK {
		t.Fatalf("restore answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	id := decodeRestoreID(t, body)

	u, err := f.updates.Get(t.Context(), f.ref, id)
	if err != nil {
		t.Fatalf("get restore update: %v", err)
	}

	if u.Kind != stack.KindRestore || u.RestoredFrom != 1 || u.CreatedBy != "tester" {
		t.Fatalf("restore update = %+v, want kind restore restoredFrom 1 createdBy tester", u)
	}

	if u.Status != stack.StatusSucceeded || u.Version != 2 {
		t.Fatalf("restore update = %+v, want status succeeded version 2", u)
	}

	status, body = f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/export", ""))
	if status != http.StatusOK || !strings.Contains(body, `"v":1`) {
		t.Fatalf("export answers %d %q, want 200 with the restored deployment", status, body)
	}
}

// TestRestoreStackVersionRejected proves a restore with no positive integer
// version answers 400 in the Pulumi shape and records no update.
func TestRestoreStackVersionRejected(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"0", "-1", "abc"} {
		t.Run("version "+version, func(t *testing.T) {
			t.Parallel()

			f := newUpdateFixture(t, allowAll())
			seedRestoreVersion(t, f)

			target := "/api/stacks/acme/web/prod/restore/" + version

			status, body := f.doUpdate(t, accessRequest(http.MethodPost, target, ""))
			if status != http.StatusBadRequest {
				t.Fatalf("restore answers %d, want %d (%s)", status, http.StatusBadRequest, body)
			}

			checkErrorShape(t, http.StatusBadRequest, body)
		})
	}
}

// TestRestoreStackMissingVersion proves a restore of a version with no
// checkpoint answers 404 in the Pulumi shape and records no update.
func TestRestoreStackMissingVersion(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedRestoreVersion(t, f)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/restore/99", ""))
	if status != http.StatusNotFound {
		t.Fatalf("restore answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}

	checkErrorShape(t, http.StatusNotFound, body)
}

// TestRestoreStackConflict proves a restore while another update runs answers
// 409 in the Pulumi shape, and the running update keeps the stack lock.
func TestRestoreStackConflict(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedRestoreVersion(t, f)

	id, _ := f.mintStartedMutating(t)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/restore/1", ""))
	if status != http.StatusConflict {
		t.Fatalf("restore during a running update answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	checkErrorShape(t, http.StatusConflict, body)

	active, err := f.updates.Active(t.Context(), f.ref)
	if err != nil || active.ID != id {
		t.Fatalf("active update = %q, %v; want the running one %q", active.ID, err, id)
	}
}

// TestStartUpdateAnswersJournalVersionZero proves StartUpdate answers
// journalVersion 0 even when the client offers journaling, so the CLI
// saves checkpoints instead of journal entries.
func TestStartUpdateAnswersJournalVersionZero(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/update", `{}`))
	if status != http.StatusOK {
		t.Fatalf("create answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var created struct {
		UpdateID string `json:"updateID"`
	}

	if err := json.Unmarshal([]byte(body), &created); err != nil || created.UpdateID == "" {
		t.Fatalf("create answers %q, want an update ID: %v", body, err)
	}

	target := "/api/stacks/acme/web/prod/update/" + created.UpdateID
	status, body = f.doUpdate(t, accessRequest(http.MethodPost, target, `{"journalVersion":1}`))

	if status != http.StatusOK {
		t.Fatalf("start answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var started struct {
		Version        int   `json:"version"`
		JournalVersion int64 `json:"journalVersion"`
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decode start: %v (%s)", err, body)
	}

	if started.JournalVersion != 0 {
		t.Fatalf("start answers journalVersion %d, want 0", started.JournalVersion)
	}
}

// verbatimPaths starts a mutating update and returns its verbatim and
// delta paths with its lease token.
func verbatimPaths(t *testing.T, f updateFixture) (updatePaths, string) {
	t.Helper()

	id, lease := f.mintStartedMutating(t)

	return mintedPaths(id), lease
}

// exportDeployment reads the stack export through HTTP and returns its
// deployment bytes exactly as stored.
func exportDeployment(t *testing.T, f updateFixture) string {
	t.Helper()

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/export", ""))
	if status != http.StatusOK {
		t.Fatalf("export answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var exported struct {
		Deployment json.RawMessage `json:"deployment"`
	}

	if err := json.Unmarshal([]byte(body), &exported); err != nil {
		t.Fatalf("decode export: %v (%s)", err, body)
	}

	return string(exported.Deployment)
}

// deltaHash returns the hex SHA-256 of result, as the CLI computes the
// checkpoint hash of a delta.
func deltaHash(result string) string {
	sum := sha256.Sum256([]byte(result))

	return hex.EncodeToString(sum[:])
}

// deltaEditJSON renders one text edit in the shape the CLI sends: a
// gotextdiff TextEdit with byte offsets into the saved deployment.
func deltaEditJSON(start, end int, newText string) string {
	return fmt.Sprintf(
		`{"Span":{"uri":"","start":{"line":1,"column":1,"offset":%d},`+
			`"end":{"line":1,"column":1,"offset":%d}},"NewText":%s}`,
		start,
		end,
		strconv.Quote(newText),
	)
}

// TestCheckpointVerbatimCycle saves a verbatim checkpoint and proves the
// export holds the inner deployment it wraps, and that a retry with the
// same sequence number succeeds.
func TestCheckpointVerbatimCycle(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	paths, lease := verbatimPaths(t, f)

	const deployment = `{"version":3,"deployment":{"resources":[]}}`
	const inner = `{"resources":[]}`

	payload := `{"version":3,"untypedDeployment":` + deployment + `,"sequenceNumber":1}`
	req := leaseRequest(http.MethodPatch, paths.Verbatim, lease, payload)
	status, body := f.doUpdate(t, req)

	if status != http.StatusOK {
		t.Fatalf("verbatim answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	status, body = f.doUpdate(t, req)
	if status != http.StatusOK {
		t.Fatalf("verbatim retry answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	if got := exportDeployment(t, f); got != inner {
		t.Fatalf("export = %q, want the inner deployment %q", got, inner)
	}
}

// TestCheckpointVerbatimConflict proves a verbatim save that repeats the
// sequence number with other content answers 409 and saves nothing, so the
// CLI falls back to a full save.
func TestCheckpointVerbatimConflict(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	paths, lease := verbatimPaths(t, f)

	const first = `{"version":3,"deployment":{"resources":[]}}`
	const other = `{"version":3,"deployment":{"resources":[{"urn":"x"}]}}`
	const firstInner = `{"resources":[]}`

	firstPayload := `{"version":3,"untypedDeployment":` + first + `,"sequenceNumber":1}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Verbatim, lease, firstPayload))

	if status != http.StatusOK {
		t.Fatalf("verbatim answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	otherPayload := `{"version":3,"untypedDeployment":` + other + `,"sequenceNumber":1}`
	status, body = f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Verbatim, lease, otherPayload))

	if status != http.StatusConflict {
		t.Fatalf("verbatim conflict answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	checkErrorShape(t, http.StatusConflict, body)

	if got := exportDeployment(t, f); got != firstInner {
		t.Fatalf("export = %q, want the first save %q after a conflict", got, firstInner)
	}
}

// TestCheckpointVerbatimNeedsLease proves a verbatim save without the
// update lease token answers 401.
func TestCheckpointVerbatimNeedsLease(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	paths, _ := verbatimPaths(t, f)

	payload := `{"version":3,"untypedDeployment":{},"sequenceNumber":1}`
	status, _ := f.doUpdate(t, anonymousRequest(http.MethodPatch, paths.Verbatim, payload))

	if status != http.StatusUnauthorized {
		t.Fatalf("verbatim without a lease answers %d, want %d", status, http.StatusUnauthorized)
	}
}

// TestCheckpointVerbatimSequenceZero proves a verbatim save with a sequence
// number below 1 answers 409 and saves nothing, so the CLI falls back to a
// full save. The CLI numbers saves from 1.
func TestCheckpointVerbatimSequenceZero(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	paths, lease := verbatimPaths(t, f)

	payload := `{"version":3,"untypedDeployment":{"version":3,"deployment":{"resources":[]}},"sequenceNumber":0}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Verbatim, lease, payload))

	if status != http.StatusConflict {
		t.Fatalf("verbatim sequence 0 answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	checkErrorShape(t, http.StatusConflict, body)
}

// TestNegativeDeltaCutoffFails proves a negative checkpoint delta cutoff
// fails handler construction instead of silently turning the capability off.
func TestNegativeDeltaCutoffFails(t *testing.T) {
	t.Parallel()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stores := &recordingStackStore{
		placed: nil, placeErr: nil,
		created:   nil,
		renamed:   nil,
		tags:      nil,
		deleted:   nil,
		forced:    nil,
		entries:   nil,
		deleteErr: nil,
	}

	stacks, err := stack.NewService(stack.Config{
		Store: stores, Namespaces: namespaces, Now: nil, State: nil,
		Retention:   0,
		ManualLocks: nil,
	})
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	mem := newMemUpdateStore()

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            mem.resolving(stacks),
		Checkpoints:        mem,
		Events:             mem,
		Namespaces:         namespaces,
		Stacks:             stacks,
		Blobs:              newMemBlob(),
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
		Tokens:             nil,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	_, err = pulumi.NewHandler(pulumi.Config{
		Placer:                  nil,
		StateGuard:              stategate.Off("the test keeps no gates"),
		SessionPolicy:           nil,
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 updates,
		Secrets:                 newTestSecrets(t, namespaces),
		Namespaces:              namespaces,
		Authenticator:           tokenAuthenticator{},
		Authorizer:              allowAll(),
		Sessions:                nil,
		AddressGate:             nil,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 baseurl.Resolver{},
		MaxBodyBytesInFlight:    0,
		Gate:                    nil,
		CheckpointDeltaCutoff:   -1,
		Audit:                   newTestAudit(),
		OnAuditFailure:          nil,
		DenialLimiter:           nil,
	})
	if !errors.Is(err, pulumi.ErrInvalidCutoff) {
		t.Fatalf("negative cutoff: err = %v, want %v", err, pulumi.ErrInvalidCutoff)
	}
}

// TestCheckpointDeltaCycle saves a verbatim checkpoint and a delta, and
// proves the export holds the patched result, a retry succeeds, and every
// invalid delta answers 409 in the Pulumi shape without saving anything.
func TestCheckpointDeltaCycle(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	paths, lease := verbatimPaths(t, f)

	const base = `{"deployment":{"a":1,"b":2}}`
	const result = `{"deployment":{"a":1,"b":3}}`

	// The digit 2 sits at byte offset 25 of the base.
	edits := "[" + deltaEditJSON(25, 26, "3") + "]"

	verbatim := `{"version":3,"untypedDeployment":` + base + `,"sequenceNumber":1}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Verbatim, lease, verbatim))

	if status != http.StatusOK {
		t.Fatalf("verbatim answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	delta := `{"version":3,"checkpointHash":` + strconv.Quote(deltaHash(result)) +
		`,"sequenceNumber":2,"deploymentDelta":` + edits + `}`
	status, body = f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Delta, lease, delta))

	if status != http.StatusOK {
		t.Fatalf("delta answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	deltaReq := leaseRequest(http.MethodPatch, paths.Delta, lease, delta)
	status, body = f.doUpdate(t, deltaReq)
	if status != http.StatusOK {
		t.Fatalf("delta retry answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	if got, want := exportDeployment(t, f), `{"a":1,"b":3}`; got != want {
		t.Fatalf("export = %q, want the patched result %q", got, want)
	}

	invalid := []struct {
		name string
		body string
	}{
		{
			name: "skipped sequence",
			body: `{"version":3,"checkpointHash":` + strconv.Quote(deltaHash(result)) +
				`,"sequenceNumber":4,"deploymentDelta":` + edits + `}`,
		},
		{
			name: "wrong hash",
			body: `{"version":3,"checkpointHash":` +
				strconv.Quote(deltaHash(`{"a":1,"b":4}`)) +
				`,"sequenceNumber":3,"deploymentDelta":` + edits + `}`,
		},
		{
			name: "same sequence, other hash",
			body: `{"version":3,"checkpointHash":` +
				strconv.Quote(deltaHash(`{"a":1,"b":4}`)) +
				`,"sequenceNumber":2,"deploymentDelta":` + edits + `}`,
		},
		{
			name: "edit outside the text",
			body: `{"version":3,"checkpointHash":` + strconv.Quote(deltaHash(result)) +
				`,"sequenceNumber":3,"deploymentDelta":[` + deltaEditJSON(0, 99, "x") + `]}`,
		},
		{
			name: "overlapping edits",
			body: `{"version":3,"checkpointHash":` + strconv.Quote(deltaHash(result)) +
				`,"sequenceNumber":3,"deploymentDelta":[` + deltaEditJSON(1, 3, "x") + `,` +
				deltaEditJSON(2, 4, "y") + `]}`,
		},
		{
			name: "delta is not a list",
			body: `{"version":3,"checkpointHash":` + strconv.Quote(deltaHash(result)) +
				`,"sequenceNumber":3,"deploymentDelta":{}}`,
		},
	}

	for _, tt := range invalid {
		status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Delta, lease, tt.body))
		if status != http.StatusConflict {
			t.Errorf("%s answers %d, want %d (%s)", tt.name, status, http.StatusConflict, body)

			continue
		}

		checkErrorShape(t, http.StatusConflict, body)
	}

	if got, want := exportDeployment(t, f), `{"a":1,"b":3}`; got != want {
		t.Fatalf("export = %q, want the patched result %q after rejected deltas", got, want)
	}
}

// TestCheckpointDeltaServesInnerDeployment proves a delta over the versioned
// bytes of a verbatim save exports the inner deployment: sequenced saves
// keep the versioned text for later deltas, while reads serve what it
// wraps.
func TestCheckpointDeltaServesInnerDeployment(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	paths, lease := verbatimPaths(t, f)

	const base = `{"version":3,"deployment":{"a":1}}`
	const result = `{"version":3,"deployment":{"a":2}}`
	const inner = `{"a":2}`

	// The digit 1 sits at byte offset 31 of the base.
	edits := "[" + deltaEditJSON(31, 32, "2") + "]"

	verbatim := `{"version":3,"untypedDeployment":` + base + `,"sequenceNumber":1}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Verbatim, lease, verbatim))

	if status != http.StatusOK {
		t.Fatalf("verbatim answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	delta := `{"version":3,"checkpointHash":` + strconv.Quote(deltaHash(result)) +
		`,"sequenceNumber":2,"deploymentDelta":` + edits + `}`
	status, body = f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Delta, lease, delta))

	if status != http.StatusOK {
		t.Fatalf("delta answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	if got := exportDeployment(t, f); got != inner {
		t.Fatalf("export = %q, want the inner deployment %q", got, inner)
	}
}

// TestCheckpointDeltaWithoutCheckpoint proves a delta on an update that
// saved nothing answers 409: the first save of an update is always full.
func TestCheckpointDeltaWithoutCheckpoint(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	paths, lease := verbatimPaths(t, f)

	delta := `{"version":3,"checkpointHash":` + strconv.Quote(deltaHash(`{}`)) +
		`,"sequenceNumber":1,"deploymentDelta":[]}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Delta, lease, delta))

	if status != http.StatusConflict {
		t.Fatalf("delta without a checkpoint answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	checkErrorShape(t, http.StatusConflict, body)
}

// TestCheckpointDeltaOverLimit proves a delta whose result is over the
// checkpoint size limit answers 409, not 413: the CLI then sends a
// verbatim save, which the normal limit governs.
func TestCheckpointDeltaOverLimit(t *testing.T) {
	t.Parallel()

	const limit = 2048

	f := newUpdateFixtureWithLimit(t, allowAll(), limit)
	paths, lease := verbatimPaths(t, f)

	// The base nearly fills the limit, so the verbatim save fits while a
	// delta that grows it past the limit does not.
	base := `{"deployment":{"a":"` + strings.Repeat("a", 1890) + `"}}`
	verbatim := `{"version":3,"untypedDeployment":` + base + `,"sequenceNumber":1}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Verbatim, lease, verbatim))

	if status != http.StatusOK {
		t.Fatalf("verbatim answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	grown := `{"deployment":{"a":"` + strings.Repeat("b", 200) + strings.Repeat("a", 1880) + `"}}`
	edits := `[` + deltaEditJSON(20, 30, strings.Repeat("b", 200)) + `]`
	delta := `{"version":3,"checkpointHash":` + strconv.Quote(deltaHash(grown)) +
		`,"sequenceNumber":2,"deploymentDelta":` + edits + `}`
	status, body = f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Delta, lease, delta))

	if status != http.StatusConflict {
		t.Fatalf("oversized delta result answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	checkErrorShape(t, http.StatusConflict, body)

	if got := exportDeployment(t, f); got != `{"a":"`+strings.Repeat("a", 1890)+`"}` {
		t.Fatal("export changed after a rejected delta")
	}
}

// capabilitiesWithConfig is one capabilities answer entry that carries a
// configuration document, such as the delta checkpoint cutoff.
type capabilitiesWithConfig struct {
	Capability    string          `json:"capability"`
	Version       int             `json:"version"`
	Configuration json.RawMessage `json:"configuration"`
}

// capabilitiesAnswer decodes a capabilities answer that carries
// configurations.
type capabilitiesAnswer struct {
	Capabilities []capabilitiesWithConfig `json:"capabilities"`
}

// capabilityName is one capabilities answer entry without configuration,
// for answers that must hide a capability.
type capabilityName struct {
	Capability string `json:"capability"`
}

// capabilitiesNames decodes a capabilities answer when only names matter.
type capabilitiesNames struct {
	Capabilities []capabilityName `json:"capabilities"`
}

// deltaCutoffConfig is the delta capability configuration as the CLI reads
// it.
type deltaCutoffConfig struct {
	CheckpointCutoffSizeBytes int `json:"checkpointCutoffSizeBytes"`
}

// TestCapabilitiesAdvertiseDeltaCutoff proves the capabilities answer
// carries delta-checkpoint-uploads-v2 with the configured cutoff.
func TestCapabilitiesAdvertiseDeltaCutoff(t *testing.T) {
	t.Parallel()

	f := newUpdateFixtureWithCutoff(t, allowAll(), 0, pulumi.DefaultCheckpointDeltaCutoff)

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, "/api/capabilities", ""))
	if status != http.StatusOK {
		t.Fatalf("capabilities answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var decoded capabilitiesAnswer

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode capabilities: %v (%s)", err, body)
	}

	for _, c := range decoded.Capabilities {
		if c.Capability != "delta-checkpoint-uploads-v2" {
			continue
		}

		if c.Version != 2 {
			t.Fatalf("delta capability version = %d, want 2 (%s)", c.Version, body)
		}

		var cfg deltaCutoffConfig

		if err := json.Unmarshal(c.Configuration, &cfg); err != nil {
			t.Fatalf("decode delta configuration: %v (%s)", err, c.Configuration)
		}

		if cfg.CheckpointCutoffSizeBytes != pulumi.DefaultCheckpointDeltaCutoff {
			t.Fatalf("cutoff = %d, want %d", cfg.CheckpointCutoffSizeBytes, pulumi.DefaultCheckpointDeltaCutoff)
		}

		return
	}

	t.Fatalf("capabilities = %s, want delta-checkpoint-uploads-v2 among them", body)
}

// TestCapabilitiesHideDeltaWhenCutoffZero proves a zero cutoff hides the
// delta checkpoint capability, so the CLI sends whole checkpoints.
func TestCapabilitiesHideDeltaWhenCutoffZero(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, "/api/capabilities", ""))
	if status != http.StatusOK {
		t.Fatalf("capabilities answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var decoded capabilitiesNames

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode capabilities: %v (%s)", err, body)
	}

	for _, c := range decoded.Capabilities {
		if c.Capability == "delta-checkpoint-uploads-v2" {
			t.Fatalf("capabilities = %s, want no delta capability with a zero cutoff", body)
		}
	}
}

// TestStartAnswersJournalVersion checks that a start answers journalVersion
// with the value that the store kept. Until the version gate of ADR 43 lands,
// every update answers 0, which means checkpoints. The field must be present,
// because the CLI reads it.
func TestStartAnswersJournalVersion(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	id := createPreviewUpdate(t, f)
	paths := mintedPaths(id)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Start, `{}`))
	if status != http.StatusOK {
		t.Fatalf("start answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var started struct {
		JournalVersion *int `json:"journalVersion"`
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decode start: %v (%s)", err, body)
	}

	if started.JournalVersion == nil {
		t.Fatalf("start answers no journalVersion (%s)", body)
	}

	if *started.JournalVersion != 0 {
		t.Fatalf("journalVersion = %d, want 0 until the version gate lands", *started.JournalVersion)
	}
}

// TestStartNegotiatesJournalVersion checks that a start answers the journal
// version of the gate. The production server supports only version 0, so a
// CLI that offers version 2 still gets 0, and a start that carries a body
// that is not JSON is refused.
func TestStartNegotiatesJournalVersion(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	id := createPreviewUpdate(t, f)
	paths := mintedPaths(id)

	status, body := f.doUpdate(t, updateRequest{
		Method:    http.MethodPost,
		Target:    paths.Start,
		Access:    "test-token",
		Lease:     "",
		Body:      `{"journalVersion":2}`,
		UserAgent: "pulumi-cli/1 (v3.268.0; linux)",
	})
	if status != http.StatusOK {
		t.Fatalf("start answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var started struct {
		JournalVersion int `json:"journalVersion"`
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decode start: %v (%s)", err, body)
	}

	if started.JournalVersion != startJournalVersion {
		t.Fatalf("journalVersion = %d, want %d, the server's maximum", started.JournalVersion, startJournalVersion)
	}
}

// TestStartRefusesMalformedOffer checks that a start whose body is not JSON is
// refused, and that the update stays not started.
func TestStartRefusesMalformedOffer(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	id := createPreviewUpdate(t, f)
	paths := mintedPaths(id)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Start, `{"journalVersion":`))
	if status != http.StatusBadRequest {
		t.Fatalf("start with a malformed body answers %d, want %d (%s)", status, http.StatusBadRequest, body)
	}
}

// badDeployment is a body for the deployment member that the CLI cannot read.
type badDeployment struct {
	name       string
	deployment string
}

// badDeployments returns the bodies for the deployment member that the CLI
// cannot read.
func badDeployments() []badDeployment {
	return []badDeployment{
		{name: "a string", deployment: `"notanobject"`},
		{name: "a number", deployment: `42`},
		{name: "an array", deployment: `[1,2]`},
		{name: "null", deployment: `null`},
		{name: "no deployment", deployment: ``},
		{name: "resources that are a string", deployment: `{"resources":"x"}`},
	}
}

// bodyWithDeployment returns a body of the given shape with deployment in the
// member that the route reads. An empty deployment leaves the member out.
func bodyWithDeployment(member, versionMember, deployment string) string {
	if deployment == "" {
		return `{"` + versionMember + `":3}`
	}

	return `{"` + versionMember + `":3,"` + member + `":` + deployment + `}`
}

// TestImportRefusesADeploymentThatIsNotAnObject proves that the import route
// answers 400 in the error shape of the Pulumi Cloud-compatible API for a
// deployment that the CLI cannot read, and that the stack keeps the checkpoint
// that it had.
func TestImportRefusesADeploymentThatIsNotAnObject(t *testing.T) {
	t.Parallel()

	for _, tc := range badDeployments() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdateFixture(t, allowAll())

			const good = `{"resources":[]}`

			status, body := f.doUpdate(t, accessRequest(
				http.MethodPost, "/api/stacks/acme/web/prod/import", `{"version":3,"deployment":`+good+`}`,
			))
			if status != http.StatusOK {
				t.Fatalf("import of a good deployment answers %d, want %d (%s)", status, http.StatusOK, body)
			}

			status, body = f.doUpdate(t, accessRequest(
				http.MethodPost, "/api/stacks/acme/web/prod/import",
				bodyWithDeployment("deployment", "version", tc.deployment),
			))
			if status != http.StatusBadRequest {
				t.Fatalf("import answers %d, want %d (%s)", status, http.StatusBadRequest, body)
			}

			checkErrorShape(t, http.StatusBadRequest, body)

			if got := exportDeployment(t, f); got != good {
				t.Fatalf("export = %s, want %s: a refused import must store nothing", got, good)
			}
		})
	}
}

// TestCheckpointSaveRefusesADeploymentThatIsNotAnObject proves that the
// checkpoint routes of a running update answer 400 in the error shape of the
// Pulumi Cloud-compatible API for a deployment that the CLI cannot read, and
// keep the last good checkpoint.
func TestCheckpointSaveRefusesADeploymentThatIsNotAnObject(t *testing.T) {
	t.Parallel()

	for _, tc := range badDeployments() {
		t.Run("whole save/"+tc.name, func(t *testing.T) {
			t.Parallel()

			checkRefusedSave(t, func(p updatePaths) string { return p.Checkpoint },
				bodyWithDeployment("deployment", "version", tc.deployment))
		})

		t.Run("verbatim save/"+tc.name, func(t *testing.T) {
			t.Parallel()

			inner := tc.deployment
			if inner != "" {
				inner = `{"deployment":` + inner + `}`
			}

			request := strings.TrimSuffix(bodyWithDeployment("untypedDeployment", "version", inner), "}") +
				`,"sequenceNumber":2}`

			checkRefusedSave(t, func(p updatePaths) string { return p.Verbatim }, request)
		})
	}
}

// TestVerbatimSaveRefusesAnEnvelopeWithNoDeployment proves that a sequenced
// save whose envelope has no deployment member answers 400 in the error shape
// of the Pulumi Cloud-compatible API, and that the stack keeps the last good
// checkpoint, so a client with a defect cannot wipe the state of a stack.
func TestVerbatimSaveRefusesAnEnvelopeWithNoDeployment(t *testing.T) {
	t.Parallel()

	for _, envelope := range []string{`{}`, `{"version":3}`, `{"resources":[]}`} {
		checkRefusedSave(t, func(p updatePaths) string { return p.Verbatim },
			`{"version":3,"untypedDeployment":`+envelope+`,"sequenceNumber":2}`)
	}
}

// checkRefusedSave sends request to the checkpoint route that path names, after
// a good verbatim save, and checks the 400 answer and the kept checkpoint.
func checkRefusedSave(t *testing.T, path func(updatePaths) string, request string) {
	t.Helper()

	f := newUpdateFixture(t, allowAll())
	paths, lease := verbatimPaths(t, f)

	good := `{"version":3,"untypedDeployment":{"deployment":{"resources":[]}},"sequenceNumber":1}`

	status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Verbatim, lease, good))
	if status != http.StatusOK {
		t.Fatalf("save of a good deployment answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	status, body = f.doUpdate(t, leaseRequest(http.MethodPatch, path(paths), lease, request))
	if status != http.StatusBadRequest {
		t.Fatalf("save answers %d, want %d (%s)", status, http.StatusBadRequest, body)
	}

	checkErrorShape(t, http.StatusBadRequest, body)

	if got := exportDeployment(t, f); got != `{"resources":[]}` {
		t.Fatalf("export = %s: a refused save must keep the last checkpoint", got)
	}
}

// exportedProviders is the secrets provider entry of an exported deployment in
// the tests.
type exportedProviders struct {
	State map[string]string `json:"state"`
}

// exportedDeploymentDoc is the deployment of an export in the tests.
type exportedDeploymentDoc struct {
	Providers exportedProviders `json:"secrets_providers"` //nolint:tagliatelle // The Pulumi wire name; it must not change.
}

// exportedProvidersDoc decodes an export in the tests.
type exportedProvidersDoc struct {
	Deployment exportedDeploymentDoc `json:"deployment"`
}

// TestExportNamesTheCurrentStackInTheSecretsProvider proves that both export
// routes name the stack that serves the deployment in the state of the service
// secrets provider. A checkpoint that a renamed stack kept holds the old name,
// and the CLI would ask for that stack.
func TestExportNamesTheCurrentStackInTheSecretsProvider(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	stale := `{"type":"service","state":{"url":"http://127.0.0.1:18080","owner":"acme","project":"web","stack":"old"}}`

	status, body := f.doUpdate(t, accessRequest(
		http.MethodPost, "/api/stacks/acme/web/prod/import",
		`{"version":3,"deployment":{"manifest":{},"secrets_providers":`+stale+`,"resources":[]}}`,
	))
	if status != http.StatusOK {
		t.Fatalf("import answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	for _, target := range []string{"/api/stacks/acme/web/prod/export", "/api/stacks/acme/web/prod/export/1"} {
		status, body = f.doUpdate(t, accessRequest(http.MethodGet, target, ""))
		if status != http.StatusOK {
			t.Fatalf("%s answers %d, want %d (%s)", target, status, http.StatusOK, body)
		}

		var doc exportedProvidersDoc

		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("%s: decode %q: %v", target, body, err)
		}

		state := doc.Deployment.Providers.State
		if state["stack"] != "prod" || state["project"] != "web" || state["owner"] != "acme" {
			t.Errorf("%s: provider state = %v, want the stack acme/web/prod", target, state)
		}

		if state["url"] != "http://127.0.0.1:18080" {
			t.Errorf("%s: provider url = %q, want it unchanged", target, state["url"])
		}
	}
}

// TestHistoryOfMissingStackIsNotFound proves that the history of a stack that
// does not exist answers 404 with the body of GET on that stack. A stack with
// no update still answers 200 with an empty list.
func TestHistoryOfMissingStackIsNotFound(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	for _, path := range []string{"/api/stacks/acme/web/" + unknownStackName, "/api/stacks/other/x/" + unknownStackName} {
		wantStatus, wantBody := f.doUpdate(t, accessRequest(http.MethodGet, path, ""))
		if wantStatus != http.StatusNotFound {
			t.Fatalf("GET %s answers %d (%s), want 404", path, wantStatus, wantBody)
		}

		status, body := f.doUpdate(t, accessRequest(http.MethodGet, path+"/updates?pageSize=10&page=1", ""))
		if status != http.StatusNotFound {
			t.Fatalf("history of %s answers %d (%s), want 404", path, status, body)
		}

		if body != wantBody {
			t.Fatalf("history body = %s, want the body of GET: %s", body, wantBody)
		}
	}

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/updates", ""))
	if status != http.StatusOK || strings.TrimSpace(body) != `{"updates":[]}` {
		t.Fatalf("history of a stack with no update answers %d %q, want 200 and no updates", status, body)
	}
}
