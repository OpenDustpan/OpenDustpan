package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/webauthntest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/mfa/gowebauthn"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// The relying party and the routes of the passkey route tests.
const (
	relyingOrigin = "https://dustpan.example.com"
	relyingID     = "dustpan.example.com"
	ownKeysTarget = "/api/dustpan/v1/me/passkeys"

	beginTarget  = ownKeysTarget + "/register/begin"
	finishTarget = ownKeysTarget + "/register/finish"
)

// newRoutePasskeys returns the passkey service of the route tests, over st.
func newRoutePasskeys(t *testing.T, st *sqlite.Store) *mfasvc.Service {
	t.Helper()

	rp, err := gowebauthn.New(relyingOrigin)
	if err != nil {
		t.Fatalf("relying party: %v", err)
	}

	svc, err := mfasvc.New(mfasvc.Config{
		Store: st, RP: rp, Audit: st, Now: nil, Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("passkey service: %v", err)
	}

	return svc
}

// passkeyDoc is the body of a passkey.
type passkeyDoc struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Transports     []string `json:"transports"`
	BackupEligible bool     `json:"backupEligible"`
	CreatedAt      string   `json:"createdAt"`
	LastUsedAt     string   `json:"lastUsedAt"`
	SuspendedAt    string   `json:"suspendedAt"`
}

// passkeyListDoc is the body of the list of passkeys.
type passkeyListDoc struct {
	Passkeys []passkeyDoc `json:"passkeys"`
}

// passkeyErrorDoc is the body of an error answer.
type passkeyErrorDoc struct {
	Code      int            `json:"code"`
	ErrorCode string         `json:"errorCode"`
	ErrorArgs map[string]any `json:"errorArgs"`
}

// passkeyClient sends the requests of one console session.
type passkeyClient struct {
	t       *testing.T
	f       accessFixture
	subject string
	cookie  *http.Cookie
	csrf    string
}

// passkeyClientOf starts a console session of subject that signed in at
// authTime.
func (f accessFixture) passkeyClientOf(t *testing.T, subject string, authTime time.Time) passkeyClient {
	t.Helper()

	cookie, csrf := f.sessionOf(t, subject, authTime)

	return passkeyClient{t: t, f: f, subject: subject, cookie: cookie, csrf: csrf}
}

// freshPasskeyClient starts a console session of subject that signed in now.
func (f accessFixture) freshPasskeyClient(t *testing.T, subject string) passkeyClient {
	t.Helper()

	return f.passkeyClientOf(t, subject, f.sessions.Now())
}

// do sends one request in the session.
func (c passkeyClient) do(method, target string, body []byte) *httptest.ResponseRecorder {
	c.t.Helper()

	req := httptest.NewRequestWithContext(c.t.Context(), method, target, strings.NewReader(string(body)))
	req.AddCookie(c.cookie)
	req.Header.Set(apiserver.CSRFHeader, c.csrf)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	rec := httptest.NewRecorder()
	c.f.handler.ServeHTTP(rec, req)

	return rec
}

// asToken sends one request with the access token of subject.
func (f accessFixture) asToken(t *testing.T, subject, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+subject)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

// expectPasskeyStatus fails unless rec has the status. It returns the error
// body of an error answer.
func expectPasskeyStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) passkeyErrorDoc {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, status, rec.Body)
	}

	var doc passkeyErrorDoc
	if rec.Body.Len() > 0 && status >= http.StatusBadRequest {
		decodeInto(t, rec.Body.String(), &doc)
	}

	return doc
}

// begin starts a register ceremony and returns the challenge of its options.
func (c passkeyClient) begin() []byte {
	c.t.Helper()

	rec := c.do(http.MethodPost, beginTarget, nil)
	expectPasskeyStatus(c.t, rec, http.StatusOK)

	challenge, err := webauthntest.ChallengeOf(rec.Body.Bytes())
	if err != nil {
		c.t.Fatalf("challenge of %s: %v", rec.Body, err)
	}

	return challenge
}

// finishBody returns the body of the end of a ceremony.
func finishBody(t *testing.T, name string, response []byte) []byte {
	t.Helper()

	body, err := json.Marshal(map[string]any{"name": name, "response": json.RawMessage(response)})
	if err != nil {
		t.Fatalf("encode the finish body: %v", err)
	}

	return body
}

// register makes the response of a new credential for the challenge.
func (c passkeyClient) register(challenge []byte) []byte {
	c.t.Helper()

	cred, err := webauthntest.NewCredential([]byte("prn-"+c.subject), false)
	if err != nil {
		c.t.Fatalf("credential: %v", err)
	}

	response, err := webauthntest.New(relyingID, relyingOrigin).Register(cred, webauthntest.For(challenge))
	if err != nil {
		c.t.Fatalf("register: %v", err)
	}

	return response
}

// enroll runs a whole register ceremony and returns the passkey.
func (c passkeyClient) enroll(name string) passkeyDoc {
	c.t.Helper()

	response := c.register(c.begin())
	rec := c.do(http.MethodPost, finishTarget, finishBody(c.t, name, response))
	expectPasskeyStatus(c.t, rec, http.StatusCreated)

	var key passkeyDoc

	decodeInto(c.t, rec.Body.String(), &key)

	return key
}

// list returns the passkeys of the session.
func (c passkeyClient) list() []passkeyDoc {
	c.t.Helper()

	rec := c.do(http.MethodGet, ownKeysTarget, nil)
	expectPasskeyStatus(c.t, rec, http.StatusOK)

	var doc passkeyListDoc

	decodeInto(c.t, rec.Body.String(), &doc)

	return doc.Passkeys
}

// serverActions returns the actions of the entries of @server with the
// outcome.
func serverActions(t *testing.T, st *sqlite.Store, outcome audit.Outcome) []string {
	t.Helper()

	entries := serverEntries(t, st)
	actions := make([]string, 0, len(entries))

	for i := range entries {
		if entries[i].Outcome == outcome {
			actions = append(actions, entries[i].Action)
		}
	}

	return actions
}

// countOf returns how many times want is in got.
func countOf(got []string, want string) int {
	n := 0

	for _, a := range got {
		if a == want {
			n++
		}
	}

	return n
}

// expectRefusals fails unless @server holds count entries with the outcome
// denied and the action.
func expectRefusals(t *testing.T, st *sqlite.Store, action string, count int) {
	t.Helper()

	if refused := serverActions(t, st, audit.OutcomeDenied); countOf(refused, action) != count {
		t.Fatalf("denied actions = %v, want %d of %s", refused, count, action)
	}
}

// expectNoSecrets fails when the body holds a name of a credential ID or of
// a key.
func expectNoSecrets(t *testing.T, body string) {
	t.Helper()

	for _, secret := range []string{"credentialId", "publicKey", "rawId"} {
		if strings.Contains(body, secret) {
			t.Fatalf("body = %s, must not hold %s", body, secret)
		}
	}
}

// expectNewPasskey fails unless key is a passkey that was just added under
// the name: it has an ID with the prefix, a time of the add, its transports,
// and no time of use and no time of suspension.
func expectNewPasskey(t *testing.T, key passkeyDoc, name string) {
	t.Helper()

	if !strings.HasPrefix(key.ID, mfa.PasskeyIDPrefix) || key.Name != name || key.CreatedAt == "" ||
		key.LastUsedAt != "" || key.SuspendedAt != "" || len(key.Transports) == 0 {
		t.Fatalf("passkey = %+v, want a new passkey named %s", key, name)
	}
}

// TestPasskeyEnrollListRemove walks the success path: a person adds a
// passkey, lists it and removes it. The list never shows a credential ID or a
// key, and each change has an audit entry.
func TestPasskeyEnrollListRemove(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	c := f.freshPasskeyClient(t, "alice")

	if got := c.list(); len(got) != 0 {
		t.Fatalf("list before = %+v, want none", got)
	}

	key := c.enroll("laptop")

	expectNewPasskey(t, key, "laptop")
	expectNoSecrets(t, c.do(http.MethodGet, ownKeysTarget, nil).Body.String())

	if got := c.list(); len(got) != 1 || got[0].ID != key.ID {
		t.Fatalf("list after the add = %+v, want %s", got, key.ID)
	}

	expectPasskeyStatus(t, c.do(http.MethodDelete, ownKeysTarget+"/"+key.ID, nil), http.StatusNoContent)

	if got := c.list(); len(got) != 0 {
		t.Fatalf("list after the removal = %+v, want none", got)
	}

	done := serverActions(t, f.store, audit.OutcomeSuccess)
	if countOf(done, mfasvc.ActionPasskeyAdded) != 1 || countOf(done, mfasvc.ActionPasskeyRemoved) != 1 {
		t.Fatalf("success actions = %v, want one add and one removal", done)
	}
}

// TestPasskeyOtherPersonsPasskey proves the owner check: a person never lists
// and never removes the passkey of another person, and the refusal has an
// audit entry.
func TestPasskeyOtherPersonsPasskey(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	alice, bob := f.freshPasskeyClient(t, "alice"), f.freshPasskeyClient(t, "bob")

	aliceKey := alice.enroll("alice-key")
	bobKey := bob.enroll("bob-key")

	if got := bob.list(); len(got) != 1 || got[0].ID != bobKey.ID {
		t.Fatalf("list of bob = %+v, want only %s", got, bobKey.ID)
	}

	// The answer for the passkey of another person is the answer for a passkey
	// that does not exist, so it tells nothing about the other person.
	other := expectPasskeyStatus(t, bob.do(http.MethodDelete, ownKeysTarget+"/"+aliceKey.ID, nil),
		http.StatusNotFound)
	unknown := expectPasskeyStatus(t, bob.do(http.MethodDelete, ownKeysTarget+"/pky-nobody", nil),
		http.StatusNotFound)

	if other.ErrorCode != errcode.MFAKeyNotFound || unknown.ErrorCode != other.ErrorCode {
		t.Fatalf("error codes = %q and %q, want both %s", other.ErrorCode, unknown.ErrorCode, errcode.MFAKeyNotFound)
	}

	if got := alice.list(); len(got) != 1 || got[0].ID != aliceKey.ID {
		t.Fatalf("list of alice = %+v, want %s: bob removed it", got, aliceKey.ID)
	}

	expectRefusals(t, f.store, mfasvc.ActionRemoveRefused, 2)
}

// TestPasskeyReplayedFinish proves that a response works once: the second
// finish of the same response is refused, adds nothing and has an audit entry.
func TestPasskeyReplayedFinish(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	c := f.freshPasskeyClient(t, "alice")
	body := finishBody(t, "laptop", c.register(c.begin()))

	expectPasskeyStatus(t, c.do(http.MethodPost, finishTarget, body), http.StatusCreated)

	replay := expectPasskeyStatus(t, c.do(http.MethodPost, finishTarget, body), http.StatusBadRequest)
	if replay.ErrorCode != errcode.MFACeremonyNotOpen {
		t.Fatalf("error code = %q, want %s", replay.ErrorCode, errcode.MFACeremonyNotOpen)
	}

	if got := c.list(); len(got) != 1 {
		t.Fatalf("list = %+v, want one passkey", got)
	}

	expectRefusals(t, f.store, mfasvc.ActionAddRefused, 1)
}

// TestPasskeyFinishInAnotherSession proves that a register ceremony belongs to
// the session that began it: the same person in another session cannot end it.
func TestPasskeyFinishInAnotherSession(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	first, second := f.freshPasskeyClient(t, "alice"), f.freshPasskeyClient(t, "alice")

	body := finishBody(t, "laptop", first.register(first.begin()))
	refusal := expectPasskeyStatus(t, second.do(http.MethodPost, finishTarget, body), http.StatusForbidden)

	if refusal.ErrorCode != errcode.MFACeremonyWrongPerson {
		t.Fatalf("error code = %q, want %s", refusal.ErrorCode, errcode.MFACeremonyWrongPerson)
	}

	if got := first.list(); len(got) != 0 {
		t.Fatalf("list = %+v, want none", got)
	}

	expectRefusals(t, f.store, mfasvc.ActionAddRefused, 1)
}

// TestPasskeyBodyTooLarge proves the size limit of the body: a body over the
// limit answers 413, adds nothing and has an audit entry.
func TestPasskeyBodyTooLarge(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	c := f.freshPasskeyClient(t, "alice")

	big := `{"name":"laptop","response":{"x":"` + strings.Repeat("a", apiserver.MaxPasskeyBodyForTest) + `"}}`
	refusal := expectPasskeyStatus(t, c.do(http.MethodPost, finishTarget, []byte(big)),
		http.StatusRequestEntityTooLarge)

	if refusal.ErrorCode != errcode.RequestBodyTooLarge ||
		refusal.ErrorArgs["maxBytes"] != float64(apiserver.MaxPasskeyBodyForTest) {
		t.Fatalf("error = %+v, want %s with the limit", refusal, errcode.RequestBodyTooLarge)
	}

	if got := c.list(); len(got) != 0 {
		t.Fatalf("list = %+v, want none", got)
	}

	expectRefusals(t, f.store, mfasvc.ActionAddRefused, 1)
}

// TestPasskeyNameWithHiddenCharacters proves the check of the name at the
// boundary: a name with a character that a person cannot see, or that the
// rules of a name do not allow, is refused before a ceremony starts, and has
// an audit entry.
func TestPasskeyNameWithHiddenCharacters(t *testing.T) {
	t.Parallel()

	// The names use escapes, so that no hidden character is in this file.
	tests := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"space", "my key"},
		{"tab", "my\tkey"},
		{"newline", "key\nnext"},
		{"nul", "key\x00"},
		{"right to left override", "key\U0000202eexe"},
		{"zero width space", "key\U0000200bname"},
		{"zero width joiner", "key\U0000200dname"},
		{"byte order mark", "\U0000feffkey"},
		{"non breaking space", "key\U000000a0name"},
		{"accent", "cl\U000000e9"},
		{"emoji", "key\U0001F511"},
		{"leading dot", ".key"},
		{"leading hyphen", "-key"},
		{"too long", strings.Repeat("k", mfa.MaxPasskeyNameLength+1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			c := f.freshPasskeyClient(t, "alice")

			rec := c.do(http.MethodPost, finishTarget, finishBody(t, tc.text, []byte(`{}`)))

			refusal := expectPasskeyStatus(t, rec, http.StatusBadRequest)
			if refusal.ErrorCode != errcode.MFAKeyInvalid && refusal.ErrorCode != errcode.RequestBodyInvalid {
				t.Fatalf("error code = %q, want %s", refusal.ErrorCode, errcode.MFAKeyInvalid)
			}

			if got := c.list(); len(got) != 0 {
				t.Fatalf("list = %+v, want none", got)
			}

			expectRefusals(t, f.store, mfasvc.ActionAddRefused, 1)
		})
	}
}

// fakePasskeys is a passkey service that counts its calls and records the
// actor and the person of each one. It answers err.
type fakePasskeys struct {
	mu      sync.Mutex
	err     error
	calls   int
	actors  []mfasvc.Actor
	people  []mfa.Person
	removed []string
}

func (p *fakePasskeys) BeginRegistration(_ context.Context, a mfasvc.Actor, person mfa.Person) ([]byte, error) {
	return []byte(`{"publicKey":{}}`), p.record(a, person)
}

func (p *fakePasskeys) FinishRegistration(
	_ context.Context, a mfasvc.Actor, person mfa.Person, name string, _ []byte,
) (mfa.Passkey, error) {
	var key mfa.Passkey

	key.ID, key.PersonID, key.Name = "pky-fake", person.ID, name

	return key, p.record(a, person)
}

func (p *fakePasskeys) Passkeys(_ context.Context, a mfasvc.Actor) ([]mfa.Passkey, error) {
	var nobody mfa.Person

	return nil, p.record(a, nobody)
}

func (p *fakePasskeys) RemovePasskey(_ context.Context, a mfasvc.Actor, id string) error {
	p.mu.Lock()
	p.removed = append(p.removed, id)
	p.mu.Unlock()

	var nobody mfa.Person

	return p.record(a, nobody)
}

// record counts one call and keeps its actor and person.
func (p *fakePasskeys) record(a mfasvc.Actor, person mfa.Person) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++
	p.actors = append(p.actors, a)
	p.people = append(p.people, person)

	return p.err
}

// newFakePasskeys returns a fake service that answers err.
func newFakePasskeys(err error) *fakePasskeys {
	return &fakePasskeys{mu: sync.Mutex{}, err: err, calls: 0, actors: nil, people: nil, removed: nil}
}

// newFakePasskeyFixture returns the fixture with the fake service.
func newFakePasskeyFixture(t *testing.T, fake *fakePasskeys) accessFixture {
	t.Helper()

	return newAccessFixtureFull(t, func(st *sqlite.Store) auditStore { return st }, fake)
}

// TestPasskeyNeedsAFreshSignIn proves the step-up check: with an old sign-in,
// or with an access token, the routes that add or remove a passkey refuse
// before the service runs, and each refusal has an audit entry.
func TestPasskeyNeedsAFreshSignIn(t *testing.T) {
	t.Parallel()

	finishRequest := `{"name":"laptop","response":{}}`
	removeTarget := ownKeysTarget + "/pky-fake"

	tests := []struct {
		name   string
		method string
		target string
		body   string
		token  bool
		status int
	}{
		{"begin with an old sign-in", http.MethodPost, beginTarget, "", false, http.StatusUnauthorized},
		{"begin with an access token", http.MethodPost, beginTarget, "", true, http.StatusForbidden},
		{"finish with an old sign-in", http.MethodPost, finishTarget, finishRequest, false, http.StatusUnauthorized},
		{"finish with an access token", http.MethodPost, finishTarget, finishRequest, true, http.StatusForbidden},
		{"remove with an old sign-in", http.MethodDelete, removeTarget, "", false, http.StatusUnauthorized},
		{"remove with an access token", http.MethodDelete, removeTarget, "", true, http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakePasskeys(nil)
			f := newFakePasskeyFixture(t, fake)

			var rec *httptest.ResponseRecorder
			if tc.token {
				rec = f.asToken(t, "alice", tc.method, tc.target, []byte(tc.body))
			} else {
				old := f.passkeyClientOf(t, "alice", f.sessions.Now().Add(-48*time.Hour))
				rec = old.do(tc.method, tc.target, []byte(tc.body))
			}

			refusal := expectPasskeyStatus(t, rec, tc.status)
			if refusal.ErrorCode != errcode.MFAFreshStepRequired {
				t.Fatalf("error code = %q, want %s", refusal.ErrorCode, errcode.MFAFreshStepRequired)
			}

			if fake.calls != 0 {
				t.Fatalf("the service ran %d times, want 0", fake.calls)
			}

			failed := serverActions(t, f.store, audit.OutcomeFailure)
			if countOf(failed, "caller:manage-sign-in") != 1 {
				t.Fatalf("failure actions = %v, want one refusal of the step", failed)
			}
		})
	}
}

// TestPasskeyListNeedsASession proves that the list needs the console session
// of a person but no fresh sign-in: an access token is refused with an audit
// entry, and a session with an old sign-in lists.
func TestPasskeyListNeedsASession(t *testing.T) {
	t.Parallel()

	t.Run("access token", func(t *testing.T) {
		t.Parallel()

		fake := newFakePasskeys(nil)
		f := newFakePasskeyFixture(t, fake)

		refusal := expectPasskeyStatus(t, f.asToken(t, "alice", http.MethodGet, ownKeysTarget, nil),
			http.StatusForbidden)
		if refusal.ErrorCode != errcode.MFASessionRequired || fake.calls != 0 {
			t.Fatalf("error code = %q with %d calls, want %s and 0", refusal.ErrorCode, fake.calls,
				errcode.MFASessionRequired)
		}

		expectRefusals(t, f.store, mfasvc.ActionListRefused, 1)
	})

	t.Run("old sign-in", func(t *testing.T) {
		t.Parallel()

		f := newFakePasskeyFixture(t, newFakePasskeys(nil))
		c := f.passkeyClientOf(t, "alice", f.sessions.Now().Add(-48*time.Hour))

		expectPasskeyStatus(t, c.do(http.MethodGet, ownKeysTarget, nil), http.StatusOK)
	})
}

// TestPasskeyActsForTheSessionOwner proves that each step acts for the owner
// of the console session, in the session, and never for a person that the
// path names.
func TestPasskeyActsForTheSessionOwner(t *testing.T) {
	t.Parallel()

	fake := newFakePasskeys(nil)
	f := newFakePasskeyFixture(t, fake)
	c := f.freshPasskeyClient(t, "alice")

	expectPasskeyStatus(t, c.do(http.MethodPost, beginTarget, nil), http.StatusOK)
	expectPasskeyStatus(t, c.do(http.MethodPost, finishTarget, finishBody(t, "laptop", []byte(`{}`))),
		http.StatusCreated)
	expectPasskeyStatus(t, c.do(http.MethodGet, ownKeysTarget, nil), http.StatusOK)
	expectPasskeyStatus(t, c.do(http.MethodDelete, ownKeysTarget+"/pky-of-bob", nil), http.StatusNoContent)

	if len(fake.actors) != 4 {
		t.Fatalf("calls = %d, want 4", len(fake.actors))
	}

	for i, a := range fake.actors {
		if a.PrincipalID != "prn-alice" || a.SessionID == "" || a.Entry.Subject == "" {
			t.Fatalf("actor %d = %+v, want the person and the session of alice", i, a)
		}
	}

	if fake.people[0].ID != "prn-alice" || fake.people[1].ID != "prn-alice" {
		t.Fatalf("people = %+v, want alice", fake.people)
	}

	if len(fake.removed) != 1 || fake.removed[0] != "pky-of-bob" {
		t.Fatalf("removed = %v, want the ID of the path, for the service to check", fake.removed)
	}
}

// TestPasskeyErrorAnswers proves the answer to each error that the service
// returns: its status and its code of the catalog.
func TestPasskeyErrorAnswers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		route   string
		status  int
		errCode string
	}{
		{"wrong person", mfa.ErrWrongPerson, "finish", http.StatusForbidden, errcode.MFACeremonyWrongPerson},
		{"verification", mfa.ErrVerification, "finish", http.StatusBadRequest, errcode.MFACeremonyFailed},
		{"limit", mfa.ErrLimit, "begin", http.StatusConflict, errcode.MFAKeyLimit},
		{"exists", mfa.ErrAlreadyExists, "finish", http.StatusConflict, errcode.MFAKeyExists},
		{"suspended", mfa.ErrSuspended, "remove", http.StatusConflict, errcode.MFAKeySuspended},
		{"last credential", mfa.ErrLastCredential, "remove", http.StatusConflict, errcode.LocalUserLastCredential},
		{"demo", localuser.ErrDemoFixed, "remove", http.StatusConflict, errcode.LocalUserDemoFixed},
		{"unavailable", mfa.ErrUnavailable, "begin", http.StatusServiceUnavailable, errcode.MFAUnavailable},
		{"invalid", mfa.ErrInvalid, "finish", http.StatusBadRequest, errcode.MFAKeyInvalid},
		{"no ceremony", mfa.ErrNotFound, "finish", http.StatusBadRequest, errcode.MFACeremonyNotOpen},
		{"no passkey", mfa.ErrNotFound, "remove", http.StatusNotFound, errcode.MFAKeyNotFound},
		{"no passkey in the list", mfa.ErrNotFound, "list", http.StatusNotFound, errcode.MFAKeyNotFound},
	}

	routes := map[string]struct {
		method string
		target string
		body   []byte
	}{
		"begin":  {http.MethodPost, beginTarget, nil},
		"finish": {http.MethodPost, finishTarget, []byte(`{"name":"laptop","response":{}}`)},
		"list":   {http.MethodGet, ownKeysTarget, nil},
		"remove": {http.MethodDelete, ownKeysTarget + "/pky-fake", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFakePasskeyFixture(t, newFakePasskeys(tc.err))
			c := f.freshPasskeyClient(t, "alice")
			route := routes[tc.route]

			got := expectPasskeyStatus(t, c.do(route.method, route.target, route.body), tc.status)
			if got.ErrorCode != tc.errCode {
				t.Fatalf("error code = %q, want %s", got.ErrorCode, tc.errCode)
			}

			if _, found := errcode.Lookup(got.ErrorCode); !found {
				t.Fatalf("error code %q is not in the catalog", got.ErrorCode)
			}
		})
	}
}

// TestPasskeyBeginIsRateLimited proves the budget of begins of a person: the
// begin over the budget answers 429 with Retry-After and has an audit entry,
// and another person keeps a budget of its own.
func TestPasskeyBeginIsRateLimited(t *testing.T) {
	t.Parallel()

	fake := newFakePasskeys(nil)
	f := newFakePasskeyFixture(t, fake)
	alice, bob := f.freshPasskeyClient(t, "alice"), f.freshPasskeyClient(t, "bob")

	for range apiserver.PasskeyBeginLimitForTest {
		expectPasskeyStatus(t, alice.do(http.MethodPost, beginTarget, nil), http.StatusOK)
	}

	rec := alice.do(http.MethodPost, beginTarget, nil)
	refusal := expectPasskeyStatus(t, rec, http.StatusTooManyRequests)

	wait, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if refusal.ErrorCode != errcode.MFARateLimited || err != nil || wait < 1 ||
		wait > int(apiserver.PasskeyBeginWindowForTest/time.Second)+1 {
		t.Fatalf("answer = %+v with Retry-After %q, want %s and a wait in seconds", refusal,
			rec.Header().Get("Retry-After"), errcode.MFARateLimited)
	}

	if fake.calls != apiserver.PasskeyBeginLimitForTest {
		t.Fatalf("the service ran %d times, want %d", fake.calls, apiserver.PasskeyBeginLimitForTest)
	}

	// More begins in the same window are refused too, with no new entry.
	for range 2 {
		expectPasskeyStatus(t, alice.do(http.MethodPost, beginTarget, nil), http.StatusTooManyRequests)
	}

	expectPasskeyStatus(t, bob.do(http.MethodPost, beginTarget, nil), http.StatusOK)
	expectRefusals(t, f.store, mfasvc.ActionAddRefused, 1)
}

// recordingAudit keeps the entries that the adapter appends in @server.
type recordingAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (r *recordingAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries = append(r.entries, e)

	return e, nil
}

// TestPasskeyRefusesASessionThatIsNoPerson proves the check of the session
// owner in the adapter, with the layers around it out of the way: a session
// of a service principal and a session with an empty owner ID are refused on
// each route with mfa.session-required, write one denied audit entry, and
// never reach the service.
func TestPasskeyRefusesASessionThatIsNoPerson(t *testing.T) {
	t.Parallel()

	owners := []struct {
		name string
		kind principal.Kind
		id   string
	}{
		{"service principal", principal.KindService, "prn-service"},
		{"empty owner ID", principal.KindPerson, ""},
	}

	routes := []struct {
		name   string
		action string
	}{
		{"begin", mfasvc.ActionAddRefused},
		{"finish", mfasvc.ActionAddRefused},
		{"list", mfasvc.ActionListRefused},
		{"remove", mfasvc.ActionRemoveRefused},
	}

	for _, o := range owners {
		for _, r := range routes {
			t.Run(o.name+" on "+r.name, func(t *testing.T) {
				t.Parallel()

				fake := newFakePasskeys(nil)
				entries := &recordingAudit{mu: sync.Mutex{}, entries: nil}

				rec := apiserver.ServePasskeyAsOwnerForTest(
					t, apiserver.PasskeyOwnerForTest{Route: r.name, Kind: o.kind, ID: o.id}, entries, fake,
				)

				refusal := expectPasskeyStatus(t, rec, http.StatusForbidden)
				if refusal.ErrorCode != errcode.MFASessionRequired || fake.calls != 0 {
					t.Fatalf("error code = %q with %d calls, want %s and 0", refusal.ErrorCode, fake.calls,
						errcode.MFASessionRequired)
				}

				if len(entries.entries) != 1 || entries.entries[0].Action != r.action ||
					entries.entries[0].Outcome != audit.OutcomeDenied {
					t.Fatalf("entries = %+v, want one denied %s", entries.entries, r.action)
				}
			})
		}
	}
}

// limiterAt returns the time d after the start of the limiter tests.
func limiterAt(d time.Duration) time.Time {
	return time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC).Add(d)
}

// TestBeginLimiterWindow proves the window of one person and the budget of
// another person.
func TestBeginLimiterWindow(t *testing.T) {
	t.Parallel()

	limit := apiserver.PasskeyBeginLimitForTest
	window := apiserver.PasskeyBeginWindowForTest
	l := apiserver.NewBeginLimiterForTest()

	for i := range limit {
		if _, ok := l.Allow("prn-a", limiterAt(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("begin %d refused, want a budget of %d", i+1, limit)
		}
	}

	wait, ok := l.Allow("prn-a", limiterAt(time.Minute))
	if ok || wait != window-time.Minute {
		t.Fatalf("begin over the budget = %v, %v, want a refusal that waits %s", wait, ok, window-time.Minute)
	}

	if _, other := l.Allow("prn-b", limiterAt(time.Minute)); !other {
		t.Fatal("another person is refused, want a budget of its own")
	}

	if _, again := l.Allow("prn-a", limiterAt(window)); !again {
		t.Fatal("the begin after the window is refused, want a new budget")
	}
}

// TestBeginLimiterFullTable proves that a full table of live windows refuses a
// new person, keeps the budget of a person in the table, and has room again
// after the windows end.
func TestBeginLimiterFullTable(t *testing.T) {
	t.Parallel()

	l := apiserver.NewBeginLimiterForTest()

	for i := range apiserver.PasskeyBeginPeopleForTest {
		if _, ok := l.Allow("prn-"+strconv.Itoa(i), limiterAt(0)); !ok {
			t.Fatalf("person %d refused, want room for %d people", i, apiserver.PasskeyBeginPeopleForTest)
		}
	}

	later := limiterAt(time.Second)

	if _, ok := l.Allow("prn-new", later); ok {
		t.Fatal("a new person passes a full table, want a refusal")
	}

	if _, ok := l.Allow("prn-1", later); !ok {
		t.Fatal("a person of the table is refused, want its budget")
	}

	if _, ok := l.Allow("prn-new", limiterAt(apiserver.PasskeyBeginWindowForTest)); !ok {
		t.Fatal("a new person is refused after the windows ended, want room")
	}
}
