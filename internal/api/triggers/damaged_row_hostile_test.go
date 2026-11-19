package triggers_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/triggers"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/logtext"
	"opendustpan.org/opendustpan/internal/logtext/logtexttest"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// storedErrorText is the text of a stored error that no answer may repeat.
const storedErrorText = "row 7 holds an internal column text"

// spyStore is a trigger.Store that records each read of a trigger and that
// answers a stored error for some IDs.
type spyStore struct {
	trigger.Store

	mu    sync.Mutex
	reads []trigger.ID
	// fail holds the error that a read of an ID returns, before any lookup.
	fail map[trigger.ID]error
}

func (s *spyStore) Get(ctx context.Context, ns namespace.Name, id trigger.ID) (trigger.Trigger, error) {
	if err := s.note(id); err != nil {
		return trigger.Trigger{}, err
	}

	return s.Store.Get(ctx, ns, id)
}

func (s *spyStore) Stored(ctx context.Context, ns namespace.Name, id trigger.ID) (trigger.Trigger, error) {
	if err := s.note(id); err != nil {
		return trigger.Trigger{}, err
	}

	return s.Store.Stored(ctx, ns, id)
}

func (s *spyStore) note(id trigger.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.reads = append(s.reads, id)

	return s.fail[id]
}

func (s *spyStore) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.reads)
}

// liarInventory answers GetUnit with the entry that lie makes of the stored
// one, like an inventory that does not honor the unit ID that it was asked.
type liarInventory struct {
	fakeInventory

	lie func(coreinventory.Entry) coreinventory.Entry
	// gets counts each GetUnit call.
	gets *atomic.Int64
}

func (l liarInventory) GetUnit(ctx context.Context, unitID string) (coreinventory.Entry, error) {
	l.gets.Add(1)

	e, err := l.fakeInventory.GetUnit(ctx, unitID)
	if err != nil || l.lie == nil {
		return e, err
	}

	return l.lie(e), nil
}

// lenientUnits is a trigger.Units that serves the unit that the inventory
// returns for an ID, whatever its namespace. With it, the manager does not
// refuse a unit of another namespace, so that a test sees the resolver alone.
type lenientUnits struct{ inv liarInventory }

func (u lenientUnits) Object(ctx context.Context, unit graph.Unit) (identity.Object, error) {
	e, err := u.inv.GetUnit(ctx, unit.ID)
	if err != nil {
		return identity.Object{}, trigger.ErrUnitGone
	}

	obj, err := identity.UnitObject(e.Tool, unit.Namespace, e.Name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("%w: %w", trigger.ErrUnitGone, err)
	}

	return obj.WithUnitID(e.UnitID), nil
}

func (u lenientUnits) Find(ctx context.Context, ns namespace.Name, unitID string) (graph.Unit, error) {
	e, err := u.inv.GetUnit(ctx, unitID)
	if err != nil {
		return graph.Unit{}, trigger.ErrUnitGone
	}

	return graph.Unit{Namespace: ns, Tool: e.Tool, ID: e.UnitID}, nil
}

// allowEverything is an authorizer that allows each check. With it, only the
// resolver of the route can answer a not-found.
type allowEverything struct{}

func (allowEverything) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return nil
}

func (allowEverything) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoList
}

// spyRig is a handler over a spy store, with the trigger "trg-1" on the unit net.
type spyRig struct {
	h     *triggers.Handler
	inner *fakeStore
	spy   *spyStore
	// gets counts each GetUnit call of the inventory.
	gets *atomic.Int64
}

func newSpyRig(
	t *testing.T, authz identity.Authorizer, lie func(coreinventory.Entry) coreinventory.Entry,
) *spyRig {
	t.Helper()

	return newSpyRigLogged(t, authz, lie, slog.New(slog.DiscardHandler))
}

// newSpyRigLogged is newSpyRig with the logger of the caller.
func newSpyRigLogged(
	t *testing.T, authz identity.Authorizer, lie func(coreinventory.Entry) coreinventory.Entry, logger *slog.Logger,
) *spyRig {
	t.Helper()

	var r0gets atomic.Int64

	inner := newStore()
	spy := &spyStore{Store: inner, mu: sync.Mutex{}, reads: nil, fail: map[trigger.ID]error{}}
	inv := fakeInventory{gone: map[string]bool{}, extra: map[string]coreinventory.Entry{}}
	liar := liarInventory{fakeInventory: inv, lie: lie, gets: &r0gets}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	manager, err := trigger.NewManager(trigger.ManagerConfig{
		Store:      inner,
		Dispatcher: &dispatcher{mu: sync.Mutex{}, reqs: nil, subjs: nil, err: nil},
		Authorizer: authz,
		Savers:     &savers{err: nil},
		Units:      lenientUnits{inv: liar},
		Fences:     fences{},
		Now:        func() time.Time { return time.Unix(1_800_000_000, 0) },
		NewID:      func() trigger.ID { return "trg-1" },
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}

	h, err := triggers.NewHandler(triggers.Config{
		Namespaces: namespaces, Units: liar, Store: spy, Manager: manager,
		Workers: nil, Authenticator: tokenAuthenticator{}, Authorizer: authz,
		Audit:          &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil},
		OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil, Logger: logger,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	r := &spyRig{h: h, inner: inner, spy: spy, gets: &r0gets}

	// The good trigger, stored like the one that a save makes.
	inner.plant(trigger.Trigger{
		ID: "trg-1", Unit: unitNet(), Kind: trigger.KindSchedule,
		Frequency: trigger.Frequency{Preset: "daily", Cron: ""}, Source: noUnit(),
		Enabled: true, LastSaver: ada, LastSaverID: "prn-" + ada,
	})

	return r
}

// plantVariant stores a copy of the good trigger under id, changed by mod.
func (r *spyRig) plantVariant(id trigger.ID, mod func(*trigger.Trigger)) {
	r.inner.mu.Lock()
	t := r.inner.triggers["trg-1"]
	r.inner.mu.Unlock()

	t.ID = id
	mod(&t)
	r.inner.plant(t)
}

// plantHostile stores the rows that the hostile table names.
func (r *spyRig) plantHostile() {
	// A trigger of another unit of the namespace.
	r.plantVariant("trg-app", func(t *trigger.Trigger) { t.Unit = graphUnit("pulumi", appID) })
	// A trigger of the unit ID of the path in another namespace.
	r.plantVariant("trg-ns", func(t *trigger.Trigger) {
		t.Unit = graph.Unit{Namespace: "other", Tool: "opentofu", ID: netID}
	})
	// A trigger of a unit of another namespace.
	r.plantVariant("trg-other", func(t *trigger.Trigger) {
		t.Unit = graph.Unit{Namespace: "other", Tool: "opentofu", ID: otherID}
	})
	// A stored row that fails its checks: the fake wraps trigger.ErrInvalid.
	r.plantVariant("trg-damaged", func(t *trigger.Trigger) { t.Kind = "bogus" })
	r.plantVariant("trg-nosaver", func(t *trigger.Trigger) { t.LastSaver = "bad\nsaver" })
	// A store that returns an error that wraps trigger.ErrInvalid by itself.
	r.plantVariant("trg-direct", func(*trigger.Trigger) {})
	r.spy.fail["trg-direct"] = fmt.Errorf("get trigger: %w: %s", trigger.ErrInvalid, storedErrorText)
}

func triggerURL(ns, unit, id string) string {
	return "/api/dustpan/v1/namespaces/" + ns + "/units/" + unit + "/triggers/" + id
}

type triggerAnswer struct {
	code   int
	body   string
	header http.Header
}

func ask(t *testing.T, r *spyRig, method, path string) triggerAnswer {
	t.Helper()

	rec := serve(t, r.h, request{method: method, path: path, token: testToken, body: "", header: nil})

	return triggerAnswer{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
}

func (a triggerAnswer) same(b triggerAnswer) bool {
	return a.code == b.code && a.body == b.body && reflect.DeepEqual(a.header, b.header)
}

// damagedRow is one row of the hostile table of the trigger routes.
type damagedRow struct {
	name string
	path string
	// readsStore is true when the ID is well formed, so that the store read is expected.
	readsStore bool
	// getOnly is true for a row that the Get read refuses: the delete route
	// reads the row as stored and may remove it.
	getOnly bool
}

func malformed(name, path string) damagedRow {
	return damagedRow{name: name, path: path, readsStore: false, getOnly: false}
}

func stored(name, path string) damagedRow {
	return damagedRow{name: name, path: path, readsStore: true, getOnly: false}
}

func damaged(name, path string) damagedRow {
	return damagedRow{name: name, path: path, readsStore: true, getOnly: true}
}

func damagedRows() []damagedRow {
	return []damagedRow{
		malformed("another namespace in the path", triggerURL("other", netID, "trg-1")),
		malformed("unit of another namespace", triggerURL("acme", otherID, "trg-other")),
		stored("trigger of another unit", triggerURL("acme", netID, "trg-app")),
		stored("trigger of another namespace, same unit ID", triggerURL("acme", netID, "trg-ns")),
		stored("trigger of a unit of another namespace", triggerURL("acme", netID, "trg-other")),
		damaged("damaged row", triggerURL("acme", netID, "trg-damaged")),
		damaged("damaged saver", triggerURL("acme", netID, "trg-nosaver")),
		stored("store error that wraps the invalid error", triggerURL("acme", netID, "trg-direct")),
		malformed("upper case ID", triggerURL("acme", netID, "TRG-1")),
		malformed("ID with a slash", triggerURL("acme", netID, "trg%2F1")),
		malformed("ID with a dot", triggerURL("acme", netID, "trg.1")),
		malformed("ID with a space", triggerURL("acme", netID, "trg%201")),
		malformed("ID with a NUL byte", triggerURL("acme", netID, "trg%00")),
		malformed("ID with a newline", triggerURL("acme", netID, "trg%0A1")),
		malformed("ID that is too long", triggerURL("acme", netID, strings.Repeat("a", trigger.MaxIDBytes+1))),
		malformed("ID of a quoted traversal", triggerURL("acme", netID, "..%2F..%2Fetc")),
	}
}

// TestTriggerRoutesAnswerHostileRowsLikeAnUnknownTrigger sends the routes that
// read a trigger a hostile ID, a trigger of another unit or namespace, and a
// stored row that fails its checks. Each answer is byte-identical to the answer
// for an unknown ID, for a caller with every right and for a caller with no
// right on the unit. A malformed ID must not reach the store.
func TestTriggerRoutesAnswerHostileRowsLikeAnUnknownTrigger(t *testing.T) {
	t.Parallel()

	callers := []struct {
		name  string
		holds []string
	}{
		{name: "caller with every right", holds: holdsAll()},
		{name: "caller with no right on the unit", holds: []string{"namespace:read acme"}},
	}

	for _, caller := range callers {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			for _, tc := range damagedRows() {
				if method == http.MethodDelete && tc.getOnly {
					continue
				}

				t.Run(caller.name+"/"+method+"/"+tc.name, func(t *testing.T) {
					t.Parallel()

					checkHostileRow(t, caller.holds, method, tc)
				})
			}
		}
	}
}

func checkHostileRow(t *testing.T, holds []string, method string, tc damagedRow) {
	t.Helper()

	unknownRig := newSpyRig(t, newAuthorizer(holds...), nil)
	unknown := ask(t, unknownRig, method, triggerURL("acme", netID, "trg-unknown"))

	if unknown.code != http.StatusNotFound {
		t.Fatalf("unknown ID = %d %s, want 404", unknown.code, unknown.body)
	}

	r := newSpyRig(t, newAuthorizer(holds...), nil)
	r.plantHostile()

	got := ask(t, r, method, tc.path)
	if !got.same(unknown) {
		t.Errorf("answer = %d %s %v, want the answer for an unknown ID: %d %s %v",
			got.code, got.body, got.header, unknown.code, unknown.body, unknown.header)
	}

	if strings.Contains(got.body, storedErrorText) {
		t.Errorf("body %s repeats the text of the stored error", got.body)
	}

	// Defense in depth: the handler stops a malformed ID before the store.
	if n := r.spy.readCount(); !tc.readsStore && n != 0 {
		t.Errorf("the store was read %d times for the path %s, want 0", n, tc.path)
	}
}

// TestTriggerRoutesServeTheGoodTrigger is the control of the hostile table: a
// caller with every right reads and deletes the good trigger, so that the
// table reaches the route.
func TestTriggerRoutesServeTheGoodTrigger(t *testing.T) {
	t.Parallel()

	r := newSpyRig(t, newAuthorizer(holdsAll()...), nil)
	r.plantHostile()

	if got := ask(t, r, http.MethodGet, triggerURL("acme", netID, "trg-1")); got.code != http.StatusOK {
		t.Fatalf("get = %d %s, want 200", got.code, got.body)
	}

	if got := ask(t, r, http.MethodDelete, triggerURL("acme", netID, "trg-1")); got.code >= http.StatusBadRequest {
		t.Fatalf("delete = %d %s, want a success", got.code, got.body)
	}
}

// TestTriggerDeleteRemovesADamagedRow proves that the delete route still reads
// a row that fails its checks, so that a damaged row can be removed.
func TestTriggerDeleteRemovesADamagedRow(t *testing.T) {
	t.Parallel()

	r := newSpyRig(t, newAuthorizer(holdsAll()...), nil)
	r.plantHostile()

	got := ask(t, r, http.MethodDelete, triggerURL("acme", netID, "trg-damaged"))
	if got.code >= http.StatusBadRequest {
		t.Fatalf("delete of a damaged row = %d %s, want a success", got.code, got.body)
	}
}

// TestResolveUnitRefusesAUnitOfAnotherNamespace proves, with an authorizer that
// allows each check, that the unit resolver alone refuses a unit that the
// inventory returns for another namespace or for another unit ID. The list
// route resolves the unit and no trigger, so no trigger check can stand in.
func TestResolveUnitRefusesAUnitOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		lie  func(coreinventory.Entry) coreinventory.Entry
	}{
		{
			name: "unit of another namespace",
			path: "/api/dustpan/v1/namespaces/acme/units/" + otherID + "/triggers",
			lie:  nil,
		},
		{
			// The inventory answers the entry of the unit app for the ID of the unit net.
			name: "entry of another unit ID",
			path: "/api/dustpan/v1/namespaces/acme/units/" + netID + "/triggers",
			lie: func(e coreinventory.Entry) coreinventory.Entry {
				for _, other := range entries() {
					if other.UnitID == appID {
						return other
					}
				}

				return e
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newSpyRig(t, allowEverything{}, tc.lie)

			unknown := ask(t, r, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units/ws-404/triggers")
			if unknown.code != http.StatusNotFound {
				t.Fatalf("unknown unit = %d %s, want 404", unknown.code, unknown.body)
			}

			got := ask(t, r, http.MethodGet, tc.path)
			if !got.same(unknown) {
				t.Errorf("answer = %d %s %v, want the answer for an unknown unit: %d %s %v",
					got.code, got.body, got.header, unknown.code, unknown.body, unknown.header)
			}
		})
	}
}

// TestResolveTriggerRefusesATriggerOfAnotherUnit proves, with an authorizer that
// allows each check, that the trigger resolver alone refuses a row of another
// namespace and a row of another unit ID, each apart from the other.
func TestResolveTriggerRefusesATriggerOfAnotherUnit(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"trg-app", "trg-ns", "trg-other"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			r := newSpyRig(t, allowEverything{}, nil)
			r.plantHostile()

			unknown := ask(t, r, http.MethodGet, triggerURL("acme", netID, "trg-unknown"))
			got := ask(t, r, http.MethodGet, triggerURL("acme", netID, id))

			if !got.same(unknown) {
				t.Errorf("answer = %d %s, want the answer for an unknown ID: %d %s",
					got.code, got.body, unknown.code, unknown.body)
			}
		})
	}
}

// TestTriggerDamagedRowIsLoggedNotServed proves that the log names the trigger
// ID and the cause of a stored row that fails its checks, and that the answer
// holds neither the cause nor the stored text.
func TestTriggerDamagedRowIsLoggedNotServed(t *testing.T) {
	t.Parallel()

	for _, id := range []trigger.ID{"trg-damaged", "trg-direct"} {
		t.Run(string(id), func(t *testing.T) {
			t.Parallel()

			var logs strings.Builder

			r := newSpyRigLogged(t, newAuthorizer(holdsAll()...), nil, slog.New(slog.NewTextHandler(&logs, nil)))
			r.plantHostile()

			got := ask(t, r, http.MethodGet, triggerURL("acme", netID, string(id)))
			if got.code != http.StatusNotFound {
				t.Fatalf("answer = %d %s, want 404", got.code, got.body)
			}

			if !strings.Contains(logs.String(), string(id)) ||
				!strings.Contains(logs.String(), "stored trigger is not served") {
				t.Errorf("log %q does not name the trigger ID %q and the cause", logs.String(), id)
			}

			for _, text := range []string{storedErrorText, "bogus", "invalid"} {
				if strings.Contains(got.body, text) {
					t.Errorf("body repeats the text %q: %s", text, got.body)
				}
			}
		})
	}
}

// TestTriggerLogBoundsStoredText proves that a store error of 5000 bytes does
// not make a log line of 5000 bytes.
func TestTriggerLogBoundsStoredText(t *testing.T) {
	t.Parallel()

	var logs strings.Builder

	r := newSpyRigLogged(t, newAuthorizer(holdsAll()...), nil, slog.New(slog.NewTextHandler(&logs, nil)))
	r.plantHostile()
	r.spy.fail["trg-direct"] = fmt.Errorf("get trigger: %w: %s", trigger.ErrInvalid, strings.Repeat("x", 5000))

	ask(t, r, http.MethodGet, triggerURL("acme", netID, "trg-direct"))

	if logs.Len() == 0 || logs.Len() > 1000 {
		t.Errorf("log of %d bytes, want 1..1000", logs.Len())
	}
}

// TestUnitDamagedObjectIsLoggedNotServed proves that the log names the unit ID
// and the cause of a stored unit with no valid object, and that the answer
// holds neither.
func TestUnitDamagedObjectIsLoggedNotServed(t *testing.T) {
	t.Parallel()

	var logs strings.Builder

	r := newSpyRigLogged(t, allowEverything{}, func(e coreinventory.Entry) coreinventory.Entry {
		e.Name = "secret//name"

		return e
	}, slog.New(slog.NewTextHandler(&logs, nil)))

	got := ask(t, r, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units/"+netID+"/triggers")
	if got.code != http.StatusNotFound || strings.Contains(got.body, "secret") {
		t.Fatalf("answer = %d %s, want a 404 with no stored text", got.code, got.body)
	}

	if !strings.Contains(logs.String(), netID) || !strings.Contains(logs.String(), "stored unit has no valid object") ||
		!strings.Contains(logs.String(), "secret") {
		t.Errorf("log %q does not name the unit ID and the cause", logs.String())
	}
}

// TestUnitDamagedObjectLogIsBounded proves that the log line of a stored unit
// with no valid object cuts a name of 5000 bytes, and holds no CR and no LF.
func TestUnitDamagedObjectLogIsBounded(t *testing.T) {
	t.Parallel()

	long := "a//" + strings.Repeat("n", 5000)
	for name, unitName := range map[string]string{"long name": long, "name with line breaks": "a//\r\nb" + long} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := logtexttest.New()
			r := newSpyRigLogged(t, allowEverything{}, func(e coreinventory.Entry) coreinventory.Entry {
				e.Name = unitName

				return e
			}, rec.Logger())

			got := ask(t, r, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units/"+netID+"/triggers")
			if got.code != http.StatusNotFound {
				t.Fatalf("answer = %d %s, want 404", got.code, got.body)
			}

			checkBoundedRecords(t, rec, "stored unit has no valid object")
		})
	}
}

// TestTriggerLogHoldsNoLineBreak proves that the log line of a stored trigger
// that fails its checks holds no CR, no LF and no other hidden rune (for
// example U+202E) from the text of the store error.
func TestTriggerLogHoldsNoLineBreak(t *testing.T) {
	t.Parallel()

	rec := logtexttest.New()
	r := newSpyRigLogged(t, newAuthorizer(holdsAll()...), nil, rec.Logger())
	r.plantHostile()
	r.spy.fail["trg-direct"] = fmt.Errorf("get trigger: %w: line one\r\nERROR forged line name\u202eexe %s",
		trigger.ErrInvalid, strings.Repeat("x", 5000))

	ask(t, r, http.MethodGet, triggerURL("acme", netID, "trg-direct"))

	checkBoundedRecords(t, rec, "stored trigger is not served")
}

// checkBoundedRecords fails when rec holds no record with message, or when an
// attribute of a record holds a CR, an LF, or more than the bound of a log
// text.
func checkBoundedRecords(t *testing.T, rec *logtexttest.Recorder, message string) {
	t.Helper()

	found := false

	for _, r := range rec.Records() {
		if r.Message != message {
			continue
		}

		found = true

		for key, value := range r.Attrs {
			if strings.ContainsAny(value, "\r\n") {
				t.Errorf("log attribute %s holds a line break: %q", key, value)
			}

			if ident.HasHidden(value) {
				t.Errorf("log attribute %s holds a hidden rune: %q", key, value)
			}

			if len(value) > logtext.MaxBytes+len("...") {
				t.Errorf("log attribute %s has %d bytes, want at most %d", key, len(value), logtext.MaxBytes+3)
			}
		}
	}

	if !found {
		t.Fatalf("no log record %q", message)
	}
}

// TestMalformedUnitIDNeverReachesTheInventory proves that the unit resolver
// refuses a unit ID that is malformed or too long before it reads the
// inventory. The authorizer allows each check, so only the resolver can answer
// the not-found answer. The list route resolves the unit and no trigger.
func TestMalformedUnitIDNeverReachesTheInventory(t *testing.T) {
	t.Parallel()

	ids := map[string]string{
		"space":        "a%20b",
		"tilde":        "ws~bad",
		"NUL":          "ws-1%00",
		"line breaks":  "ws-1%0D%0AX:%201",
		"quote":        "%22%3Cscript%3E",
		"leading dot":  ".hidden",
		"leading dash": "-lead",
		"too long":     strings.Repeat("a", 101),
		"very long":    strings.Repeat("a", 5000),
	}

	for name, id := range ids {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newSpyRig(t, allowEverything{}, nil)

			got := ask(t, r, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units/"+id+"/triggers")
			if got.code != http.StatusNotFound {
				t.Errorf("answer = %d %s, want 404", got.code, got.body)
			}

			if n := r.gets.Load(); n != 0 {
				t.Errorf("inventory GetUnit calls = %d, want 0", n)
			}
		})
	}

	// Control: a well formed unit ID reaches the inventory.
	r := newSpyRig(t, allowEverything{}, nil)
	got := ask(t, r, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units/"+netID+"/triggers")
	if got.code != http.StatusOK {
		t.Fatalf("control answer = %d %s, want 200", got.code, got.body)
	}

	if r.gets.Load() == 0 {
		t.Error("the control request did not reach the inventory")
	}
}
