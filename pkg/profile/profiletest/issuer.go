package profiletest

import (
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// IssuerStore is every persistence port the issuer suite exercises.
type IssuerStore struct {
	Issuer     profile.IssuerStore
	Namespaces namespace.Store
}

// IssuerFactory returns a fresh, empty store for one subtest.
type IssuerFactory func(t *testing.T) IssuerStore

// RunIssuer runs the suite of profile.IssuerStore: the states of a key, the
// one-active-key rule, the rotation at each boundary, the fence, the audit
// entries and the settings. Every store adapter runs it.
func RunIssuer(t *testing.T, newStore IssuerFactory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, s IssuerStore)
	}{
		{"a namespace with no key has an empty JWKS", testIssuerNoKeys},
		{"the first make stores an active and a next key", testIssuerInitial},
		{"the first make is a conflict when a key exists", testIssuerInitialConflict},
		{"a published read holds no private key", testIssuerPublishedNoSecret},
		{"a promotion keeps the next key and retires the old one", testIssuerPromote},
		{"a retired and a deleted key keep no private key", testIssuerNoPrivateAfterRetire},
		{"a deleted key leaves the JWKS and stays in the history", testIssuerDeleted},
		{"a move from a wrong state changes nothing", testIssuerWrongFrom},
		{"a second active key is a conflict", testIssuerSecondActive},
		{"a third retired key is a conflict", testIssuerThirdRetired},
		{"a next or active key with no private key is refused", testIssuerNeedsSealed},
		{"a retired key with a private key is refused", testIssuerRetiredSealed},
		{"keys of two namespaces are apart", testIssuerNamespaces},
		{"a stale fence refuses a change of keys", testIssuerFenced},
		{"an invalid or missing audit entry changes nothing", testIssuerAudit},
		{"an emergency change replaces every key", testIssuerEmergency},
		{"settings default and store with a version", testIssuerSettings},
		{"settings on a stale version are a conflict", testIssuerSettingsStale},
		{"a stale fence refuses settings and a copy check", testIssuerSettingsFenced},
		{"a copy check keeps the version and only the listed keys", testIssuerCopyCheck},
		{"a change of issuerURL clears the copy check", testIssuerCopyCheckResets},
		{"a copy check of another URL or of no URL is refused", testIssuerCopyCheckURL},
		{"a namespace is found by its ID", testIssuerResolve},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

func issuerNamespace(t *testing.T, s IssuerStore, name string) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: epoch(), Name: namespace.Name(name), PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.Namespaces.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}

	return ns
}

// twoKeys is the count of live keys after the first make.
const twoKeys = 2

func issuerEntries(ns namespace.Name) []audit.Entry { return entries(ns, "namespace:manage-issuer") }

func testKey(ns namespace.Name, state profile.KeyState, at time.Time) profile.Key {
	k := profile.Key{
		CreatedAt: at, ActivatedAt: time.Time{}, RetiredAt: time.Time{}, DeletedAt: time.Time{},
		ID: profile.NewKeyID(), Namespace: ns, State: state, Public: []byte("public-key"), Sealed: nil,
	}

	if state.HoldsPrivate() {
		k.Sealed = []byte("sealed-private-key")
	}

	if state == profile.KeyActive {
		k.ActivatedAt = at
	}

	return k
}

// keyPair is the first active key and the first next key of a namespace.
type keyPair struct {
	active profile.Key
	next   profile.Key
}

// firstKeys makes the first active and next key of ns.
func firstKeys(t *testing.T, s IssuerStore, ns namespace.Namespace) keyPair {
	t.Helper()

	active, next := testKey(ns.Name, profile.KeyActive, epoch()), testKey(ns.Name, profile.KeyNext, epoch())

	err := s.Issuer.Apply(
		t.Context(),
		ns.Name,
		profile.KeyChange{Add: []profile.Key{active, next}, Move: nil, Initial: true},
		ns.Fence(),
		issuerEntries(ns.Name),
	)
	if err != nil {
		t.Fatalf("first make: %v", err)
	}

	return keyPair{active: active, next: next}
}

func apply(t *testing.T, s IssuerStore, ns namespace.Namespace, ch profile.KeyChange) error {
	t.Helper()

	return s.Issuer.Apply(t.Context(), ns.Name, ch, ns.Fence(), issuerEntries(ns.Name))
}

func stateOf(t *testing.T, s IssuerStore, ns namespace.Name, id profile.KeyID) profile.KeyState {
	t.Helper()

	history, err := s.Issuer.History(t.Context(), ns)
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	for idx := range history {
		k := &history[idx]
		if k.ID == id {
			return k.State
		}
	}

	return ""
}

func ids(keys []profile.Key) []profile.KeyID {
	out := make([]profile.KeyID, 0, len(keys))
	for idx := range keys {
		k := &keys[idx]
		out = append(out, k.ID)
	}

	slices.Sort(out)

	return out
}

func testIssuerNoKeys(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")

	got, err := s.Issuer.Published(t.Context(), ns.Name)
	if err != nil || len(got) != 0 {
		t.Errorf("Published = %v, %v, want none", got, err)
	}

	live, err := s.Issuer.Live(t.Context(), ns.Name)
	if err != nil || len(live) != 0 {
		t.Errorf("Live = %v, %v, want none", live, err)
	}
}

func testIssuerInitial(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	pair := firstKeys(t, s, ns)
	active, next := pair.active, pair.next

	live, err := s.Issuer.Live(t.Context(), ns.Name)
	if err != nil || !slices.Equal(ids(live), ids([]profile.Key{active, next})) {
		t.Fatalf("Live = %v, %v", ids(live), err)
	}

	for idx := range live {
		k := &live[idx]
		if len(k.Sealed) == 0 || string(k.Public) != "public-key" || k.Namespace != ns.Name {
			t.Errorf("live key %s = %+v", k.ID, k)
		}
	}

	if stateOf(t, s, ns.Name, active.ID) != profile.KeyActive || stateOf(t, s, ns.Name, next.ID) != profile.KeyNext {
		t.Error("states after the first make are wrong")
	}
}

func testIssuerInitialConflict(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	firstKeys(t, s, ns)

	err := apply(t, s, ns, profile.KeyChange{
		Add:  []profile.Key{testKey(ns.Name, profile.KeyActive, epoch()), testKey(ns.Name, profile.KeyNext, epoch())},
		Move: nil, Initial: true,
	})
	wantErr(t, "second first make", err, profile.ErrConflict)
}

func testIssuerPublishedNoSecret(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	firstKeys(t, s, ns)

	got, err := s.Issuer.Published(t.Context(), ns.Name)
	if err != nil || len(got) != 2 {
		t.Fatalf("Published = %v, %v", got, err)
	}

	for idx := range got {
		k := &got[idx]
		if len(k.Sealed) != 0 {
			t.Errorf("published key %s holds a private key", k.ID)
		}
	}
}

func testIssuerPromote(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	pair := firstKeys(t, s, ns)
	active, next := pair.active, pair.next
	at := epoch().Add(90 * 24 * time.Hour)
	fresh := testKey(ns.Name, profile.KeyNext, at)

	err := apply(t, s, ns, profile.KeyChange{
		Add: []profile.Key{fresh}, Initial: false,
		Move: []profile.KeyMove{
			{At: at, ID: active.ID, From: profile.KeyActive, To: profile.KeyRetired},
			{At: at, ID: next.ID, From: profile.KeyNext, To: profile.KeyActive},
		},
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}

	pub, err := s.Issuer.Published(t.Context(), ns.Name)
	if err != nil || len(pub) != 3 {
		t.Fatalf("Published = %d keys, %v, want 3", len(pub), err)
	}

	pair2 := promotedSet{old: active.ID, promoted: next.ID, fresh: fresh.ID, at: at}
	for idx := range pub {
		checkPromoted(t, &pub[idx], pair2)
	}
}

// promotedSet names the three keys of a promotion and its time.
type promotedSet struct {
	at       time.Time
	old      profile.KeyID
	promoted profile.KeyID
	fresh    profile.KeyID
}

// checkPromoted checks the state of one published key after a promotion.
func checkPromoted(t *testing.T, k *profile.Key, w promotedSet) {
	t.Helper()

	if k.ID == w.old && (k.State != profile.KeyRetired || !k.RetiredAt.Equal(w.at)) {
		t.Errorf("old key = %+v", k)
	}

	if k.ID == w.promoted && (k.State != profile.KeyActive || !k.ActivatedAt.Equal(w.at)) {
		t.Errorf("promoted key = %+v", k)
	}

	if k.ID == w.fresh && k.State != profile.KeyNext {
		t.Errorf("new next key = %+v", k)
	}
}

func testIssuerNoPrivateAfterRetire(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	pair := firstKeys(t, s, ns)
	active, next := pair.active, pair.next
	at := epoch().Add(time.Hour)

	err := apply(t, s, ns, profile.KeyChange{Add: nil, Initial: false, Move: []profile.KeyMove{
		{At: at, ID: active.ID, From: profile.KeyActive, To: profile.KeyRetired},
		{At: at, ID: next.ID, From: profile.KeyNext, To: profile.KeyActive},
	}})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}

	live, err := s.Issuer.Live(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("Live: %v", err)
	}

	for idx := range live {
		k := &live[idx]
		if k.State == profile.KeyRetired && len(k.Sealed) != 0 {
			t.Errorf("retired key %s keeps its private key", k.ID)
		}

		if k.State == profile.KeyActive && len(k.Sealed) == 0 {
			t.Errorf("active key %s lost its private key", k.ID)
		}
	}
}

func testIssuerDeleted(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	pair := firstKeys(t, s, ns)
	active, next := pair.active, pair.next
	at := epoch().Add(time.Hour)

	err := apply(t, s, ns, profile.KeyChange{Add: nil, Initial: false, Move: []profile.KeyMove{
		{At: at, ID: next.ID, From: profile.KeyNext, To: profile.KeyDeleted},
	}})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	pub := mustPublished(t, s, ns.Name)
	if len(pub) != 1 || pub[0].ID != active.ID {
		t.Errorf("Published after delete = %v", ids(pub))
	}

	history, err := s.Issuer.History(t.Context(), ns.Name)
	if err != nil || len(history) != 2 {
		t.Fatalf("History = %v, %v", history, err)
	}

	for idx := range history {
		h := &history[idx]
		if h.ID == next.ID && (h.State != profile.KeyDeleted || !h.DeletedAt.Equal(at)) {
			t.Errorf("deleted key in history = %+v", h)
		}
	}

	err = apply(t, s, ns, profile.KeyChange{Add: nil, Initial: false, Move: []profile.KeyMove{
		{At: at, ID: next.ID, From: profile.KeyNext, To: profile.KeyActive},
	}})
	wantErr(t, "move of a deleted key", err, profile.ErrConflict)
}

func testIssuerWrongFrom(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	pair := firstKeys(t, s, ns)
	active, next := pair.active, pair.next
	at := epoch().Add(time.Hour)

	err := apply(t, s, ns, profile.KeyChange{
		Add: []profile.Key{testKey(ns.Name, profile.KeyRetired, at)}, Initial: false,
		Move: []profile.KeyMove{
			{At: at, ID: next.ID, From: profile.KeyNext, To: profile.KeyDeleted},
			{At: at, ID: active.ID, From: profile.KeyNext, To: profile.KeyRetired},
		},
	})
	wantErr(t, "move with a wrong state", err, profile.ErrConflict)

	if stateOf(t, s, ns.Name, next.ID) != profile.KeyNext || stateOf(t, s, ns.Name, active.ID) != profile.KeyActive {
		t.Error("a refused change moved a key")
	}

	live := mustLive(t, s, ns.Name)
	if len(live) != twoKeys {
		t.Errorf("a refused change added a key: %d keys", len(live))
	}

	err = apply(t, s, ns, profile.KeyChange{Add: nil, Initial: false, Move: []profile.KeyMove{
		{At: at, ID: profile.NewKeyID(), From: profile.KeyNext, To: profile.KeyActive},
	}})
	wantErr(t, "move of an unknown key", err, profile.ErrConflict)
}

func testIssuerSecondActive(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	firstKeys(t, s, ns)

	err := apply(t, s, ns, profile.KeyChange{
		Add: []profile.Key{testKey(ns.Name, profile.KeyActive, epoch())}, Move: nil, Initial: false,
	})
	wantErr(t, "second active key", err, profile.ErrConflict)

	err = apply(t, s, ns, profile.KeyChange{
		Add: []profile.Key{testKey(ns.Name, profile.KeyNext, epoch())}, Move: nil, Initial: false,
	})
	wantErr(t, "second next key", err, profile.ErrConflict)
}

func testIssuerThirdRetired(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	firstKeys(t, s, ns)

	for range profile.MaxRetiredKeys {
		err := apply(t, s, ns, profile.KeyChange{
			Add: []profile.Key{testKey(ns.Name, profile.KeyRetired, epoch())}, Move: nil, Initial: false,
		})
		if err != nil {
			t.Fatalf("add retired key: %v", err)
		}
	}

	err := apply(t, s, ns, profile.KeyChange{
		Add: []profile.Key{testKey(ns.Name, profile.KeyRetired, epoch())}, Move: nil, Initial: false,
	})
	wantErr(t, "third retired key", err, profile.ErrConflict)
}

func testIssuerNeedsSealed(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	bare := testKey(ns.Name, profile.KeyActive, epoch())
	bare.Sealed = nil

	err := apply(t, s, ns, profile.KeyChange{Add: []profile.Key{bare}, Move: nil, Initial: true})
	if err == nil {
		t.Fatal("an active key with no private key was stored")
	}

	pub := mustPublished(t, s, ns.Name)
	if len(pub) != 0 {
		t.Errorf("a refused key is published: %v", ids(pub))
	}
}

func testIssuerRetiredSealed(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	firstKeys(t, s, ns)

	k := testKey(ns.Name, profile.KeyRetired, epoch())
	k.Sealed = []byte("kept")

	if err := apply(t, s, ns, profile.KeyChange{Add: []profile.Key{k}, Move: nil, Initial: false}); err == nil {
		t.Fatal("a retired key with a private key was stored")
	}
}

func testIssuerNamespaces(t *testing.T, s IssuerStore) {
	t.Helper()

	a, b := issuerNamespace(t, s, "acme"), issuerNamespace(t, s, "globex")
	ka := firstKeys(t, s, a).active
	kb := firstKeys(t, s, b).active

	pub := mustPublished(t, s, a.Name)
	if slices.Contains(ids(pub), kb.ID) || !slices.Contains(ids(pub), ka.ID) {
		t.Errorf("acme publishes %v", ids(pub))
	}

	err := apply(t, s, b, profile.KeyChange{Add: nil, Initial: false, Move: []profile.KeyMove{
		{At: epoch(), ID: ka.ID, From: profile.KeyActive, To: profile.KeyDeleted},
	}})
	wantErr(t, "move of a key of another namespace", err, profile.ErrConflict)

	if stateOf(t, s, a.Name, ka.ID) != profile.KeyActive {
		t.Error("a change in one namespace moved a key of another")
	}
}

func testIssuerFenced(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	stale := ns.Fence()
	stale.FailoverVersion--

	err := s.Issuer.Apply(t.Context(), ns.Name, profile.KeyChange{
		Add: []profile.Key{testKey(ns.Name, profile.KeyActive, epoch())}, Move: nil, Initial: true,
	}, stale, issuerEntries(ns.Name))
	wantErr(t, "Apply", err, namespace.ErrFenced)

	pub := mustPublished(t, s, ns.Name)
	if len(pub) != 0 {
		t.Errorf("a fenced change stored %d keys", len(pub))
	}
}

func testIssuerAudit(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	ch := profile.KeyChange{Add: []profile.Key{testKey(ns.Name, profile.KeyActive, epoch())}, Move: nil, Initial: true}

	bad := entry(ns.Name, "namespace:manage-issuer")
	bad.Subject = ""

	wantErr(t, "invalid entry", s.Issuer.Apply(t.Context(), ns.Name, ch, ns.Fence(), []audit.Entry{bad}),
		audit.ErrInvalidEntry)
	wantErr(t, "entry of another namespace",
		s.Issuer.Apply(t.Context(), ns.Name, ch, ns.Fence(), entries("globex", "namespace:manage-issuer")),
		audit.ErrInvalidEntry)
	wantErr(t, "no entry", s.Issuer.Apply(t.Context(), ns.Name, ch, ns.Fence(), nil), profile.ErrInvalid)

	pub := mustPublished(t, s, ns.Name)
	if len(pub) != 0 {
		t.Errorf("a refused change stored %d keys", len(pub))
	}
}

func testIssuerEmergency(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	pair := firstKeys(t, s, ns)
	active, next := pair.active, pair.next
	at := epoch().Add(time.Hour)
	a2, n2 := testKey(ns.Name, profile.KeyActive, at), testKey(ns.Name, profile.KeyNext, at)

	err := apply(t, s, ns, profile.KeyChange{Add: []profile.Key{a2, n2}, Initial: false, Move: []profile.KeyMove{
		{At: at, ID: active.ID, From: profile.KeyActive, To: profile.KeyDeleted},
		{At: at, ID: next.ID, From: profile.KeyNext, To: profile.KeyDeleted},
	}})
	if err != nil {
		t.Fatalf("emergency change: %v", err)
	}

	pub := mustPublished(t, s, ns.Name)
	if !slices.Equal(ids(pub), ids([]profile.Key{a2, n2})) {
		t.Errorf("Published after emergency = %v", ids(pub))
	}
}

func issuerSettings(ns namespace.Name) profile.IssuerSettings {
	s := profile.DefaultIssuerSettings(ns)
	s.IssuerURL = "https://keys.example.com/acme"
	s.IncludeActorName = true
	s.UpdatedAt = epoch()
	s.UpdatedBy = subject

	return s
}

func testIssuerSettings(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")

	got, err := s.Issuer.GetIssuerSettings(t.Context(), ns.Name)
	if err != nil || !isDefaultSettings(got) {
		t.Fatalf("default settings = %+v, %v", got, err)
	}

	put, err := s.Issuer.PutIssuerSettings(t.Context(), issuerSettings(ns.Name), 0, ns.Fence(), issuerEntries(ns.Name))
	if err != nil || put.Version != 1 {
		t.Fatalf("Put = %+v, %v", put, err)
	}

	got, err = s.Issuer.GetIssuerSettings(t.Context(), ns.Name)
	if err != nil || !isStoredSettings(got) {
		t.Errorf("stored settings = %+v, %v", got, err)
	}

	_, err = s.Issuer.GetIssuerSettings(t.Context(), "nobody")
	wantErr(t, "settings of an unknown namespace", err, namespace.ErrNotFound)
}

func isDefaultSettings(got profile.IssuerSettings) bool {
	return got.Version == 0 && got.IssuerURL == "" && got.AllowBlockSubjects && !got.IncludeActorName &&
		slices.Equal(got.SubjectTemplate, profile.DefaultSubjectTemplate())
}

func isStoredSettings(got profile.IssuerSettings) bool {
	return got.Version == 1 && got.IssuerURL == "https://keys.example.com/acme" && got.IncludeActorName &&
		got.UpdatedAt.Equal(epoch()) && got.UpdatedBy == subject
}

func testIssuerSettingsStale(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	in := issuerSettings(ns.Name)

	if _, err := s.Issuer.PutIssuerSettings(t.Context(), in, 0, ns.Fence(), issuerEntries(ns.Name)); err != nil {
		t.Fatalf("first put: %v", err)
	}

	_, err := s.Issuer.PutIssuerSettings(t.Context(), in, 0, ns.Fence(), issuerEntries(ns.Name))
	wantErr(t, "put on a stale version", err, profile.ErrConflict)

	in.AllowBlockSubjects = false

	put, err := s.Issuer.PutIssuerSettings(t.Context(), in, 1, ns.Fence(), issuerEntries(ns.Name))
	if err != nil || put.Version != 2 {
		t.Errorf("second put = %+v, %v", put, err)
	}
}

func testIssuerSettingsFenced(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	stale := ns.Fence()
	stale.FailoverVersion--

	_, err := s.Issuer.PutIssuerSettings(t.Context(), issuerSettings(ns.Name), 0, stale, issuerEntries(ns.Name))
	wantErr(t, "PutIssuerSettings", err, namespace.ErrFenced)

	staleCheck := profile.CopyCheck{
		At:       epoch(),
		URL:      issuerSettings(ns.Name).IssuerURL,
		OK:       true,
		Steps:    nil,
		Verified: nil,
	}
	err = s.Issuer.RecordCopyCheck(t.Context(), ns.Name, staleCheck, stale, issuerEntries(ns.Name))
	wantErr(t, "RecordCopyCheck", err, namespace.ErrFenced)

	bad := entry(ns.Name, "namespace:manage-issuer")
	bad.Subject = ""

	_, err = s.Issuer.PutIssuerSettings(t.Context(), issuerSettings(ns.Name), 0, ns.Fence(), []audit.Entry{bad})
	wantErr(t, "put with an invalid entry", err, audit.ErrInvalidEntry)

	got := mustSettings(t, s, ns.Name)
	if got.Version != 0 {
		t.Errorf("a refused write stored settings: %+v", got)
	}
}

func testIssuerCopyCheck(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	pair := firstKeys(t, s, ns)
	active, next := pair.active, pair.next

	_, putErr := s.Issuer.PutIssuerSettings(t.Context(), issuerSettings(ns.Name), 0, ns.Fence(), issuerEntries(ns.Name))
	if putErr != nil {
		t.Fatalf("put: %v", putErr)
	}

	check := profile.CopyCheck{
		At:  epoch().Add(time.Hour),
		URL: issuerSettings(ns.Name).IssuerURL,
		OK:  true,
		Steps: []profile.CopyStep{
			{Name: "discovery", OK: true, Detail: "read"},
			{Name: "jwks", OK: true, Detail: "read"},
		},
		Verified: []profile.KeyID{active.ID, next.ID, profile.NewKeyID()},
	}

	if err := s.Issuer.RecordCopyCheck(t.Context(), ns.Name, check, ns.Fence(), issuerEntries(ns.Name)); err != nil {
		t.Fatalf("RecordCopyCheck: %v", err)
	}

	got, err := s.Issuer.GetIssuerSettings(t.Context(), ns.Name)
	if err != nil || got.Version != 1 || got.CopyCheck == nil || !got.CopyCheck.OK || len(got.CopyCheck.Steps) != 2 {
		t.Fatalf("settings after a copy check = %+v, %v", got, err)
	}

	if !got.CopyCheck.Holds(got.IssuerURL, active.ID) || !got.CopyCheck.Holds(got.IssuerURL, next.ID) ||
		len(got.CopyCheck.Verified) != 2 {
		t.Errorf("verified keys = %v, want the two keys of the namespace", got.CopyCheck.Verified)
	}
}

func testIssuerCopyCheckResets(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	pair := firstKeys(t, s, ns)
	set := issuerSettings(ns.Name)

	put, err := s.Issuer.PutIssuerSettings(t.Context(), set, 0, ns.Fence(), issuerEntries(ns.Name))
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	check := profile.CopyCheck{
		At: epoch(), URL: set.IssuerURL, OK: true, Steps: nil, Verified: []profile.KeyID{pair.active.ID},
	}
	if err = s.Issuer.RecordCopyCheck(t.Context(), ns.Name, check, ns.Fence(), issuerEntries(ns.Name)); err != nil {
		t.Fatalf("RecordCopyCheck: %v", err)
	}

	cases := []struct {
		name     string
		url      string
		wantKept bool
	}{
		{"the same URL keeps the check", set.IssuerURL, true},
		{"another URL clears the check", "https://other.example.com/acme", false},
		{"no URL clears the check", "", false},
	}

	version := put.Version

	for _, tc := range cases {
		next := put
		next.IssuerURL = tc.url

		put, err = s.Issuer.PutIssuerSettings(t.Context(), next, version, ns.Fence(), issuerEntries(ns.Name))
		if err != nil {
			t.Fatalf("%s: put: %v", tc.name, err)
		}

		version = put.Version

		got, getErr := s.Issuer.GetIssuerSettings(t.Context(), ns.Name)
		if getErr != nil {
			t.Fatalf("%s: get: %v", tc.name, getErr)
		}

		if kept := got.CopyCheck != nil; kept != tc.wantKept {
			t.Errorf("%s: copy check kept = %v, want %v", tc.name, kept, tc.wantKept)
		}

		if tc.wantKept && !got.CopyCheck.Holds(tc.url, pair.active.ID) {
			t.Errorf("%s: the kept check does not hold the active key", tc.name)
		}

		// The check that was cleared must not come back for the old URL.
		if got.CopyCheck != nil && got.CopyCheck.Holds("https://other.example.com/acme", pair.active.ID) {
			t.Errorf("%s: a check of the first URL holds for another URL", tc.name)
		}
	}
}

func testIssuerCopyCheckURL(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")
	pair := firstKeys(t, s, ns)
	set := issuerSettings(ns.Name)

	if _, err := s.Issuer.PutIssuerSettings(t.Context(), set, 0, ns.Fence(), issuerEntries(ns.Name)); err != nil {
		t.Fatalf("put: %v", err)
	}

	cases := []struct {
		want error
		name string
		url  string
	}{
		{profile.ErrConflict, "a check of another URL than the stored one", "https://other.example.com/acme"},
		{profile.ErrInvalid, "a check with no URL", ""},
	}

	for _, tc := range cases {
		check := profile.CopyCheck{
			At: epoch(), URL: tc.url, OK: true, Steps: nil, Verified: []profile.KeyID{pair.active.ID},
		}

		err := s.Issuer.RecordCopyCheck(t.Context(), ns.Name, check, ns.Fence(), issuerEntries(ns.Name))
		wantErr(t, tc.name, err, tc.want)
	}

	// Nothing was stored.
	got, err := s.Issuer.GetIssuerSettings(t.Context(), ns.Name)
	if err != nil || got.CopyCheck != nil {
		t.Errorf("settings after the refused checks = %+v, %v; want no copy check", got, err)
	}

	// A namespace with no settings has no URL that a check could name.
	other := issuerNamespace(t, s, "globex")
	check := profile.CopyCheck{At: epoch(), URL: set.IssuerURL, OK: true, Steps: nil, Verified: nil}
	err = s.Issuer.RecordCopyCheck(t.Context(), other.Name, check, other.Fence(), issuerEntries(other.Name))
	wantErr(t, "a namespace with no settings", err, profile.ErrConflict)
}

func testIssuerResolve(t *testing.T, s IssuerStore) {
	t.Helper()

	ns := issuerNamespace(t, s, "acme")

	got, err := s.Issuer.NamespaceByID(t.Context(), ns.ID)
	if err != nil || got.Name != ns.Name || got.ID != ns.ID {
		t.Errorf("NamespaceByID = %+v, %v", got, err)
	}

	_, err = s.Issuer.NamespaceByID(t.Context(), namespace.NewID())
	wantErr(t, "unknown ID", err, namespace.ErrNotFound)
}

func mustPublished(t *testing.T, s IssuerStore, ns namespace.Name) []profile.Key {
	t.Helper()

	keys, err := s.Issuer.Published(t.Context(), ns)
	if err != nil {
		t.Fatalf("Published: %v", err)
	}

	return keys
}

func mustLive(t *testing.T, s IssuerStore, ns namespace.Name) []profile.Key {
	t.Helper()

	keys, err := s.Issuer.Live(t.Context(), ns)
	if err != nil {
		t.Fatalf("Live: %v", err)
	}

	return keys
}

func mustSettings(t *testing.T, s IssuerStore, ns namespace.Name) profile.IssuerSettings {
	t.Helper()

	set, err := s.Issuer.GetIssuerSettings(t.Context(), ns)
	if err != nil {
		t.Fatalf("GetIssuerSettings: %v", err)
	}

	return set
}
