package secrettest

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/secret"
)

// InstallationFactory returns a fresh, empty store for one subtest of the
// installation suite. The store must also hold namespace keys, so the
// suite can prove that the two kinds of keys stay apart.
type InstallationFactory func(t *testing.T) InstallationStore

// InstallationStore is every port the installation suite exercises.
type InstallationStore interface {
	Store
	secret.InstallationKeyStore
}

// RunInstallation executes the conformance suite of
// secret.InstallationKeyStore against stores produced by newStore.
func RunInstallation(t *testing.T, newStore InstallationFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s InstallationStore)
		name string
	}{
		{name: "installation key round trip keeps the first key", test: testInstallationRoundTrip},
		{name: "concurrent installation creates agree on one key", test: testInstallationConcurrent},
		{name: "get of a missing installation key is ErrKeyNotFound", test: testInstallationMissing},
		{name: "a scope with a namespace is ErrInvalidScope", test: testInstallationRefusesNamespace},
		{name: "installation wrapper IDs are listed apart", test: testInstallationWrapperIDs},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

func installationKey(t *testing.T, id, wrapperID, ciphertext string) secret.WrappedKey {
	t.Helper()

	scope, err := secret.InstallationScope(id)
	if err != nil {
		t.Fatalf("installation scope %q: %v", id, err)
	}

	return newKey(scope, wrapperID, ciphertext)
}

func testInstallationRoundTrip(t *testing.T, s InstallationStore) {
	t.Helper()

	want := installationKey(t, "sign-in-corp", "kek-1", "wrapped-1")

	got, err := s.CreateInstallationKey(t.Context(), want)
	if err != nil {
		t.Fatalf("create installation key: %v", err)
	}

	assertKey(t, got, want)

	again, err := s.CreateInstallationKey(t.Context(), installationKey(t, "sign-in-corp", "kek-2", "wrapped-2"))
	if err != nil {
		t.Fatalf("create installation key again: %v", err)
	}

	assertKey(t, again, want)

	stored, err := s.GetInstallationKey(t.Context(), "sign-in-corp")
	if err != nil {
		t.Fatalf("get installation key: %v", err)
	}

	assertKey(t, stored, want)
}

func testInstallationConcurrent(t *testing.T, s InstallationStore) {
	t.Helper()

	keys := []secret.WrappedKey{
		installationKey(t, "sign-in-corp", "kek-1", "wrapped-first"),
		installationKey(t, "sign-in-corp", "kek-2", "wrapped-second"),
	}

	got := make([]secret.WrappedKey, len(keys))
	errs := make([]error, len(keys))

	var wg sync.WaitGroup

	for i, k := range keys {
		wg.Go(func() {
			got[i], errs[i] = s.CreateInstallationKey(t.Context(), k)
		})
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent create %d: %v", i, err)
		}
	}

	assertKey(t, got[1], got[0])
}

func testInstallationMissing(t *testing.T, s InstallationStore) {
	t.Helper()

	if _, err := s.GetInstallationKey(t.Context(), "sign-in-none"); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Fatalf("get of a missing installation key = %v, want ErrKeyNotFound", err)
	}

	ids, err := s.ListInstallationWrapperIDs(t.Context())
	if err != nil || ids == nil || len(ids) != 0 {
		t.Fatalf("installation wrapper IDs with no key = %#v, %v; want an empty list", ids, err)
	}
}

func testInstallationRefusesNamespace(t *testing.T, s InstallationStore) {
	t.Helper()

	ns := newNamespace(t, s)
	scope := newScope(t, ns.Name, "stk-aaaaaaaaaaaaaaaaaaaa")

	_, err := s.CreateInstallationKey(t.Context(), newKey(scope, "kek-1", "wrapped"))
	if !errors.Is(err, secret.ErrInvalidScope) {
		t.Fatalf("installation create with a namespace scope = %v, want ErrInvalidScope", err)
	}

	if _, err := s.GetInstallationKey(t.Context(), scope.ID); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Fatalf("get after a refused create = %v, want ErrKeyNotFound", err)
	}
}

// testInstallationWrapperIDs proves that an installation key is listed by
// ListInstallationWrapperIDs and not by ListWrapperIDs, and that a
// namespace key with the same ID is another key.
func testInstallationWrapperIDs(t *testing.T, s InstallationStore) {
	t.Helper()

	ns := newNamespace(t, s)
	st := createStack(t, s, ns, "prod")
	nsScope := newScope(t, ns.Name, st.ID)

	if _, err := s.CreateKey(t.Context(), newKey(nsScope, "kek-ns", "wrapped-ns"), ns.Fence()); err != nil {
		t.Fatalf("create namespace key: %v", err)
	}

	same := installationKey(t, nsScope.ID, "kek-install", "wrapped-install")
	if _, err := s.CreateInstallationKey(t.Context(), same); err != nil {
		t.Fatalf("create installation key: %v", err)
	}

	stored, err := s.GetInstallationKey(t.Context(), nsScope.ID)
	if err != nil {
		t.Fatalf("get installation key: %v", err)
	}

	assertKey(t, stored, same)

	ids, err := s.ListInstallationWrapperIDs(t.Context())
	if err != nil || !slices.Equal(ids, []string{"kek-install"}) {
		t.Fatalf("installation wrapper IDs = %v, %v; want [kek-install]", ids, err)
	}

	nsIDs, err := s.ListWrapperIDs(t.Context())
	if err != nil || !slices.Equal(nsIDs, []string{"kek-ns"}) {
		t.Fatalf("namespace wrapper IDs = %v, %v; want [kek-ns]", nsIDs, err)
	}
}
