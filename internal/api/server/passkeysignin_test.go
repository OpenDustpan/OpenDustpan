package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/webauthntest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/mfa/gowebauthn"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// The routes of the passkey sign-in.
const (
	signInBeginTarget  = "/api/dustpan/v1/sign-in/passkey/begin"
	signInFinishTarget = "/api/dustpan/v1/sign-in/passkey/finish"
	evilOrigin         = "https://evil.example"
)

// passkeySignInEnv is a handler with the passkey sign-in, two people with a
// passkey each (a local user and a break-glass account) and the budgets of the
// source address.
type passkeySignInEnv struct {
	f        accessFixture
	svc      *mfasvc.Service
	begins   *fakeLimiter
	failures *fakeLimiter
	auth     *webauthntest.Authenticator
	lena     *webauthntest.Credential
	ana      *webauthntest.Credential
}

// newPasskeyLimiter returns a budget that refuses nothing and counts.
func newPasskeyLimiter() *fakeLimiter {
	return &fakeLimiter{mu: sync.Mutex{}, failures: 0, refuse: false}
}

// newPasskeySignInEnv builds the handler and registers a passkey for the local
// user prn-lena and for the break-glass account prn-ana.
func newPasskeySignInEnv(t *testing.T) *passkeySignInEnv {
	t.Helper()

	e := new(passkeySignInEnv)
	e.begins, e.failures = newPasskeyLimiter(), newPasskeyLimiter()
	e.auth = webauthntest.New(relyingID, relyingOrigin)

	e.f = newAccessFixtureSignIn(t, func(st *sqlite.Store) auditStore { return st }, nil,
		func(in *apiserver.SignIn, st *sqlite.Store) {
			e.svc = newRoutePasskeys(t, st)
			e.svc.WatchBreakGlass(st, st)
			in.Passkeys, in.PasskeySignIn = e.svc, e.svc
			in.PasskeyBeginLimiter, in.PasskeyLimiter = e.begins, e.failures
		},
	)

	e.seedPerson(t, "prn-lena", "local:lena", false)
	e.seedPerson(t, "prn-ana", "break-glass:ana", true)
	e.lena = e.register(t, "prn-lena")
	e.ana = e.register(t, "prn-ana")

	return e
}

// seedPerson stores the person id with the subject.
func (e *passkeySignInEnv) seedPerson(t *testing.T, id, subject string, breakGlass bool) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: id, Subject: subject, DisplayName: id,
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
		BreakGlass: breakGlass,
	}

	entry := audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: "test", IDPSubject: "", TokenID: "",
		Action: "server:manage-principals", Object: principal.Ref(id), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	if err := e.f.store.CreatePrincipal(t.Context(), p, entry); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// register adds a passkey for the person id through the service, as the
// register ceremony of that person does, and returns its credential.
func (e *passkeySignInEnv) register(t *testing.T, id string) *webauthntest.Credential {
	t.Helper()

	entry := audit.Entry{
		At: time.Time{}, Namespace: "", Subject: id, IDPSubject: "", TokenID: "", Action: "caller:manage-sign-in",
		Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "test", Channel: audit.ChannelConsole, Kind: "", Outcome: "", RequestID: "", ObjectID: "",
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
	actor := mfasvc.Actor{Entry: entry, PrincipalID: id, SessionID: "ses-" + id}
	person := mfa.Person{ID: id, Name: id, DisplayName: id}

	options, err := e.svc.BeginRegistration(t.Context(), actor, person)
	if err != nil {
		t.Fatalf("begin the registration of %s: %v", id, err)
	}

	challenge, err := webauthntest.ChallengeOf(options)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}

	cred, err := webauthntest.NewCredential([]byte(id), false)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}

	response, err := e.auth.Register(cred, webauthntest.For(challenge))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := e.svc.FinishRegistration(t.Context(), actor, person, "key", response); err != nil {
		t.Fatalf("finish the registration of %s: %v", id, err)
	}

	return cred
}

// post sends a public POST with the extra headers.
func (e *passkeySignInEnv) post(t *testing.T, target, body string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
	maps.Copy(req.Header, header)

	rec := httptest.NewRecorder()
	e.f.handler.ServeHTTP(rec, req)

	return rec
}

// begin starts a sign-in ceremony and returns its challenge.
func (e *passkeySignInEnv) begin(t *testing.T) []byte {
	t.Helper()

	rec := e.post(t, signInBeginTarget, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("begin: status %d, body %s, want 200", rec.Code, rec.Body)
	}

	if strings.Contains(rec.Body.String(), "allowCredentials\":[{") {
		t.Fatalf("begin: the options name a credential: %s", rec.Body)
	}

	var doc struct {
		PublicKey json.RawMessage `json:"publicKey"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode the options: %v", err)
	}

	challenge, err := webauthntest.ChallengeOf(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("challenge of %s: %v", rec.Body, err)
	}

	return challenge
}

// finishBody returns the body of the end of a sign-in with the response and
// the reason.
func signInFinishBody(t *testing.T, response []byte, reason string) string {
	t.Helper()

	b, err := json.Marshal(map[string]any{"response": json.RawMessage(response), "reason": reason})
	if err != nil {
		t.Fatalf("encode the finish body: %v", err)
	}

	return string(b)
}

// signIn runs a whole ceremony of cred with the ceremony changes in change,
// and returns the answer of the finish.
func (e *passkeySignInEnv) signIn(
	t *testing.T, cred *webauthntest.Credential, reason string, header http.Header,
	change func(c webauthntest.Ceremony) webauthntest.Ceremony,
) *httptest.ResponseRecorder {
	t.Helper()

	c := webauthntest.For(e.begin(t))
	if change != nil {
		c = change(c)
	}

	response, err := e.auth.Assert(cred, c)
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	return e.post(t, signInFinishTarget, signInFinishBody(t, response, reason), header)
}

// want fails unless rec has the status and, for an error, the error code.
func want(t *testing.T, what string, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("%s: status %d, body %s, want %d", what, rec.Code, rec.Body, status)
	}

	if code == "" {
		return
	}

	var doc passkeyErrorDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.ErrorCode != code {
		t.Fatalf("%s: body %s, want the error code %q", what, rec.Body, code)
	}
}

// signedIn fails unless rec is a good sign-in with a session cookie.
func signedIn(t *testing.T, what string, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	want(t, what, rec, http.StatusNoContent, "")

	for _, c := range rec.Result().Cookies() {
		if c.Name == apiserver.SessionCookie && c.Value != "" {
			return c
		}
	}

	t.Fatalf("%s: no session cookie in %v", what, rec.Result().Header)

	return nil
}

// noSession fails unless rec sets no session cookie.
func noSession(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()

	for _, c := range rec.Result().Cookies() {
		if c.Name == apiserver.SessionCookie && c.Value != "" {
			t.Fatalf("%s: the refused sign-in set a session cookie", what)
		}
	}
}

// TestPasskeySignInLocalUser signs a local user in with no user name and no
// password, and checks that the session works and that the sign-in is audited.
func TestPasskeySignInLocalUser(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	cookie := signedIn(t, "sign-in", e.signIn(t, e.lena, "", nil, nil))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/session", nil)
	req.AddCookie(cookie)

	rec := httptest.NewRecorder()
	e.f.handler.ServeHTTP(rec, req)
	want(t, "read the session", rec, http.StatusOK, "")

	// The cookie of a local user keeps the length of the session policy.
	if cookie.MaxAge <= int(time.Hour/time.Second) {
		t.Errorf("cookie max age = %d seconds, want the policy length (over 3600)", cookie.MaxAge)
	}

	if !strings.Contains(rec.Body.String(), "local:lena") {
		t.Errorf("session = %s, want the subject local:lena", rec.Body)
	}

	if e.failures.count() != 0 {
		t.Errorf("failures = %d, want none for a good sign-in", e.failures.count())
	}

	var found bool

	entries := serverEntries(t, e.f.store)
	for i := range entries {
		en := &entries[i]
		if en.Action == "session:sign-in" && en.Subject == "local:lena" && en.Outcome == audit.OutcomeSuccess {
			found = changeNew(en, "method") == "passkey" && changeNew(en, "factorCount") == "2"
		}
	}

	if !found {
		t.Error("no sign-in entry of local:lena with the method passkey and two factors")
	}
}

// changeNew returns the new value of the change key of en, or "".
func changeNew(en *audit.Entry, key string) string {
	for _, c := range en.Changes {
		if c.Key == key {
			return c.New
		}
	}

	return ""
}

// TestPasskeySignInBeginNamesNoPerson checks that the options of the begin
// name no person and no credential, and that the challenge is new each time.
func TestPasskeySignInBeginNamesNoPerson(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	first := e.post(t, signInBeginTarget, "", nil)
	second := e.post(t, signInBeginTarget, "", nil)

	want(t, "first begin", first, http.StatusOK, "")
	want(t, "second begin", second, http.StatusOK, "")

	for _, rec := range []*httptest.ResponseRecorder{first, second} {
		body := rec.Body.String()
		if strings.Contains(body, "prn-lena") || strings.Contains(body, "prn-ana") ||
			strings.Contains(
				body,
				`"userVerification":"preferred"`,
			) || !strings.Contains(body, `"userVerification":"required"`) {
			t.Errorf("options = %s, want no person and user verification required", body)
		}
	}

	if first.Body.String() == second.Body.String() {
		t.Error("two begins gave the same options")
	}
}

// passkeyRefusalCase is one hostile sign-in. It sends a response that the layers
// must refuse, and checks the answer, the failure count and the audit entry.
type passkeyRefusalCase struct {
	name   string
	setup  func(t *testing.T, e *passkeySignInEnv)
	cred   func(e *passkeySignInEnv) *webauthntest.Credential
	header http.Header
	change func(c webauthntest.Ceremony) webauthntest.Ceremony
}

// lenaCredential is the credential of the local user.
func lenaCredential(e *passkeySignInEnv) *webauthntest.Credential { return e.lena }

// ceremonyCase returns the refusal case of the local user whose response the
// change alters.
func ceremonyCase(name string, change func(c webauthntest.Ceremony) webauthntest.Ceremony) passkeyRefusalCase {
	return passkeyRefusalCase{name: name, setup: nil, cred: lenaCredential, header: nil, change: change}
}

// TestPasskeySignInRefusals sends hostile responses through the whole route.
// Each one gets the one text of a failed sign-in, no cookie, one more failure
// of the source address, and at least one new audit entry that is not a
// success.
func TestPasskeySignInRefusals(t *testing.T) {
	t.Parallel()

	cases := []passkeyRefusalCase{
		ceremonyCase("no user verification", func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithFlags(webauthntest.FlagUserPresent)
		}),
		ceremonyCase("user handle of another person", func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithUserHandle([]byte("prn-ana"))
		}),
		ceremonyCase("user handle of nobody", func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithUserHandle([]byte("prn-none"))
		}),
		ceremonyCase("origin of another site in the client data", func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithOrigin(evilOrigin)
		}),
		ceremonyCase("relying party of another site", func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithRPID("evil.example")
		}),
		ceremonyCase("signature that does not match", func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithBadSignature()
		}),
		ceremonyCase("type of a registration", func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithType("webauthn.create")
		}),
		{
			name: "origin of the server only in a header", setup: nil, cred: lenaCredential,
			header: http.Header{"Origin": {relyingOrigin}, "Host": {"dustpan.example.com"}},
			change: func(c webauthntest.Ceremony) webauthntest.Ceremony { return c.WithOrigin(evilOrigin) },
		},
		{
			name: "disabled person", cred: lenaCredential, header: nil, change: nil,
			setup: func(t *testing.T, e *passkeySignInEnv) {
				t.Helper()

				if err := e.f.store.DisablePrincipal(t.Context(), "prn-lena", disableEntry()); err != nil {
					t.Fatalf("disable: %v", err)
				}
			},
		},
		{
			name: "unknown credential", setup: nil, header: nil, change: nil,
			cred: func(*passkeySignInEnv) *webauthntest.Credential {
				c, err := webauthntest.NewCredential([]byte("prn-lena"), false)
				if err != nil {
					t.Fatalf("make the credential: %v", err)
				}

				return c
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newPasskeySignInEnv(t)
			if tc.setup != nil {
				tc.setup(t, e)
			}

			mark := lastSequence(serverEntries(t, e.f.store))
			rec := e.signIn(t, tc.cred(e), "", tc.header, tc.change)

			want(t, tc.name, rec, http.StatusUnauthorized, errcode.MFASignInFailed)
			noSession(t, tc.name, rec)

			if e.failures.count() != 1 {
				t.Errorf("failures = %d, want 1", e.failures.count())
			}

			if fresh := entriesAfter(serverEntries(t, e.f.store), mark); !hasRefusal(fresh) {
				t.Errorf("entries after the refusal = %+v, want a refusal entry", fresh)
			}
		})
	}
}

// disableEntry returns the audit entry of the disable of a person.
func disableEntry() audit.Entry {
	at := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)

	return audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: "test", IDPSubject: "", TokenID: "",
		Action: "server:manage-principals", Object: "principal:prn-lena", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "",
		Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// lastSequence returns the highest sequence of entries.
func lastSequence(entries []audit.Entry) int64 {
	var last int64

	for i := range entries {
		last = max(last, entries[i].Sequence)
	}

	return last
}

// entriesAfter returns the entries with a sequence above mark.
func entriesAfter(entries []audit.Entry, mark int64) []audit.Entry {
	var out []audit.Entry

	for i := range entries {
		if entries[i].Sequence > mark {
			out = append(out, entries[i])
		}
	}

	return out
}

// hasRefusal reports whether entries holds an entry that is a denial or a
// failure.
func hasRefusal(entries []audit.Entry) bool {
	for i := range entries {
		if entries[i].Outcome == audit.OutcomeDenied || entries[i].Outcome == audit.OutcomeFailure {
			return true
		}
	}

	return false
}

// TestPasskeySignInHeaderOriginIsNotTrusted checks that a header cannot
// change the origin that the server checks: a response for the right origin
// signs in whatever the header says.
func TestPasskeySignInHeaderOriginIsNotTrusted(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	rec := e.signIn(t, e.lena, "", http.Header{"Origin": {evilOrigin}, "Referer": {evilOrigin + "/"}}, nil)
	signedIn(t, "sign-in with a header of another origin", rec)
}

// TestPasskeySignInReplay checks that a response works once: the challenge is
// gone after the first use, and the replay fails and counts.
func TestPasskeySignInReplay(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	challenge := e.begin(t)

	response, err := e.auth.Assert(e.lena, webauthntest.For(challenge))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	body := signInFinishBody(t, response, "")

	signedIn(t, "first use", e.post(t, signInFinishTarget, body, nil))

	before := len(serverEntries(t, e.f.store))
	again := e.post(t, signInFinishTarget, body, nil)

	want(t, "replay", again, http.StatusUnauthorized, errcode.MFASignInFailed)
	noSession(t, "replay", again)

	if e.failures.count() != 1 || len(serverEntries(t, e.f.store)) <= before {
		t.Errorf("failures = %d, entries grew = %v, want 1 and true", e.failures.count(),
			len(serverEntries(t, e.f.store)) > before)
	}
}

// TestPasskeySignInCounterRegression checks that a counter that does not
// grow suspends the passkey, and that the suspended passkey signs in no more,
// also with a good response.
func TestPasskeySignInCounterRegression(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	signedIn(t, "first sign-in", e.signIn(t, e.lena, "", nil, nil))

	cloned := e.signIn(t, e.lena, "", nil, func(c webauthntest.Ceremony) webauthntest.Ceremony {
		return c.WithCounter(1)
	})
	want(t, "counter that went back", cloned, http.StatusUnauthorized, errcode.MFASignInFailed)
	noSession(t, "counter that went back", cloned)

	keys, err := e.f.store.Passkeys(t.Context(), "prn-lena")
	if err != nil || len(keys) != 1 || keys[0].SuspendedAt.IsZero() {
		t.Fatalf("passkeys = %+v, %v, want one suspended passkey", keys, err)
	}

	before := len(serverEntries(t, e.f.store))
	good := e.signIn(t, e.lena, "", nil, func(c webauthntest.Ceremony) webauthntest.Ceremony {
		return c.WithCounter(50)
	})

	want(t, "suspended passkey", good, http.StatusUnauthorized, errcode.MFASignInFailed)
	noSession(t, "suspended passkey", good)

	if len(serverEntries(t, e.f.store)) <= before {
		t.Error("the refusal of the suspended passkey wrote no audit entry")
	}
}

// TestPasskeySignInBodies checks that a body that is not one JSON object with
// a response is refused, counts as a failure, and writes an audit entry.
func TestPasskeySignInBodies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		code string
		http int
	}{
		{"empty", "", errcode.RequestBodyInvalid, http.StatusBadRequest},
		{"not an object", `[]`, errcode.RequestBodyInvalid, http.StatusBadRequest},
		{"response is a string", `{"response":"x"}`, errcode.RequestBodyInvalid, http.StatusBadRequest},
		{"unknown field", `{"response":{},"extra":1}`, errcode.RequestBodyInvalid, http.StatusBadRequest},
		{"response without a challenge", `{"response":{}}`, errcode.MFASignInFailed, http.StatusUnauthorized},
		{
			"body over the limit", `{"response":{"x":"` + strings.Repeat("a", 80*1024) + `"}}`,
			errcode.RequestBodyTooLarge, http.StatusRequestEntityTooLarge,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newPasskeySignInEnv(t)
			before := len(serverEntries(t, e.f.store))

			rec := e.post(t, signInFinishTarget, tc.body, nil)
			want(t, tc.name, rec, tc.http, tc.code)
			noSession(t, tc.name, rec)

			if e.failures.count() != 1 {
				t.Errorf("failures = %d, want 1", e.failures.count())
			}

			if len(serverEntries(t, e.f.store)) <= before {
				t.Error("the refusal wrote no audit entry")
			}
		})
	}
}

// TestPasskeySignInLimits checks the budgets of the source address: a begin
// spends one unit of the begin budget, and a refused address starts no
// ceremony and checks no response.
func TestPasskeySignInLimits(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	e.begin(t)
	e.begin(t)

	if e.begins.count() != 2 {
		t.Fatalf("begins counted = %d, want 2: each begin writes a challenge row", e.begins.count())
	}

	e.begins.refuse = true

	want(t, "begin over the budget", e.post(t, signInBeginTarget, "", nil), http.StatusTooManyRequests, "")

	if e.begins.count() != 2 {
		t.Errorf("begins counted = %d, want 2: a refused begin stores no challenge", e.begins.count())
	}

	e.begins.refuse, e.failures.refuse = false, true

	want(t, "begin from a source with too many failures", e.post(t, signInBeginTarget, "", nil),
		http.StatusTooManyRequests, "")

	response, err := e.auth.Assert(e.lena, webauthntest.For(e.begin2(t)))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	rec := e.post(t, signInFinishTarget, signInFinishBody(t, response, ""), nil)
	want(t, "finish from a source with too many failures", rec, http.StatusTooManyRequests, "")
	noSession(t, "finish from a source with too many failures", rec)
}

// TestPasskeySignInChallengeCap fills the installation with the most open
// sign-in challenges, as many sources would. A begin over the cap answers 503
// and stores no row: after one open challenge ends, exactly one begin works.
func TestPasskeySignInChallengeCap(t *testing.T) {
	t.Parallel()

	e := new(passkeySignInEnv)
	capped := new(cappedChallenges)

	e.begins, e.failures = newPasskeyLimiter(), newPasskeyLimiter()
	e.f = newAccessFixtureSignIn(t, func(st *sqlite.Store) auditStore { return st }, nil,
		func(in *apiserver.SignIn, st *sqlite.Store) {
			capped.PasskeyStore = st

			rp, err := gowebauthn.New(relyingOrigin)
			if err != nil {
				t.Fatalf("relying party: %v", err)
			}

			svc, err := mfasvc.New(mfasvc.Config{
				Store: capped, RP: rp, Audit: st, Now: nil, Log: slog.New(slog.DiscardHandler),
			})
			if err != nil {
				t.Fatalf("passkey service: %v", err)
			}

			in.Passkeys, in.PasskeySignIn = svc, svc
			in.PasskeyBeginLimiter, in.PasskeyLimiter = e.begins, e.failures
		},
	)

	capped.open.Store(mfa.MaxOpenSignInChallenges)

	before := len(serverEntries(t, e.f.store))
	over := e.post(t, signInBeginTarget, "", nil)
	want(t, "begin over the cap", over, http.StatusServiceUnavailable, errcode.MFAUnavailable)

	if len(serverEntries(t, e.f.store)) <= before {
		t.Error("the refused begin wrote no audit entry")
	}

	if capped.stored.Load() != 0 {
		t.Errorf("the refused begin stored %d rows, want 0", capped.stored.Load())
	}

	capped.open.Store(mfa.MaxOpenSignInChallenges - 1)

	want(t, "begin under the cap", e.post(t, signInBeginTarget, "", nil), http.StatusOK, "")

	if capped.stored.Load() != 1 {
		t.Errorf("the begin under the cap stored %d rows, want 1", capped.stored.Load())
	}
}

// cappedChallenges is a passkey store whose count of open sign-in challenges
// a test sets. It applies the cap as the stores do: a sign-in challenge at the
// cap stores no row and returns mfa.ErrChallengeLimit. The stores run the same
// rule in the transaction of the write, and the conformance suite checks it
// against each of them with the real count.
type cappedChallenges struct {
	mfa.PasskeyStore

	open   atomic.Int64
	stored atomic.Int64
}

func (c *cappedChallenges) CreateChallenge(ctx context.Context, ch mfa.Challenge, d mfa.Digest) error {
	if ch.Kind == mfa.CeremonySignIn && c.open.Load() >= mfa.MaxOpenSignInChallenges {
		return mfa.ErrChallengeLimit
	}

	c.stored.Add(1)

	if err := c.PasskeyStore.CreateChallenge(ctx, ch, d); err != nil {
		return fmt.Errorf("create the challenge: %w", err)
	}

	return nil
}

// badSignature alters the signature of a response.
func badSignature(c webauthntest.Ceremony) webauthntest.Ceremony { return c.WithBadSignature() }

// failedAssertionAlerts returns the alerts of failed assertions that the
// outbox holds.
func (e *passkeySignInEnv) failedAssertionAlerts(t *testing.T) []notify.Notification {
	t.Helper()

	var out []notify.Notification

	all := e.alerts(t)
	for i := range all {
		if all[i].Type == notify.SignInLocked {
			out = append(out, all[i])
		}
	}

	return out
}

// TestPasskeySignInBreakGlassFailureAlert checks that 10 failed assertions
// for the passkey of a break-glass account raise one alert to the
// administrators, at most once in an hour, and that the alert locks nothing.
// Failures for a local user raise no alert.
func TestPasskeySignInBreakGlassFailureAlert(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	for range authgate.BreakGlassAlertLimit {
		want(t, "failed assertion of a local user", e.signIn(t, e.lena, "", nil, badSignature),
			http.StatusUnauthorized, errcode.MFASignInFailed)
	}

	if got := e.failedAssertionAlerts(t); len(got) != 0 {
		t.Fatalf("alerts for a local user = %+v, want none", got)
	}

	for i := range authgate.BreakGlassAlertLimit - 1 {
		want(t, "failed assertion of a break-glass account", e.signIn(t, e.ana, "", nil, badSignature),
			http.StatusUnauthorized, errcode.MFASignInFailed)

		if got := e.failedAssertionAlerts(t); len(got) != 0 {
			t.Fatalf("alerts after %d failures = %+v, want none", i+1, got)
		}
	}

	want(t, "tenth failed assertion", e.signIn(t, e.ana, "", nil, badSignature),
		http.StatusUnauthorized, errcode.MFASignInFailed)

	got := e.failedAssertionAlerts(t)
	if len(got) != 1 || len(got[0].Recipients) != 1 || !got[0].Recipients[0].Admins ||
		!strings.Contains(got[0].Summary, "prn-ana") || !strings.Contains(got[0].Summary, "not locked") {
		t.Fatalf("alerts = %+v, want one alert for the administrators about prn-ana", got)
	}

	// Ten more failures in the same hour raise no second alert.
	for range authgate.BreakGlassAlertLimit {
		e.signIn(t, e.ana, "", nil, badSignature)
	}

	if got := e.failedAssertionAlerts(t); len(got) != 1 {
		t.Fatalf("alerts after 20 failures = %d, want 1", len(got))
	}

	// The alert is not a lock: the owner signs in.
	signedIn(t, "sign-in after the alert", e.signIn(t, e.ana, breakGlassReasonText, nil, nil))
}

// begin2 makes a challenge while the failure budget refuses the source, by
// letting it pass for one begin.
func (e *passkeySignInEnv) begin2(t *testing.T) []byte {
	t.Helper()

	e.failures.refuse = false
	c := e.begin(t)
	e.failures.refuse = true

	return c
}

// TestPasskeySignInBreakGlass checks that a break-glass account signs in with
// a passkey and a reason, that the alert of the use is stored in the same
// transaction as the session, and that a missing reason starts no session.
func TestPasskeySignInBreakGlass(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	missing := e.signIn(t, e.ana, "", nil, nil)
	want(t, "no reason", missing, http.StatusBadRequest, errcode.MFABreakGlassReasonRequired)
	noSession(t, "no reason", missing)

	if got := e.alerts(t); len(got) != 0 {
		t.Fatalf("alerts after a refused sign-in = %+v, want none", got)
	}

	if e.failures.count() != 0 {
		t.Errorf("failures = %d, want none: the person only forgot the reason", e.failures.count())
	}

	cookie := signedIn(t, "break-glass sign-in", e.signIn(t, e.ana, breakGlassReasonText, nil, nil))

	// The cookie of a break-glass session ends at the limit of 1 hour.
	if cookie.MaxAge <= 0 || cookie.MaxAge > int(time.Hour/time.Second) {
		t.Errorf("cookie max age = %d seconds, want 1 to 3600", cookie.MaxAge)
	}

	checkBreakGlassAlert(t, e.alerts(t))
	checkBreakGlassEntry(t, serverEntries(t, e.f.store))
}

// breakGlassReasonText is the reason that the break-glass tests type.
const breakGlassReasonText = "the identity provider is down"

// checkBreakGlassAlert fails unless got is one critical alert of a break-glass
// use of prn-ana for the administrators, with the reason and the session.
func checkBreakGlassAlert(t *testing.T, got []notify.Notification) {
	t.Helper()

	if len(got) != 1 {
		t.Fatalf("alerts = %+v, want one", got)
	}

	n := &got[0]
	if n.Type != notify.BreakGlassUsed || n.Severity != notify.SeverityCritical || len(n.Recipients) != 1 ||
		!n.Recipients[0].Admins || !strings.Contains(n.Summary, breakGlassReasonText) ||
		n.Attributes["principal"] != "prn-ana" || n.Attributes["session"] == "" ||
		n.ObjectID != n.Attributes["session"] {
		t.Fatalf("alert = %+v, want a critical break-glass.used alert of the session of prn-ana", *n)
	}
}

// checkBreakGlassEntry fails unless entries holds the successful sign-in of
// the break-glass account with the typed reason.
func checkBreakGlassEntry(t *testing.T, entries []audit.Entry) {
	t.Helper()

	for i := range entries {
		en := &entries[i]
		if en.Action == "session:sign-in" && en.Outcome == audit.OutcomeSuccess && en.Subject == "break-glass:ana" {
			if en.Reason != breakGlassReasonText {
				t.Errorf("entry reason = %q, want the typed reason", en.Reason)
			}

			return
		}
	}

	t.Error("no sign-in entry of the break-glass account")
}

// alerts returns the notifications of the server scope.
func (e *passkeySignInEnv) alerts(t *testing.T) []notify.Notification {
	t.Helper()

	got, err := e.f.store.Notifications().After(t.Context(), audit.ServerScope, 0, 100)
	if err != nil {
		t.Fatalf("read the outbox: %v", err)
	}

	return got
}

// TestPasskeySignInLocalUserRaisesNoAlert checks that the alert is for the
// break-glass accounts only.
func TestPasskeySignInLocalUserRaisesNoAlert(t *testing.T) {
	t.Parallel()

	e := newPasskeySignInEnv(t)

	signedIn(t, "sign-in", e.signIn(t, e.lena, "typed anyway", nil, nil))

	if got := e.alerts(t); len(got) != 0 {
		t.Errorf("alerts = %+v, want none for a local user", got)
	}
}

// TestPasskeySignInNeedsTheBudgets checks that a handler with no budget
// mounts no passkey sign-in route.
func TestPasskeySignInNeedsTheBudgets(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		tweak func(in *apiserver.SignIn)
	}{
		{"no begin budget", func(in *apiserver.SignIn) { in.PasskeyBeginLimiter = nil }},
		{"no failure budget", func(in *apiserver.SignIn) { in.PasskeyLimiter = nil }},
		{"no passkey sign-in service", func(in *apiserver.SignIn) { in.PasskeySignIn = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixtureSignIn(t, func(st *sqlite.Store) auditStore { return st }, nil,
				func(in *apiserver.SignIn, st *sqlite.Store) {
					svc := newRoutePasskeys(t, st)
					in.Passkeys, in.PasskeySignIn = svc, svc
					in.PasskeyBeginLimiter, in.PasskeyLimiter = newPasskeyLimiter(), newPasskeyLimiter()
					tc.tweak(in)
				},
			)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, signInBeginTarget, nil)
			rec := httptest.NewRecorder()
			f.handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusOK {
				t.Errorf("begin answered 200 with %s", tc.name)
			}
		})
	}
}
