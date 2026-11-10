package server_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/forgelinksvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forgelink"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	linkRequestsURL = "/api/dustpan/v1/me/linked-account-requests"
	linkShowURL     = linkRequestsURL + "/show"
	linksURL        = "/api/dustpan/v1/me/linked-accounts"
	ghAccountPath   = "/github/github.com/"
)

// startedDoc, linkRequestDoc, linkDoc, acceptedDoc and linkListDoc are the
// answers of the routes of the links.
type startedDoc struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

type provedAccountDoc struct {
	Kind  string `json:"kind"`
	Host  string `json:"host"`
	ID    string `json:"id"`
	Login string `json:"login"`
}

type currentLinkDoc struct {
	Principal string `json:"principal"`
}

type linkRequestDoc struct {
	Proved      bool              `json:"proved"`
	Account     *provedAccountDoc `json:"account"`
	CurrentLink *currentLinkDoc   `json:"currentLink"`
}

type linkDoc struct {
	Account   provedAccountDoc `json:"account"`
	Principal string           `json:"principal"`
}

type acceptedDoc struct {
	Link              linkDoc `json:"link"`
	ReplacedPrincipal *string `json:"replacedPrincipal"`
}

type linkListDoc struct {
	Links []linkDoc `json:"links"`
}

type linkErrorDoc struct {
	ErrorCode string `json:"errorCode"`
}

// newLinkFixture returns a fixture with the routes of the links mounted.
func newLinkFixture(t *testing.T) accessFixture {
	t.Helper()

	return newAccessFixtureLinks(t, func(st *sqlite.Store) auditStore { return st }, gitHostLinksOf)
}

// asPerson sends one request as the console session of subject.
func (f accessFixture) asPerson(t *testing.T, subject string, c apiCall, out any) {
	t.Helper()

	f.asSession(t, subject, f.sessions.Now(), c, out)
}

// startLink starts a link request as subject and returns its token.
func (f accessFixture) startLink(t *testing.T, subject string) string {
	t.Helper()

	var started startedDoc
	f.asPerson(t, subject, apiCall{http.MethodPost, linkRequestsURL, "", http.StatusCreated}, &started)

	if len(started.Token) < forgelink.MinTokenLen {
		t.Fatalf("token = %q, want at least %d characters", started.Token, forgelink.MinTokenLen)
	}

	return started.Token
}

// proveLink records the proof of the account id on the request of token, as the
// intake of the signed events of a Git host does.
func (f accessFixture) proveLink(t *testing.T, token, id, login string) {
	t.Helper()

	p := forgelink.Proof{
		Account: forgelink.Account{Kind: forge.KindGitHub, Host: "github.com", ID: id}, Login: login,
		DeliveryID: "delivery-" + id,
	}

	svc := forgelinksvc.NewService(f.store.ForgeLinks(), f.store, nil, nil).WithIntake()
	if _, err := svc.Prove(t.Context(), token, p); err != nil {
		t.Fatalf("prove: %v", err)
	}
}

// linkAlice makes the link of the account id for alice through the routes and
// the proof.
func (f accessFixture) linkAlice(t *testing.T, id string) {
	t.Helper()

	token := f.startLink(t, "alice")
	f.proveLink(t, token, id, "login-"+id)
	f.asPerson(t, "alice", apiCall{http.MethodPost, linksURL, `{"token":"` + token + `"}`, http.StatusCreated}, nil)
}

// staleLinkSession starts a console session of subject whose sign-in is one
// minute older than the token freshness window. The session is active: a read
// works. It returns the cookie and the CSRF value of the session.
func (f accessFixture) staleLinkSession(t *testing.T, subject string) (*http.Cookie, string) {
	t.Helper()

	cookie, csrf := f.sessionOf(t, subject, f.sessions.Now().Add(-session.DefaultTokenFreshness-time.Minute))

	f.asSessionCookie(t, cookie, csrf, apiCall{http.MethodGet, linksURL, "", http.StatusOK}, nil)

	return cookie, csrf
}

func TestGitHostLinkFlow(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)
	token := f.startLink(t, "alice")

	var shown linkRequestDoc
	f.asPerson(t, "alice", apiCall{http.MethodPost, linkShowURL, `{"token":"` + token + `"}`, http.StatusOK}, &shown)

	if shown.Proved || shown.Account != nil {
		t.Fatalf("show before the proof = %+v, want an open request", shown)
	}

	f.proveLink(t, token, "4711", "alice-gh")

	f.asPerson(t, "alice", apiCall{http.MethodPost, linkShowURL, `{"token":"` + token + `"}`, http.StatusOK}, &shown)

	if !shown.Proved || shown.Account == nil || shown.Account.ID != "4711" || shown.Account.Login != "alice-gh" ||
		shown.CurrentLink != nil {
		t.Fatalf("show after the proof = %+v", shown)
	}

	var accepted acceptedDoc
	f.asPerson(
		t,
		"alice",
		apiCall{http.MethodPost, linksURL, `{"token":"` + token + `"}`, http.StatusCreated},
		&accepted,
	)

	if accepted.Link.Account.ID != "4711" || accepted.Link.Principal != "prn-alice" ||
		accepted.ReplacedPrincipal != nil {
		t.Fatalf("accepted = %+v", accepted)
	}

	// The token is good once.
	var failure linkErrorDoc
	f.asPerson(
		t,
		"alice",
		apiCall{http.MethodPost, linksURL, `{"token":"` + token + `"}`, http.StatusConflict},
		&failure,
	)

	if failure.ErrorCode != "git-host.link-request-used" {
		t.Fatalf("second accept code = %q", failure.ErrorCode)
	}
}

// TestGitHostLinkListAndDelete proves that a person lists and deletes only its
// own links.
func TestGitHostLinkListAndDelete(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)
	f.linkAlice(t, "4711")

	var mine, others linkListDoc
	f.asPerson(t, "alice", apiCall{http.MethodGet, linksURL, "", http.StatusOK}, &mine)
	f.asPerson(t, "bob", apiCall{http.MethodGet, linksURL, "", http.StatusOK}, &others)

	if len(mine.Links) != 1 || len(others.Links) != 0 {
		t.Fatalf("links of alice %+v and of bob %+v", mine, others)
	}

	// Bob cannot delete the link of alice. Alice can, once.
	del := linksURL + ghAccountPath + "4711"
	f.asPerson(t, "bob", apiCall{http.MethodDelete, del, "", http.StatusNotFound}, nil)
	f.asPerson(t, "alice", apiCall{http.MethodDelete, del, "", http.StatusNoContent}, nil)
	f.asPerson(t, "alice", apiCall{http.MethodDelete, del, "", http.StatusNotFound}, nil)
}

// TestGitHostLinkNeedsBothSides proves that neither the person nor the account
// makes a link alone.
func TestGitHostLinkNeedsBothSides(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)
	token := f.startLink(t, "alice")

	var failure linkErrorDoc
	f.asPerson(
		t,
		"alice",
		apiCall{http.MethodPost, linksURL, `{"token":"` + token + `"}`, http.StatusConflict},
		&failure,
	)

	if failure.ErrorCode != "git-host.link-not-proved" {
		t.Fatalf("accept without a proof code = %q", failure.ErrorCode)
	}

	// A proof alone, for a request that nobody accepted, makes no link.
	other := f.startLink(t, "bob")
	f.proveLink(t, other, "99", "mallory")

	var list linkListDoc
	f.asPerson(t, "bob", apiCall{http.MethodGet, linksURL, "", http.StatusOK}, &list)

	if len(list.Links) != 0 {
		t.Fatalf("links after a proof alone = %+v, want none", list.Links)
	}

	// The body of the accept names no account: a field for it is refused.
	f.proveLink(t, token, "4711", "alice-gh")
	f.asPerson(t, "alice", apiCall{
		http.MethodPost, linksURL, `{"token":"` + token + `","accountID":"1"}`, http.StatusBadRequest,
	}, &failure)

	if failure.ErrorCode != "git-host.link-body-invalid" {
		t.Fatalf("code = %q", failure.ErrorCode)
	}

	// The person who did not start the request cannot show or accept it.
	f.asPerson(
		t,
		"bob",
		apiCall{http.MethodPost, linkShowURL, `{"token":"` + token + `"}`, http.StatusNotFound},
		&failure,
	)
	f.asPerson(t, "bob", apiCall{http.MethodPost, linksURL, `{"token":"` + token + `"}`, http.StatusNotFound}, &failure)

	if failure.ErrorCode != "git-host.link-request-not-found" {
		t.Fatalf("code = %q", failure.ErrorCode)
	}
}

// TestGitHostLinkChangesNeedASession proves that an access token starts, shows,
// accepts and deletes nothing, and that it reads the list. The accept needs a
// fresh sign-in, so an access token gets the fresh-step code there.
func TestGitHostLinkChangesNeedASession(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)
	f.linkAlice(t, "4711")

	tests := []struct {
		call apiCall
		code string
	}{
		{apiCall{http.MethodPost, linkRequestsURL, "", http.StatusForbidden}, "git-host.link-person-required"},
		{
			apiCall{
				http.MethodPost, linkShowURL,
				`{"token":"` + strings.Repeat("a", forgelink.MinTokenLen) + `"}`, http.StatusForbidden,
			},
			"git-host.link-person-required",
		},
		{
			apiCall{
				http.MethodPost, linksURL,
				`{"token":"` + strings.Repeat("a", forgelink.MinTokenLen) + `"}`, http.StatusForbidden,
			},
			freshErrorCode,
		},
		{
			apiCall{http.MethodDelete, linksURL + ghAccountPath + "4711", "", http.StatusForbidden},
			"git-host.link-person-required",
		},
	}

	for _, tc := range tests {
		var failure linkErrorDoc

		f.as(t, "alice", tc.call, &failure)

		if failure.ErrorCode != tc.code {
			t.Errorf("%s %s code = %q, want %q", tc.call.method, tc.call.path, failure.ErrorCode, tc.code)
		}
	}

	var list linkListDoc
	f.as(t, "alice", apiCall{http.MethodGet, linksURL, "", http.StatusOK}, &list)

	if len(list.Links) != 1 {
		t.Fatalf("links by token = %+v, want the link of alice", list.Links)
	}

	// The refusals did not delete the link.
	f.asPerson(t, "alice", apiCall{http.MethodGet, linksURL, "", http.StatusOK}, &list)

	if len(list.Links) != 1 {
		t.Fatalf("links after the refusals = %+v", list.Links)
	}
}

// linkAcceptCase is one row of TestGitHostLinkAcceptNeedsAFreshSignIn: the
// state of the accept and the links that each person holds after it.
type linkAcceptCase struct {
	name string
	// replace makes the accept take over the link of alice, so that the
	// refusal must keep that link.
	replace bool
	// stale accepts from a session whose sign-in is older than the window.
	stale bool
	want  int
	code  string
	// alice and bob are the counts of the links of each person after.
	alice, bob int
}

// checkLinkAcceptFreshness runs one row of TestGitHostLinkAcceptNeedsAFreshSignIn.
func checkLinkAcceptFreshness(t *testing.T, tc linkAcceptCase) {
	t.Helper()

	f := newLinkFixture(t)

	if tc.replace {
		f.linkAlice(t, "4711")
	}

	token := f.startLink(t, "bob")
	f.proveLink(t, token, "4711", "alice-gh")

	body := `{"token":"` + token + `"}`
	if tc.replace {
		body = `{"token":"` + token + `","previousPrincipal":"prn-alice"}`
	}

	call := apiCall{http.MethodPost, linksURL, body, tc.want}

	if tc.stale {
		cookie, csrf := f.staleLinkSession(t, "bob")

		var failure linkErrorDoc
		f.asSessionCookie(t, cookie, csrf, call, &failure)

		if failure.ErrorCode != tc.code {
			t.Fatalf("code = %q, want %q", failure.ErrorCode, tc.code)
		}
	} else {
		var accepted acceptedDoc
		f.asPerson(t, "bob", call, &accepted)

		if accepted.Link.Principal != "prn-bob" || accepted.ReplacedPrincipal != nil {
			t.Fatalf("accepted = %+v, want the link of bob", accepted)
		}
	}

	for _, holder := range []struct {
		subject string
		want    int
	}{{"alice", tc.alice}, {"bob", tc.bob}} {
		var list linkListDoc
		f.asPerson(t, holder.subject, apiCall{http.MethodGet, linksURL, "", http.StatusOK}, &list)

		if len(list.Links) != holder.want {
			t.Fatalf("links of %s = %+v, want %d", holder.subject, list.Links, holder.want)
		}
	}

	if tc.stale {
		if got := countLinkEntries(serverLinkEntries(t, f), "failure", "@caller"); got != 1 {
			t.Fatalf("refusal entries = %d, want 1", got)
		}
	}
}

// TestGitHostLinkAcceptNeedsAFreshSignIn proves that the accept of a link
// needs a sign-in inside the token freshness window: a session that is older
// than the window is refused with the fresh-step code, no link is stored, and
// the refusal is recorded. A session that signed in inside the window accepts.
func TestGitHostLinkAcceptNeedsAFreshSignIn(t *testing.T) {
	t.Parallel()

	for _, tc := range []linkAcceptCase{
		{"stale session", false, true, http.StatusUnauthorized, freshErrorCode, 0, 0},
		{"stale session takes over no link", true, true, http.StatusUnauthorized, freshErrorCode, 1, 0},
		{"fresh session", false, false, http.StatusCreated, "", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkLinkAcceptFreshness(t, tc)
		})
	}
}

// TestGitHostLinkAcceptRefusesANonSessionCaller proves that the accept stays
// refused for a caller that is not a console session: an access token of a
// person, and an access token of a service principal that the server made.
// The fresh check refuses both before the route reaches its session check.
func TestGitHostLinkAcceptRefusesANonSessionCaller(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		service bool
	}{
		{"access token of a person", "alice", false},
		{"access token of a service principal", "ci-deploy", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newLinkFixture(t)

			if tc.service {
				f.seedService(t, tc.subject)
			}

			token := f.startLink(t, "alice")
			f.proveLink(t, token, "4711", "alice-gh")

			var failure linkErrorDoc
			f.as(t, tc.subject, apiCall{
				http.MethodPost, linksURL, `{"token":"` + token + `"}`, http.StatusForbidden,
			}, &failure)

			if failure.ErrorCode != freshErrorCode {
				t.Fatalf("code = %q, want %q", failure.ErrorCode, freshErrorCode)
			}

			if got := countLinkEntries(serverLinkEntries(t, f), "failure", "@caller"); got != 1 {
				t.Fatalf("refusal entries = %d, want 1", got)
			}

			var list linkListDoc
			f.asPerson(t, "alice", apiCall{http.MethodGet, linksURL, "", http.StatusOK}, &list)

			if len(list.Links) != 0 {
				t.Fatalf("links after the refusal = %+v, want none", list.Links)
			}
		})
	}
}

// staleAcceptRefusal is the body of the refusal of a stale accept.
type staleAcceptRefusal struct {
	Message        string `json:"message"`
	ErrorCode      string `json:"errorCode"`
	Reauthenticate string `json:"reauthenticate"`
}

// checkStaleAcceptRefusal fails when the refusal does not name the fresh
// sign-in step, or when it names one of the parts of the link.
func checkStaleAcceptRefusal(t *testing.T, refusal staleAcceptRefusal, named []string) {
	t.Helper()

	if refusal.ErrorCode != freshErrorCode {
		t.Fatalf("code = %q, want %q", refusal.ErrorCode, freshErrorCode)
	}

	if !strings.Contains(refusal.Message, "sign in again") ||
		!strings.HasPrefix(refusal.Reauthenticate, "/api/server/sign-in/") {
		t.Fatalf("the refusal does not name the fresh sign-in step: %s %s", refusal.Message, refusal.Reauthenticate)
	}

	for _, part := range named {
		if strings.Contains(refusal.Message, part) || strings.Contains(refusal.Reauthenticate, part) {
			t.Errorf("the refusal names %q: %s %s", part, refusal.Message, refusal.Reauthenticate)
		}
	}
}

// checkStaleAcceptEntries fails when a failure entry of the links names one of
// the parts of the link, and returns the count of the failure entries.
func checkStaleAcceptEntries(t *testing.T, entries, named []string) int {
	t.Helper()

	refusals := 0

	for _, entry := range entries {
		if !strings.Contains(entry, "failure") {
			continue
		}

		refusals++

		for _, part := range named {
			if strings.Contains(entry, part) {
				t.Errorf("the refusal entry names %q: %s", part, entry)
			}
		}
	}

	return refusals
}

// TestGitHostLinkStaleAcceptNamesNoAccount proves that the refusal of a stale
// accept and its audit entry name the fresh sign-in step only: no account, no
// host, no login and no previous principal.
func TestGitHostLinkStaleAcceptNamesNoAccount(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)
	f.linkAlice(t, "4711")

	token := f.startLink(t, "bob")
	f.proveLink(t, token, "4711", "alice-gh")

	cookie, csrf := f.staleLinkSession(t, "bob")

	var refusal staleAcceptRefusal
	f.asSessionCookie(t, cookie, csrf, apiCall{
		http.MethodPost, linksURL, `{"token":"` + token + `","previousPrincipal":"prn-alice"}`, http.StatusUnauthorized,
	}, &refusal)

	named := []string{"4711", "github.com", "alice-gh", "prn-alice", token}
	checkStaleAcceptRefusal(t, refusal, named)

	entries := serverLinkEntries(t, f)

	if got := checkStaleAcceptEntries(t, entries, named); got != 1 {
		t.Fatalf("refusal entries = %d, want 1: %q", got, entries)
	}
}

// TestGitHostLinkNoSilentReplace proves that a second link never replaces the
// first without the principal that the person saw.
func TestGitHostLinkNoSilentReplace(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)
	f.linkAlice(t, "4711")

	token := f.startLink(t, "bob")
	f.proveLink(t, token, "4711", "alice-gh")

	var shown linkRequestDoc
	f.asPerson(t, "bob", apiCall{http.MethodPost, linkShowURL, `{"token":"` + token + `"}`, http.StatusOK}, &shown)

	if shown.CurrentLink == nil || shown.CurrentLink.Principal != "prn-alice" {
		t.Fatalf("show = %+v, want the current link of alice", shown)
	}

	var failure linkErrorDoc
	for _, body := range []string{
		`{"token":"` + token + `"}`,
		`{"token":"` + token + `","previousPrincipal":"prn-bob"}`,
	} {
		f.asPerson(t, "bob", apiCall{http.MethodPost, linksURL, body, http.StatusConflict}, &failure)

		if failure.ErrorCode != "git-host.link-conflict" {
			t.Fatalf("code = %q for %s", failure.ErrorCode, body)
		}
	}

	var list linkListDoc
	f.asPerson(t, "alice", apiCall{http.MethodGet, linksURL, "", http.StatusOK}, &list)

	if len(list.Links) != 1 || list.Links[0].Principal != "prn-alice" {
		t.Fatalf("links of alice = %+v, want the link kept", list.Links)
	}

	var accepted acceptedDoc
	f.asPerson(t, "bob", apiCall{
		http.MethodPost, linksURL, `{"token":"` + token + `","previousPrincipal":"prn-alice"}`, http.StatusCreated,
	}, &accepted)

	if accepted.ReplacedPrincipal == nil || *accepted.ReplacedPrincipal != "prn-alice" ||
		accepted.Link.Principal != "prn-bob" {
		t.Fatalf("accepted = %+v", accepted)
	}
}

// TestGitHostLinkIDsMatchExactly proves that the account in a path matches by
// the exact kind, host and ID, and that a login names nothing.
func TestGitHostLinkIDsMatchExactly(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)
	f.linkAlice(t, "AbC")

	for _, c := range []apiCall{
		{http.MethodDelete, linksURL + ghAccountPath + "abc", "", http.StatusNotFound},
		{http.MethodDelete, linksURL + ghAccountPath + "login-AbC", "", http.StatusNotFound},
		{http.MethodDelete, linksURL + "/github/ghe.example.com/AbC", "", http.StatusNotFound},
		{http.MethodDelete, linksURL + "/gitlab/github.com/AbC", "", http.StatusNotFound},
		{http.MethodDelete, linksURL + "/github/GitHub.com/AbC", "", http.StatusBadRequest},
		{http.MethodDelete, linksURL + "/svn/github.com/AbC", "", http.StatusBadRequest},
	} {
		f.asPerson(t, "alice", c, nil)
	}

	var list linkListDoc
	f.asPerson(t, "alice", apiCall{http.MethodGet, linksURL, "", http.StatusOK}, &list)

	if len(list.Links) != 1 {
		t.Fatalf("links = %+v, want the link kept", list.Links)
	}

	f.asPerson(t, "alice", apiCall{http.MethodDelete, linksURL + ghAccountPath + "AbC", "", http.StatusNoContent}, nil)
}

func TestGitHostLinkBodies(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)
	good := `"token":"` + strings.Repeat("a", forgelink.MinTokenLen) + `"`

	tests := []struct {
		name string
		body string
		want int
		code string
	}{
		{"unknown field", `{` + good + `,"x":1}`, http.StatusBadRequest, "git-host.link-body-invalid"},
		{"not JSON", `token`, http.StatusBadRequest, "git-host.link-body-invalid"},
		{"empty body", ``, http.StatusBadRequest, "git-host.link-body-invalid"},
		{"two values", `{` + good + `}{}`, http.StatusBadRequest, "git-host.link-body-invalid"},
		{
			"too large",
			`{"token":"` + strings.Repeat("a", 5000) + `"}`,
			http.StatusRequestEntityTooLarge,
			"git-host.link-body-too-large",
		},
		{"short token", `{"token":"short"}`, http.StatusNotFound, "git-host.link-request-not-found"},
		{"unknown token", `{` + good + `}`, http.StatusNotFound, "git-host.link-request-not-found"},
	}

	for _, tc := range tests {
		var failure linkErrorDoc
		f.asPerson(t, "alice", apiCall{http.MethodPost, linkShowURL, tc.body, tc.want}, &failure)

		if failure.ErrorCode != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, failure.ErrorCode, tc.code)
		}
	}
}

func TestGitHostLinkRequestLimit(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)

	for range forgelink.MaxOpenRequests {
		f.startLink(t, "alice")
	}

	var failure linkErrorDoc
	f.asPerson(t, "alice", apiCall{http.MethodPost, linkRequestsURL, "", http.StatusTooManyRequests}, &failure)

	if failure.ErrorCode != "git-host.link-limit" {
		t.Fatalf("code = %q", failure.ErrorCode)
	}

	f.startLink(t, "bob")
}

// TestGitHostLinkAdministratorDelete proves that an administrator deletes the
// link of a principal, and only that principal's link.
func TestGitHostLinkAdministratorDelete(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)
	f.linkAlice(t, "4711")

	path := func(principal string) string {
		return "/api/dustpan/v1/principals/" + principal + "/linked-accounts/github/github.com/4711"
	}

	f.as(t, "bob", apiCall{http.MethodDelete, path("prn-alice"), "", http.StatusForbidden}, nil)
	f.as(t, "admin", apiCall{http.MethodDelete, path("prn-bob"), "", http.StatusNotFound}, nil)
	f.as(t, "admin", apiCall{http.MethodDelete, path("prn-alice"), "", http.StatusNoContent}, nil)
	f.as(t, "admin", apiCall{http.MethodDelete, path("prn-alice"), "", http.StatusNotFound}, nil)

	var list linkListDoc
	f.asPerson(t, "alice", apiCall{http.MethodGet, linksURL, "", http.StatusOK}, &list)

	if len(list.Links) != 0 {
		t.Fatalf("links after the delete = %+v", list.Links)
	}
}

// serverLinkEntries returns the audit entries of the links in the server scope,
// as text with the outcome in front.
func serverLinkEntries(t *testing.T, f accessFixture) []string {
	t.Helper()

	var out []string

	for page := 1; ; page++ {
		listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: page, Size: audit.MaxPageSize})
		if err != nil {
			t.Fatalf("list audit: %v", err)
		}

		for i := range listing.Entries {
			e := &listing.Entries[i]
			if e.Action == "caller:manage-git-host-links" {
				out = append(out, string(e.Outcome)+" "+e.Object+" "+audit.EncodeChanges(e.Changes))
			}
		}

		if len(listing.Entries) < audit.MaxPageSize {
			return out
		}
	}
}

// countLinkEntries counts the entries that contain every one of the parts.
func countLinkEntries(entries []string, parts ...string) int {
	n := 0

	for _, e := range entries {
		if !slices.ContainsFunc(parts, func(p string) bool { return !strings.Contains(e, p) }) {
			n++
		}
	}

	return n
}

// TestGitHostLinkAuditEntries proves that a link, a delete and a refusal each
// write an entry that names the account and never a token.
func TestGitHostLinkAuditEntries(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)
	token := f.startLink(t, "alice")
	f.proveLink(t, token, "4711", "alice-gh")
	f.asPerson(t, "alice", apiCall{http.MethodPost, linksURL, `{"token":"` + token + `"}`, http.StatusCreated}, nil)
	f.as(t, "alice", apiCall{http.MethodDelete, linksURL + ghAccountPath + "4711", "", http.StatusForbidden}, nil)
	f.asPerson(t, "alice", apiCall{http.MethodDelete, linksURL + ghAccountPath + "4711", "", http.StatusNoContent}, nil)

	entries := serverLinkEntries(t, f)

	if n := countLinkEntries(entries, token); n != 0 {
		t.Fatalf("%d entries hold the token", n)
	}

	made := countLinkEntries(
		entries,
		"success",
		`"key":"forgeLink.change","new":"linked"`,
		`"new":"4711"`,
		`"new":"prn-alice"`,
	)
	deleted := countLinkEntries(entries, "success", `"key":"forgeLink.change","new":"deleted"`, `"new":"4711"`)
	denied := countLinkEntries(entries, "denied")

	if made != 1 || deleted != 1 || denied != 1 {
		t.Fatalf("entries: %d made, %d deleted, %d denied, want 1 each: %q", made, deleted, denied, entries)
	}
}

// TestGitHostLinkRoutesCheckTheSessionFirst proves the check of the route, which
// the service does not replace: a request with an access token is refused before
// the route reads its body or its path. A bad body or a bad account gives 403 and
// not 400, because the service never sees them. The accept answers the code of
// the fresh check, which runs before the route too.
func TestGitHostLinkRoutesCheckTheSessionFirst(t *testing.T) {
	t.Parallel()

	f := newLinkFixture(t)

	tests := []struct {
		call apiCall
		code string
	}{
		{apiCall{http.MethodPost, linkShowURL, `not json`, http.StatusForbidden}, "git-host.link-person-required"},
		{apiCall{http.MethodPost, linksURL, `{"unknown":1}`, http.StatusForbidden}, freshErrorCode},
		{
			apiCall{http.MethodDelete, linksURL + "/svn/GitHub.com/x", "", http.StatusForbidden},
			"git-host.link-person-required",
		},
	}

	for _, tc := range tests {
		var failure linkErrorDoc

		f.as(t, "alice", tc.call, &failure)

		if failure.ErrorCode != tc.code {
			t.Errorf("%s %s code = %q, want %q", tc.call.method, tc.call.path, failure.ErrorCode, tc.code)
		}
	}
}

// TestGitHostLinkRequestRoutesWaitForTheIntake proves that the routes that start,
// show and accept a request are not mounted for a service with no intake, and
// that the list and the deletes are.
func TestGitHostLinkRequestRoutesWaitForTheIntake(t *testing.T) {
	t.Parallel()

	f := newAccessFixtureLinks(
		t,
		func(st *sqlite.Store) auditStore { return st },
		func(st *sqlite.Store) *forgelinksvc.Service {
			return forgelinksvc.NewService(st.ForgeLinks(), st, nil, nil)
		},
	)

	for _, c := range []apiCall{
		{http.MethodPost, linkRequestsURL, "", http.StatusNotFound},
		{http.MethodPost, linkShowURL, `{}`, http.StatusNotFound},
		{http.MethodPost, linksURL, `{}`, http.StatusMethodNotAllowed},
		{http.MethodGet, linksURL, "", http.StatusOK},
		{http.MethodDelete, linksURL + ghAccountPath + "1", "", http.StatusNotFound},
	} {
		f.asPerson(t, "alice", c, nil)
	}
}
