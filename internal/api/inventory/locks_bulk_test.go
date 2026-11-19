package inventory_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	bulkLockPath   = "/api/dustpan/v1/namespaces/acme/unit-locks/bulk-lock"
	bulkUnlockPath = "/api/dustpan/v1/namespaces/acme/unit-locks/bulk-unlock"
	// bulkReason is the reason that the bulk tests give.
	bulkReason = "incident 4411"
)

// bulkResultDoc is one result of a bulk answer.
type bulkResultDoc struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Lock   *struct {
		Kind   string `json:"kind"`
		Object string `json:"object"`
		Holder string `json:"holder"`
		Reason string `json:"reason"`
	} `json:"lock"`
	Error *struct {
		Code      int    `json:"code"`
		ErrorCode string `json:"errorCode"`
		Message   string `json:"message"`
	} `json:"error"`
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
// the ID u-other, and the replay records. withRecords false leaves them out.
func newBulkFixture(t *testing.T, authz identity.Authorizer, n int, withRecords bool) bulkFixture {
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

	records := idempotencytest.NewMemory()

	cfg := apiinventory.Config{
		Placements: nil, OutputSharing: nil, Outputs: nil, Views: nil, Namespaces: nsSvc,
		Units: &memReader{
			entries: map[namespace.Name][]coreinventory.Entry{"acme": entries, "globex": {other}}, err: nil,
		},
		Deleted: &memDeleted{entries: nil}, History: nil, Lifecycles: testLifecycles(),
		ReadScope: fixedScope{err: nil, ids: nil, all: true}, Collections: nil, Locks: locks, Records: nil,
		Moves: nil, Authenticator: subjectAuthenticator{}, Authorizer: authz, Audit: orTestAudit(auditStore),
		OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil, PublicURL: "",
		Logger: slog.New(slog.DiscardHandler), Now: nil, StateGuard: stategate.Off("the test keeps no gates"),
	}
	if withRecords {
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

	return f.postCtx(t, t.Context(), subject, path, body, headers...)
}

func (f bulkFixture) postCtx(
	t *testing.T, ctx context.Context, subject, path, body string, headers ...string,
) *httptest.ResponseRecorder {
	t.Helper()

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

// answerOf decodes a 200 bulk answer.
func answerOf(t *testing.T, rec *httptest.ResponseRecorder) bulkAnswerDoc {
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
		t.Fatalf("result %+v: want ok", r)
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

func (f bulkFixture) auditActions(action string) int {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	n := 0

	for i := range f.store.entries {
		if f.store.entries[i].Action == action {
			n++
		}
	}

	return n
}

func plainAuthz() identity.Authorizer { return bulkAuthz{deny: nil, onLock: nil, n: &atomic.Int64{}} }

// TestBulkLockMixed: one unit has a lock already, the others get the lock of
// the call, and the results keep the order of the request.
func TestBulkLockMixed(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 5, true)

	if rec := f.post(t, "bob", bulkLockPath, bulkLockBody("earlier", bulkUnitID(2))); rec.Code != http.StatusOK {
		t.Fatalf("seed lock: %d %s", rec.Code, rec.Body.String())
	}

	ids := []string{bulkUnitID(4), bulkUnitID(2), bulkUnitID(0), bulkUnitID(3)}
	got := answerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, ids...)))

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

	// The single read route shows the same holder and reason.
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/inventory/unit-lock?unit="+bulkUnitRef(4), nil)
	req.Header.Set("Authorization", "token alice-token")
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), bulkReason) ||
		!strings.Contains(rec.Body.String(), `"alice"`) {
		t.Fatalf("single read: %d %s", rec.Code, rec.Body.String())
	}

	// The earlier holder keeps the lock.
	held, err := f.store.GetLock(t.Context(), mustUnit(t, 2))
	if err != nil || held.Holder != "bob" {
		t.Fatalf("lock of unit 2 = %+v, %v; the old lock must stay", held, err)
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

	f := newBulkFixture(t, plainAuthz(), 3, true)

	ids := []string{bulkUnitID(0), bulkUnitID(1), bulkUnitID(0)}
	got := answerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, ids...)))

	wantOK(t, &got.Results[0])
	wantOK(t, &got.Results[1])
	wantError(t, &got.Results[2], http.StatusConflict, "lock.already-locked")

	if n := f.auditActions(string(identity.ActionUnitLock)); n != 2 {
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
	f := newBulkFixture(t, authz, 3, true)

	// The single call on the same unit gives the status that the item must give.
	single := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/api/inventory/unit-lock?unit="+bulkUnitRef(1), strings.NewReader(`{"reason":"x"}`))
	req.Header.Set("Authorization", "token alice-token")
	f.handler.ServeHTTP(single, req)

	if single.Code != http.StatusForbidden {
		t.Fatalf("single lock = %d, want 403", single.Code)
	}

	before := len(f.audit.denied())

	got := answerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(3)...)))

	wantOK(t, &got.Results[0])
	wantError(t, &got.Results[1], http.StatusForbidden, got.Results[1].Error.ErrorCode)
	wantOK(t, &got.Results[2])

	if got.Results[1].Error.ErrorCode == "" {
		t.Fatal("the denied item has no error code")
	}

	if after := len(f.audit.denied()); after != before+1 {
		t.Fatalf("denied audit entries = %d, want %d", after, before+1)
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
	f := newBulkFixture(t, authz, 3, true)

	ids := []string{"u-missing", "u-other", bulkUnitID(1), "a/b", "", strings.Repeat("x", 101), "u-000?x=1"}
	got := answerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, ids...)))

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

	f := newBulkFixture(t, plainAuthz(), 3, true)
	answerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(3)...)))

	got := answerOf(t, f.post(t, "alice", bulkUnlockPath,
		bulkUnlockBody(bulkUnitID(0), bulkUnitID(1), bulkUnitID(0))))

	wantOK(t, &got.Results[0])
	wantOK(t, &got.Results[1])
	wantError(t, &got.Results[2], http.StatusNotFound, "lock.not-locked")

	if f.held() != 1 {
		t.Fatalf("locks = %d, want 1", f.held())
	}

	if n := f.auditActions(string(identity.ActionUnitLock)); n != 3+2 {
		t.Fatalf("audit entries = %d, want 5", n)
	}
}

// TestBulkUnlockNotHolder: another holder gives lock.not-holder, the lock
// stays, and the locks of the caller are removed.
func TestBulkUnlockNotHolder(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 3, true)
	answerOf(t, f.post(t, "bob", bulkLockPath, bulkLockBody("b", bulkUnitID(1))))
	answerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody("a", bulkUnitID(0), bulkUnitID(2))))

	got := answerOf(t, f.post(t, "alice", bulkUnlockPath, bulkUnlockBody(idsUpTo(3)...)))

	wantOK(t, &got.Results[0])
	wantError(t, &got.Results[1], http.StatusConflict, "lock.not-holder")
	wantOK(t, &got.Results[2])

	if l, err := f.store.GetLock(t.Context(), mustUnit(t, 1)); err != nil || l.Holder != "bob" {
		t.Fatalf("lock of bob = %+v, %v; it must stay", l, err)
	}

	if strings.Contains(got.Results[1].Error.Message, "\"b\"") {
		t.Fatalf("not-holder message leaks the reason: %s", got.Results[1].Error.Message)
	}
}

// TestBulkUnlockNotLocked: a unit with no lock gives lock.not-locked. A body
// with a reason is refused.
func TestBulkUnlockNotLocked(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 2, true)

	got := answerOf(t, f.post(t, "alice", bulkUnlockPath, bulkUnlockBody(idsUpTo(2)...)))
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

	f := newBulkFixture(t, plainAuthz(), 101, true)

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

	if got := answerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(1)...))); len(got.Results) != 1 {
		t.Fatalf("1 unit: %d results", len(got.Results))
	}

	if got := answerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(100)...))); len(got.Results) != 100 {
		t.Fatalf("100 units: %d results", len(got.Results))
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
// writes no second lock or audit entry. The same key with another body gives
// 422. An item with a 5xx store error is done again on the retry.
func TestBulkLockReplay(t *testing.T) {
	t.Parallel()

	t.Run("same body", func(t *testing.T) {
		t.Parallel()

		f := newBulkFixture(t, plainAuthz(), 3, true)
		body := bulkLockBody(bulkReason, idsUpTo(3)...)

		first := answerOf(t, f.post(t, "alice", bulkLockPath, body, "Idempotency-Key", "k1"))
		entries := f.auditActions(string(identity.ActionUnitLock))

		second := answerOf(t, f.post(t, "alice", bulkLockPath, body, "Idempotency-Key", "k1"))

		for i := range second.Results {
			wantOK(t, &second.Results[i])

			if second.Results[i].ID != first.Results[i].ID {
				t.Fatalf("replay result %d has another ID", i)
			}
		}

		if n := f.auditActions(string(identity.ActionUnitLock)); n != entries || n != 3 {
			t.Fatalf("audit entries = %d after the retry, want 3", n)
		}
	})

	t.Run("other body", func(t *testing.T) {
		t.Parallel()

		f := newBulkFixture(t, plainAuthz(), 3, true)
		answerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(2)...), "Idempotency-Key", "k1"))

		rec := f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(3)...), "Idempotency-Key", "k1")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("same key, other body = %d, want 422", rec.Code)
		}

		if f.held() != 2 {
			t.Fatalf("locks = %d, want 2", f.held())
		}
	})

	t.Run("server error is done again", func(t *testing.T) {
		t.Parallel()

		f := newBulkFixture(t, plainAuthz(), 3, true)
		fail := errors.New("store down")

		f.store.putErr = func(target string) error {
			if target == bulkUnitRef(1) {
				return fail
			}

			return nil
		}

		body := bulkLockBody(bulkReason, idsUpTo(3)...)

		first := answerOf(t, f.post(t, "alice", bulkLockPath, body, "Idempotency-Key", "k2"))
		wantOK(t, &first.Results[0])

		if first.Results[1].Error == nil || first.Results[1].Error.Code != http.StatusInternalServerError {
			t.Fatalf("second result = %+v, want 500", first.Results[1])
		}

		f.store.putErr = nil

		second := answerOf(t, f.post(t, "alice", bulkLockPath, body, "Idempotency-Key", "k2"))
		for i := range second.Results {
			wantOK(t, &second.Results[i])
		}

		if f.held() != 3 || f.auditActions(string(identity.ActionUnitLock)) != 3 {
			t.Fatalf("locks = %d, entries = %d, want 3 and 3", f.held(), f.auditActions(string(identity.ActionUnitLock)))
		}
	})

	t.Run("no replay port", func(t *testing.T) {
		t.Parallel()

		f := newBulkFixture(t, plainAuthz(), 1, false)

		rec := f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(1)...), "Idempotency-Key", "k3")
		if rec.Code != http.StatusBadRequest || f.held() != 0 {
			t.Fatalf("key with no records = %d, locks = %d; want 400 and none", rec.Code, f.held())
		}
	})
}

// TestBulkLockRoundTrips: an item uses no more store calls than the single PUT
// on the same kind of unit.
func TestBulkLockRoundTrips(t *testing.T) {
	t.Parallel()

	f := newBulkFixture(t, plainAuthz(), 100, true)

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

	answerOf(t, f.post(t, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(100)[1:]...)))

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
			if n == 3 {
				cancel()
			}
		},
	}
	f := newBulkFixture(t, authz, 10, true)

	f.postCtx(t, ctx, "alice", bulkLockPath, bulkLockBody(bulkReason, idsUpTo(10)...))

	if f.held() != 3 {
		t.Fatalf("locks = %d, want 3: the loop must stop when the context ends", f.held())
	}

	if n := f.auditActions(string(identity.ActionUnitLock)); n != 3 {
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
		{"lone surrogate", bulkLockPath, `{"reason":"\ud800","units":` + unit + `}`, nil, 400},
		{"invalid utf-8", bulkLockPath, "{\"reason\":\"a\xffb\",\"units\":" + unit + "}", nil, 400},
		{"units not array", bulkLockPath, `{"reason":"r","units":"u-000"}`, nil, 400},
		{"null units", bulkLockPath, `{"reason":"r","units":null}`, nil, 400},
		{"empty body", bulkLockPath, ``, nil, 400},
		{"unlock with reason", bulkUnlockPath, `{"reason":"r","units":` + unit + `}`, nil, 400},
		{"two key headers", bulkLockPath, `{"reason":"r","units":` + unit + `}`,
			[]string{"Idempotency-Key", "a", "Idempotency-Key", "b"}, 400},
		{"key with control byte", bulkLockPath, `{"reason":"r","units":` + unit + `}`,
			[]string{"Idempotency-Key", "a\tb"}, 400},
		{"empty key", bulkLockPath, `{"reason":"r","units":` + unit + `}`, []string{"Idempotency-Key", ""}, 400},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newBulkFixture(t, plainAuthz(), 2, true)

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
			deny:   map[string]bool{grant(identity.ActionNamespaceRead, "namespace:acme"): true},
			onLock: nil, n: &atomic.Int64{},
		}
		f := newBulkFixture(t, authz, 2, true)

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
