package trustrules_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// exchangeURL is the exchange route of the namespace acme.
const exchangeURL = rulesURL + "/exchange"

// ciIssuer is the issuer of the static rules of the exchange tests.
const ciIssuer = "ci-cluster-7"

// ciKey signs the tokens of the exchange tests.
type ciKey struct {
	priv ed25519.PrivateKey
	jwks string
}

func newCIKey(t *testing.T) ciKey {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return ciKey{
		priv: priv,
		jwks: `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","x":"` + base64.RawURLEncoding.EncodeToString(pub) +
			`"}]}`,
	}
}

// token returns a token of the issuer with the subject sub, valid now.
func (k ciKey) token(t *testing.T, sub string) string {
	t.Helper()

	now := time.Now()

	claims, err := json.Marshal(map[string]any{
		"iss": ciIssuer, "aud": "https://dustpan.example.com/acme", "sub": sub, "repository": "acme/infra",
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "jti": "run-7",
	})
	if err != nil {
		t.Fatalf("encode claims: %v", err)
	}

	input := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT","kid":"k1"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(claims)

	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(k.priv, []byte(input)))
}

// staticRuleBody returns the body of a create of a static rule called name
// with the keys of k.
func staticRuleBody(name, principalID string, k ciKey) string {
	return `{"name":` + quote(name) + `,"issuer":"` + ciIssuer + `","audience":"https://dustpan.example.com/acme",` +
		`"keySource":"static","staticKeys":` + quote(k.jwks) + `,"principal":` + quote(principalID) + `,` +
		`"conditions":[{"claim":"sub","values":["repo:acme/infra:ref:refs/heads/main"]}]}`
}

// exchangeAnswer is the answer of an exchange.
type exchangeAnswer struct {
	Token     string `json:"token"`
	TokenID   string `json:"id"`
	ExpiresAt string `json:"expiresAt"`
	Principal string `json:"principal"`
	Rule      string `json:"rule"`
}

// exchanged is the answer of one exchange.
type exchanged struct {
	header http.Header
	body   []byte
	status int
}

// exchangeRaw sends one exchange with no credential and returns the answer.
func (e *env) exchangeRaw(path, body string) exchanged {
	e.t.Helper()

	req, err := http.NewRequestWithContext(e.t.Context(), http.MethodPost, e.server.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatalf("request: %v", err)
	}

	resp, err := e.server.Client().Do(req)
	if err != nil {
		e.t.Fatalf("send: %v", err)
	}

	out, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()

	if readErr != nil || closeErr != nil {
		e.t.Fatalf("read the answer: %v, %v", readErr, closeErr)
	}

	return exchanged{header: resp.Header, body: out, status: resp.StatusCode}
}

// exchangeBody returns the body of an exchange of tok.
func exchangeBody(tok string) string {
	return `{"token":` + quote(tok) + `}`
}

// mustCreateStatic creates a static rule called name for the principal
// worker with the keys of k.
func (e *env) mustCreateStatic(name string, k ciKey) {
	e.t.Helper()

	status, body := e.admin(http.MethodPost, rulesURL, staticRuleBody(name, e.worker, k))
	if status != http.StatusCreated {
		e.t.Fatalf("create rule %q = %d %s", name, status, body)
	}
}

// mainSubject is the subject of a token of the main branch.
const mainSubject = "repo:acme/infra:ref:refs/heads/main"

func TestExchangeIssuesAnAccessTokenOfThePrincipal(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	k := newCIKey(t)
	e.mustCreateStatic("ci", k)

	res := e.exchangeRaw(exchangeURL, exchangeBody(k.token(t, mainSubject)))
	if res.status != http.StatusCreated {
		t.Fatalf("exchange = %d %s, want 201", res.status, res.body)
	}

	if got := res.header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	var got exchangeAnswer
	decode(t, res.body, &got)

	checkExchanged(t, e, &got)
}

// checkExchanged fails the test unless got is a token of the principal
// worker that lives one hour, authenticates as a trust token, and has its
// audit entry.
func checkExchanged(t *testing.T, e *env, got *exchangeAnswer) {
	t.Helper()

	if got.Principal != e.worker || !strings.HasPrefix(got.Token, "odp_") || !strings.HasPrefix(got.TokenID, "tok-") {
		t.Fatalf("answer = %+v", got)
	}

	expires, err := time.Parse(time.RFC3339, got.ExpiresAt)
	if err != nil || time.Until(expires) > time.Hour || time.Until(expires) < 59*time.Minute {
		t.Fatalf("expiresAt = %q, %v; want one hour from now", got.ExpiresAt, err)
	}

	p, err := e.tokens.AuthenticateToken(t.Context(), got.Token)
	if err != nil || p.Credential != identity.CredentialTrustToken {
		t.Fatalf("the exchanged token authenticates as %+v, %v; want a trust token", p, err)
	}

	checkLastExchangeEntry(t, e, got.TokenID)
}

// checkLastExchangeEntry fails the test unless the last trust rule entry of
// acme is the exchange that issued the token tokenID.
func checkLastExchangeEntry(t *testing.T, e *env, tokenID string) {
	t.Helper()

	entries := e.ruleEntries()
	if len(entries) == 0 {
		t.Fatal("no audit entry of a trust rule")
	}

	last := entries[len(entries)-1]
	if last.Action != "trust-rule:exchange" || last.TokenID != tokenID || last.Outcome != audit.OutcomeSuccess {
		t.Fatalf("last entry = %+v, want the exchange", last)
	}
}

// TestExchangeAnswersOneRefusalForEachBadToken checks that each refusal
// answers 401 with trust-rule.no-match and no other detail.
func TestExchangeAnswersOneRefusalForEachBadToken(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	k := newCIKey(t)
	e.mustCreateStatic("ci", k)

	cases := map[string]exchanged{
		"another branch": e.exchangeRaw(exchangeURL, exchangeBody(k.token(t, "repo:acme/infra:ref:refs/heads/x"))),
		"another key":    e.exchangeRaw(exchangeURL, exchangeBody(newCIKey(t).token(t, mainSubject))),
		"not a token":    e.exchangeRaw(exchangeURL, exchangeBody("not-a-jwt")),
		"an unknown namespace": e.exchangeRaw("/api/dustpan/v1/namespaces/absent/trust-rules/exchange",
			exchangeBody(k.token(t, mainSubject))),
	}

	for name, res := range cases {
		if res.status != http.StatusUnauthorized || errorCode(t, res.body) != "trust-rule.no-match" {
			t.Errorf("%s: exchange = %d %s, want 401 trust-rule.no-match", name, res.status, res.body)
		}
	}

	if res := e.exchangeRaw(exchangeURL, `{"rule":"trr-1"}`); res.status != http.StatusBadRequest {
		t.Errorf("no token: exchange = %d %s, want 400", res.status, res.body)
	}

	if refusals(t, e) == 0 {
		t.Fatal("no refusal entry in the audit log of the namespace")
	}
}

// refusals returns the number of entries of refused exchanges of acme.
func refusals(t *testing.T, e *env) int {
	t.Helper()

	listing, err := e.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	n := 0

	for i := range listing.Entries {
		if listing.Entries[i].Action == "trust-rule:exchange" && listing.Entries[i].Outcome == audit.OutcomeDenied {
			n++
		}
	}

	return n
}

// TestExchangeOfTwoMatchingRulesAnswers409 checks the answer to a token that
// two rules accept.
func TestExchangeOfTwoMatchingRulesAnswers409(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	k := newCIKey(t)
	e.mustCreateStatic("one", k)
	e.mustCreateStatic("two", k)

	res := e.exchangeRaw(exchangeURL, exchangeBody(k.token(t, mainSubject)))
	if res.status != http.StatusConflict || errorCode(t, res.body) != "trust-rule.ambiguous" {
		t.Fatalf("exchange = %d %s, want 409 trust-rule.ambiguous", res.status, res.body)
	}
}
