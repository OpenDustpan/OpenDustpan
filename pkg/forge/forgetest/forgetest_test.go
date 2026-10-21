package forgetest_test

import (
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forge/forgetest"
)

func TestCaseNamesAreUnique(t *testing.T) {
	t.Parallel()

	names := forgetest.CaseNames()
	if len(names) == 0 {
		t.Fatal("the suite has no case")
	}

	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if seen[n] {
			t.Errorf("the case %q is in the suite twice", n)
		}

		seen[n] = true
	}
}

// TestRunSkipsCasesWithNoBody runs the suite with a factory that skips each
// case that asks for a harness. A case with no body must not ask for one,
// so the factory is called once for each case that has a body.
func TestRunSkipsCasesWithNoBody(t *testing.T) {
	t.Parallel()

	var asked atomic.Int64

	forgetest.Run(t, func(t *testing.T) forgetest.Harness {
		t.Helper()
		asked.Add(1)
		t.Skip("a test of the suite gives no harness")

		return forgetest.Harness{
			Opener:      nil,
			Connection:  forge.Connection{ID: "", Kind: "", Auth: "", BaseURL: "", Params: nil, Push: false},
			Credential:  forge.Credential{SetID: "", Secret: nil, CreatedAt: time.Time{}},
			EventSecret: nil,
			Forge:       nil,
			Account:     forge.Account{ID: "", Login: "", ExpiresAt: time.Time{}, NoExpiry: false, Scopes: nil},
			Repo:        "",
			Fixture:     nil,
		}
	})

	t.Cleanup(func() {
		if got, want := int(asked.Load()), len(forgetest.CaseNamesWithBody()); got != want {
			t.Errorf("%d cases asked for a harness, want %d (the cases with a body)", got, want)
		}
	})
}

func TestCaseNamesWithBodyAreCases(t *testing.T) {
	t.Parallel()

	all := make(map[string]bool)
	for _, n := range forgetest.CaseNames() {
		all[n] = true
	}

	with := forgetest.CaseNamesWithBody()
	if len(with) == 0 || len(with) >= len(all) {
		t.Fatalf("%d of %d cases have a body", len(with), len(all))
	}

	for _, n := range with {
		if !all[n] {
			t.Errorf("the case %q has a body and is not in the suite", n)
		}
	}
}
