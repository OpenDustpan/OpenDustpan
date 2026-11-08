package dustpan

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// errReaderDown is the error of an inventory reader that does not answer.
var errReaderDown = errors.New("inventory down")

// fakeInventory serves the entries of one namespace.
type fakeInventory struct {
	entries []inventory.Entry
	err     error
}

func (f fakeInventory) ListUnits(_ context.Context, _ namespace.Name) ([]inventory.Entry, error) {
	return f.entries, f.err
}

// QueryUnits implements inventory.Reader over the entries of ListUnits.
func (f fakeInventory) QueryUnits(ctx context.Context, q inventory.UnitQuery) ([]inventory.Entry, error) {
	all, err := f.ListUnits(ctx, q.Namespace)
	if err != nil {
		return nil, err
	}

	return inventory.PageUnits(all, q), nil
}

func (f fakeInventory) GetUnit(_ context.Context, unitID string) (inventory.Entry, error) {
	if f.err != nil {
		return inventory.Entry{}, f.err
	}

	for i := range f.entries {
		if f.entries[i].UnitID == unitID {
			return f.entries[i], nil
		}
	}

	return inventory.Entry{}, inventory.ErrUnitNotFound
}

func gateTestEntry() inventory.Entry {
	return inventory.Entry{
		LastRelease: nil,
		LastChanged: time.Time{}, Namespace: "acme", Tool: "opentofu", Name: "web", UnitID: "ws-1", DisplayName: "",
		Status: "", ResourceCount: nil, Lock: nil, ManualLock: nil, Collection: "prod",
		Labels: map[string]string{"team": "core", "env": "prod"},
	}
}

// fakeUnitIDs finds the ID of the unit web, and counts the lookups.
type fakeUnitIDs struct {
	calls int
}

func (f *fakeUnitIDs) UnitID(_ context.Context, obj identity.Object) (string, bool, error) {
	f.calls++

	if obj.UnitName() == "web" {
		return "ws-1", true, nil
	}

	return "", false, nil
}

// The ways that a test names the unit web.
const (
	byReference = iota
	byStableID
	byIndexedLookup
)

// gateFacts returns the facts source for the way mode names the unit.
func gateFacts(entries []inventory.Entry, mode int) (inventoryFacts, *fakeUnitIDs) {
	lookup := &fakeUnitIDs{calls: 0}
	f := inventoryFacts{reader: fakeInventory{entries: entries, err: nil}, ids: nil, deleted: nil}

	if mode == byIndexedLookup {
		f.ids = lookup
	}

	return f, lookup
}

func gateTestUnit(t *testing.T, mode int) identity.Object {
	t.Helper()

	u, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	if mode == byStableID {
		return u.WithUnitID("ws-1")
	}

	return u
}

// namingModes returns the three ways that a test names the unit web.
func namingModes() []int {
	return []int{byReference, byStableID, byIndexedLookup}
}

func gatePrincipal(subject string, kind identity.Kind, credential identity.Credential) identity.Principal {
	return identity.Principal{
		Subject: subject, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: kind, Credential: credential,
		JobID: "",
	}
}

// unitLabels returns the labels that Unit gives for unit.
func unitLabels(ctx context.Context, f inventoryFacts, unit identity.Object) ([]string, error) {
	info, err := f.Unit(ctx, unit)

	return info.Labels, err
}

func TestInventoryFactsGiveCollectionAndSortedLabels(t *testing.T) {
	t.Parallel()

	for _, mode := range namingModes() {
		f, lookup := gateFacts([]inventory.Entry{gateTestEntry()}, mode)

		coll, err := f.Collection(t.Context(), gateTestUnit(t, mode))
		if err != nil || coll != collection.Name("prod") {
			t.Fatalf("Collection(mode %d) = %q, %v, want prod", mode, coll, err)
		}

		labels, err := unitLabels(t.Context(), f, gateTestUnit(t, mode))
		if err != nil || !slices.Equal(labels, []string{"env=prod", "team=core"}) {
			t.Fatalf("Labels(mode %d) = %v, %v, want the sorted labels", mode, labels, err)
		}

		if want := map[bool]int{true: 2, false: 0}[mode == byIndexedLookup]; lookup.calls != want {
			t.Fatalf("mode %d made %d indexed lookups, want %d", mode, lookup.calls, want)
		}
	}
}

func TestInventoryFactsOfAnUnknownUnit(t *testing.T) {
	t.Parallel()

	for _, mode := range namingModes() {
		f, _ := gateFacts(nil, mode)

		// A reference with a stable ID names a unit that must be live or
		// deleted, so an unknown one fails closed. A name that the store does
		// not know is a unit that is not yet in the inventory.
		wantNotFound := mode != byStableID

		for name, read := range map[string]func() error{
			"Collection": func() error { _, err := f.Collection(t.Context(), gateTestUnit(t, mode)); return err },
			"Labels":     func() error { _, err := unitLabels(t.Context(), f, gateTestUnit(t, mode)); return err },
		} {
			if err := read(); errors.Is(err, gate.ErrNotFound) != wantNotFound || err == nil && !wantNotFound {
				t.Fatalf("%s(mode %d) = %v, want gate.ErrNotFound: %v", name, mode, err, wantNotFound)
			}
		}
	}
}

func TestInventoryFactsOfAUnitInNoCollection(t *testing.T) {
	t.Parallel()

	e := gateTestEntry()
	e.Collection = ""
	f, _ := gateFacts([]inventory.Entry{e}, byStableID)

	if _, err := f.Collection(t.Context(), gateTestUnit(t, byStableID)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("Collection() = %v, want gate.ErrNotFound", err)
	}

	// Unit reads a known unit: the unit is in no collection, and it is not
	// a unit that the inventory does not know.
	info, err := f.Unit(t.Context(), gateTestUnit(t, byStableID))
	if err != nil || info.Collection != "" || !slices.Equal(info.Labels, []string{"env=prod", "team=core"}) {
		t.Fatalf("Unit() = %+v, %v, want no collection and the sorted labels", info, err)
	}
}

func TestInventoryFactsFailClosedOnAReaderError(t *testing.T) {
	t.Parallel()

	for _, mode := range namingModes() {
		f, _ := gateFacts(nil, mode)
		f.reader = fakeInventory{entries: nil, err: errReaderDown}

		if mode == byIndexedLookup {
			f.ids = &fakeUnitIDs{calls: 0}
		}

		_, err := unitLabels(t.Context(), f, gateTestUnit(t, mode))
		if !errors.Is(err, errReaderDown) || errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("Labels(mode %d) = %v, want the reader error and no ErrNotFound", mode, err)
		}
	}
}

// errPrincipalDown is the error of a principal read that does not answer.
var errPrincipalDown = errors.New("principal down")

// fakePrincipalReads serves one principal record and a list of active group
// names.
type fakePrincipalReads struct {
	record    principal.Principal
	recordErr error
	groups    []string
	groupsErr error
	calls     int
}

func (f *fakePrincipalReads) GetPrincipalBySubject(
	_ context.Context, subject string,
) (principal.Principal, error) {
	if f.recordErr != nil {
		return principal.Principal{}, f.recordErr
	}

	if f.record.Subject != subject {
		return principal.Principal{}, principal.ErrNotFound
	}

	return f.record, nil
}

func (f *fakePrincipalReads) ListActiveGroupNamesOf(
	_ context.Context, principalID string,
) ([]string, error) {
	f.calls++

	if f.groupsErr != nil {
		return nil, f.groupsErr
	}

	if principalID != f.record.ID {
		return nil, principal.ErrNotFound
	}

	return f.groups, nil
}

func TestPrincipalSubjects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		kind       identity.Kind
		credential identity.Credential
		wantKind   gate.PrincipalKind
		wantCred   gate.CredentialKind
	}{
		{"unknown kind counts as a person", "", "", gate.PrincipalPerson, gate.CredentialAccessToken},
		{
			"person with a session",
			identity.KindPerson,
			identity.CredentialSession,
			gate.PrincipalPerson,
			gate.CredentialSession,
		},
		{
			"service with an access token",
			identity.KindService,
			identity.CredentialAccessToken,
			gate.PrincipalService,
			gate.CredentialAccessToken,
		},
		{
			"workload with a trust token",
			identity.KindWorkload,
			identity.CredentialTrustToken,
			gate.PrincipalService,
			gate.CredentialTrustToken,
		},
		{
			"service with a job token",
			identity.KindService,
			identity.CredentialJobToken,
			gate.PrincipalService,
			gate.CredentialJobToken,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := gatePrincipal("alice", tc.kind, tc.credential)
			reads := &fakePrincipalReads{
				record: principal.Principal{
					CreatedAt: time.Time{}, UpdatedAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-alice",
					Subject: "alice", DisplayName: "Alice", Kind: principal.KindPerson, Source: principal.SourceLocal,
					Namespace: "", GroupCount: 0, BreakGlass: false,
				},
				recordErr: nil, groups: nil, groupsErr: nil, calls: 0,
			}

			s, err := principalSubjects{principals: reads}.Subject(t.Context(), p)
			if err != nil || s.ID != "alice" || s.Kind != tc.wantKind || s.Credential != tc.wantCred ||
				s.BreakGlassAccount || !s.AuthAt.IsZero() {
				t.Fatalf("Subject() = %+v, %v, want kind %s and credential %s", s, err, tc.wantKind, tc.wantCred)
			}
		})
	}
}

// TestPrincipalSubjectsResolveTheRecordGroupsAndAuthentication proves the
// reads of the principal record, its groups and the authentication time.
func TestPrincipalSubjectsResolveTheRecordGroupsAndAuthentication(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.November, 8, 10, 0, 0, 0, time.UTC)

	alice := principal.Principal{
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-alice",
		Subject: "alice", DisplayName: "Alice", Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 2, BreakGlass: false,
	}
	glass := alice
	glass.ID, glass.Subject, glass.DisplayName, glass.BreakGlass = "prn-glass", "glass", "Glass", true

	aliceGroups := []string{"zeta", "alpha", "zeta"}

	var none principal.Principal

	cases := []struct {
		name           string
		principal      identity.Principal
		record         principal.Principal
		recordErr      error
		groups         []string
		groupsErr      error
		signedIn       bool
		wantGroups     []string
		wantAuthAt     time.Time
		wantBreakGlass bool
		wantErr        error
		wantGroupsRead int
	}{
		{
			"a session with a sign-in and duplicate group names",
			gatePrincipal("alice", identity.KindPerson, identity.CredentialSession),
			alice, nil, aliceGroups, nil, true,
			[]string{"alpha", "zeta"},
			at, false, nil, 1,
		},
		{
			"an access token of the same person",
			gatePrincipal("alice", identity.KindPerson, identity.CredentialAccessToken),
			alice, nil, aliceGroups, nil, false,
			[]string{"alpha", "zeta"},
			time.Time{},
			false, nil, 1,
		},
		{
			"a session of a break-glass person",
			gatePrincipal("glass", identity.KindPerson, identity.CredentialSession),
			glass, nil, nil, nil, true,
			nil, at, true, nil, 1,
		},
		{
			"an access token of a break-glass person",
			gatePrincipal("glass", identity.KindPerson, identity.CredentialAccessToken),
			glass, nil, nil, nil, false,
			nil,
			time.Time{},
			false, nil, 1,
		},
		{
			"a session with no sign-in",
			gatePrincipal("alice", identity.KindPerson, identity.CredentialSession),
			alice, nil, nil, nil, false,
			nil,
			time.Time{},
			false, nil, 1,
		},
		{
			"a principal with no kind and no record",
			gatePrincipal("admin", "", identity.CredentialAccessToken),
			none, nil, nil, nil, false,
			nil,
			time.Time{},
			false, nil, 0,
		},
		{
			"a principal with a kind and no record",
			gatePrincipal("alice", identity.KindPerson, identity.CredentialAccessToken),
			none, nil, nil, nil, false,
			nil,
			time.Time{},
			false, identity.ErrDenied, 0,
		},
		{
			"a failed principal read",
			gatePrincipal("alice", identity.KindPerson, identity.CredentialAccessToken),
			none, errPrincipalDown, nil, nil, false,
			nil,
			time.Time{},
			false, errPrincipalDown, 0,
		},
		{
			"a failed groups read",
			gatePrincipal("alice", identity.KindPerson, identity.CredentialAccessToken),
			alice, nil, nil, errPrincipalDown, false,
			nil,
			time.Time{},
			false, errPrincipalDown, 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reads := &fakePrincipalReads{
				record: tc.record, recordErr: tc.recordErr, groups: tc.groups, groupsErr: tc.groupsErr, calls: 0,
			}

			ctx := t.Context()
			if tc.signedIn {
				ctx = sessionauth.WithSignIn(ctx, sessionauth.SignIn{At: at, Provider: "corp"})
			}

			s, err := principalSubjects{principals: reads}.Subject(ctx, tc.principal)

			if tc.wantErr != nil {
				checkSubjectError(t, err, tc.wantErr, reads.calls, tc.wantGroupsRead)

				return
			}

			if err != nil {
				t.Fatalf("Subject() = %+v, %v, want no error", s, err)
			}

			checkSubject(t, s, subjectWant{
				id: tc.principal.Subject, groups: tc.wantGroups, authAt: tc.wantAuthAt,
				breakGlass: tc.wantBreakGlass,
			})

			if reads.calls != tc.wantGroupsRead {
				t.Fatalf("ListActiveGroupNamesOf() calls = %d, want %d", reads.calls, tc.wantGroupsRead)
			}
		})
	}
}

// checkSubjectError checks the error of a refused subject and the count of the
// group reads.
func checkSubjectError(t *testing.T, err, want error, calls, wantCalls int) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("Subject() error = %v, want an error that matches %v", err, want)
	}

	if calls != wantCalls {
		t.Fatalf("ListActiveGroupNamesOf() calls = %d, want %d", calls, wantCalls)
	}
}

// subjectWant names the facts that a resolved subject must have.
type subjectWant struct {
	id         string
	groups     []string
	authAt     time.Time
	breakGlass bool
}

// checkSubject checks the facts of a resolved subject.
func checkSubject(t *testing.T, s gate.Subject, want subjectWant) {
	t.Helper()

	if s.ID != want.id || !slices.Equal(s.Groups, want.groups) || !s.AuthAt.Equal(want.authAt) ||
		s.BreakGlassAccount != want.breakGlass {
		t.Fatalf("Subject() = %+v, want ID %q, groups %v, auth at %v and break-glass %v",
			s, want.id, want.groups, want.authAt, want.breakGlass)
	}
}

func TestPrincipalSubjectsRefuseAnEmptySubject(t *testing.T) {
	t.Parallel()

	for _, subject := range []string{"", "  "} {
		_, err := principalSubjects{principals: nil}.Subject(t.Context(), gatePrincipal(subject, "", ""))
		if !errors.Is(err, identity.ErrDenied) {
			t.Fatalf("Subject(%q) = %v, want identity.ErrDenied", subject, err)
		}
	}
}

func TestPrincipalSubjectsRefuseAnUnknownCredential(t *testing.T) {
	t.Parallel()

	for _, credential := range []identity.Credential{"unknown", "Session", "job-token ", "x\ny"} {
		_, err := principalSubjects{principals: nil}.Subject(
			t.Context(), gatePrincipal("alice", identity.KindPerson, credential),
		)
		if !errors.Is(err, identity.ErrDenied) {
			t.Fatalf("Subject(credential %q) = %v, want identity.ErrDenied", credential, err)
		}
	}
}

// TestStateWriteGatesOffWithAStoreThatKeepsNoGates checks that a store with
// no gates gives the explicit guard of the gates off, with its reason, and
// never a nil guard.
func TestStateWriteGatesOffWithAStoreThatKeepsNoGates(t *testing.T) {
	t.Parallel()

	gates, err := newStateWriteGates(new(settings), nil)
	if err != nil {
		t.Fatalf("newStateWriteGates(no gate store) = %v, want nil", err)
	}

	if gates.svc != nil || gates.guard == nil {
		t.Fatalf("newStateWriteGates(no gate store) = service %v, guard %v, want no service and a guard",
			gates.svc, gates.guard)
	}

	eff := gates.effective()
	if eff.Mode != stategate.ModeOff || eff.Reason != reasonNoGateStore {
		t.Fatalf("effective = %+v, want off with %q", eff, reasonNoGateStore)
	}
}

// openGateStore opens a SQLite store, which keeps gates.
func openGateStore(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}

// TestStateWriteGatesRefuseAGateStoreWithAMissingPart sends a store that
// keeps gates with no inventory reader or no namespace service straight to
// the rule, as if the option check had passed it. The server must not start.
func TestStateWriteGatesRefuseAGateStoreWithAMissingPart(t *testing.T) {
	t.Parallel()

	st := openGateStore(t)

	nss, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	cases := []struct {
		name       string
		inventory  inventory.Reader
		namespaces *namespace.Service
	}{
		{"no inventory reader", nil, nss},
		{"no namespace service", st, nil},
		{"neither", nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := new(settings)
			cfg.namespaceStore, cfg.inventoryReader, cfg.now = st, tc.inventory, time.Now

			gates, err := newStateWriteGates(cfg, tc.namespaces)
			if !errors.Is(err, ErrMissingOption) || !errors.Is(err, errGateParts) {
				t.Fatalf("newStateWriteGates() error = %v, want ErrMissingOption and errGateParts", err)
			}

			if gates.guard != nil || gates.svc != nil {
				t.Fatalf("newStateWriteGates() gave a guard %v with the error", gates.guard)
			}

			if _, claimErr := claimGates(cfg, gates); !errors.Is(claimErr, errGateParts) {
				t.Fatalf("claimGates() error = %v, want errGateParts: the claimer follows the same rule", claimErr)
			}
		})
	}
}

// gateStoreWithoutPrincipals is a gate store that keeps no principal records.
// Its GetPrincipal has another signature, so it does not meet the principal
// store.
type gateStoreWithoutPrincipals struct {
	*sqlite.Store
}

func (gateStoreWithoutPrincipals) GetPrincipal(context.Context, string) (struct{}, error) {
	return struct{}{}, nil
}

// TestStateWriteGatesRefuseAGateStoreWithNoPrincipalReads sends a store that
// keeps gates with no principal records straight to the rule, as if the option
// check had passed it. The server must not start.
func TestStateWriteGatesRefuseAGateStoreWithNoPrincipalReads(t *testing.T) {
	t.Parallel()

	st := openGateStore(t)

	nss, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	cfg := new(settings)
	cfg.namespaceStore = gateStoreWithoutPrincipals{Store: st}
	cfg.inventoryReader, cfg.now = st, time.Now

	gates, err := newStateWriteGates(cfg, nss)
	if !errors.Is(err, ErrMissingOption) || !errors.Is(err, errGateParts) {
		t.Fatalf("newStateWriteGates() error = %v, want ErrMissingOption and errGateParts", err)
	}

	if gates.guard != nil || gates.svc != nil {
		t.Fatalf("newStateWriteGates() gave a service %v with the error", gates.svc)
	}
}

// TestClaimGuardOffFindsNoGate checks that the claimer follows the rule of
// the tool adapters: with the gates off, no gate covers a unit, and the guard
// is not nil.
func TestClaimGuardOffFindsNoGate(t *testing.T) {
	t.Parallel()

	gates, err := newStateWriteGates(new(settings), nil)
	if err != nil {
		t.Fatalf("newStateWriteGates: %v", err)
	}

	guard, err := claimGates(new(settings), gates)
	if err != nil {
		t.Fatalf("claimGates() = %v, want nil", err)
	}

	unit, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	enforced, err := guard.Enforced(t.Context(), unit)
	if err != nil || len(enforced) != 0 {
		t.Fatalf("Enforced() = %v, %v, want no gate and no error", enforced, err)
	}
}

func TestInventoryFactsOfAnUnknownUnitByIndexedLookup(t *testing.T) {
	t.Parallel()

	e := gateTestEntry()
	e.Name = "other"
	f, lookup := gateFacts([]inventory.Entry{e}, byIndexedLookup)

	u, err := identity.UnitObject("opentofu", "acme", "other")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = unitLabels(t.Context(), f, u); !errors.Is(err, gate.ErrNotFound) || lookup.calls != 1 {
		t.Fatalf(
			"Labels(unit the store does not know) = %v after %d lookups, want gate.ErrNotFound after 1",
			err,
			lookup.calls,
		)
	}
}

// errDeletedDown is the error of a store that does not answer.
var errDeletedDown = errors.New("deleted facts down")

// fakeDeleted serves the facts of the deleted units, by unit ID.
type fakeDeleted struct {
	facts map[string]inventory.DeletedFacts
	err   error
}

func (f fakeDeleted) GetDeletedUnitFacts(
	_ context.Context, _ namespace.Name, _, unitID string,
) (inventory.DeletedFacts, error) {
	if f.err != nil {
		return inventory.DeletedFacts{}, f.err
	}

	if facts, ok := f.facts[unitID]; ok {
		return facts, nil
	}

	return inventory.DeletedFacts{}, inventory.ErrNotFound
}

func TestInventoryFactsOfADeletedUnitGiveItsRealCollectionAndLabels(t *testing.T) {
	t.Parallel()

	f, _ := gateFacts(nil, byStableID)
	f.deleted = fakeDeleted{
		facts: map[string]inventory.DeletedFacts{
			"ws-1": {Collection: "prod", Labels: map[string]string{"team": "core", "env": "prod"}},
		},
		err: nil,
	}

	coll, err := f.Collection(t.Context(), gateTestUnit(t, byStableID))
	if err != nil || coll != collection.Name("prod") {
		t.Fatalf("Collection(deleted unit) = %q, %v, want prod", coll, err)
	}

	labels, err := unitLabels(t.Context(), f, gateTestUnit(t, byStableID))
	if err != nil || !slices.Equal(labels, []string{"env=prod", "team=core"}) {
		t.Fatalf("Labels(deleted unit) = %v, %v, want the sorted labels", labels, err)
	}
}

// A deleted unit that is not found must never read as a unit with no
// collection: a gate on its collection would not match, and the write would go on.
func TestInventoryFactsOfAUnitThatIsNeitherLiveNorDeletedFailClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		deleted inventory.DeletedFactsReader
	}{
		{"the unit is not deleted", fakeDeleted{facts: nil, err: nil}},
		{"the store keeps no deleted units", nil},
		{"the store does not answer", fakeDeleted{facts: nil, err: errDeletedDown}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, _ := gateFacts(nil, byStableID)
			f.deleted = tc.deleted

			if _, err := f.Collection(t.Context(), gateTestUnit(t, byStableID)); err == nil ||
				errors.Is(err, gate.ErrNotFound) {
				t.Fatalf("Collection() = %v, want an error that is not gate.ErrNotFound", err)
			}

			if _, err := unitLabels(t.Context(), f, gateTestUnit(t, byStableID)); err == nil ||
				errors.Is(err, gate.ErrNotFound) {
				t.Fatalf("Labels() = %v, want an error that is not gate.ErrNotFound", err)
			}
		})
	}
}

// A stable ID whose entry belongs to another namespace or tool never gives the
// facts of that entry, and never reads as a unit with no collection.
func TestInventoryFactsRefuseAnEntryOfAnotherNamespaceOrTool(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(e *inventory.Entry)
	}{
		{"another namespace", func(e *inventory.Entry) { e.Namespace = "globex" }},
		{"another tool", func(e *inventory.Entry) { e.Tool = "pulumi" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entry := gateTestEntry()
			tc.change(&entry)

			f, _ := gateFacts([]inventory.Entry{entry}, byStableID)

			if _, err := f.Collection(t.Context(), gateTestUnit(t, byStableID)); !errors.Is(err, errUnitMismatch) ||
				errors.Is(err, gate.ErrNotFound) {
				t.Fatalf("Collection() = %v, want errUnitMismatch and not gate.ErrNotFound", err)
			}

			if _, err := unitLabels(t.Context(), f, gateTestUnit(t, byStableID)); !errors.Is(err, errUnitMismatch) ||
				errors.Is(err, gate.ErrNotFound) {
				t.Fatalf("Labels() = %v, want errUnitMismatch and not gate.ErrNotFound", err)
			}
		})
	}
}
