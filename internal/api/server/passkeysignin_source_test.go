package server_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/mfa"
)

// floodBegins sends count begins from the peer address remote and returns the
// answer of the last one.
func (e *passkeySignInEnv) floodBegins(t *testing.T, remote string, count int, header http.Header) int {
	t.Helper()

	e.remote = remote
	defer func() { e.remote = "" }()

	last := 0

	for range count {
		last = e.post(t, signInBeginTarget, "", header).Code
	}

	return last
}

// sourceRefusals counts the audit entries of a refused ceremony start.
func sourceRefusals(entries []audit.Entry) int {
	n := 0

	for i := range entries {
		if entries[i].Action == "passkey.ceremony-refused" && entries[i].Outcome == audit.OutcomeDenied {
			n++
		}
	}

	return n
}

// fillSource sends the most begins that one source can hold from the peer
// address remote and fails unless each one answers 200.
func (e *passkeySignInEnv) fillSource(t *testing.T, remote string) {
	t.Helper()

	if got := e.floodBegins(t, remote, mfa.MaxOpenSignInChallengesPerSource, nil); got != http.StatusOK {
		t.Fatalf("last begin under the count = %d, want 200", got)
	}
}

// TestPasskeySignInSourceChallengeCap checks the count of open sign-in
// challenges of one source. A source that holds the most of them is refused
// with 429 and the code of the cap, and the refusal writes an audit entry.
func TestPasskeySignInSourceChallengeCap(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	const flood = "198.51.100.7:4000"

	e.fillSource(t, flood)

	before := sourceRefusals(serverEntries(t, e.f.store))

	e.remote = flood
	over := e.post(t, signInBeginTarget, "", nil)
	e.remote = ""

	want(t, "begin over the count of a source", over, http.StatusTooManyRequests, errcode.MFASignInSourceLimit)

	if after := sourceRefusals(serverEntries(t, e.f.store)); after != before+1 {
		t.Errorf("audit entries of a refusal = %d, want %d: the refusal must write one", after, before+1)
	}

	if got := e.floodBegins(t, "198.51.100.8:4000", 1, nil); got != http.StatusOK {
		t.Errorf("begin of another source = %d, want 200", got)
	}
}

// TestPasskeySignInSourceIgnoresForwardingHeaders checks that the cap counts
// the address that the trusted-proxy rules resolve. Defense in depth: a header
// from a peer that is not a trusted proxy, with another address, changes
// nothing.
func TestPasskeySignInSourceIgnoresForwardingHeaders(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	const flood = "198.51.100.7:4000"

	e.fillSource(t, flood)

	header := http.Header{"X-Forwarded-For": {"203.0.113.50"}, "X-Real-Ip": {"203.0.113.51"}}
	if got := e.floodBegins(t, flood, 1, header); got != http.StatusTooManyRequests {
		t.Errorf("begin with a forged header = %d, want 429", got)
	}
}

// TestPasskeySignInSourceIPv6Range checks that an IPv6 address counts as its
// /64 range: another address of the range is refused, and the next range works.
func TestPasskeySignInSourceIPv6Range(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	e.fillSource(t, "[2001:db8:0:1::1]:4000")

	if got := e.floodBegins(t, "[2001:db8:0:1:ffff::2]:4000", 1, nil); got != http.StatusTooManyRequests {
		t.Errorf("begin of another address of the range = %d, want 429", got)
	}

	if got := e.floodBegins(t, "[2001:db8:0:2::1]:4000", 1, nil); got != http.StatusOK {
		t.Errorf("begin of the next range = %d, want 200", got)
	}
}

// TestPasskeySignInSourceNoAddress checks that requests with no valid address
// share one count and are never free: the cap fails closed.
func TestPasskeySignInSourceNoAddress(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	e.fillSource(t, "not-an-address")

	if got := e.floodBegins(t, "also-not-an-address", 1, nil); got != http.StatusTooManyRequests {
		t.Errorf("begin with no valid address over the count = %d, want 429", got)
	}
}

// TestPasskeySignInSourceFloodKeepsBreakGlass checks that one source that
// fills its count does not stop the sign-in of a person from another source,
// the sign-in of a break-glass account included, and that the flood does not
// reach the installation total.
func TestPasskeySignInSourceFloodKeepsBreakGlass(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	// A flood from one source: far more starts than its count.
	e.floodBegins(t, "198.51.100.7:4000", 5*mfa.MaxOpenSignInChallengesPerSource, nil)

	e.remote = "198.51.100.7:4000"
	want(t, "begin of the flooding source", e.post(t, signInBeginTarget, "", nil),
		http.StatusTooManyRequests, errcode.MFASignInSourceLimit)

	e.remote = "192.0.2.99:5000"

	signedIn(t, "local user from another source", e.signIn(t, e.lena, "", nil, nil))
	signedIn(t, "break-glass sign-in from another source", e.signIn(t, e.ana, breakGlassReasonText, nil, nil))
}
