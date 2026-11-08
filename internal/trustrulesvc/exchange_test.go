package trustrulesvc_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// exchangeNow is the clock of the exchange tests.
func exchangeNow() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}

// issuerKey signs tokens with one Ed25519 key.
type issuerKey struct {
	priv ed25519.PrivateKey
	jwks string
}

func newIssuerKey(t *testing.T) issuerKey {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return issuerKey{
		priv: priv,
		jwks: `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","x":"` + base64.RawURLEncoding.EncodeToString(pub) +
			`"}]}`,
	}
}

// sign returns a token with the claims c and the header h.
func (k issuerKey) sign(t *testing.T, h, c map[string]any) string {
	t.Helper()

	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		return base64.RawURLEncoding.EncodeToString(b)
	}

	input := enc(h) + "." + enc(c)

	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(k.priv, []byte(input)))
}

// token returns a valid token of the issuer of the static rule.
func (k issuerKey) token(t *testing.T, change func(c map[string]any)) string {
	t.Helper()

	now := exchangeNow()
	c := map[string]any{
		"iss": staticIssuer, "aud": "https://dustpan.example.com", "sub": "repo:acme/infra:ref:refs/heads/main",
		"repository": "acme/infra", "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "jti": "run-1",
	}

	if change != nil {
		change(c)
	}

	return k.sign(t, map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": "k1"}, c)
}

// staticIssuer is the issuer of the static rules of the tests.
const staticIssuer = "ci-cluster-7"

// fakeRuleStore holds the rules of acme and records the exchanges.
type fakeRuleStore struct {
	fakeStore

	rules     []trustrule.Rule
	exchanges []trustrule.Exchange
	entries   []audit.Entry
	err       error
}

func (f *fakeRuleStore) List(context.Context, namespace.Name) ([]trustrule.Rule, error) {
	return f.rules, nil
}

func (f *fakeRuleStore) RecordExchange(
	_ context.Context, x trustrule.Exchange, _ namespace.Fence, e audit.Entry,
) error {
	if f.err != nil {
		return f.err
	}

	if err := e.Validate(); err != nil {
		return err
	}

	f.exchanges = append(f.exchanges, x)
	f.entries = append(f.entries, e)

	return nil
}

// fakeTokens makes tokens that live one hour.
type fakeTokens struct{}

func (fakeTokens) NewTrustToken(
	_ context.Context, at time.Time, principalID, name, createdBy string,
) (accesstoken.Issued, error) {
	t := accesstoken.Token{
		CreatedAt: at, ExpiresAt: at.Add(time.Hour), LastUsedAt: time.Time{}, RevokedAt: time.Time{},
		DisabledAt: time.Time{}, ID: "tok-exchanged", PrincipalID: principalID, Name: name,
		Kind: accesstoken.KindTrust, ReplacedBy: "", CreatedBy: createdBy,
	}

	return accesstoken.Issued{Token: t, Secret: accesstoken.Secret{}, Digest: accesstoken.Digest{}}, nil
}

// memAudit keeps the entries of the refusals.
type memAudit struct {
	audit.Store

	mu      sync.Mutex
	entries []audit.Entry
	// server holds the entries of the installation.
	server []audit.Entry
	// fail is the number of the next appends that fail.
	fail int
	// failServer is the number of the next appends of the installation
	// that fail.
	failServer int
}

func (m *memAudit) Append(_ context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	m.mu.Lock()
	failNow := m.fail > 0
	m.fail = max(m.fail-1, 0)
	m.mu.Unlock()

	if failNow {
		return audit.Entry{}, errAuditDown
	}

	if e.Namespace != fence.Namespace {
		return audit.Entry{}, audit.ErrInvalidEntry
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = append(m.entries, e)

	return e, nil
}

func (m *memAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	if e.Namespace != audit.ServerScope {
		return audit.Entry{}, audit.ErrInvalidEntry
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failServer > 0 {
		m.failServer--

		return audit.Entry{}, errAuditDown
	}

	m.server = append(m.server, e)

	return e, nil
}

func (m *memAudit) all() []audit.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]audit.Entry(nil), m.entries...)
}

func (m *memAudit) serverEntries() []audit.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]audit.Entry(nil), m.server...)
}

// errAuditDown is the error of an append that fails on purpose.
var errAuditDown = errors.New("the audit store is down")

// fakeKeys answers the keys of a discovery issuer, and counts the reads.
type fakeKeys struct {
	keys  trustrule.KeySet
	err   error
	reads int
}

func (f *fakeKeys) Keys(context.Context, string, string) (trustrule.KeySet, error) {
	f.reads++

	return f.keys, f.err
}

// exchangeFixture is an exchanger over fakes, with one static rule of acme
// for the principal prn-worker.
type exchangeFixture struct {
	x      *trustrulesvc.Exchanger
	rules  *fakeRuleStore
	audit  *memAudit
	keys   *fakeKeys
	issuer issuerKey
	people fakePrincipals
}

func newExchangeFixture(t *testing.T) *exchangeFixture {
	t.Helper()

	return newExchangeFixtureAt(t, exchangeNow)
}

// newExchangeFixtureAt is newExchangeFixture with the clock now.
func newExchangeFixtureAt(t *testing.T, now func() time.Time) *exchangeFixture {
	t.Helper()

	return newExchangeFixtureWith(t, now, nil)
}

// newExchangeFixtureWith is newExchangeFixtureAt with the limiter of the
// refusals refusals. Nil means the limiter of the exchanger.
func newExchangeFixtureWith(t *testing.T, now func() time.Time, refusals *denylimit.Limiter) *exchangeFixture {
	t.Helper()

	return newExchangeFixtureOwn(t, now, refusals, nil, slog.New(slog.DiscardHandler))
}

// newExchangeFixtureOwn is newExchangeFixtureWith with the check own of the
// issuers of this installation and the logger log of the exchanger. Nil own
// means no issuer.
func newExchangeFixtureOwn(
	t *testing.T, now func() time.Time, refusals *denylimit.Limiter, own trustrulesvc.OwnIssuer,
	log *slog.Logger,
) *exchangeFixture {
	t.Helper()

	k := newIssuerKey(t)

	ks, err := trustrule.ParseKeySet([]byte(k.jwks))
	if err != nil {
		t.Fatalf("key set: %v", err)
	}

	f := &exchangeFixture{
		rules: &fakeRuleStore{
			fakeStore: fakeStore{created: nil, entries: nil, deleted: nil},
			rules:     []trustrule.Rule{staticRule(k.jwks, "deploy", "prn-worker")},
			exchanges: nil, entries: nil, err: nil,
		},
		audit:  &memAudit{Store: nil, mu: sync.Mutex{}, entries: nil, server: nil, fail: 0, failServer: 0},
		keys:   &fakeKeys{keys: ks, err: nil, reads: 0},
		issuer: k,
		people: fakePrincipals{
			"prn-worker":   principalOf("prn-worker", principal.KindService, "acme"),
			"prn-other":    principalOf("prn-other", principal.KindService, "acme"),
			"prn-person":   principalOf("prn-person", principal.KindPerson, "acme"),
			"prn-workload": principalOf("prn-workload", principal.KindWorkload, "acme"),
			"prn-robot":    principalOf("prn-robot", principal.Kind("robot"), "acme"),
		},
		x: nil,
	}

	f.x, err = trustrulesvc.NewExchanger(trustrulesvc.ExchangeConfig{
		Rules: f.rules, Exchanges: f.rules, Namespaces: fakeNamespaces{}, Principals: f.people, Tokens: fakeTokens{},
		Audit: f.audit, ServerAudit: f.audit, Refusals: refusals, Keys: f.keys,
		InstallationURLs: []string{"https://dustpan.example.com"}, OwnIssuer: own,
		Now: now, Log: log,
	})
	if err != nil {
		t.Fatalf("exchanger: %v", err)
	}

	return f
}

// staticRule returns a static rule of acme with the keys jwks.
func staticRule(jwks, name, principalID string) trustrule.Rule {
	return trustrule.Rule{
		CreatedAt: exchangeNow(), ID: trustrule.NewID(), Namespace: "acme", Name: name, Issuer: staticIssuer,
		Audience: "https://dustpan.example.com", Keys: trustrule.KeySourceStatic, StaticKeys: jwks,
		PrincipalID: principalID, CreatedBy: "ada",
		Conditions: []trustrule.Condition{{Claim: "sub", Values: []string{"repo:acme/infra:ref:refs/heads/main"}}},
	}
}

// exchange runs one exchange of tok with no rule named.
func (f *exchangeFixture) exchange(t *testing.T, tok string) (trustrulesvc.ExchangeResult, error) {
	t.Helper()

	return f.x.Exchange(t.Context(), trustrulesvc.ExchangeInput{
		Namespace: "acme", Token: tok, Rule: "", Request: emptyEntry(),
	})
}

func TestExchangeIssuesATokenAndWritesItsEntry(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t)

	res, err := f.exchange(t, f.issuer.token(t, nil))
	if err != nil {
		t.Fatalf("Exchange = %v", err)
	}

	if res.Token.PrincipalID != "prn-worker" || res.Token.Kind != accesstoken.KindTrust ||
		res.Rule.ID != f.rules.rules[0].ID || len(f.rules.exchanges) != 1 {
		t.Fatalf("result = %+v, exchanges = %d", res, len(f.rules.exchanges))
	}

	checkExchangeEntry(t, f.rules.entries[0], res.Rule.ID)
}

// checkExchangeEntry fails the test unless e is the entry of the exchange
// through the rule id: the principal, the token, and the claims that name
// the run.
func checkExchangeEntry(t *testing.T, e audit.Entry, id trustrule.ID) {
	t.Helper()

	if e.Action != trustrulesvc.ActionExchange || e.Subject != "prn-worker" || e.TokenID != "tok-exchanged" ||
		e.Outcome != audit.OutcomeSuccess || e.ObjectID != string(id) {
		t.Fatalf("entry = %+v", e)
	}

	changed := map[string]string{}
	for _, c := range e.Changes {
		changed[c.Key] = c.New
	}

	if changed["subject"] != "repo:acme/infra:ref:refs/heads/main" || changed["jti"] != "run-1" ||
		changed["issuer"] != staticIssuer {
		t.Fatalf("changes = %v", changed)
	}
}

// TestExchangeRefusesEachBadToken sends a token that fails one check each,
// as if every other check had passed. Each answers ErrNoMatch, writes a
// refusal entry that names the check, and issues no token.
func TestExchangeRefusesEachBadToken(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, f *exchangeFixture) string{
		"another issuer": func(t *testing.T, f *exchangeFixture) string {
			t.Helper()

			return f.issuer.token(t, func(c map[string]any) { c["iss"] = "ci-cluster-8" })
		},
		"another key": func(t *testing.T, _ *exchangeFixture) string {
			t.Helper()

			return newIssuerKey(t).token(t, nil)
		},
		"another audience": func(t *testing.T, f *exchangeFixture) string {
			t.Helper()

			return f.issuer.token(t, func(c map[string]any) { c["aud"] = "https://other.example.com" })
		},
		"an expired token": func(t *testing.T, f *exchangeFixture) string {
			t.Helper()

			return f.issuer.token(t, func(c map[string]any) { c["exp"] = exchangeNow().Add(-time.Hour).Unix() })
		},
		"a token of another branch": func(t *testing.T, f *exchangeFixture) string {
			t.Helper()

			return f.issuer.token(t, func(c map[string]any) { c["sub"] = "repo:acme/infra:ref:refs/heads/evil" })
		},
		"alg none": func(t *testing.T, f *exchangeFixture) string {
			t.Helper()

			parts := strings.Split(f.issuer.token(t, nil), ".")
			header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))

			return header + "." + parts[1] + "."
		},
		"not a token": func(*testing.T, *exchangeFixture) string { return "odp_not-a-jwt" },
	}

	for name, tokenOf := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newExchangeFixture(t)
			tok := tokenOf(t, f)

			if _, err := f.exchange(t, tok); !errors.Is(err, trustrulesvc.ErrNoMatch) {
				t.Fatalf("Exchange = %v, want ErrNoMatch", err)
			}

			if len(f.rules.exchanges) != 0 {
				t.Fatal("the refused exchange issued a token")
			}

			entries := f.audit.all()
			if len(entries) != 1 || entries[0].Outcome != audit.OutcomeDenied || entries[0].Reason == "" {
				t.Fatalf("refusal entries = %+v, want one denied entry with a reason", entries)
			}

			if strings.Contains(entries[0].Reason, tok) {
				t.Fatal("the refusal entry holds the token")
			}
		})
	}
}

// TestExchangeRechecksThePrincipalAndTheIssuer changes the principal or the
// issuer of a rule after its create, as a restore or another version could.
// The exchange refuses the token.
func TestExchangeRechecksThePrincipalAndTheIssuer(t *testing.T) {
	t.Parallel()

	cases := map[string]func(f *exchangeFixture){
		"a disabled principal": func(f *exchangeFixture) {
			p := f.people["prn-worker"]
			p.DisabledAt = exchangeNow()
			f.people["prn-worker"] = p
		},
		"a deleted principal": func(f *exchangeFixture) { delete(f.people, "prn-worker") },
		"a person":            func(f *exchangeFixture) { f.rules.rules[0].PrincipalID = "prn-person" },
		"a principal of another namespace": func(f *exchangeFixture) {
			f.people["prn-worker"] = principalOf("prn-worker", principal.KindService, "other")
		},
		"an issuer of this installation": func(f *exchangeFixture) {
			f.rules.rules[0].Issuer = "https://dustpan.example.com/oidc/ns-1"
		},
	}

	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newExchangeFixture(t)
			tok := f.issuer.token(t, func(c map[string]any) { c["iss"] = f.rules.rules[0].Issuer })
			change(f)

			if f.rules.rules[0].Issuer != staticIssuer {
				tok = f.issuer.token(t, func(c map[string]any) { c["iss"] = f.rules.rules[0].Issuer })
			}

			if _, err := f.exchange(t, tok); !errors.Is(err, trustrulesvc.ErrNoMatch) {
				t.Fatalf("Exchange = %v, want ErrNoMatch", err)
			}

			if len(f.rules.exchanges) != 0 {
				t.Fatal("the refused exchange issued a token")
			}
		})
	}
}

// TestExchangeNeedsAServicePrincipal checks the kind check alone: each
// principal is of the namespace and not disabled, so only its kind refuses
// the exchange. The reason of the refusal entry names the check.
func TestExchangeNeedsAServicePrincipal(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"prn-person", "prn-workload", "prn-robot"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			f := newExchangeFixture(t)
			f.rules.rules[0].PrincipalID = id

			if _, err := f.exchange(t, f.issuer.token(t, nil)); !errors.Is(err, trustrulesvc.ErrNoMatch) {
				t.Fatalf("Exchange = %v, want ErrNoMatch", err)
			}

			if len(f.rules.exchanges) != 0 {
				t.Fatal("the refused exchange issued a token")
			}

			entries := f.audit.all()
			if len(entries) != 1 || !strings.Contains(entries[0].Reason, "not a service principal") {
				t.Fatalf("refusal entries = %+v, want the reason of the kind check", entries)
			}
		})
	}
}

// TestExchangeOfTwoMatchingRulesNeedsTheRule checks that a token that two
// rules accept is refused unless the request names one rule.
func TestExchangeOfTwoMatchingRulesNeedsTheRule(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t)
	second := staticRule(f.issuer.jwks, "other", "prn-other")
	f.rules.rules = append(f.rules.rules, second)

	tok := f.issuer.token(t, nil)

	if _, err := f.exchange(t, tok); !errors.Is(err, trustrulesvc.ErrAmbiguous) {
		t.Fatalf("Exchange = %v, want ErrAmbiguous", err)
	}

	res, err := f.x.Exchange(t.Context(), trustrulesvc.ExchangeInput{
		Namespace: "acme", Token: tok, Rule: second.ID, Request: emptyEntry(),
	})
	if err != nil || res.Token.PrincipalID != "prn-other" {
		t.Fatalf("Exchange of the named rule = %+v, %v; want the token of prn-other", res, err)
	}
}

// TestExchangeReadsTheKeysOfADiscoveryRule checks that a discovery rule
// reads its keys through the fetcher, and that a fetch that fails refuses
// the token.
func TestExchangeReadsTheKeysOfADiscoveryRule(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t)
	f.rules.rules[0].Keys, f.rules.rules[0].StaticKeys = trustrule.KeySourceDiscovery, ""
	f.rules.rules[0].Issuer = "https://token.actions.example.com"

	tok := f.issuer.token(t, func(c map[string]any) { c["iss"] = "https://token.actions.example.com" })

	if _, err := f.exchange(t, tok); err != nil {
		t.Fatalf("Exchange = %v", err)
	}

	if f.keys.reads != 1 {
		t.Fatalf("key reads = %d, want 1", f.keys.reads)
	}

	f.keys.err = trustrule.ErrKeysUnavailable

	if _, err := f.exchange(t, tok); !errors.Is(err, trustrulesvc.ErrNoMatch) {
		t.Fatalf("Exchange with no keys = %v, want ErrNoMatch", err)
	}
}

// syncBuffer keeps what a slog handler writes, guarded by a mutex: a handler
// may write from another goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.b.Write(p); err != nil {
		return 0, fmt.Errorf("write the log line: %w", err)
	}

	return len(p), nil
}

// lines returns the lines that the handler wrote so far.
func (s *syncBuffer) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	text := strings.TrimSuffix(s.b.String(), "\n")
	if text == "" {
		return nil
	}

	return strings.Split(text, "\n")
}

// TestExchangeWarnsOnceWhenTheKeysOfAnIssuerCannotBeRead sends three tokens
// of one issuer whose keys the fetcher cannot read: the exchange warns once
// in the minute of the warning limiter, and warns again in the next minute.
func TestExchangeWarnsOnceWhenTheKeysOfAnIssuerCannotBeRead(t *testing.T) {
	t.Parallel()

	const issuer = "https://token.actions.example.com"

	var (
		mu  sync.Mutex
		now = exchangeNow()
	)

	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		return now
	}

	var warnings syncBuffer

	f := newExchangeFixtureOwn(t, clock, nil, nil, slog.New(slog.NewTextHandler(&warnings, nil)))
	f.rules.rules[0].Keys, f.rules.rules[0].StaticKeys = trustrule.KeySourceDiscovery, ""
	f.rules.rules[0].Issuer = issuer
	f.keys.err = trustrule.ErrKeysUnavailable

	tok := f.issuer.token(t, func(c map[string]any) { c["iss"] = issuer })

	for range 3 {
		if _, err := f.exchange(t, tok); !errors.Is(err, trustrulesvc.ErrNoMatch) {
			t.Fatalf("Exchange = %v, want ErrNoMatch", err)
		}
	}

	lines := warnings.lines()
	if len(lines) != 1 {
		t.Fatalf("warnings = %q, want one warning for the issuer in the minute", lines)
	}

	if line := lines[0]; !strings.Contains(line, "level=WARN") ||
		!strings.Contains(line, "trust rule keys could not be read") ||
		!strings.Contains(line, "namespace=acme") || !strings.Contains(line, string(f.rules.rules[0].ID)) ||
		!strings.Contains(line, trustrule.ErrKeysUnavailable.Error()) {
		t.Fatalf("warning = %q, want the level, the message, the namespace, the rule and the error", line)
	}

	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()

	if _, err := f.exchange(t, tok); !errors.Is(err, trustrulesvc.ErrNoMatch) {
		t.Fatalf("Exchange = %v, want ErrNoMatch", err)
	}

	if lines := warnings.lines(); len(lines) != 2 {
		t.Fatalf("warnings = %q, want a second warning after the minute", lines)
	}
}

// TestExchangeBoundsTheRefusalEntries sends many bad tokens: the first
// writes an entry, and the others of the window write none.
func TestExchangeBoundsTheRefusalEntries(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t)

	for range 50 {
		if _, err := f.exchange(t, "not-a-token"); !errors.Is(err, trustrulesvc.ErrNoMatch) {
			t.Fatalf("Exchange = %v, want ErrNoMatch", err)
		}
	}

	if got := len(f.audit.all()); got != 1 {
		t.Fatalf("refusal entries = %d, want 1 for the window", got)
	}
}

// TestExchangeCountsTheRefusalsOfAWindow checks that the next window writes
// the count of the refusals that the window before it held back.
func TestExchangeCountsTheRefusalsOfAWindow(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		now = exchangeNow()
	)

	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		return now
	}

	f := newExchangeFixtureAt(t, clock)

	for range 10 {
		if _, err := f.exchange(t, "not-a-token"); !errors.Is(err, trustrulesvc.ErrNoMatch) {
			t.Fatalf("Exchange = %v, want ErrNoMatch", err)
		}
	}

	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()

	if _, err := f.exchange(t, "not-a-token"); !errors.Is(err, trustrulesvc.ErrNoMatch) {
		t.Fatalf("Exchange = %v, want ErrNoMatch", err)
	}

	entries := f.audit.all()

	found := false

	for _, e := range entries {
		if strings.Contains(e.Reason, "9 refused exchanges") {
			found = true
		}
	}

	if !found {
		t.Fatalf("entries = %+v, want one with the count of the 9 held back refusals", entries)
	}
}

// TestExchangeRefusesWhenTheStoreRefuses checks that a rule or a principal
// that the store refuses in its transaction refuses the token.
func TestExchangeRefusesWhenTheStoreRefuses(t *testing.T) {
	t.Parallel()

	for _, storeErr := range []error{trustrule.ErrNotFound, trustrule.ErrPrincipal} {
		f := newExchangeFixture(t)
		f.rules.err = storeErr

		if _, err := f.exchange(t, f.issuer.token(t, nil)); !errors.Is(err, trustrulesvc.ErrNoMatch) {
			t.Errorf("store %v: Exchange = %v, want ErrNoMatch", storeErr, err)
		}
	}
}

func TestNewExchangerNeedsItsPorts(t *testing.T) {
	t.Parallel()

	// Each port alone: a config with every port but the installation log.
	f := newExchangeFixture(t)

	_, err := trustrulesvc.NewExchanger(trustrulesvc.ExchangeConfig{
		Rules: f.rules, Exchanges: f.rules, Namespaces: fakeNamespaces{}, Principals: f.people, Tokens: fakeTokens{},
		Audit: f.audit, ServerAudit: nil, Refusals: nil, Keys: f.keys, InstallationURLs: nil, OwnIssuer: nil,
		Now: nil, Log: nil,
	})
	if !errors.Is(err, trustrule.ErrMissingDependency) {
		t.Fatalf("NewExchanger with no installation log = %v, want ErrMissingDependency", err)
	}

	_, err = trustrulesvc.NewExchanger(trustrulesvc.ExchangeConfig{
		Rules: nil, Exchanges: nil, Namespaces: nil, Principals: nil, Tokens: nil, Audit: nil, ServerAudit: nil,
		Refusals: nil, Keys: nil, InstallationURLs: nil, OwnIssuer: nil, Now: nil, Log: nil,
	})
	if !errors.Is(err, trustrule.ErrMissingDependency) {
		t.Fatalf("NewExchanger = %v, want ErrMissingDependency", err)
	}
}

// TestExchangeWritesTheEntryOfAnOversizeReason checks that a reason over the
// limit of the audit log is cut, so the refusal still writes its entry.
func TestExchangeWritesTheEntryOfAnOversizeReason(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t)

	header := `{"alg":"EdDSA","` + strings.Repeat("a", 600) + `\u2028":1}`
	tok := base64.RawURLEncoding.EncodeToString([]byte(header)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"`+staticIssuer+`"}`)) + ".c2ln"

	if _, err := f.exchange(t, tok); !errors.Is(err, trustrulesvc.ErrNoMatch) || errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("Exchange = %v, want ErrNoMatch and a written entry", err)
	}

	entries := f.audit.all()
	if len(entries) != 1 {
		t.Fatalf("refusal entries = %d, want 1", len(entries))
	}

	if err := audit.ValidateReason(entries[0].Reason); err != nil || entries[0].Reason == "" {
		t.Fatalf("reason %q: %v", entries[0].Reason, err)
	}
}

// TestExchangeRefusesARuleFieldThatIsNotARuleID checks the check of the
// service alone: the handler did not run, and the rule field holds a line
// break. The refusal writes its entry with no object ID.
func TestExchangeRefusesARuleFieldThatIsNotARuleID(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t)

	_, err := f.x.Exchange(t.Context(), trustrulesvc.ExchangeInput{
		Namespace: "acme", Token: f.issuer.token(t, nil), Rule: "trr-x\n", Request: emptyEntry(),
	})
	if !errors.Is(err, trustrulesvc.ErrNoMatch) {
		t.Fatalf("Exchange = %v, want ErrNoMatch", err)
	}

	if len(f.rules.exchanges) != 0 {
		t.Fatal("the refused exchange issued a token")
	}

	entries := f.audit.all()
	if len(entries) != 1 || entries[0].ObjectID != "" || !strings.Contains(entries[0].Reason, "rule field") {
		t.Fatalf("refusal entries = %+v, want one entry of the rule field with no object ID", entries)
	}
}

// TestExchangeNeverPutsAnInvalidRuleIDInTheLog checks the last check of the
// object of a refusal alone: the stored rule has an ID that is not a rule
// ID, its keys verify the token, and its principal is disabled.
func TestExchangeNeverPutsAnInvalidRuleIDInTheLog(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t)
	f.rules.rules[0].ID = "trr-x\n"

	p := f.people["prn-worker"]
	p.DisabledAt = exchangeNow()
	f.people["prn-worker"] = p

	if _, err := f.exchange(t, f.issuer.token(t, nil)); !errors.Is(err, trustrulesvc.ErrNoMatch) {
		t.Fatalf("Exchange = %v, want ErrNoMatch", err)
	}

	entries := f.audit.all()
	if len(entries) != 1 || entries[0].ObjectID != "" {
		t.Fatalf("refusal entries = %+v, want one entry with no object ID", entries)
	}
}

// TestExchangeJunkNeverHidesASignedRefusal checks the keys of the limiter:
// junk first, then a token that the keys of the rule verify for a subject
// that the rule does not accept. Each writes its own entry in one window.
func TestExchangeJunkNeverHidesASignedRefusal(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t)

	for range 5 {
		if _, err := f.exchange(t, "not-a-token"); !errors.Is(err, trustrulesvc.ErrNoMatch) {
			t.Fatalf("junk: Exchange = %v, want ErrNoMatch", err)
		}
	}

	fork := f.issuer.token(t, func(c map[string]any) { c["sub"] = "repo:evil/fork:ref:refs/heads/main" })

	if _, err := f.exchange(t, fork); !errors.Is(err, trustrulesvc.ErrNoMatch) {
		t.Fatalf("signed: Exchange = %v, want ErrNoMatch", err)
	}

	entries := f.audit.all()
	if len(entries) != 2 {
		t.Fatalf("refusal entries = %d, want one for the junk and one for the signed token", len(entries))
	}

	signed := entries[1]
	if !strings.Contains(signed.Reason, "repo:evil/fork") || signed.ObjectID != string(f.rules.rules[0].ID) {
		t.Fatalf("signed refusal = %+v, want its subject and its rule", signed)
	}

	// A second signed refusal for the same rule and subject is counted.
	if _, err := f.exchange(t, fork); !errors.Is(err, trustrulesvc.ErrNoMatch) {
		t.Fatalf("signed again: Exchange = %v, want ErrNoMatch", err)
	}

	if got := len(f.audit.all()); got != 2 {
		t.Fatalf("refusal entries = %d, want the same signed refusal counted in its window", got)
	}
}

// TestExchangeFailsClosedAndKeepsTheWindow checks a failed write of a
// refusal entry: the error reaches the caller, and the next refusal of the
// same window writes its entry with the count of the failed one.
func TestExchangeFailsClosedAndKeepsTheWindow(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t)
	f.audit.fail = 1

	_, err := f.exchange(t, "not-a-token")
	if !errors.Is(err, trustrulesvc.ErrNoMatch) || !errors.Is(err, errAuditDown) {
		t.Fatalf("Exchange with a failed entry = %v, want ErrNoMatch and the error of the write", err)
	}

	if _, err := f.exchange(t, "not-a-token"); err == nil || errors.Is(err, errAuditDown) {
		t.Fatalf("Exchange after the failure = %v, want ErrNoMatch", err)
	}

	entries := f.audit.all()
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Reason, "1 refused exchanges") {
		t.Fatalf("refusal entries = %+v, want one entry that counts the refusal of the failed write", entries)
	}
}

// TestExchangeCountsTheRefusalsBeyondTheKeysOfANamespace checks a flood of
// signed tokens with more subjects than the limiter keeps keys for one
// namespace: the refusals after the flood go to the aggregate of the
// namespace, and the next window writes their count in its audit log.
func TestExchangeCountsTheRefusalsBeyondTheKeysOfANamespace(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		now = exchangeNow()
	)

	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		return now
	}

	f := newExchangeFixtureAt(t, clock)

	refuse := func(sub string) {
		t.Helper()

		tok := f.issuer.token(t, func(c map[string]any) { c["sub"] = sub })
		if _, err := f.exchange(t, tok); !errors.Is(err, trustrulesvc.ErrNoMatch) {
			t.Fatalf("subject %s: Exchange = %v, want ErrNoMatch", sub, err)
		}
	}

	for i := range denylimit.DefaultMaxKeysPerPrincipal + 1 {
		refuse(fmt.Sprintf("repo:evil/fork:ref:refs/heads/b%d", i))
	}

	flood := len(f.audit.all())

	for i := range 5 {
		refuse(fmt.Sprintf("repo:victim-probe/p%d:ref:refs/heads/main", i))
	}

	if got := len(f.audit.all()); got != flood {
		t.Fatalf("entries after the probes = %d, want %d: the probes are held back in the window", got, flood)
	}

	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()

	refuse("repo:evil/fork:ref:refs/heads/next")

	counted := false

	for _, e := range f.audit.all()[flood:] {
		if e.Namespace == "acme" && strings.HasPrefix(e.Reason, "5 refused exchanges") {
			counted = true
		}
	}

	if !counted {
		t.Fatalf("entries after the window = %+v, want the count of the 5 held-back refusals", f.audit.all()[flood:])
	}
}

// TestExchangeGivesEachSubjectOfARuleItsOwnEntry checks the subject in the
// limiter key of a signed refusal: two subjects of one rule each write an
// entry in one window.
func TestExchangeGivesEachSubjectOfARuleItsOwnEntry(t *testing.T) {
	t.Parallel()

	f := newExchangeFixture(t)

	for _, sub := range []string{"repo:evil/fork:ref:refs/heads/a", "repo:other/fork:ref:refs/heads/b"} {
		tok := f.issuer.token(t, func(c map[string]any) { c["sub"] = sub })
		if _, err := f.exchange(t, tok); !errors.Is(err, trustrulesvc.ErrNoMatch) {
			t.Fatalf("subject %s: Exchange = %v, want ErrNoMatch", sub, err)
		}
	}

	entries := f.audit.all()
	if len(entries) != 2 || !strings.Contains(entries[0].Reason, "evil/fork") ||
		!strings.Contains(entries[1].Reason, "other/fork") {
		t.Fatalf("refusal entries = %+v, want one entry for each subject", entries)
	}
}

// TestExchangeWritesTheOverflowCountInTheLogOfTheInstallation fills a small
// limiter: the refusals of the overflow are counted in the log of the
// installation, and no entry of the namespace carries their count.
func TestExchangeWritesTheOverflowCountInTheLogOfTheInstallation(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		now = exchangeNow()
	)

	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		return now
	}

	limiter := denylimit.New(denylimit.Config{
		Window: time.Minute, MaxKeys: 2, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: clock,
	})
	f := newExchangeFixtureWith(t, clock, limiter)

	refuse := func(sub string) {
		t.Helper()

		tok := f.issuer.token(t, func(c map[string]any) { c["sub"] = sub })
		if _, err := f.exchange(t, tok); !errors.Is(err, trustrulesvc.ErrNoMatch) {
			t.Fatalf("subject %s: Exchange = %v, want ErrNoMatch", sub, err)
		}
	}

	// Two keys fill the limiter: the third subject opens the overflow, and
	// the next two are held back in it.
	for _, sub := range []string{"a", "b", "c", "d", "e"} {
		refuse("repo:evil/fork:ref:refs/heads/" + sub)
	}

	got := f.audit.serverEntries()
	if len(got) != 1 || !strings.Contains(got[0].Reason, "limiter of refused exchanges is full") {
		t.Fatalf("entries of the installation = %+v, want the entry that opens the overflow", got)
	}

	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()

	refuse("repo:evil/fork:ref:refs/heads/f")

	server := f.audit.serverEntries()
	if len(server) != 2 || !strings.HasPrefix(server[1].Reason, "2 refused exchanges of any namespace") ||
		server[1].Outcome != audit.OutcomeDenied || server[1].Action != trustrulesvc.ActionExchange {
		t.Fatalf("entries of the installation = %+v, want the count of the 2 held-back refusals", server)
	}

	for _, e := range f.audit.all() {
		if strings.Contains(e.Reason, "refused exchanges") {
			t.Fatalf("namespace entry %+v carries a count of the overflow", e)
		}
	}
}

// TestExchangeKeepsTheOverflowCountOutOfTheNamespace checks the entry that
// opens the next window of the overflow while it still carries a count: the
// count goes to the log of the installation, and the entry of the namespace
// has none. The limiter sweeps at most once in a quarter of a window, so a
// refusal just after the window opens the overflow before a sweep.
func TestExchangeKeepsTheOverflowCountOutOfTheNamespace(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		now = exchangeNow()
	)

	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		return now
	}

	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()

		now = now.Add(d)
	}

	limiter := denylimit.New(denylimit.Config{
		Window: time.Minute, MaxKeys: 2, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: clock,
	})
	f := newExchangeFixtureWith(t, clock, limiter)

	refuse := func(sub string) {
		t.Helper()

		tok := f.issuer.token(t, func(c map[string]any) { c["sub"] = "repo:evil/fork:ref:refs/heads/" + sub })
		if _, err := f.exchange(t, tok); !errors.Is(err, trustrulesvc.ErrNoMatch) {
			t.Fatalf("subject %s: Exchange = %v, want ErrNoMatch", sub, err)
		}
	}

	for _, sub := range []string{"a", "b", "c", "d", "e"} {
		refuse(sub)
	}

	advance(59 * time.Second)
	refuse("g")

	advance(2 * time.Second)
	refuse("h")

	server := f.audit.serverEntries()
	if len(server) != 2 || !strings.Contains(server[1].Reason, "3 refused exchanges of any namespace") {
		t.Fatalf("entries of the installation = %+v, want the second overflow entry with the count 3", server)
	}

	for _, e := range f.audit.all() {
		if strings.Contains(e.Reason, "refused exchanges") {
			t.Fatalf("namespace entry %+v carries a count of the overflow", e)
		}
	}
}

// TestExchangeFailsClosedWhenTheInstallationLogFails checks a failed write
// of the overflow entry in the log of the installation: the error reaches
// the answer, the limiter keeps the window, and the next refusal writes the
// entry with the count of the failed one.
func TestExchangeFailsClosedWhenTheInstallationLogFails(t *testing.T) {
	t.Parallel()

	limiter := denylimit.New(denylimit.Config{
		Window: time.Minute, MaxKeys: 2, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: exchangeNow,
	})
	f := newExchangeFixtureWith(t, exchangeNow, limiter)

	exchange := func(sub string) error {
		t.Helper()

		tok := f.issuer.token(t, func(c map[string]any) { c["sub"] = "repo:evil/fork:ref:refs/heads/" + sub })
		_, err := f.exchange(t, tok)

		return err
	}

	for _, sub := range []string{"a", "b"} {
		if err := exchange(sub); !errors.Is(err, trustrulesvc.ErrNoMatch) || errors.Is(err, errAuditDown) {
			t.Fatalf("subject %s: Exchange = %v, want ErrNoMatch", sub, err)
		}
	}

	f.audit.mu.Lock()
	f.audit.failServer = 1
	f.audit.mu.Unlock()

	if err := exchange("c"); !errors.Is(err, trustrulesvc.ErrNoMatch) || !errors.Is(err, errAuditDown) {
		t.Fatalf("overflow with a failed installation log: Exchange = %v, want ErrNoMatch and the error", err)
	}

	if err := exchange("d"); !errors.Is(err, trustrulesvc.ErrNoMatch) || errors.Is(err, errAuditDown) {
		t.Fatalf("overflow after the failure: Exchange = %v, want ErrNoMatch", err)
	}

	server := f.audit.serverEntries()
	if len(server) != 1 || !strings.Contains(server[0].Reason, "1 refused exchanges of any namespace") {
		t.Fatalf("entries of the installation = %+v, want one entry that carries the failed refusal", server)
	}
}

// errIssuerDown is the error of an issuer service that cannot answer.
var errIssuerDown = errors.New("the issuer store is down")

// fakeOwnIssuer answers whether an issuer is an issuer of this installation.
type fakeOwnIssuer struct {
	owns map[string]bool
	err  error
}

func (f fakeOwnIssuer) OwnsIssuer(_ context.Context, issuer string) (bool, error) {
	return f.owns[issuer], f.err
}

// TestExchangeAsksTheIssuerOfThisInstallation runs the check of the issuers
// of this installation at the exchange alone: the issuer of the rule is not
// under an installation URL, so only the issuer service of the server can
// refuse it. A namespace can set its issuerURL after the rule exists, so the
// exchange asks again. When the service cannot answer, the exchange refuses.
func TestExchangeAsksTheIssuerOfThisInstallation(t *testing.T) {
	t.Parallel()

	cases := map[string]fakeOwnIssuer{
		"an issuer that this installation signs for": {owns: map[string]bool{staticIssuer: true}, err: nil},
		"a service that cannot answer":               {owns: nil, err: errIssuerDown},
	}

	for name, own := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newExchangeFixtureOwn(t, exchangeNow, nil, own, slog.New(slog.DiscardHandler))

			if _, err := f.exchange(t, f.issuer.token(t, nil)); !errors.Is(err, trustrulesvc.ErrNoMatch) {
				t.Fatalf("Exchange = %v, want ErrNoMatch", err)
			}

			if len(f.rules.exchanges) != 0 {
				t.Fatal("the refused exchange issued a token")
			}

			entries := f.audit.all()
			if len(entries) != 1 || !strings.Contains(entries[0].Reason, "issuer") {
				t.Fatalf("refusal entries = %+v, want the refusal of the issuer", entries)
			}
		})
	}

	// The same rule exchanges when the service does not sign for the issuer.
	f := newExchangeFixtureOwn(t, exchangeNow, nil, fakeOwnIssuer{owns: map[string]bool{}, err: nil},
		slog.New(slog.DiscardHandler))
	if _, err := f.exchange(t, f.issuer.token(t, nil)); err != nil {
		t.Fatalf("Exchange with an issuer of another party = %v, want a token", err)
	}
}
