package inventory_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	bulkLockPath   = "/api/dustpan/v1/namespaces/acme/unit-locks/bulk-lock"
	bulkUnlockPath = "/api/dustpan/v1/namespaces/acme/unit-locks/bulk-unlock"
	// bulkReason is the reason that the bulk tests give.
	bulkReason = "incident 4411"
)

// bulkResultDoc is one result of a bulk answer.
type bulkResultDoc struct {
	ID     string        `json:"id"`
	Status string        `json:"status"`
	Lock   *bulkLockDoc  `json:"lock"`
	Error  *bulkErrorDoc `json:"error"`
}

type bulkLockDoc struct {
	Kind   string `json:"kind"`
	Object string `json:"object"`
	Holder string `json:"holder"`
	Reason string `json:"reason"`
}

type bulkErrorDoc struct {
	Code      int    `json:"code"`
	ErrorCode string `json:"errorCode"`
	Message   string `json:"message"`
}

type bulkAnswerDoc struct {
	Results []bulkResultDoc `json:"results"`
}

// bulkAuthz allows every action except the pairs that it lists. A pair is the
// action and the object string, as grant builds it.
type bulkAuthz struct {
	deny map[string]bool
	// onLock runs for each unit:lock check, with the count of the checks so far.
	onLock func(n int)
	n      *atomic.Int64
}

func (a bulkAuthz) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionUnitLock && a.onLock != nil {
		a.onLock(int(a.n.Add(1)))
	}

	if a.deny[grant(action, obj.String())] {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

func (bulkAuthz) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// errStoreDown is the error of a store that fails.
var errStoreDown = errors.New("store down")

// bulkFixture is a handler with the bulk lock routes over in-memory ports.
type bulkFixture struct {
	handler testHandler
	store   *memLocks
	audit   *memAudit
	records *idempotencytest.Memory
}

// bulkUnitID and bulkUnitObject name unit i of the fixture.
func bulkUnitID(i int) string { return fmt.Sprintf("u-%03d", i) }

func bulkUnitRef(i int) string { return fmt.Sprintf("pulumi:acme/web/bulk-%03d", i) }

// newBulkFixture makes a handler with n units in acme, one unit in globex with
// the ID u-other, and the replay records.
func newBulkFixture(t *testing.T, authz identity.Authorizer, n int) bulkFixture {
	t.Helper()

	return newBulkFixtureWith(t, authz, n, bulkOptions{noRecords: false, wrap: nil, limiter: nil})
}

// bulkOptions changes the fixture: noRecords leaves out the replay records, and
// wrap puts a wrapper around the unit reader.
type bulkOptions struct {
	noRecords bool
	wrap      func(coreinventory.Reader) coreinventory.Reader
	// limiter is the denial limiter of the handler. Nil takes the default.
	limiter *denylimit.Limiter
}

// newBulkFixtureWith is newBulkFixture with the options.
func newBulkFixtureWith(t *testing.T, authz identity.Authorizer, n int, opts bulkOptions) bulkFixture {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	nsSvc, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	store := newMemLocks()
	auditStore := &memAudit{mu: sync.Mutex{}, entries: nil}

	locks, err := manuallock.NewService(
		manuallock.Config{Store: store, Namespaces: nsStore, Authorizer: authz, Now: nil, Phases: nil},
	)
	if err != nil {
		t.Fatalf("manual lock service: %v", err)
	}

	entries := make([]coreinventory.Entry, 0, n)

	for i := range n {
		entries = append(entries, coreinventory.Entry{
			LastChanged: changeTime(), Namespace: "acme", Tool: "pulumi", Name: fmt.Sprintf("web/bulk-%03d", i),
			UnitID: bulkUnitID(i), DisplayName: "", Status: "succeeded", ResourceCount: nil, Lock: nil,
			LastRelease: nil, ManualLock: nil, Collection: "", Labels: map[string]string{},
		})
	}

	other := coreinventory.Entry{
		LastChanged: changeTime(), Namespace: "globex", Tool: "pulumi", Name: "web/bulk-other",
		UnitID: "u-other", DisplayName: "", Status: "succeeded", ResourceCount: nil, Lock: nil,
		LastRelease: nil, ManualLock: nil, Collection: "", Labels: map[string]string{},
	}

	var units coreinventory.Reader = &memReader{
		entries: map[namespace.Name][]coreinventory.Entry{"acme": entries, "globex": {other}}, err: nil,
	}
	if opts.wrap != nil {
		units = opts.wrap(units)
	}

	records := idempotencytest.NewMemory()
	store.records = records

	cfg := apiinventory.Config{
		Placements: nil, OutputSharing: nil, Outputs: nil, Views: nil, Namespaces: nsSvc,
		Units:   units,
		Deleted: &memDeleted{entries: nil}, History: nil, Lifecycles: testLifecycles(),
		ReadScope: fixedScope{err: nil, ids: nil, all: true}, Collections: nil, Locks: locks, Records: nil,
		Moves: nil, Authenticator: subjectAuthenticator{}, Authorizer: authz, Audit: orTestAudit(auditStore),
		OnAuditFailure: nil, DenialLimiter: opts.limiter, Sessions: nil, PublicURL: "",
		Logger: slog.New(slog.DiscardHandler), Now: nil, StateGuard: stategate.Off("the test keeps no gates"),
	}
	if !opts.noRecords {
		cfg.Records = records
	}

	handler, err := newCheckedHandler(t, cfg)
	if err != nil {
		t.Fatalf("inventory handler: %v", err)
	}

	return bulkFixture{handler: handler, store: store, audit: auditStore, records: records}
}

// post sends a bulk call as subject with the given headers.
func (f bulkFixture) post(t *testing.T, subject, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()

	return f.postCtx(t.Context(), subject, path, body, headers...)
}

func (f bulkFixture) postCtx(
	ctx context.Context, subject, path, body string, headers ...string,
) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "token "+subject+"-token")

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

// lockBody makes a bulk-lock body for the unit IDs.
func bulkLockBody(reason string, ids ...string) string {
	return fmt.Sprintf(`{"reason":%q,"units":%s}`, reason, unitsJSON(ids))
}

func bulkUnlockBody(ids ...string) string { return fmt.Sprintf(`{"units":%s}`, unitsJSON(ids)) }

func unitsJSON(ids []string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf(`{"id":%q}`, id))
	}

	return "[" + strings.Join(parts, ",") + "]"
}

func idsUpTo(n int) []string {
	ids := make([]string, 0, n)
	for i := range n {
		ids = append(ids, bulkUnitID(i))
	}

	return ids
}

// bulkAnswerOf decodes a 200 bulk answer.
func bulkAnswerOf(t *testing.T, rec *httptest.ResponseRecorder) bulkAnswerDoc {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var out bulkAnswerDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode answer: %v: %s", err, rec.Body.String())
	}

	return out
}

// wantError checks that result r is an error result with the status and code.
func wantError(t *testing.T, r *bulkResultDoc, status int, code string) {
	t.Helper()

	if r.Status != "error" || r.Error == nil || r.Error.Code != status || r.Error.ErrorCode != code {
		t.Fatalf("result %+v: want error %d %s", r, status, code)
	}

	if r.Lock != nil {
		t.Fatalf("result %s: an error result holds a lock", r.ID)
	}
}

func wantOK(t *testing.T, r *bulkResultDoc) {
	t.Helper()

	if r.Status != "ok" || r.Error != nil {
		t.Fatalf("result %+v err=%+v: want ok", r, r.Error)
	}
}

// statusCode reads the status and the error code of a refusal body.
func refusalOf(t *testing.T, rec *httptest.ResponseRecorder) (int, string) {
	t.Helper()

	var doc struct {
		Code      int    `json:"code"`
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode refusal %d: %v: %s", rec.Code, err, rec.Body.String())
	}

	return rec.Code, doc.ErrorCode
}

func (f bulkFixture) held() int {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	return len(f.store.locks)
}

func (f bulkFixture) lockEntries() int {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	n := 0

	for i := range f.store.entries {
		if f.store.entries[i].Action == string(identity.ActionUnitLock) {
			n++
		}
	}

	return n
}

func plainAuthz() bulkAuthz { return bulkAuthz{deny: nil, onLock: nil, n: &atomic.Int64{}} }

// TestBulkLockMixed: one unit has a lock already, the others get the lock of
// the call, and the results keep the order of the request.
func TestBulkLockMixed(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 5)

	if rec := f.post(t, "bob", bulkLockPath, bulkLockBody("earlier", bulkUnitID(2))); rec.Code != http.StatusOK {
		t.Fatalf("seed lock: %d %s", rec.Code, rec.Body.String())
	}

	ids := []string{bulkUnitID(4), bulkUnitID(2), bulkUnitID(0), bulkUnitID(3)}
	got := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, ids...)))

	if len(got.Results) != len(ids) {
		t.Fatalf("results = %d, want %d", len(got.Results), len(ids))
	}

	for i, id := range ids {
		if got.Results[i].ID != id {
			t.Fatalf("result %d is %s, want %s: order must follow the request", i, got.Results[i].ID, id)
		}
	}

	wantOK(t, &got.Results[0])
	wantError(t, &got.Results[1], http.StatusConflict, "lock.already-locked")
	wantOK(t, &got.Results[2])
	wantOK(t, &got.Results[3])

	if l := got.Results[0].Lock; l == nil || l.Holder != "alice" || l.Reason != bulkReason || l.Kind != "unit" {
		t.Fatalf("lock of the first result = %+v", l)
	}

	f.wantSingleRead(t, 4)

	// The earlier holder keeps the lock.
	held, err := f.store.GetLock(t.Context(), mustUnit(t, 2))
	if err != nil || held.Holder != "bob" {
		t.Fatalf("lock of unit 2 = %+v, %v; the old lock must stay", held, err)
	}
}

// wantSingleRead checks that the single read route shows the holder alice and
// the reason of the bulk call for unit i.
func (f bulkFixture) wantSingleRead(t *testing.T, i int) {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/inventory/unit-lock?unit="+bulkUnitRef(i), nil)
	req.Header.Set("Authorization", "token alice-token")
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), bulkReason) ||
		!strings.Contains(rec.Body.String(), `"alice"`) {
		t.Fatalf("single read: %d %s", rec.Code, rec.Body.String())
	}
}

func mustUnit(t *testing.T, i int) manuallock.Target {
	t.Helper()

	obj, err := identity.ParseObject(bulkUnitRef(i))
	if err != nil {
		t.Fatalf("parse %s: %v", bulkUnitRef(i), err)
	}

	return manuallock.UnitTarget(collection.UnitRef{
		Namespace: obj.Namespace(), Tool: obj.Tool(), Name: obj.UnitName(),
	})
}

// TestBulkLockAudit: each locked unit has one audit entry, and a repeated ID
// gives lock.already-locked on its second result and no second entry.
func TestBulkLockAudit(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3)

	ids := []string{bulkUnitID(0), bulkUnitID(1), bulkUnitID(0)}
	got := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, ids...)))

	wantOK(t, &got.Results[0])
	wantOK(t, &got.Results[1])
	wantError(t, &got.Results[2], http.StatusConflict, "lock.already-locked")

	if n := f.lockEntries(); n != 2 {
		t.Fatalf("lock audit entries = %d, want 2", n)
	}

	for i := range f.store.entries {
		if f.store.entries[i].Reason != bulkReason || f.store.entries[i].Subject != "alice" {
			t.Fatalf("audit entry %d = %+v", i, f.store.entries[i])
		}
	}
}

// TestBulkLockDenied: a caller who reads a unit and may not lock it gets the
// 403 of the single call for that unit, a denied audit entry, and the other
// units are locked.
func TestBulkLockDenied(t *testing.T) {
	t.Parallel()

	authz := bulkAuthz{
		deny: map[string]bool{grant(identity.ActionUnitLock, bulkUnitRef(1)): true}, onLock: nil, n: &atomic.Int64{},
	}
	f := newBulkFixture(t, authz, 3)

	got := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(3)...)))

	wantOK(t, &got.Results[0])

	if got.Results[1].Error == nil {
		t.Fatalf("the denied item %+v has no error", got.Results[1])
	}

	if got.Results[1].Error.ErrorCode == "" {
		t.Fatal("the denied item has no error code")
	}

	wantError(t, &got.Results[1], http.StatusForbidden, got.Results[1].Error.ErrorCode)
	wantOK(t, &got.Results[2])

	if n := len(f.audit.denied()); n != 1 {
		t.Fatalf("denied audit entries = %d, want 1", n)
	}

	// The single call on the same unit gives the status that the item gave.
	single := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/api/inventory/unit-lock?unit="+bulkUnitRef(1), strings.NewReader(`{"reason":"x"}`))
	req.Header.Set("Authorization", "token alice-token")
	f.handler.ServeHTTP(single, req)

	if single.Code != http.StatusForbidden {
		t.Fatalf("single lock = %d, want 403", single.Code)
	}

	if _, err := f.store.GetLock(t.Context(), mustUnit(t, 1)); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Fatalf("denied unit has a lock: %v", err)
	}
}

// TestBulkLockNotVisible: an ID that does not exist, an ID of another
// namespace and a unit that the caller cannot read give one equal result.
func TestBulkLockNotVisible(t *testing.T) {
	t.Parallel()

	authz := bulkAuthz{
		deny: map[string]bool{grant(identity.ActionUnitRead, bulkUnitRef(1)): true}, onLock: nil, n: &atomic.Int64{},
	}
	f := newBulkFixture(t, authz, 3)

	ids := []string{"u-missing", "u-other", bulkUnitID(1), "a/b", "", strings.Repeat("x", 101), "u-000?x=1"}
	got := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, ids...)))

	if len(got.Results) != len(ids) {
		t.Fatalf("results = %d, want %d", len(got.Results), len(ids))
	}

	first := got.Results[0]

	for i := range got.Results {
		r := got.Results[i]
		wantError(t, &r, http.StatusNotFound, "lock.target-not-found")

		if r.Error.Message != first.Error.Message {
			t.Fatalf("result %d message %q differs from %q", i, r.Error.Message, first.Error.Message)
		}
	}

	if f.held() != 0 {
		t.Fatalf("locks = %d, want none", f.held())
	}
}

// TestBulkUnlockHolder: the holder removes its locks. A repeated ID gives
// lock.not-locked on the second result.
func TestBulkUnlockHolder(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3)
	bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(3)...)))

	got := bulkAnswerOf(t, f.post(t, "alice", bulkUnlockPath,
		bulkUnlockBody(bulkUnitID(0), bulkUnitID(1), bulkUnitID(0))))

	wantOK(t, &got.Results[0])
	wantOK(t, &got.Results[1])
	wantError(t, &got.Results[2], http.StatusNotFound, "lock.not-locked")

	if f.held() != 1 {
		t.Fatalf("locks = %d, want 1", f.held())
	}

	if n := f.lockEntries(); n != 3+2 {
		t.Fatalf("audit entries = %d, want 5", n)
	}
}

// TestBulkUnlockNotHolder: another holder gives lock.not-holder, the lock
// stays, and the locks of the caller are removed.
func TestBulkUnlockNotHolder(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3)
	bulkAnswerOf(t, f.post(t, "bob", bulkLockPath, bulkLockBody("b", bulkUnitID(1))))
	bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody("a", bulkUnitID(0), bulkUnitID(2))))

	got := bulkAnswerOf(t, f.post(t, "alice", bulkUnlockPath, bulkUnlockBody(idsUpTo(3)...)))

	wantOK(t, &got.Results[0])
	wantError(t, &got.Results[1], http.StatusConflict, "lock.not-holder")
	wantOK(t, &got.Results[2])

	if l, err := f.store.GetLock(t.Context(), mustUnit(t, 1)); err != nil || l.Holder != "bob" {
		t.Fatalf("lock of bob = %+v, %v; it must stay", l, err)
	}

	// The refusal of the holder check writes one denied audit entry.
	if n := len(f.audit.denied()); n != 1 {
		t.Fatalf("denied audit entries = %d, want 1", n)
	}

	if strings.Contains(got.Results[1].Error.Message, "\"b\"") {
		t.Fatalf("not-holder message leaks the reason: %s", got.Results[1].Error.Message)
	}
}

// TestBulkUnlockNotLocked: a unit with no lock gives lock.not-locked. A body
// with a reason is refused.
func TestBulkUnlockNotLocked(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 2)

	got := bulkAnswerOf(t, f.post(t, "alice", bulkUnlockPath, bulkUnlockBody(idsUpTo(2)...)))
	wantError(t, &got.Results[0], http.StatusNotFound, "lock.not-locked")
	wantError(t, &got.Results[1], http.StatusNotFound, "lock.not-locked")

	rec := f.post(t, "alice", bulkUnlockPath, `{"reason":"x","units":[{"id":"u-000"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unlock body with a reason = %d, want 400", rec.Code)
	}
}

// TestBulkLockLimits: 0, 1, 100 and 101 units, and a body at and over the
// limit of 20480 bytes.
func TestBulkLockLimits(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 101)

	for _, n := range []int{0, 101} {
		rec := f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(n)...))
		if status, code := refusalOf(t, rec); status != http.StatusBadRequest || code != "lock.bulk-size" {
			t.Fatalf("%d units: %d %s, want 400 lock.bulk-size", n, status, code)
		}

		rec = f.post(t, "alice", bulkUnlockPath, bulkUnlockBody(idsUpTo(n)...))
		if status, code := refusalOf(t, rec); status != http.StatusBadRequest || code != "lock.bulk-size" {
			t.Fatalf("unlock, %d units: %d %s, want 400 lock.bulk-size", n, status, code)
		}
	}

	if f.held() != 0 {
		t.Fatalf("a refused call changed %d locks", f.held())
	}

	for _, n := range []int{1, 100} {
		got := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(n)...)))
		if len(got.Results) != n {
			t.Fatalf("%d units: %d results", n, len(got.Results))
		}
	}

	const limit = 20480

	pad := func(size int) string {
		head := `{"reason":"r","units":[{"id":"u-000"}],"x":"`
		tail := `"}`

		return head + strings.Repeat("a", size-len(head)-len(tail)) + tail
	}

	// A body of exactly the limit passes the size check; the unknown field then
	// gives 400. A body of one byte more gives the body-too-large refusal.
	if rec := f.post(t, "alice", bulkLockPath, pad(limit)); rec.Code != http.StatusBadRequest ||
		strings.Contains(rec.Body.String(), "too large") {
		t.Fatalf("body of %d bytes = %d %s", limit, rec.Code, rec.Body.String())
	}

	if rec := f.post(t, "alice", bulkLockPath, pad(limit+1)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body of %d bytes = %d, want 413", limit+1, rec.Code)
	}
}

// TestBulkLockReplay: a retry with the same key answers the stored results and
// writes no second lock or audit entry.
func TestBulkLockReplay(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3)
	body := bulkLockBody(bulkReason, idsUpTo(3)...)

	first := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, body, "Idempotency-Key", "k1"))
	entries := f.lockEntries()

	second := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, body, "Idempotency-Key", "k1"))

	for i := range second.Results {
		wantOK(t, &second.Results[i])

		if second.Results[i].ID != first.Results[i].ID {
			t.Fatalf("replay result %d has another ID", i)
		}
	}

	if n := f.lockEntries(); n != entries || n != 3 {
		t.Fatalf("audit entries = %d after the retry, want 3", n)
	}
}

// TestBulkLockReplayOtherBody: the same key with another body gives 422.
func TestBulkLockReplayOtherBody(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3)
	bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(2)...), "Idempotency-Key", "k1"))

	rec := f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(3)...), "Idempotency-Key", "k1")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("same key, other body = %d, want 422", rec.Code)
	}

	if f.held() != 2 {
		t.Fatalf("locks = %d, want 2", f.held())
	}
}

// TestBulkLockReplayServerError: an item with a 5xx store error is done again
// on the retry.
func TestBulkLockReplayServerError(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3)

	f.store.putErr = func(target string) error {
		if target == bulkUnitRef(1) {
			return errStoreDown
		}

		return nil
	}

	body := bulkLockBody(bulkReason, idsUpTo(3)...)

	first := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, body, "Idempotency-Key", "k2"))
	wantOK(t, &first.Results[0])

	if first.Results[1].Error == nil || first.Results[1].Error.Code != http.StatusInternalServerError {
		t.Fatalf("second result = %+v, want 500", first.Results[1])
	}

	f.store.putErr = nil

	second := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, body, "Idempotency-Key", "k2"))
	for i := range second.Results {
		wantOK(t, &second.Results[i])
	}

	if f.held() != 3 || f.lockEntries() != 3 {
		t.Fatalf("locks = %d, entries = %d, want 3 and 3", f.held(), f.lockEntries())
	}
}

// TestBulkLockReplayNoPort: a key on a server with no replay records gives 400.
func TestBulkLockReplayNoPort(t *testing.T) {
	t.Parallel()

	f := newBulkFixtureWith(t, plainAuthz(), 1, bulkOptions{noRecords: true, wrap: nil, limiter: nil})

	rec := f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(1)...), "Idempotency-Key", "k3")
	if rec.Code != http.StatusBadRequest || f.held() != 0 {
		t.Fatalf("key with no records = %d, locks = %d; want 400 and none", rec.Code, f.held())
	}
}

// TestBulkLockRoundTrips: an item uses no more store calls than the single PUT
// on the same kind of unit.
func TestBulkLockRoundTrips(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 100)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/api/inventory/unit-lock?unit="+bulkUnitRef(0), strings.NewReader(`{"reason":"x"}`))
	req.Header.Set("Authorization", "token alice-token")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("single lock = %d", rec.Code)
	}

	single := f.store.calls
	f.store.calls = 0

	bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(100)[1:]...)))

	if limit := single * 99; f.store.calls > limit {
		t.Fatalf("bulk store calls = %d, budget = %d (%d for each item)", f.store.calls, limit, single)
	}
}

// TestBulkLockContextEnds: the loop stops before the next item when the request
// context ends, and an item that did not run has no lock and no audit entry.
func TestBulkLockContextEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	authz := bulkAuthz{
		deny: nil, n: &atomic.Int64{},
		onLock: func(n int) {
			// Each item checks unit:lock two times: in the route and in the
			// service. Check 5 is the route check of the third item.
			if n == 5 {
				cancel()
			}
		},
	}
	f := newBulkFixture(t, authz, 10)

	f.postCtx(ctx, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(10)...))

	if f.held() != 3 {
		t.Fatalf("locks = %d, want 3: the loop must stop when the context ends", f.held())
	}

	if n := f.lockEntries(); n != 3 {
		t.Fatalf("audit entries = %d, want 3", n)
	}
}

// TestBulkLockHostile sends hostile bodies and headers directly to the handler.
// Each case must answer 400 (or 413, 422) and change no lock.
func TestBulkLockHostile(t *testing.T) {
	t.Parallel()

	unit := `[{"id":"u-000"}]`

	cases := []struct {
		name    string
		path    string
		body    string
		headers []string
		want    int
	}{
		{"unknown field", bulkLockPath, `{"reason":"r","units":` + unit + `,"x":1}`, nil, 400},
		{"unknown item field", bulkLockPath, `{"reason":"r","units":[{"id":"u-000","name":"x"}]}`, nil, 400},
		{"second value", bulkLockPath, `{"reason":"r","units":` + unit + `}{}`, nil, 400},
		{"stray brace", bulkLockPath, `{"reason":"r","units":` + unit + `}}`, nil, 400},
		{"missing reason", bulkLockPath, `{"units":` + unit + `}`, nil, 400},
		{"empty reason", bulkLockPath, `{"reason":"","units":` + unit + `}`, nil, 400},
		{"long reason", bulkLockPath, `{"reason":"` + strings.Repeat("a", 501) + `","units":` + unit + `}`, nil, 400},
		{"line break", bulkLockPath, `{"reason":"a\nb","units":` + unit + `}`, nil, 400},
		{"carriage return", bulkLockPath, `{"reason":"a\rb","units":` + unit + `}`, nil, 400},
		{"tab", bulkLockPath, `{"reason":"a\tb","units":` + unit + `}`, nil, 400},
		{"C1 control", bulkLockPath, `{"reason":"a\u0085b","units":` + unit + `}`, nil, 400},
		{"bidi override", bulkLockPath, `{"reason":"a\u202eb","units":` + unit + `}`, nil, 400},
		{"lone surrogate", bulkLockPath, `{"reason":"\ud800","units":` + unit + `}`, nil, 400},
		{"invalid utf-8", bulkLockPath, "{\"reason\":\"a\xffb\",\"units\":" + unit + "}", nil, 400},
		{"units not array", bulkLockPath, `{"reason":"r","units":"u-000"}`, nil, 400},
		{"null units", bulkLockPath, `{"reason":"r","units":null}`, nil, 400},
		{"empty body", bulkLockPath, ``, nil, 400},
		{"unlock with reason", bulkUnlockPath, `{"reason":"r","units":` + unit + `}`, nil, 400},
		{
			"two key headers", bulkLockPath, `{"reason":"r","units":` + unit + `}`,
			[]string{"Idempotency-Key", "a", "Idempotency-Key", "b"},
			400,
		},
		{
			"key with control byte", bulkLockPath, `{"reason":"r","units":` + unit + `}`,
			[]string{"Idempotency-Key", "a\tb"},
			400,
		},
		{"empty key", bulkLockPath, `{"reason":"r","units":` + unit + `}`, []string{"Idempotency-Key", ""}, 400},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newBulkFixture(t, plainAuthz(), 2)

			rec := f.post(t, "alice", tc.path, tc.body, tc.headers...)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}

			if f.held() != 0 {
				t.Fatalf("a refused call wrote %d locks", f.held())
			}
		})
	}
}

// TestBulkLockRouteNeedsTheNamespace: a caller with no namespace:read gets the
// refusal of a namespace route, and no unit is touched.
func TestBulkLockRouteNeedsTheNamespace(t *testing.T) {
	t.Parallel()

	for _, path := range []string{bulkLockPath, bulkUnlockPath} {
		authz := bulkAuthz{
			deny:   map[string]bool{grant(identity.ActionNamespaceRead, "acme"): true},
			onLock: nil, n: &atomic.Int64{},
		}
		f := newBulkFixture(t, authz, 2)

		body := bulkLockBody(bulkReason, idsUpTo(2)...)
		if path == bulkUnlockPath {
			body = bulkUnlockBody(idsUpTo(2)...)
		}

		rec := f.post(t, "alice", path, body)
		if rec.Code != http.StatusForbidden || f.held() != 0 {
			t.Fatalf("%s: %d, locks = %d; want 403 and none", path, rec.Code, f.held())
		}
	}
}

// lookupLog is a unit reader that keeps the IDs that the handler looks up.
type lookupLog struct {
	coreinventory.Reader

	mu  sync.Mutex
	ids []string
}

func (l *lookupLog) GetUnit(ctx context.Context, id string) (coreinventory.Entry, error) {
	l.mu.Lock()
	l.ids = append(l.ids, id)
	l.mu.Unlock()

	return l.Reader.GetUnit(ctx, id)
}

// TestBulkLockIDCheckBeforeLookup: an ID with a path part, a query part, a
// space, a control byte or more than 100 characters never reaches the unit
// lookup. The check is its own layer: the lookup would answer not found too.
func TestBulkLockIDCheckBeforeLookup(t *testing.T) {
	t.Parallel()

	log := &lookupLog{Reader: nil, mu: sync.Mutex{}, ids: nil}
	f := newBulkFixtureWith(
		t,
		plainAuthz(),
		1,
		bulkOptions{noRecords: false, limiter: nil, wrap: func(r coreinventory.Reader) coreinventory.Reader {
			log.Reader = r

			return log
		}},
	)

	bad := []string{"a/b", "a?b=1", "a b", "a\tb", "-lead", ".lead", strings.Repeat("x", 101), ""}
	got := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, bad...)))

	for i := range got.Results {
		wantError(t, &got.Results[i], http.StatusNotFound, "lock.target-not-found")
	}

	if len(log.ids) != 0 {
		t.Fatalf("the unit lookup saw %q: an invalid ID must stop before it", log.ids)
	}
}

// keyedPost sends a bulk call with the Idempotency-Key key.
func (f bulkFixture) keyedPost(t *testing.T, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()

	return f.post(t, "alice", path, body, "Idempotency-Key", key)
}

// TestBulkLockAuditKeyed: with an Idempotency-Key, a repeated ID is two items
// with two records. The second result is lock.already-locked, as without a key.
// The test fails when the key of an item holds the ID only: the second item then
// answers the stored result of the first.
func TestBulkLockAuditKeyed(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3)

	ids := []string{bulkUnitID(0), bulkUnitID(1), bulkUnitID(0)}
	got := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, bulkLockBody(bulkReason, ids...), "kd1"))

	wantOK(t, &got.Results[0])
	wantOK(t, &got.Results[1])
	wantError(t, &got.Results[2], http.StatusConflict, "lock.already-locked")

	if n := f.lockEntries(); n != 2 {
		t.Fatalf("lock audit entries = %d, want 2", n)
	}

	// The retry gives the same three results and writes nothing.
	again := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, bulkLockBody(bulkReason, ids...), "kd1"))

	wantOK(t, &again.Results[0])
	wantOK(t, &again.Results[1])
	wantError(t, &again.Results[2], http.StatusConflict, "lock.already-locked")

	if n := f.lockEntries(); n != 2 {
		t.Fatalf("lock audit entries after the retry = %d, want 2", n)
	}
}

// TestBulkUnlockHolderKeyed: the keyed bulk unlock of a repeated ID gives
// lock.not-locked on the second result.
func TestBulkUnlockHolderKeyed(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3)
	bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(3)...)))

	got := bulkAnswerOf(t, f.keyedPost(t, bulkUnlockPath,
		bulkUnlockBody(bulkUnitID(0), bulkUnitID(1), bulkUnitID(0)), "kd2"))

	wantOK(t, &got.Results[0])
	wantOK(t, &got.Results[1])
	wantError(t, &got.Results[2], http.StatusNotFound, "lock.not-locked")

	if f.held() != 1 {
		t.Fatalf("locks = %d, want 1", f.held())
	}

	if n := f.lockEntries(); n != 3+2 {
		t.Fatalf("audit entries = %d, want 5", n)
	}
}

// TestBulkLockReplayKeepsRefusals: the record of an item keeps a refusal with a
// status below 500. After the conflicting lock is gone, a retry with the same
// key still answers 409 for that item and writes no lock.
func TestBulkLockReplayKeepsRefusals(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3)
	bulkAnswerOf(t, f.post(t, "bob", bulkLockPath, bulkLockBody("b", bulkUnitID(1))))

	body := bulkLockBody(bulkReason, idsUpTo(2)...)

	first := f.keyedPost(t, bulkLockPath, body, "kr1")
	got := bulkAnswerOf(t, first)

	wantOK(t, &got.Results[0])
	wantError(t, &got.Results[1], http.StatusConflict, "lock.already-locked")

	if h := first.Header().Get("Idempotent-Replayed"); h != "" {
		t.Fatalf("first call: Idempotent-Replayed = %q, want none", h)
	}

	bulkAnswerOf(t, f.post(t, "bob", bulkUnlockPath, bulkUnlockBody(bulkUnitID(1))))

	retry := f.keyedPost(t, bulkLockPath, body, "kr1")
	again := bulkAnswerOf(t, retry)

	wantOK(t, &again.Results[0])
	wantError(t, &again.Results[1], http.StatusConflict, "lock.already-locked")

	if f.held() != 1 {
		t.Fatalf("locks = %d, want 1: the retry must not lock the unit that the 409 item named", f.held())
	}

	if h := retry.Header().Get("Idempotent-Replayed"); h != "true" {
		t.Fatalf("full replay: Idempotent-Replayed = %q, want true", h)
	}
}

// TestBulkLockReplayedHeader: the header is on the answer only when no item ran
// again. An item with a 5xx result runs again and clears the header.
func TestBulkLockReplayedHeader(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3)

	f.store.putErr = func(target string) error {
		if target == bulkUnitRef(1) {
			return errStoreDown
		}

		return nil
	}

	body := bulkLockBody(bulkReason, idsUpTo(3)...)

	first := f.keyedPost(t, bulkLockPath, body, "kh1")
	bulkAnswerOf(t, first)

	if h := first.Header().Get("Idempotent-Replayed"); h != "" {
		t.Fatalf("first call: header = %q, want none", h)
	}

	f.store.putErr = nil

	partial := f.keyedPost(t, bulkLockPath, body, "kh1")
	bulkAnswerOf(t, partial)

	if h := partial.Header().Get("Idempotent-Replayed"); h != "" {
		t.Fatalf("retry that ran an item again: header = %q, want none", h)
	}

	full := f.keyedPost(t, bulkLockPath, body, "kh1")
	bulkAnswerOf(t, full)

	if h := full.Header().Get("Idempotent-Replayed"); h != "true" {
		t.Fatalf("retry that ran no item: header = %q, want true", h)
	}
}

// TestBulkLockReadDeniedAudit: a refused unit:read gives the not-found result
// and one denied audit entry, and no lock is written.
func TestBulkLockReadDeniedAudit(t *testing.T) {
	t.Parallel()

	for _, path := range []string{bulkLockPath, bulkUnlockPath} {
		authz := bulkAuthz{
			deny: map[string]bool{
				grant(identity.ActionUnitRead, bulkUnitRef(1)): true,
			},
			onLock: nil,
			n:      &atomic.Int64{},
		}
		f := newBulkFixture(t, authz, 3)

		body := bulkLockBody(bulkReason, idsUpTo(3)...)
		if path == bulkUnlockPath {
			body = bulkUnlockBody(idsUpTo(3)...)
		}

		got := bulkAnswerOf(t, f.post(t, "alice", path, body))
		wantError(t, &got.Results[1], http.StatusNotFound, "lock.target-not-found")

		if n := len(f.audit.denied()); n != 1 {
			t.Fatalf("%s: denied audit entries = %d, want 1", path, n)
		}
	}
}

// TestBulkUnlockDeniedAudit: a refused unit:lock on a bulk unlock gives the 403
// result and a denied audit entry, and the lock stays.
func TestBulkUnlockDeniedAudit(t *testing.T) {
	t.Parallel()

	authz := plainAuthz()
	authz.deny = map[string]bool{}
	f := newBulkFixture(t, authz, 3)

	// Alice holds the lock on unit 1. The check of unit:lock denies her after that.
	bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, bulkUnitID(1))))

	authz.deny[grant(identity.ActionUnitLock, bulkUnitRef(1))] = true

	got := bulkAnswerOf(t, f.post(t, "alice", bulkUnlockPath, bulkUnlockBody(bulkUnitID(1))))

	if got.Results[0].Error == nil {
		t.Fatalf("the denied item %+v has no error", got.Results[0])
	}

	wantError(t, &got.Results[0], http.StatusForbidden, got.Results[0].Error.ErrorCode)

	if n := len(f.audit.denied()); n != 1 {
		t.Fatalf("denied audit entries = %d, want 1", n)
	}

	if f.held() != 1 {
		t.Fatalf("locks = %d, want 1: a denied unlock must keep the lock", f.held())
	}
}

// TestBulkLockItemReused: an item whose record holds another request gives the
// 422 result, and the unit stays unlocked. The test writes the record of the
// item by hand with another material, as if a bug or a collision had.
func TestBulkLockItemReused(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 2)

	sum := sha256.New()
	sum.Write([]byte("kru"))
	sum.Write([]byte{0})
	sum.Write([]byte("0"))
	sum.Write([]byte{0})
	sum.Write([]byte(bulkUnitID(0)))

	itemKey, err := idempotency.ParseKey("item-" + hex.EncodeToString(sum.Sum(nil)))
	if err != nil {
		t.Fatalf("item key: %v", err)
	}

	ref := idempotency.Ref{Principal: "alice", Method: http.MethodPost, Path: bulkLockPath, Key: itemKey}

	if _, err = f.records.Begin(t.Context(), ref, []byte(`{"another":"request"}`), time.Time{}); err != nil {
		t.Fatalf("seed record: %v", err)
	}

	got := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, bulkLockBody(bulkReason, bulkUnitID(0)), "kru"))
	wantError(t, &got.Results[0], http.StatusUnprocessableEntity, "request.idempotency-key-reused")

	if f.held() != 0 {
		t.Fatalf("locks = %d, want none", f.held())
	}
}

// wrongIDReader answers every lookup of from with the entry of to.
type wrongIDReader struct {
	coreinventory.Reader

	from, to string
}

func (w wrongIDReader) GetUnit(ctx context.Context, id string) (coreinventory.Entry, error) {
	if id == w.from {
		return w.Reader.GetUnit(ctx, w.to)
	}

	return w.Reader.GetUnit(ctx, id)
}

// TestBulkLockUnitIDMustMatch: a reader that answers the entry of another unit
// gives the not-found result, and no lock is written on the other unit. The
// check of the entry ID is its own layer: the reader is the other layer.
func TestBulkLockUnitIDMustMatch(t *testing.T) {
	t.Parallel()

	f := newBulkFixtureWith(
		t,
		plainAuthz(),
		2,
		bulkOptions{noRecords: false, limiter: nil, wrap: func(r coreinventory.Reader) coreinventory.Reader {
			return wrongIDReader{Reader: r, from: bulkUnitID(0), to: bulkUnitID(1)}
		}},
	)

	got := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, bulkUnitID(0))))
	wantError(t, &got.Results[0], http.StatusNotFound, "lock.target-not-found")

	if f.held() != 0 {
		t.Fatalf("locks = %d, want none", f.held())
	}
}

// bulkCallKey is the key of the call record that the route makes for key.
func bulkCallKey(t *testing.T, key string) idempotency.Key {
	t.Helper()

	sum := sha256.Sum256([]byte(key))

	k, err := idempotency.ParseKey("call-" + hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("call key: %v", err)
	}

	return k
}

// bulkItemRecordKey is the key of the record that the route makes for the item
// at pos with the ID id of the call with key.
func bulkItemRecordKey(t *testing.T, key string, pos int, id string) idempotency.Key {
	t.Helper()

	sum := sha256.New()
	sum.Write([]byte(key))
	sum.Write([]byte{0})
	sum.Write([]byte(strconv.Itoa(pos)))
	sum.Write([]byte{0})
	sum.Write([]byte(id))

	k, err := idempotency.ParseKey("item-" + hex.EncodeToString(sum.Sum(nil)))
	if err != nil {
		t.Fatalf("item key: %v", err)
	}

	return k
}

// seedItemRecord writes the record of an item by hand. A nil answer leaves the
// record pending.
func (f bulkFixture) seedItemRecord(
	t *testing.T, k idempotency.Key, material string, answer *idempotency.Answer,
) {
	t.Helper()

	ref := idempotency.Ref{Principal: "alice", Method: http.MethodPost, Path: bulkLockPath, Key: k}

	res, err := f.records.Begin(t.Context(), ref, []byte(material), time.Time{})
	if err != nil {
		t.Fatalf("seed record: %v", err)
	}

	if answer == nil {
		return
	}

	if err = f.records.Complete(t.Context(), ref, res.Token, *answer); err != nil {
		t.Fatalf("complete seeded record: %v", err)
	}
}

// unknownIDMessage returns the message of the result for an ID that does not
// exist.
func (f bulkFixture) unknownIDMessage(t *testing.T) string {
	t.Helper()

	got := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, "u-missing")))
	wantError(t, &got.Results[0], http.StatusNotFound, "lock.target-not-found")

	return got.Results[0].Error.Message
}

// TestBulkLockReplayAfterReadRevoked: a retry with the same key checks unit:read
// again for each item. An item that the caller can no longer read answers as an
// ID that does not exist, with one denied audit entry, and does not answer its
// stored result. The test fails when the replay skips the unit:read check.
func TestBulkLockReplayAfterReadRevoked(t *testing.T) {
	t.Parallel()

	authz := plainAuthz()
	authz.deny = map[string]bool{}
	f := newBulkFixture(t, authz, 3)
	body := bulkLockBody(bulkReason, bulkUnitID(0), bulkUnitID(1))

	first := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, body, "krr"))
	wantOK(t, &first.Results[0])
	wantOK(t, &first.Results[1])

	if n := len(f.audit.denied()); n != 0 {
		t.Fatalf("denied audit entries before the revoke = %d, want 0", n)
	}

	authz.deny[grant(identity.ActionUnitRead, bulkUnitRef(1))] = true

	got := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, body, "krr"))

	wantOK(t, &got.Results[0])

	if l := got.Results[0].Lock; l == nil || l.Holder != "alice" || l.Reason != bulkReason {
		t.Fatalf("replayed lock of the first item = %+v", l)
	}

	wantError(t, &got.Results[1], http.StatusNotFound, "lock.target-not-found")

	if got.Results[1].Error.Message != f.unknownIDMessage(t) {
		t.Fatalf("message %q differs from the message of an unknown ID", got.Results[1].Error.Message)
	}

	denied := f.audit.denied()
	if len(denied) != 1 || denied[0].Action != string(identity.ActionUnitRead) ||
		!strings.Contains(denied[0].Object, bulkUnitRef(1)) {
		t.Fatalf("denied audit entries = %+v, want one unit:read entry for %s", denied, bulkUnitRef(1))
	}
}

// TestBulkLockKeyScopePrincipal: the key is scoped by the principal. Bob sends
// the key and the body of alice and gets his own fresh results. Bob sends the
// key of alice with another body and does not get the 422 of a reused key. No
// test relies on a record that names alice.
func TestBulkLockKeyScopePrincipal(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3)
	body := bulkLockBody(bulkReason, bulkUnitID(0))

	bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, body, "Idempotency-Key", "ksp"))
	bulkAnswerOf(t, f.post(t, "alice", bulkUnlockPath, bulkUnlockBody(bulkUnitID(0))))

	rec := f.post(t, "bob", bulkLockPath, body, "Idempotency-Key", "ksp")
	got := bulkAnswerOf(t, rec)

	if h := rec.Header().Get("Idempotent-Replayed"); h != "" {
		t.Fatalf("bob: Idempotent-Replayed = %q, want none", h)
	}

	wantOK(t, &got.Results[0])

	if l := got.Results[0].Lock; l == nil || l.Holder != "bob" {
		t.Fatalf("bob got the lock %+v, want a fresh lock of bob", l)
	}

	// Another body with the key of alice and bob: the call record of carol is
	// her own, so the call is not refused as a reused key.
	rec = f.post(t, "carol", bulkLockPath, bulkLockBody(bulkReason, bulkUnitID(2), bulkUnitID(1)),
		"Idempotency-Key", "ksp")
	if rec.Code != http.StatusOK {
		t.Fatalf("carol with the key of alice and bob = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// TestBulkLockKeyScopePath: the key is scoped by the route path. The same key on
// bulk-unlock after bulk-lock gives fresh results, and the key of a call that
// names another unit set on the same route is refused only on that route.
func TestBulkLockKeyScopePath(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 2)

	bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, bulkLockBody(bulkReason, bulkUnitID(0)), "ksa"))

	rec := f.keyedPost(t, bulkUnlockPath, bulkUnlockBody(bulkUnitID(0)), "ksa")
	got := bulkAnswerOf(t, rec)

	wantOK(t, &got.Results[0])

	if h := rec.Header().Get("Idempotent-Replayed"); h != "" {
		t.Fatalf("unlock with the key of the lock: Idempotent-Replayed = %q, want none", h)
	}

	if f.held() != 0 {
		t.Fatalf("locks = %d, want 0: the unlock must run", f.held())
	}

	// The same route and the same key with another body is refused.
	rec = f.keyedPost(t, bulkLockPath, bulkLockBody(bulkReason, bulkUnitID(1)), "ksa")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("same route, same key, other body = %d, want 422", rec.Code)
	}
}

// TestBulkLockKeyScopeNamespace: the key is scoped by the namespace in the path.
// The same key on the route of another namespace with another body gives a fresh
// answer, and not the 422 of a reused key.
func TestBulkLockKeyScopeNamespace(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 2)

	bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, bulkLockBody(bulkReason, bulkUnitID(0)), "ksn"))

	// The route of namespace globex answers 404 before the key, so the test
	// reaches the key with the route of acme in another escaped form.
	const escaped = "/api/dustpan/v1/namespaces/%61cme/unit-locks/bulk-lock"

	rec := f.keyedPost(t, escaped, bulkLockBody(bulkReason, bulkUnitID(1)), "ksn")
	if rec.Code != http.StatusOK {
		t.Fatalf("same key, other escaped path, other body = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// failingLockAuthz fails the unit:lock check of one object with a plain error,
// as a policy backend that is down does.
type failingLockAuthz struct {
	identity.Authorizer

	object string
}

func (a failingLockAuthz) Authorize(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionUnitLock && obj.String() == a.object {
		return errStoreDown
	}

	return a.Authorizer.Authorize(ctx, p, action, obj)
}

// TestBulkLockAuthorizerFails: an Authorizer that fails with an error that is
// not a refusal fails the item closed with 500 and writes exactly one audit
// entry with the reason refusal.ReasonFailed. A refusal writes exactly one entry
// with the reason refusal.ReasonPolicy.
func TestBulkLockAuthorizerFails(t *testing.T) {
	t.Parallel()

	for _, unlock := range []bool{false, true} {
		path, body := bulkLockPath, bulkLockBody(bulkReason, idsUpTo(3)...)
		if unlock {
			path, body = bulkUnlockPath, bulkUnlockBody(idsUpTo(3)...)
		}

		// The explicit limiter shows a second Record: the limiter holds back a
		// repeat of the key and counts it as pending.
		lim := denylimit.New(denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: nil})
		f := newBulkFixtureWith(t, failingLockAuthz{Authorizer: plainAuthz(), object: bulkUnitRef(1)}, 3,
			bulkOptions{noRecords: false, wrap: nil, limiter: lim})
		if unlock {
			// Alice holds the locks, so the unlock reaches the check of unit:lock.
			f.store.mu.Lock()

			for i := range 3 {
				f.store.locks[mustUnit(t, i).String()] = manuallock.Lock{
					Target: mustUnit(t, i),
					Holder: "alice",
					Since:  time.Time{},
					Reason: "held",
				}
			}

			f.store.mu.Unlock()
		}

		got := bulkAnswerOf(t, f.post(t, "alice", path, body))

		if got.Results[1].Error == nil || got.Results[1].Error.Code != http.StatusInternalServerError {
			t.Fatalf("unlock=%v: result of the failing item = %+v, want 500", unlock, got.Results[1])
		}

		wantOK(t, &got.Results[0])
		wantOK(t, &got.Results[2])

		entries := f.audit.denied()
		if len(entries) != 1 || !strings.HasPrefix(entries[0].Reason, refusal.ReasonFailed) ||
			entries[0].Action != string(identity.ActionUnitLock) {
			t.Fatalf("unlock=%v: denied audit entries = %+v, want one with reason %q",
				unlock, entries, refusal.ReasonFailed)
		}

		if pending := lim.Drain(); len(pending) != 0 {
			t.Fatalf("unlock=%v: the limiter holds %+v: the item recorded more than once", unlock, pending)
		}
	}
}

// TestBulkLockRefusalOneEntry: a refusal of unit:lock writes exactly one entry with
// the reason refusal.ReasonPolicy.
func TestBulkLockRefusalOneEntry(t *testing.T) {
	t.Parallel()

	authz := bulkAuthz{
		deny: map[string]bool{grant(identity.ActionUnitLock, bulkUnitRef(1)): true}, onLock: nil, n: &atomic.Int64{},
	}
	// The explicit limiter shows a second Record: the limiter holds back a
	// repeat of the key and counts it as pending.
	lim := denylimit.New(denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: nil})
	f := newBulkFixtureWith(t, authz, 3, bulkOptions{noRecords: false, wrap: nil, limiter: lim})
	bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(3)...)))

	entries := f.audit.denied()
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Reason, refusal.ReasonPolicy) {
		t.Fatalf("denied audit entries = %+v, want one with reason %q", entries, refusal.ReasonPolicy)
	}

	if pending := lim.Drain(); len(pending) != 0 {
		t.Fatalf("the limiter holds %+v: the refusal recorded more than once", pending)
	}
}

// TestBulkLockRateLimitedNotStored: an item with a 429 result is not stored. A
// retry with the same key does the item again. The test fails when the record
// keeps the 429 result.
func TestBulkLockRateLimitedNotStored(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 2)

	f.store.putErr = func(target string) error {
		if target == bulkUnitRef(1) {
			return fmt.Errorf("limiter: %w", identity.ErrRateLimited)
		}

		return nil
	}

	body := bulkLockBody(bulkReason, idsUpTo(2)...)

	first := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, body, "k429"))
	wantOK(t, &first.Results[0])

	if first.Results[1].Error == nil || first.Results[1].Error.Code != http.StatusTooManyRequests {
		t.Fatalf("second result = %+v, want 429", first.Results[1])
	}

	f.store.putErr = nil

	again := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, body, "k429"))
	wantOK(t, &again.Results[0])
	wantOK(t, &again.Results[1])
}

// TestBulkLockStoredIDMismatch: a stored result with the ID of another unit gives
// a 500 result for the item, and not the stored result. The test fails when the
// replay does not compare the ID.
func TestBulkLockStoredIDMismatch(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 2)
	material := fmt.Sprintf(`{"id":%q,"reason":%q,"unlock":false}`, bulkUnitID(0), bulkReason)
	stored := idempotency.Answer{
		Status: http.StatusOK, Headers: nil,
		Body: []byte(fmt.Sprintf(`{"id":%q,"status":"ok"}`, bulkUnitID(1))),
	}
	f.seedItemRecord(t, bulkItemRecordKey(t, "kid", 0, bulkUnitID(0)), material, &stored)

	got := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, bulkLockBody(bulkReason, bulkUnitID(0)), "kid"))

	if r := got.Results[0]; r.ID != bulkUnitID(0) || r.Error == nil || r.Error.Code != http.StatusInternalServerError {
		t.Fatalf("result = %+v, want a 500 result for %s", r, bulkUnitID(0))
	}
}

// TestBulkLockItemMaterial: the record of an item is compared by the reason. A
// stored record for the same key, position and ID answers when the reason is the
// same and gives the 422 result when the reason is not. The first half fails when
// the material of an item leaves out the reason.
func TestBulkLockItemMaterial(t *testing.T) {
	t.Parallel()

	seed := func(t *testing.T) bulkFixture {
		t.Helper()

		f := newBulkFixture(t, plainAuthz(), 2)
		material := fmt.Sprintf(`{"id":%q,"reason":%q,"unlock":false}`, bulkUnitID(0), bulkReason)
		stored := idempotency.Answer{
			Status: http.StatusOK, Headers: nil,
			Body: []byte(fmt.Sprintf(
				`{"id":%q,"status":"ok","lock":{"kind":"unit","holder":"seeded","reason":%q}}`,
				bulkUnitID(0), bulkReason,
			)),
		}
		f.seedItemRecord(t, bulkItemRecordKey(t, "kim", 0, bulkUnitID(0)), material, &stored)

		return f
	}

	t.Run("same reason", func(t *testing.T) {
		t.Parallel()

		f := seed(t)
		got := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, bulkLockBody(bulkReason, bulkUnitID(0)), "kim"))

		if l := got.Results[0].Lock; l == nil || l.Holder != "seeded" {
			t.Fatalf("result = %+v, want the stored result", got.Results[0])
		}

		if f.held() != 0 {
			t.Fatalf("locks = %d, want none: the stored result answers", f.held())
		}
	})

	t.Run("other reason", func(t *testing.T) {
		t.Parallel()

		f := seed(t)
		got := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, bulkLockBody("another reason", bulkUnitID(0)), "kim"))
		wantError(t, &got.Results[0], http.StatusUnprocessableEntity, "request.idempotency-key-reused")

		if f.held() != 0 {
			t.Fatalf("locks = %d, want none", f.held())
		}
	})
}

// TestBulkLockCallInProgress: a key whose first call has not ended gives 409 for
// the call, and no item runs.
func TestBulkLockCallInProgress(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 2)
	body := bulkLockBody(bulkReason, bulkUnitID(0))

	ref := idempotency.Ref{
		Principal: "alice", Method: http.MethodPost, Path: bulkLockPath, Key: bulkCallKey(t, "kip"),
	}
	if _, err := f.records.Begin(t.Context(), ref, []byte(body), time.Time{}); err != nil {
		t.Fatalf("seed call record: %v", err)
	}

	rec := f.keyedPost(t, bulkLockPath, body, "kip")
	status, code := refusalOf(t, rec)
	if status != http.StatusConflict || code != "request.idempotency-key-in-progress" {
		t.Fatalf("call in progress = %d %s, want 409 request.idempotency-key-in-progress", status, code)
	}

	if f.held() != 0 {
		t.Fatalf("locks = %d, want none", f.held())
	}
}

// TestBulkLockItemInProgress: an item whose first request has not ended gives the
// 409 result for the item, and the other items run.
func TestBulkLockItemInProgress(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 2)
	material := fmt.Sprintf(`{"id":%q,"reason":%q,"unlock":false}`, bulkUnitID(0), bulkReason)
	f.seedItemRecord(t, bulkItemRecordKey(t, "kii", 0, bulkUnitID(0)), material, nil)

	got := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, bulkLockBody(bulkReason, idsUpTo(2)...), "kii"))
	wantError(t, &got.Results[0], http.StatusConflict, "request.idempotency-key-in-progress")
	wantOK(t, &got.Results[1])

	if strings.Contains(got.Results[0].Error.Message, "unit") {
		t.Fatalf("message %q uses the word unit", got.Results[0].Error.Message)
	}
}

// TestBulkLockKeyedContextEnds: a keyed call that ends early frees its key, so a
// retry with the same key does not get 409. The items that ran answer their
// stored results and the others run.
func TestBulkLockKeyedContextEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	authz := bulkAuthz{
		deny: nil, n: &atomic.Int64{},
		onLock: func(n int) {
			if n == 5 {
				cancel()
			}
		},
	}
	f := newBulkFixture(t, authz, 10)
	body := bulkLockBody(bulkReason, idsUpTo(10)...)

	f.postCtx(ctx, "alice", bulkLockPath, body, "Idempotency-Key", "kce")

	if f.held() != 3 {
		t.Fatalf("locks = %d, want 3", f.held())
	}

	rec := f.keyedPost(t, bulkLockPath, body, "kce")
	got := bulkAnswerOf(t, rec)

	for i := range got.Results {
		wantOK(t, &got.Results[i])
	}

	if f.held() != 10 || f.lockEntries() != 10 {
		t.Fatalf("locks = %d, entries = %d, want 10 and 10", f.held(), f.lockEntries())
	}

	if h := rec.Header().Get("Idempotent-Replayed"); h != "" {
		t.Fatalf("retry that ran 7 items: Idempotent-Replayed = %q, want none", h)
	}
}

// TestBulkLockNotStoredFreesKey: a write that passes while the store keeps no
// answer frees the key of the item, so a retry runs the item again and does not
// give the in-progress result.
func TestBulkLockNotStoredFreesKey(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 1)
	f.store.records = nil

	body := bulkLockBody(bulkReason, bulkUnitID(0))

	first := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, body, "knf"))
	wantOK(t, &first.Results[0])

	again := bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, body, "knf"))
	wantError(t, &again.Results[0], http.StatusConflict, "lock.already-locked")
}

// failingUnitReader fails the lookup of one ID while fail is true.
type failingUnitReader struct {
	coreinventory.Reader

	id   string
	fail *atomic.Bool
}

func (r failingUnitReader) GetUnit(ctx context.Context, id string) (coreinventory.Entry, error) {
	if id == r.id && r.fail.Load() {
		return coreinventory.Entry{}, errStoreDown
	}

	return r.Reader.GetUnit(ctx, id)
}

// TestBulkLockReplayedHeaderPreRecord: the header follows what ran, also for the
// items that fail before their record.
func TestBulkLockReplayedHeaderPreRecord(t *testing.T) {
	t.Parallel()

	t.Run("all items fail before the record", func(t *testing.T) {
		t.Parallel()

		f := newBulkFixture(t, plainAuthz(), 2)

		rec := f.keyedPost(t, bulkLockPath, bulkLockBody(bulkReason, "u-missing", "u-other"), "khp")
		got := bulkAnswerOf(t, rec)

		wantError(t, &got.Results[0], http.StatusNotFound, "lock.target-not-found")
		wantError(t, &got.Results[1], http.StatusNotFound, "lock.target-not-found")

		if h := rec.Header().Get("Idempotent-Replayed"); h != "" {
			t.Fatalf("first call: Idempotent-Replayed = %q, want none", h)
		}
	})

	t.Run("a 5xx before the record clears the header", func(t *testing.T) {
		t.Parallel()

		fail := &atomic.Bool{}
		f := newBulkFixtureWith(
			t,
			plainAuthz(),
			2,
			bulkOptions{noRecords: false, limiter: nil, wrap: func(r coreinventory.Reader) coreinventory.Reader {
				return failingUnitReader{Reader: r, id: bulkUnitID(1), fail: fail}
			}},
		)
		body := bulkLockBody(bulkReason, idsUpTo(2)...)

		bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, body, "khq"))

		fail.Store(true)

		rec := f.keyedPost(t, bulkLockPath, body, "khq")
		got := bulkAnswerOf(t, rec)

		wantOK(t, &got.Results[0])

		if got.Results[1].Error == nil || got.Results[1].Error.Code != http.StatusInternalServerError {
			t.Fatalf("second result = %+v, want 500", got.Results[1])
		}

		if h := rec.Header().Get("Idempotent-Replayed"); h != "" {
			t.Fatalf("retry with a 5xx item: Idempotent-Replayed = %q, want none", h)
		}
	})
}

// TestBulkLockReasonBoundary: a reason of exactly 500 characters passes and one
// of 501 does not.
func TestBulkLockReasonBoundary(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 1)

	rec := f.post(t, "alice", bulkLockPath, bulkLockBody(strings.Repeat("a", 501), bulkUnitID(0)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("501 characters = %d, want 400", rec.Code)
	}

	got := bulkAnswerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(strings.Repeat("a", 500), bulkUnitID(0))))
	wantOK(t, &got.Results[0])
}

// Latency test of the bulk lock route against the real stores.
const (
	// latencyUnits is the number of units that one bulk call locks.
	latencyUnits = 100
	// latencyRuns is the number of timed calls for each variant.
	latencyRuns = 20
	// latencyP95Limit is the p95 limit of one bulk call of latencyUnits units.
	latencyP95Limit = 3000 * time.Millisecond
	// latencyPostgresEnv names the environment variable with the PostgreSQL URL.
	latencyPostgresEnv = "DUSTPAN_TEST_POSTGRES_URL"
)

// latencyStore is what the latency test needs from a real store.
type latencyStore interface {
	manuallock.Store
	namespace.Store
	workspace.Store
	audit.Store
	coreinventory.Reader
}

// TestBulkLockLatency measures bulk-lock of 100 unlocked units, with and
// without an Idempotency-Key header, on SQLite and on PostgreSQL. The p95 of
// the calls must stay at or below latencyP95Limit.
func TestBulkLockLatency(t *testing.T) {
	t.Parallel()

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		s, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close sqlite store: %v", err)
			}
		})

		records := s.Idempotency(idempotencytest.TestWrapper(t, 1))

		runBulkLatency(t, s, records)
	})

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()

		base := os.Getenv(latencyPostgresEnv)
		if base == "" {
			t.Skipf("set %s to run the PostgreSQL latency test", latencyPostgresEnv)
		}

		s, err := postgres.Open(t.Context(), latencyPostgresURL(t, base))
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}

		t.Cleanup(s.Close)

		records := s.Idempotency(idempotencytest.TestWrapper(t, 1))

		runBulkLatency(t, s, records)
	})
}

// runBulkLatency seeds latencyUnits workspaces in acme in s, then times
// latencyRuns bulk locks without a key and latencyRuns with a key. It unlocks
// the units between the runs, outside the timing.
func runBulkLatency(t *testing.T, s latencyStore, records idempotency.Store) {
	t.Helper()

	f, ids := newLatencyFixture(t, s, records)
	lockBody := bulkLockBody(bulkReason, ids...)
	unlockBody := bulkUnlockBody(ids...)

	for _, keyed := range []bool{false, true} {
		name := "no key"
		if keyed {
			name = "key"
		}

		times := timeBulkLocks(
			t,
			f,
			latencyCall{name: name, keyed: keyed, lockBody: lockBody, unlockBody: unlockBody, now: time.Now},
		)

		slices.Sort(times)

		p50 := times[(len(times)-1)/2]
		p95 := times[(len(times)*95+99)/100-1]
		maxTime := times[len(times)-1]

		t.Logf("bulk-lock of %d units, %s, %d runs: p50 %v, p95 %v, max %v",
			latencyUnits, name, len(times), p50, p95, maxTime)

		// The race detector makes a store call several times slower, and a run of
		// the whole suite loads the machine too. The target is for a build with
		// no detector; with it, the test still runs the calls and logs the times.
		if p95 > latencyP95Limit && !raceDetector {
			t.Errorf("%s: p95 %v is above the limit %v", name, p95, latencyP95Limit)
		}
	}
}

// latencyCall is one series of timed calls.
type latencyCall struct {
	name                 string
	keyed                bool
	lockBody, unlockBody string
	// now is the clock of the measurement. The test passes the real clock: a
	// latency test measures real time by definition.
	now func() time.Time
}

// timeBulkLocks posts the bulk lock latencyRuns times and returns the time of
// each call. After each call it unlocks the units outside the timing.
func timeBulkLocks(t *testing.T, f bulkFixture, c latencyCall) []time.Duration {
	t.Helper()

	name, keyed, lockBody, unlockBody := c.name, c.keyed, c.lockBody, c.unlockBody

	times := make([]time.Duration, 0, latencyRuns)

	for run := range latencyRuns {
		var headers []string
		if keyed {
			headers = []string{"Idempotency-Key", fmt.Sprintf("latency-%d", run)}
		}

		start := c.now()
		rec := f.post(t, "alice", bulkLockPath, lockBody, headers...)
		took := c.now().Sub(start)

		for i, r := range bulkAnswerOf(t, rec).Results {
			if r.Status != "ok" {
				t.Fatalf("%s run %d item %d: status %q, want ok", name, run, i, r.Status)
			}
		}

		times = append(times, took)

		// Unlock outside the timing, so that the next run locks 100 unlocked units.
		if un := f.post(t, "alice", bulkUnlockPath, unlockBody); un.Code != http.StatusOK {
			t.Fatalf("%s run %d: bulk unlock status %d, body %s", name, run, un.Code, un.Body.String())
		}
	}

	return times
}

// newLatencyFixture seeds the namespace acme and latencyUnits workspaces in s,
// and returns a handler over the real stores and the IDs of the units.
func newLatencyFixture(t *testing.T, s latencyStore, records idempotency.Store) (bulkFixture, []string) {
	t.Helper()

	ctx := t.Context()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: changeTime(), Name: "acme", PrimaryInstallation: "test-region",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	for i := range latencyUnits {
		w := workspace.Workspace{
			NamespaceID: "", CreatedAt: changeTime(), UpdatedAt: changeTime(),
			Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
			LastRelease: workspace.NoRelease(), ID: workspace.ID(ident.NewID("ws-")),
			Ref:      workspace.Ref{Namespace: ns.Name, Name: workspace.Name(fmt.Sprintf("bulk-%03d", i))},
			Revision: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
			Origin: identity.StateOrigin{Job: "", Commit: ""},
		}
		if err := s.CreateWorkspace(ctx, w, ns.Fence()); err != nil {
			t.Fatalf("create workspace %d: %v", i, err)
		}
	}

	entries, err := s.ListUnits(ctx, ns.Name)
	if err != nil {
		t.Fatalf("list units: %v", err)
	}

	if len(entries) != latencyUnits {
		t.Fatalf("seeded %d units, want %d", len(entries), latencyUnits)
	}

	ids := make([]string, 0, len(entries))
	for i := range entries {
		ids = append(ids, entries[i].UnitID)
	}

	nsSvc, err := namespace.NewService(namespace.Config{Store: s, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	authz := plainAuthz()

	locks, err := manuallock.NewService(
		manuallock.Config{Store: s, Namespaces: s, Authorizer: authz, Now: nil, Phases: nil},
	)
	if err != nil {
		t.Fatalf("manual lock service: %v", err)
	}

	handler, err := newCheckedHandler(t, apiinventory.Config{
		Placements: nil, OutputSharing: nil, Outputs: nil, Views: nil, Namespaces: nsSvc,
		Units: s, Deleted: &memDeleted{entries: nil}, History: nil, Lifecycles: testLifecycles(),
		ReadScope: fixedScope{err: nil, ids: nil, all: true}, Collections: nil, Locks: locks,
		Records: records, Moves: nil, Authenticator: subjectAuthenticator{}, Authorizer: authz,
		Audit: orTestAudit(nil), OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil, PublicURL: "",
		Logger: slog.New(slog.DiscardHandler), Now: nil, StateGuard: stategate.Off("the test keeps no gates"),
	})
	if err != nil {
		t.Fatalf("inventory handler: %v", err)
	}

	return bulkFixture{handler: handler, store: nil, audit: nil, records: nil}, ids
}

// latencyPostgresURL makes a database for one test on the server of base: it
// migrates a template database that it drops with the test, and returns the
// URL of a clone of the template. This package has no TestMain that makes a
// shared template.
func latencyPostgresURL(t *testing.T, base string) string {
	t.Helper()

	ctx := t.Context()

	template, err := pgtest.CloneName("dustpan_test_latency")
	if err != nil {
		t.Fatalf("template name: %v", err)
	}

	migrate := func(ctx context.Context, dbURL string) error {
		s, openErr := postgres.Open(ctx, dbURL)
		if openErr != nil {
			return fmt.Errorf("open store: %w", openErr)
		}

		closeStore := s.Close
		closeStore()

		return nil
	}

	if buildErr := pgtest.BuildTemplate(ctx, base, template, migrate); buildErr != nil {
		t.Fatalf("make the template database: %v", buildErr)
	}

	// The cleanup is registered before the clone, so it runs after the clone drop.
	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		conn, connErr := pgx.Connect(dropCtx, base)
		if connErr != nil {
			t.Logf("connect to drop the template %s: %v", template, connErr)

			return
		}

		defer func() {
			if closeErr := conn.Close(dropCtx); closeErr != nil {
				t.Logf("close: %v", closeErr)
			}
		}()

		drop := "DROP DATABASE IF EXISTS " + pgx.Identifier{template}.Sanitize() + " WITH (FORCE)"
		if _, execErr := conn.Exec(dropCtx, drop); execErr != nil {
			t.Logf("drop the template %s: %v", template, execErr)
		}
	})

	return pgtest.MigratedURLWithTimeout(t, base, template, time.Minute)
}

// seedAliceLocks makes alice the holder of the locks on units 0 to n-1.
func (f bulkFixture) seedAliceLocks(t *testing.T, n int) {
	t.Helper()

	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	for i := range n {
		f.store.locks[mustUnit(t, i).String()] = manuallock.Lock{
			Target: mustUnit(t, i), Holder: "alice", Since: time.Time{}, Reason: "held",
		}
	}
}

// bulkCase is the path and the body of a bulk lock or a bulk unlock of n units.
func bulkCase(unlock bool, n int) (string, string) {
	if unlock {
		return bulkUnlockPath, bulkUnlockBody(idsUpTo(n)...)
	}

	return bulkLockPath, bulkLockBody(bulkReason, idsUpTo(n)...)
}

// TestBulkLockReplayAfterLockRevoked: a replay checks unit:lock again before it
// answers the stored result. The first keyed call is ok. Then unit:lock is
// refused. The replay gives 403, writes exactly one denied entry with the
// reason refusal.ReasonPolicy and leaves the lock store as it was.
func TestBulkLockReplayAfterLockRevoked(t *testing.T) {
	t.Parallel()

	for _, unlock := range []bool{false, true} {
		authz := plainAuthz()
		authz.deny = map[string]bool{}
		f := newBulkFixture(t, authz, 2)

		if unlock {
			f.seedAliceLocks(t, 2)
		}

		path, body := bulkCase(unlock, 1)

		first := bulkAnswerOf(t, f.keyedPost(t, path, body, "klr"))
		wantOK(t, &first.Results[0])

		heldBefore := f.held()
		entriesBefore := f.lockEntries()

		authz.deny[grant(identity.ActionUnitLock, bulkUnitRef(0))] = true

		rec := f.keyedPost(t, path, body, "klr")
		got := bulkAnswerOf(t, rec)

		if got.Results[0].Error == nil || got.Results[0].Error.Code != http.StatusForbidden {
			t.Fatalf("unlock=%v: replay result = %+v, want 403", unlock, got.Results[0])
		}

		if got.Results[0].Lock != nil {
			t.Fatalf("unlock=%v: the replay answered the stored lock %+v", unlock, got.Results[0].Lock)
		}

		denied := f.audit.denied()
		if len(denied) != 1 || denied[0].Action != string(identity.ActionUnitLock) ||
			!strings.HasPrefix(denied[0].Reason, refusal.ReasonPolicy) {
			t.Fatalf("unlock=%v: denied audit entries = %+v, want one unit:lock entry with reason %q",
				unlock, denied, refusal.ReasonPolicy)
		}

		if f.held() != heldBefore || f.lockEntries() != entriesBefore {
			t.Fatalf("unlock=%v: the replay changed the lock store", unlock)
		}

		if h := rec.Header().Get("Idempotent-Replayed"); h != "" {
			t.Fatalf("unlock=%v: Idempotent-Replayed = %q, want none: the item was refused, not replayed", unlock, h)
		}
	}
}

// serviceOnlyAuthz makes the first unit:lock check pass and the second one call
// second. The first check is the pre-check of the item. The second is the check
// in the lock service.
func serviceOnlyAuthz(second func(deny map[string]bool)) bulkAuthz {
	authz := plainAuthz()
	authz.deny = map[string]bool{}
	authz.onLock = func(n int) {
		if n == 2 {
			second(authz.deny)
		}
	}

	return authz
}

// TestBulkLockServiceDenialRecorded: a denial that only the lock service decides
// gives 403 and exactly one denied entry with the reason reasonBulkLockDenied.
// The test fails when bulkLockFailure does not record this denial.
func TestBulkLockServiceDenialRecorded(t *testing.T) {
	t.Parallel()

	for _, unlock := range []bool{false, true} {
		authz := serviceOnlyAuthz(func(deny map[string]bool) {
			deny[grant(identity.ActionUnitLock, bulkUnitRef(0))] = true
		})
		lim := denylimit.New(denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: nil})
		f := newBulkFixtureWith(t, authz, 1, bulkOptions{noRecords: false, wrap: nil, limiter: lim})

		if unlock {
			f.seedAliceLocks(t, 1)
		}

		path, body := bulkCase(unlock, 1)
		got := bulkAnswerOf(t, f.post(t, "alice", path, body))

		if got.Results[0].Error == nil || got.Results[0].Error.Code != http.StatusForbidden {
			t.Fatalf("unlock=%v: result = %+v, want 403", unlock, got.Results[0])
		}

		denied := f.audit.denied()
		if len(denied) != 1 || denied[0].Action != string(identity.ActionUnitLock) ||
			!strings.HasPrefix(denied[0].Reason, "unit:lock denied for a unit of a bulk call") {
			t.Fatalf("unlock=%v: denied audit entries = %+v, want one with the service denial reason", unlock, denied)
		}

		if pending := lim.Drain(); len(pending) != 0 {
			t.Fatalf("unlock=%v: the limiter holds %+v: more than one record", unlock, pending)
		}
	}
}

// TestBulkLockServiceAuthorizerFails: an Authorizer that fails inside the lock
// service, after the pre-check passed, gives 500 and one entry with the reason
// refusal.ReasonFailed.
func TestBulkLockServiceAuthorizerFails(t *testing.T) {
	t.Parallel()

	for _, unlock := range []bool{false, true} {
		var calls atomic.Int64

		authz := failOnSecondLockAuthz{Authorizer: plainAuthz(), n: &calls}
		lim := denylimit.New(denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: nil})
		f := newBulkFixtureWith(t, authz, 1, bulkOptions{noRecords: false, wrap: nil, limiter: lim})

		if unlock {
			f.seedAliceLocks(t, 1)
		}

		path, body := bulkCase(unlock, 1)
		got := bulkAnswerOf(t, f.post(t, "alice", path, body))

		if got.Results[0].Error == nil || got.Results[0].Error.Code != http.StatusInternalServerError {
			t.Fatalf("unlock=%v: result = %+v, want 500", unlock, got.Results[0])
		}

		denied := f.audit.denied()
		if len(denied) != 1 || !strings.HasPrefix(denied[0].Reason, refusal.ReasonFailed) {
			t.Fatalf("unlock=%v: denied audit entries = %+v, want one with reason %q",
				unlock, denied, refusal.ReasonFailed)
		}

		if pending := lim.Drain(); len(pending) != 0 {
			t.Fatalf("unlock=%v: the limiter holds %+v: more than one record", unlock, pending)
		}
	}
}

// failOnSecondLockAuthz fails the second unit:lock check with a plain error.
type failOnSecondLockAuthz struct {
	identity.Authorizer

	n *atomic.Int64
}

func (a failOnSecondLockAuthz) Authorize(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionUnitLock && a.n.Add(1) == 2 {
		return errStoreDown
	}

	return a.Authorizer.Authorize(ctx, p, action, obj)
}

// TestBulkLockReplayedHeaderNeedsReplay: the header is on the answer only when
// at least one item answered its stored result. A retry in which every item
// fails before the record has no replayed item and no header.
func TestBulkLockReplayedHeaderNeedsReplay(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 2)
	body := bulkLockBody(bulkReason, "u-missing", "u-other")

	bulkAnswerOf(t, f.keyedPost(t, bulkLockPath, body, "khn"))

	rec := f.keyedPost(t, bulkLockPath, body, "khn")
	got := bulkAnswerOf(t, rec)

	wantError(t, &got.Results[0], http.StatusNotFound, "lock.target-not-found")

	if h := rec.Header().Get("Idempotent-Replayed"); h != "" {
		t.Fatalf("retry with no replayed item: Idempotent-Replayed = %q, want none", h)
	}
}
