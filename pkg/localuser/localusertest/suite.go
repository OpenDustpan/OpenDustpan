// Package localusertest is the conformance suite of the localuser ports. A
// store adapter runs it:
//
//	func TestLocalUserConformance(t *testing.T) {
//		t.Parallel()
//		localusertest.Run(t, func(t *testing.T) localusertest.Store { return newMyStore(t) })
//	}
//
// A case that needs a port that no store has yet skips, with the reason.
package localusertest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// Store is every persistence port the suite exercises: the ports of
// localuser, and the ports that make the principals, the access tokens and
// the installation kind of the cases and read their state.
type Store interface {
	localuser.Store
	localuser.PasswordStore
	localuser.ResetStore
	audit.Store
	principal.RecordStore
	session.PrincipalStateStore
	accesstoken.Store
	installation.KindStore
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// HasherFactory returns the hasher under test for one subtest.
type HasherFactory func(t *testing.T) localuser.PasswordHasher

// BlocklistFactory returns the blocklist under test for one subtest.
type BlocklistFactory func(t *testing.T) localuser.Blocklist

// CaseNames returns the names of the cases of the store suite, in the order
// of the run.
func CaseNames() []string {
	cases := storeCases()
	names := make([]string, 0, len(cases))

	for _, c := range cases {
		names = append(names, c.name)
	}

	return names
}

// HasherCaseNames returns the names of the cases of the PasswordHasher
// suite, in the order of the run.
func HasherCaseNames() []string {
	cases := hasherCases()
	names := make([]string, 0, len(cases))

	for _, c := range cases {
		names = append(names, c.name)
	}

	return names
}

// BlocklistCaseNames returns the names of the cases of the Blocklist suite,
// in the order of the run.
func BlocklistCaseNames() []string {
	cases := blocklistCases()
	names := make([]string, 0, len(cases))

	for _, c := range cases {
		names = append(names, c.name)
	}

	return names
}

// Run runs every case of the store suite against the stores that newStore
// returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	if newStore == nil {
		t.Fatal("localusertest.Run needs a store factory")
	}

	for _, c := range storeCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if c.skip != "" {
				t.Skip(c.skip)
			}

			c.test(t, newStore(t))
		})
	}
}

// RunHasher runs every case of the PasswordHasher suite against the hashers
// that newHasher returns.
func RunHasher(t *testing.T, newHasher HasherFactory) {
	t.Helper()

	if newHasher == nil {
		t.Fatal("localusertest.RunHasher needs a hasher factory")
	}

	for _, c := range hasherCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.test(t, newHasher(t))
		})
	}
}

// RunBlocklist runs every case of the Blocklist suite against the lists that
// newList returns.
func RunBlocklist(t *testing.T, newList BlocklistFactory) {
	t.Helper()

	if newList == nil {
		t.Fatal("localusertest.RunBlocklist needs a blocklist factory")
	}

	for _, c := range blocklistCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.test(t, newList(t))
		})
	}
}
