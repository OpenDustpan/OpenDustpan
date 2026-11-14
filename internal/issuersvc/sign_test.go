package issuersvc_test

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

func (h *harness) verify(value, aud string) (map[string]any, error) {
	h.t.Helper()

	return profiletest.VerifyToken(h.jwks(h.ns), value, profiletest.Expect{
		Now: h.clock.Now(), Issuer: h.issuer(h.ns), Audience: aud,
	})
}

func TestLoginSignsAVerifiableToken(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	tok := h.mustLogin()

	claims, err := h.verify(tok.Value, "sts.amazonaws.com")
	if err != nil {
		t.Fatalf("independent verifier refused the token: %v", err)
	}

	want := map[string]any{
		"iss": h.issuer(h.ns), "sub": defaultSubject, "aud": "sts.amazonaws.com", "namespace": "acme",
		"namespace_id": h.ns.ID.String(), "purpose": "login", "access": "write", "tool": "pulumi",
	}

	for k, v := range want {
		if claims[k] != v {
			t.Errorf("claim %s = %v, want %v", k, claims[k], v)
		}
	}

	iat, exp, nbf := num(t, claims, "iat"), num(t, claims, "exp"), num(t, claims, "nbf")
	if exp-iat != 300 || iat-nbf != 30 || int64(iat) != h.clock.Now().Unix() {
		t.Errorf("iat %v, nbf %v, exp %v: want a lifetime of 300 s and a skew of 30 s", iat, nbf, exp)
	}

	if tok.Issuer != h.issuer(h.ns) || tok.ID != claims["jti"] || tok.KeyID == "" ||
		!tok.ExpiresAt.Equal(time.Unix(int64(exp), 0)) {
		t.Errorf("token fields = %+v", tok)
	}

	checkHeader(t, tok.Value, tok.KeyID)
}

// checkHeader checks that the header of a token holds the three members of the
// issuer.
func checkHeader(t *testing.T, value string, kid profile.KeyID) {
	t.Helper()

	raw := mustB64(t, strings.Split(value, ".")[0])

	var header map[string]string
	if err := json.Unmarshal(raw, &header); err != nil || header["alg"] != "RS256" || header["typ"] != "JWT" ||
		header["kid"] != string(kid) || len(header) != 3 {
		t.Errorf("header = %v, %v", header, err)
	}
}

func TestLoginMakesTheFirstKeysAndRecordsWhy(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	if got := h.keysOf(h.ns); len(got) != 0 {
		t.Fatalf("a new namespace has %d keys", len(got))
	}

	h.mustLogin()

	keys := h.keysOf(h.ns)
	if len(keys) != 2 {
		t.Fatalf("keys after the first open = %d, want an active and a next key", len(keys))
	}

	list, err := h.store.List(t.Context(), h.ns.Name, audit.Page{Number: 1, Size: 10})
	if err != nil || len(list.Entries) == 0 {
		t.Fatalf("audit list = %v, %v", list, err)
	}

	first := list.Entries[len(list.Entries)-1]
	if first.Subject != alice || !strings.Contains(first.Reason, "first open") || first.Kind != audit.KindChange {
		t.Errorf("entry of the first keys = %+v", first)
	}
}

func TestTokenEntryHoldsTheFactsAndNeverTheToken(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	tok := h.mustLogin()

	if tok.Entry.Kind != audit.KindAccess || tok.Entry.Action != "profile:open" || tok.Entry.Namespace != "acme" {
		t.Fatalf("entry = %+v", tok.Entry)
	}

	got := map[string]string{}
	for _, c := range tok.Entry.Changes {
		got[c.Key] = c.New
	}

	for k, v := range map[string]string{
		"profile": "aws", "block": "aws", "audience": "sts.amazonaws.com", "subject": defaultSubject, "jti": tok.ID,
		"kid": string(tok.KeyID), "purpose": "login",
	} {
		if got[k] != v {
			t.Errorf("entry change %s = %q, want %q", k, got[k], v)
		}
	}

	text := fmt.Sprintf("%+v", tok.Entry)
	if strings.Contains(text, tok.Value) || strings.Contains(text, strings.Split(tok.Value, ".")[2]) {
		t.Error("the audit entry holds the token")
	}

	if _, err := h.store.Append(t.Context(), h.ns.Fence(), tok.Entry); err != nil {
		t.Errorf("the store refuses the entry of a token: %v", err)
	}
}

func TestLoginRefusesABadAudience(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	for name, aud := range map[string]string{
		"empty":                   "",
		"256 bytes":               strings.Repeat("a", 256),
		"newline":                 "sts\n.amazonaws.com",
		"nul":                     "sts\x00",
		"delete":                  "sts\x7f",
		"c1 control":              "sts\u0085",
		"the public URL":          "https://api.example.com",
		"the public URL slash":    "https://api.example.com/",
		"under the public URL":    "https://api.example.com/api/v1",
		"upper case public URL":   "HTTPS://API.EXAMPLE.COM/x",
		"public URL with query":   "https://api.example.com?x=1",
		"invalid UTF-8":           "sts\xff",
		"public URL with a slash": "https://api.example.com//",
	} {
		_, err := h.login(aud, 5*time.Minute)
		wantErr(t, "audience "+name, err, profile.ErrInvalid)
	}

	if got := h.keysOf(h.ns); len(got) != 0 {
		t.Errorf("a refused audience made %d keys", len(got))
	}

	for _, aud := range []string{"x", strings.Repeat("a", 255), "api://AzureADTokenExchange", "https://api.example.org"} {
		if _, err := h.login(aud, 5*time.Minute); err != nil {
			t.Errorf("audience %.20q refused: %v", aud, err)
		}
	}
}

func TestLoginRefusesALifetimeOutOfBounds(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	for name, d := range map[string]time.Duration{
		"zero": 0, "negative": -time.Minute, "59 seconds": 59 * time.Second, "61 minutes": 61 * time.Minute,
		"24 hours": 24 * time.Hour,
	} {
		_, err := h.login("sts.amazonaws.com", d)
		wantErr(t, "lifetime "+name, err, profile.ErrInvalid)
	}

	for _, d := range []time.Duration{time.Minute, 15 * time.Minute, 60 * time.Minute} {
		tok, err := h.login("sts.amazonaws.com", d)
		if err != nil {
			t.Fatalf("lifetime %s: %v", d, err)
		}

		if tok.ExpiresAt.Sub(tok.IssuedAt) != d {
			t.Errorf("lifetime = %s, want %s", tok.ExpiresAt.Sub(tok.IssuedAt), d)
		}
	}
}

func TestATokenNeverOutlivesItsLifetime(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	tok := h.mustLogin()

	h.clock.Advance(299 * time.Second)

	if _, err := h.verify(tok.Value, "sts.amazonaws.com"); err != nil {
		t.Fatalf("the token is refused before its exp: %v", err)
	}

	h.clock.Advance(time.Second)

	if _, err := h.verify(tok.Value, "sts.amazonaws.com"); err == nil {
		t.Error("the token passes at its exp")
	}

	h.clock.Advance(-10 * time.Minute)

	if _, err := h.verify(tok.Value, "sts.amazonaws.com"); err == nil {
		t.Error("the token passes before its nbf")
	}
}

func TestAWrongAudienceIsRefusedByTheVerifier(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	tok := h.mustLogin()

	for _, aud := range []string{"api://AzureADTokenExchange", "sts.amazonaws.com ", "STS.AMAZONAWS.COM", ""} {
		if _, err := h.verify(tok.Value, aud); err == nil {
			t.Errorf("the verifier accepts the audience %q", aud)
		}
	}
}

func TestLoginRefusesClaimsThatReplaceTheIssuer(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	for _, name := range []string{
		"iss", "sub", "aud", "iat", "nbf", "exp", "jti", "namespace", "namespace_id", "purpose", "Access",
		"camelCase", "has-dash", "", "1abc", "a.b", "https://evil.example/claim",
	} {
		o := h.open(h.ns, defaultSubject)
		o.Claims = map[string]any{name: "x"}

		_, err := h.svc.Login(t.Context(), o, "aws", "sts.amazonaws.com", 5*time.Minute)
		wantErr(t, "claim "+name, err, profile.ErrInvalid)
	}

	o := h.open(h.ns, defaultSubject)
	o.Claims = map[string]any{
		"https://aws.amazon.com/tags": map[string]any{"principal_tags": map[string]any{"a": []string{"b"}}},
	}

	if _, err := h.svc.Login(t.Context(), o, "aws", "sts.amazonaws.com", 5*time.Minute); err != nil {
		t.Errorf("the AWS tags claim is refused: %v", err)
	}
}

// TestLoginRefusesAuthenticationClaims sends an open whose claims hold an
// authentication claim. A relying party can read such a claim as a fresh or a
// stronger sign-in, so the issuer refuses the open and signs no token.
func TestLoginRefusesAuthenticationClaims(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	for name, value := range map[string]any{
		"auth_time": h.clock.Now().Unix(), "amr": []string{"mfa", "hwk"}, "acr": "phr",
	} {
		o := h.open(h.ns, defaultSubject)
		o.Claims[name] = value

		tok, err := h.svc.Login(t.Context(), o, "aws", "sts.amazonaws.com", 5*time.Minute)
		wantErr(t, "claim "+name, err, profile.ErrInvalid)

		if tok.Value != "" {
			t.Errorf("claim %s: a token was signed", name)
		}
	}
}

func TestLoginChecksTheSubject(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	for name, tc := range map[string]struct {
		sub  string
		want error
	}{
		"empty":     {"", profile.ErrInvalid},
		"newline":   {"namespace:acme\nx", profile.ErrInvalid},
		"too long":  {strings.Repeat("a", 601), profile.ErrSubjectTooLong},
		"600 bytes": {strings.Repeat("a", 600), nil},
	} {
		_, err := h.svc.Login(t.Context(), h.open(h.ns, tc.sub), "aws", "sts.amazonaws.com", 5*time.Minute)
		if tc.want == nil {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}

			continue
		}

		wantErr(t, name, err, tc.want)
	}
}

func TestAdapterTokenBindsTheBody(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	body := []byte(`{"op":"read"}`)

	tok, err := h.svc.Adapter(t.Context(), h.open(h.ns, defaultSubject), "ext", "https://adapter.example.com", body)
	if err != nil {
		t.Fatalf("Adapter: %v", err)
	}

	claims, err := h.verify(tok.Value, "https://adapter.example.com")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	sum := sha256.Sum256(body)
	if claims["purpose"] != "adapter" || claims["body_sha256"] != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Errorf("claims = %v", claims)
	}

	if exp, iat := num(t, claims, "exp"), num(t, claims, "iat"); exp-iat != 300 {
		t.Errorf("adapter lifetime = %v s, want 300", exp-iat)
	}

	login := h.mustLogin()

	other, err := h.verify(login.Value, "sts.amazonaws.com")
	if err != nil {
		t.Fatal(err)
	}

	if _, has := other["body_sha256"]; has {
		t.Error("a login token holds body_sha256")
	}
}

// forge builds a token with the given header and payload, signed by sign.
func forge(tb testing.TB, header, payload map[string]any, sign func(signing string) []byte) string {
	tb.Helper()

	enc := func(v any) string {
		return base64.RawURLEncoding.EncodeToString(mustJSON(tb, v))
	}

	signing := enc(header) + "." + enc(payload)

	return signing + "." + base64.RawURLEncoding.EncodeToString(sign(signing))
}

func TestAlgorithmDowngradeIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	tok := h.mustLogin()

	claims, err := h.verify(tok.Value, "sts.amazonaws.com")
	if err != nil {
		t.Fatal(err)
	}

	jwks := h.jwks(h.ns)

	var doc profile.JWKS
	if err = json.Unmarshal(jwks, &doc); err != nil {
		t.Fatal(err)
	}

	pub := h.keysOf(h.ns)[0].Public
	want := profiletest.Expect{Now: h.clock.Now(), Issuer: h.issuer(h.ns), Audience: "sts.amazonaws.com"}
	kid := string(tok.KeyID)

	for name, token := range map[string]string{
		"alg none":         forge(t, map[string]any{"alg": "none", "typ": "JWT", "kid": kid}, claims, func(string) []byte { return nil }),
		"alg None":         forge(t, map[string]any{"alg": "None", "kid": kid}, claims, func(string) []byte { return nil }),
		"alg empty":        forge(t, map[string]any{"alg": "", "kid": kid}, claims, func(string) []byte { return nil }),
		"no alg":           forge(t, map[string]any{"kid": kid}, claims, func(string) []byte { return nil }),
		"none with a body": tok.Value[:strings.LastIndex(tok.Value, ".")+1],
		"HS256 with the public key as the secret": forge(t, map[string]any{"alg": "HS256", "typ": "JWT", "kid": kid}, claims,
			func(s string) []byte { m := hmac.New(sha256.New, pub); m.Write([]byte(s)); return m.Sum(nil) }),
		"HS256 with the n value as the secret": forge(t, map[string]any{"alg": "HS256", "kid": kid}, claims,
			func(s string) []byte {
				m := hmac.New(sha256.New, []byte(doc.Keys[0].N))
				m.Write([]byte(s))
				return m.Sum(nil)
			}),
		"PS256": forge(t, map[string]any{"alg": "PS256", "kid": kid}, claims, func(string) []byte { return make([]byte, 256) }),
		"RS256 with a zero signature": forge(t, map[string]any{"alg": "RS256", "kid": kid}, claims,
			func(string) []byte { return make([]byte, 256) }),
		"RS256 with an unknown kid": forge(t, map[string]any{"alg": "RS256", "kid": "oik-aaaaaaaaaaaaaaaaaaaa"}, claims,
			func(string) []byte { return make([]byte, 256) }),
		"RS256 with no kid": forge(t, map[string]any{"alg": "RS256"}, claims, func(string) []byte { return make([]byte, 256) }),
	} {
		if _, verr := profiletest.VerifyToken(jwks, token, want); verr == nil {
			t.Errorf("%s: the verifier accepts the token", name)
		}
	}

	// Every token that the issuer signs holds the one algorithm.
	for range 3 {
		parts := strings.Split(h.mustLogin().Value, ".")
		raw := mustB64(t, parts[0])

		var header map[string]string
		if err = json.Unmarshal(raw, &header); err != nil || header["alg"] != "RS256" {
			t.Errorf("header = %s", raw)
		}
	}
}

func TestATamperedPayloadIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	tok := h.mustLogin()
	parts := strings.Split(tok.Value, ".")

	raw := mustB64(t, parts[1])
	changed := strings.Replace(string(raw), "write", "admin", 1)
	forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(changed)) + "." + parts[2]

	if _, err := h.verify(forged, "sts.amazonaws.com"); err == nil {
		t.Error("the verifier accepts a changed payload")
	}
}

func TestATokenOfAnotherNamespaceDoesNotVerify(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	other := h.addNamespace("globex")
	mine := h.mustLogin()

	theirs, err := h.svc.Login(t.Context(), h.open(other, "namespace:globex:access:write"), "aws",
		"sts.amazonaws.com", 5*time.Minute)
	if err != nil {
		t.Fatalf("Login in globex: %v", err)
	}

	if mine.KeyID == theirs.KeyID {
		t.Fatal("two namespaces share a key")
	}

	// A token of acme against the issuer and the keys of globex.
	for name, want := range map[string]profiletest.Expect{
		"globex issuer": {Now: h.clock.Now(), Issuer: h.issuer(other), Audience: "sts.amazonaws.com"},
		"acme issuer":   {Now: h.clock.Now(), Issuer: h.issuer(h.ns), Audience: "sts.amazonaws.com"},
	} {
		if _, verr := profiletest.VerifyToken(h.jwks(other), mine.Value, want); verr == nil {
			t.Errorf("a token of acme verifies against the keys of globex (%s)", name)
		}

		if _, verr := profiletest.VerifyToken(h.jwks(h.ns), theirs.Value, want); verr == nil {
			t.Errorf("a token of globex verifies against the keys of acme (%s)", name)
		}
	}

	// A token of acme that carries the kid of globex is a forgery.
	parts := strings.Split(mine.Value, ".")
	hdr := mustJSON(t, map[string]string{"alg": "RS256", "typ": "JWT", "kid": string(theirs.KeyID)})
	forged := base64.RawURLEncoding.EncodeToString(hdr) + "." + parts[1] + "." + parts[2]

	_, verr := profiletest.VerifyToken(h.jwks(other), forged, profiletest.Expect{
		Now: h.clock.Now(), Issuer: h.issuer(h.ns), Audience: "sts.amazonaws.com",
	})
	if verr == nil {
		t.Error("a token of acme with the kid of globex verifies")
	}

	if _, verr = h.verify(mine.Value, "sts.amazonaws.com"); verr != nil {
		t.Errorf("the token of acme does not verify at acme: %v", verr)
	}
}

func TestAStaleFenceSignsNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	stale := h.open(h.ns, defaultSubject)
	stale.FailoverVersion--

	_, err := h.svc.Login(t.Context(), stale, "aws", "sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login with a stale failover version", err, namespace.ErrFenced)

	_, err = h.svc.Adapter(t.Context(), stale, "ext", "x", nil)
	wantErr(t, "Adapter with a stale failover version", err, namespace.ErrFenced)

	wrongID := h.open(h.ns, defaultSubject)
	wrongID.NamespaceID = namespace.NewID()

	_, err = h.svc.Login(t.Context(), wrongID, "aws", "sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login with another namespace ID", err, namespace.ErrFenced)

	if got := h.keysOf(h.ns); len(got) != 0 {
		t.Errorf("a refused open made %d keys", len(got))
	}
}

func TestNoIssuerHostNoToken(t *testing.T) {
	t.Parallel()

	h := newHarness(t, func(c *issuerConfig, _ *harness) { c.IssuerBaseURL, c.PublicURL = "", "" })

	_, err := h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login", err, profile.ErrIssuerNotConfigured)

	_, err = h.svc.Discovery(t.Context(), h.ns.ID)
	wantErr(t, "Discovery", err, profile.ErrIssuerNotConfigured)

	if !strings.Contains(err.Error(), "issuerBaseURL") || !strings.Contains(err.Error(), "publicURL") {
		t.Errorf("the error does not name the two settings: %v", err)
	}
}

func TestPublicURLIsTheHostWhenNoIssuerBaseURL(t *testing.T) {
	t.Parallel()

	h := newHarness(
		t,
		func(c *issuerConfig, _ *harness) { c.IssuerBaseURL, c.PublicURL = "", "https://api.example.com/" },
	)

	d, err := h.svc.Discovery(t.Context(), h.ns.ID)
	if err != nil || d.Issuer != "https://api.example.com/oidc/"+h.ns.ID.String() {
		t.Errorf("Discovery = %+v, %v", d, err)
	}
}

func TestSignatureIsPKCS1v15OverSHA256(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	tok := h.mustLogin()
	parts := strings.Split(tok.Value, ".")

	pub, err := h.keysOf(h.ns)[0].PublicKey()
	if err != nil {
		t.Fatal(err)
	}

	for _, k := range h.keysOf(h.ns) {
		if k.ID != tok.KeyID {
			continue
		}

		if pub, err = k.PublicKey(); err != nil {
			t.Fatal(err)
		}
	}

	sig := mustB64(t, parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))

	if err = rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Errorf("signature: %v", err)
	}

	if pub.N.BitLen() != 2048 {
		t.Errorf("key size = %d", pub.N.BitLen())
	}
}
