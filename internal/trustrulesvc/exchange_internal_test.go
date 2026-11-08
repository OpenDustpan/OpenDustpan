package trustrulesvc

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// The two issuer checks of an exchange read the same claim, so each masks
// the other when they run together. These tests run each one alone.

// internalNow is the clock of the internal tests.
func internalNow() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}

// signedRule returns a static rule of the issuer named by issuer and a token
// of tokenIssuer that the keys of the rule verify.
func signedRule(t *testing.T, issuer, tokenIssuer string) (trustrule.Rule, string) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	enc := base64.RawURLEncoding
	jwks := `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","x":"` + enc.EncodeToString(pub) + `"}]}`

	claims, err := json.Marshal(map[string]any{
		"iss": tokenIssuer, "aud": "https://dustpan.example.com", "sub": "repo:acme/infra:ref:refs/heads/main",
		"iat": internalNow().Unix(), "exp": internalNow().Add(5 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("encode claims: %v", err)
	}

	input := enc.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT","kid":"k1"}`)) + "." + enc.EncodeToString(claims)
	tok := input + "." + enc.EncodeToString(ed25519.Sign(priv, []byte(input)))

	r := trustrule.Rule{
		CreatedAt: internalNow(), ID: trustrule.NewID(), Namespace: "acme", Name: "deploy", Issuer: issuer,
		Audience: "https://dustpan.example.com", Keys: trustrule.KeySourceStatic, StaticKeys: jwks,
		PrincipalID: "prn-worker", CreatedBy: "ada",
		Conditions: []trustrule.Condition{{Claim: "sub", Values: []string{"repo:acme/infra:ref:refs/heads/main"}}},
	}

	return r, tok
}

// bareExchanger returns an exchanger with only its clock, its warning
// limiter and a logger that discards: check and candidates of a static rule
// need nothing else.
func bareExchanger() *Exchanger {
	return &Exchanger{
		cfg: ExchangeConfig{
			Rules: nil, Exchanges: nil, Namespaces: nil, Principals: nil, Tokens: nil, Audit: nil, ServerAudit: nil,
			Refusals: nil, Keys: nil, InstallationURLs: nil, OwnIssuer: nil, Now: nil, Log: nil,
		},
		own: nil, refusals: nil,
		warnings: denylimit.New(denylimit.Config{
			Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: internalNow,
		}),
		log: slog.New(slog.DiscardHandler), now: internalNow,
	}
}

// TestCheckRefusesATokenOfAnotherIssuer runs the issuer check of check
// alone: the rule was not selected by the issuer of the token.
func TestCheckRefusesATokenOfAnotherIssuer(t *testing.T) {
	t.Parallel()

	r, tok := signedRule(t, "ci-cluster-7", "ci-cluster-8")

	_, verified, err := bareExchanger().check(t.Context(), &r, tok, "k1")
	if !verified || !errors.Is(err, trustrule.ErrToken) || !strings.Contains(err.Error(), "iss") {
		t.Fatalf("check = verified %v, %v; want a verified token refused by its iss claim", verified, err)
	}

	same, tok := signedRule(t, "ci-cluster-7", "ci-cluster-7")
	if _, _, err := bareExchanger().check(t.Context(), &same, tok, "k1"); err != nil {
		t.Fatalf("check of a token of the issuer of the rule = %v", err)
	}
}

// TestCandidatesSelectsTheRulesOfTheIssuer runs the issuer filter of
// candidates alone: a rule of another issuer is never checked, also when
// its keys would verify the token.
func TestCandidatesSelectsTheRulesOfTheIssuer(t *testing.T) {
	t.Parallel()

	r, tok := signedRule(t, "ci-cluster-7", "ci-cluster-7")
	in := ExchangeInput{Namespace: "acme", Token: tok, Rule: "", Request: emptyInternalEntry()}

	if got := bareExchanger().candidates(t.Context(), []trustrule.Rule{r}, in, "ci-cluster-8", "k1"); len(got) != 0 {
		t.Fatalf("candidates of another issuer = %d, want 0", len(got))
	}

	if got := bareExchanger().candidates(t.Context(), []trustrule.Rule{r}, in, "ci-cluster-7", "k1"); len(got) != 1 {
		t.Fatalf("candidates of the issuer = %d, want 1", len(got))
	}
}

// emptyInternalEntry is the request entry of the internal tests.
func emptyInternalEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "",
		Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

// TestCleanMakesAValidReason runs clean alone on text that no reason of the
// exchange holds today: raw hidden characters, many-byte characters, and
// bytes that are not UTF-8. Each output passes the check of the audit log.
func TestCleanMakesAValidReason(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"a line break":             "a\nb",
		"a line separator":         "a\u2028b",
		"a paragraph separator":    "a\u2029b",
		"a direction override":     "a\u202eb",
		"many-byte characters":     strings.Repeat("é", audit.MaxReasonLen+10),
		"hidden characters only":   strings.Repeat("\u2028", audit.MaxReasonLen),
		"bytes that are not UTF-8": "a\xffb",
	}

	for name, in := range cases {
		out := clean(in, audit.MaxReasonLen)
		if err := audit.ValidateReason(out); err != nil || out == "" {
			t.Errorf("%s: clean = %q, %v; want a valid reason", name, out, err)
		}
	}
}

// TestFlushRefusesACountThatNamesNoNamespace runs the namespace check of
// flush alone: a held-back count of an aggregate whose limiter principal
// does not name a valid namespace gives an error and reads no namespace.
// The exchanger has no namespace port, so a read would panic.
func TestFlushRefusesACountThatNamesNoNamespace(t *testing.T) {
	t.Parallel()

	for _, p := range []string{refusedSubject + ":Not A Namespace", refusedSubject + "-signed:../acme"} {
		pending := []denylimit.Pending{{
			Key: denylimit.Key{
				Principal: p, Action: denylimit.AggregateMark, Kind: denylimit.AggregateMark,
				Scope: denylimit.AggregateMark,
			},
			Suppressed: 3, Overflow: false,
		}}

		if err := bareExchanger().flush(t.Context(), emptyInternalEntry(), pending); err == nil {
			t.Errorf("flush of the aggregate of %q = nil, want an error", p)
		}
	}

	if got := namespaceOfLimiterPrincipal(refusedSubject + "-signed:acme"); got != "acme" {
		t.Errorf("namespace of the signed principal = %q, want acme", got)
	}

	if got := namespaceOfLimiterPrincipal("bo"); got != "" {
		t.Errorf("namespace of another principal = %q, want none", got)
	}
}
