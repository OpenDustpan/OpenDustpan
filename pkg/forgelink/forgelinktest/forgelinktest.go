// Package forgelinktest is the conformance suite for forgelink.Store. Every
// store adapter runs Run:
//
//	func TestForgeLinkConformance(t *testing.T) {
//		t.Parallel()
//		forgelinktest.Run(t, func(t *testing.T) forgelinktest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory runs the suite in the tests of this package, so
// each case runs before the first SQL adapter.
package forgelinktest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forgelink"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Harness is one fresh, empty store for one case.
type Harness struct {
	Store forgelink.Store
	// Principal makes a principal with a name that the case chooses and returns
	// its ID. An adapter with a foreign key to the principals needs the row; the
	// reference adapter returns name.
	Principal func(t *testing.T, name string) string
	// AuditEntries returns the audit entries of the server scope, in append order.
	AuditEntries func(t *testing.T) []audit.Entry
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

type suiteCase struct {
	name string
	run  func(t *testing.T, h Harness)
}

// Run executes the conformance suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, tc := range cases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

func cases() []suiteCase {
	return []suiteCase{
		{"a request is stored and read by the hash of its token", caseRequestRoundTrip},
		{"a request with the hash of a stored one is a conflict", caseRequestConflict},
		{"an invalid request is refused", caseRequestInvalid},
		{"a person has a bounded number of open requests", caseRequestLimit},
		{"an unknown request is not found", caseRequestUnknown},
		{"a proof is recorded on the request", caseProve},
		{"a repeated proof of the same account changes nothing", caseProveRepeat},
		{"a second account cannot prove a request that another account proved", caseProveOtherAccount},
		{"a proof of an expired, used or unknown request is refused", caseProveRefusals},
		{"an invalid proof is refused", caseProveInvalid},
		{"an accept without a proof is refused and the request stays good", caseAcceptNotProved},
		{"a proof alone makes no link", caseProofAloneMakesNoLink},
		{"another principal cannot accept the request", caseAcceptDenied},
		{"an accept makes the link of the account that proved itself", caseAccept},
		{"an accept is single use", caseAcceptOnce},
		{"an expired request cannot be accepted", caseAcceptExpired},
		{"an accept never replaces a link silently", caseNoSilentReplace},
		{"an accept that replaces a link names the principal that lost it", caseReplace},
		{"a replaced link writes a second entry under the old holder", caseReplaceEntry},
		{"an invalid entry for the old holder stops a replace", caseReplaceBadEntry},
		{"a refused accept leaves the request good", caseRefusedAcceptKeepsRequest},
		{"the fields of an account match exactly", caseExactIDs},
		{"a renamed login is the same account", caseRenamedLogin},
		{"of many accepts at once exactly one succeeds", caseConcurrentAccept},
		{"of many proofs at once exactly one succeeds", caseConcurrentProve},
		{"links of a principal are listed in order and apart from others", caseLinksOf},
		{"a delete removes the link and appends an entry", caseDelete},
		{"a delete of a link that does not exist is not found", caseDeleteMissing},
		{"a link made again has the time of the new link", caseRelinkTime},
		{"an audit entry of another scope stops the write", caseEntryScope},
		{"an accept appends exactly one entry", caseAcceptAudit},
	}
}

// The fixtures.
const (
	tokenA = "tokenAtokenAtokenAtokenAtokenAtokenAtokenAtokenA"
	tokenB = "tokenBtokenBtokenBtokenBtokenBtokenBtokenBtokenB"
	host   = "github.com"
)

func at(n time.Duration) time.Time {
	return time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC).Add(n)
}

func account(id string) forgelink.Account {
	return forgelink.Account{Kind: forge.KindGitHub, Host: host, ID: id}
}

func proof(id, login string) forgelink.Proof {
	return forgelink.Proof{Account: account(id), Login: login, DeliveryID: "delivery-" + id}
}

// entry returns a valid audit entry of the kind change in the server scope.
func entry(action string) audit.Entry {
	return audit.Entry{
		At: at(0), Namespace: audit.ServerScope, Subject: "ana", IDPSubject: "", TokenID: "", Action: action,
		Object: "@caller", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "suite", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "req-suite", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		Reason: "", PrincipalKind: "", JobID: "",
	}
}

func linkEntry() audit.Entry { return entry("caller:manage-git-host-links") }

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

func must(t *testing.T, what string, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// start stores the request of token for principal, made at the start of the
// fixtures, and returns its hash.
func start(t *testing.T, h Harness, token, holder string) string {
	t.Helper()

	r, err := forgelink.NewRequest(token, holder, at(0))
	must(t, "new request", err)
	must(t, "create request", h.Store.CreateRequest(t.Context(), r))

	return r.TokenSHA256
}

// prove records the proof of the account id on the request hash.
func prove(t *testing.T, h Harness, hash, id, login string) {
	t.Helper()

	_, err := h.Store.ProveRequest(t.Context(), hash, proof(id, login), at(proveAt))
	must(t, "prove", err)
}

// accept accepts the request hash for principal at one minute after the start.
func accept(t *testing.T, h Harness, hash, who, previous string) (forgelink.Link, string, error) {
	t.Helper()

	accepted, err := h.Store.AcceptRequest(
		t.Context(),
		acceptance(hash, who, previous, at(acceptAt)),
		linkEntry(),
	)

	return accepted.Link, accepted.Replaced, err
}

// link makes a link of the account id to principal by a full request.
func link(t *testing.T, h Harness, token, holder, id string) {
	t.Helper()

	hash := start(t, h, token, holder)
	prove(t, h, hash, id, "login-"+id)

	_, _, err := accept(t, h, hash, holder, "")
	must(t, "accept", err)
}

// acceptance returns the acceptance of the request hash by accepting at the time at.
func acceptance(hash, accepting, previous string, at time.Time) forgelink.Acceptance {
	a := forgelink.Acceptance{TokenSHA256: hash, Principal: accepting, Previous: previous, At: at, Lost: linkEntry()}
	if previous != "" {
		a.Lost.Object = principal.Ref(previous)
	}

	return a
}

func entries(t *testing.T, h Harness) int {
	t.Helper()

	return len(h.AuditEntries(t))
}
