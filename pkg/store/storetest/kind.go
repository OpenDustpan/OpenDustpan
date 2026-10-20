package storetest

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/installation"
)

// KindFactory returns a fresh, empty store for one subtest of RunKind.
type KindFactory func(t *testing.T) installation.KindStore

// RunKind executes the conformance suite of installation.KindStore against
// stores produced by newStore.
func RunKind(t *testing.T, newStore KindFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s installation.KindStore)
		name string
	}{
		{name: "claim stores the first kind", test: testKindClaim},
		{name: "claim with production first", test: testKindClaimProduction},
		{name: "claim rejects other values", test: testKindClaimInvalid},
		{name: "claims at the same time store one kind", test: testKindClaimConcurrent},
		{name: "claim on a closed store is ErrUnavailable", test: testKindUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// testKindClaim covers the claim of the installation kind: a claim on a new
// store stores the kind and returns it, and a later claim with the same or
// another kind returns the first kind and changes nothing.
func testKindClaim(t *testing.T, s installation.KindStore) {
	t.Helper()

	ctx := t.Context()

	steps := []struct {
		name  string
		claim installation.Kind
		want  installation.Kind
	}{
		{name: "first claim stores the kind", claim: installation.KindDev, want: installation.KindDev},
		{name: "same kind again returns it", claim: installation.KindDev, want: installation.KindDev},
		{name: "another kind returns the first kind", claim: installation.KindProduction, want: installation.KindDev},
		{
			name:  "the other kind still returns the first kind",
			claim: installation.KindProduction,
			want:  installation.KindDev,
		},
	}

	for _, step := range steps {
		got, err := s.ClaimKind(ctx, step.claim)
		if err != nil {
			t.Fatalf("%s: claim %q: %v", step.name, step.claim, err)
		}

		if got != step.want {
			t.Fatalf("%s: claim %q = %q, want %q", step.name, step.claim, got, step.want)
		}
	}
}

// testKindClaimProduction covers a claim with production first: the stored
// kind is production, and a claim with dev returns production.
func testKindClaimProduction(t *testing.T, s installation.KindStore) {
	t.Helper()

	ctx := t.Context()

	for _, claim := range []installation.Kind{installation.KindProduction, installation.KindDev} {
		got, err := s.ClaimKind(ctx, claim)
		if err != nil {
			t.Fatalf("claim %q: %v", claim, err)
		}

		if got != installation.KindProduction {
			t.Fatalf("claim %q = %q, want production", claim, got)
		}
	}
}

// testKindClaimInvalid covers a claim with a value that is not a kind: it
// reports ErrInvalidKind and changes nothing, so a later claim still stores
// its kind.
func testKindClaimInvalid(t *testing.T, s installation.KindStore) {
	t.Helper()

	ctx := t.Context()

	for _, bad := range []installation.Kind{"", "staging", "Dev"} {
		if _, err := s.ClaimKind(ctx, bad); !errors.Is(err, installation.ErrInvalidKind) {
			t.Fatalf("claim %q: err = %v, want ErrInvalidKind", bad, err)
		}
	}

	got, err := s.ClaimKind(ctx, installation.KindDev)
	if err != nil || got != installation.KindDev {
		t.Fatalf("claim dev after invalid claims = %q, %v, want dev without error", got, err)
	}
}

// testKindClaimConcurrent covers claims at the same time: every caller gets
// the same stored kind, and that kind is one of the two that were claimed.
func testKindClaimConcurrent(t *testing.T, s installation.KindStore) {
	t.Helper()

	ctx := t.Context()

	const callers = 8

	results := make([]installation.Kind, callers)
	errs := make([]error, callers)

	var wg sync.WaitGroup

	for i := range callers {
		kind := installation.KindDev
		if i%2 == 1 {
			kind = installation.KindProduction
		}

		wg.Go(func() {
			results[i], errs[i] = s.ClaimKind(ctx, kind)
		})
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
	}

	for i, got := range results {
		if got != results[0] {
			t.Fatalf(
				"claim %d = %q, claim 0 = %q: claims at the same time stored more than one kind",
				i,
				got,
				results[0],
			)
		}
	}

	if !results[0].Valid() {
		t.Fatalf("stored kind = %q, want dev or production", results[0])
	}
}

// testKindUnavailable covers the claim on a closed store: it reports
// installation.ErrUnavailable without driver text, here forced by Close.
func testKindUnavailable(t *testing.T, s installation.KindStore) {
	t.Helper()

	ctx := t.Context()
	closeStore(t, s)

	_, err := s.ClaimKind(ctx, installation.KindDev)
	if !errors.Is(err, installation.ErrUnavailable) {
		t.Fatalf("claim on a closed store: err = %v, want ErrUnavailable", err)
	}

	for _, text := range []string{"sql: database is closed", "closed pool", "SQLITE", "sqlite3", "pgx"} {
		if strings.Contains(err.Error(), text) {
			t.Fatalf("claim on a closed store error %q holds driver text %q", err, text)
		}
	}
}
