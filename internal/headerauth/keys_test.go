package headerauth_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/headerauth"
	"opendustpan.org/opendustpan/pkg/session"
)

// jwksOf returns a JWKS that holds the public key of s with the key ID kid.
func jwksOf(t *testing.T, s signer, kid string) []byte {
	t.Helper()

	set, err := json.Marshal(map[string]any{"keys": []any{jwkOf(t, s.public, kid, s.alg)}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return set
}

// customToken returns a token of the custom test issuer, signed by s, with
// the key ID kid.
func customToken(t *testing.T, s signer, kid string) string {
	t.Helper()

	shape := jwtShapes(jwtSetup{preset: headerauth.PresetCustom, alg: s.alg, claim: ""})
	h := shape.header()
	h["kid"] = kid

	return encodeToken(t, s, h, shape.claims(alice()), base64.RawURLEncoding)
}

// customFacts returns the facts of a request with the token raw of the
// custom test issuer.
func customFacts(raw string) session.RequestFacts {
	return facts(map[string][]string{"X-Auth-Token": {raw}})
}

// Layer 2, contain: the key fetch follows no redirect, also when the client
// of the caller follows redirects. Another host that serves keys never
// signs a person in.
func TestTheKeyFetchRefusesARedirect(t *testing.T) {
	t.Parallel()

	attacker := newSigner(t, es256)
	other := newKeyServer(t, map[string][]byte{"/jwks": jwksOf(t, attacker, keyID)})
	ks := newKeyServer(t, map[string][]byte{})
	ks.redirect["/jwks"] = other.server.URL + "/jwks"

	// The client of the test server follows redirects and trusts both
	// servers.
	client := ks.server.Client()
	cfg := jwtConfig(jwtSetup{preset: headerauth.PresetCustom, alg: es256, claim: ""}, ks, &atomic.Bool{})
	cfg.HTTPClient = client
	m := newMethod(t, cfg)

	_, err := m.FromRequest(t.Context(), customFacts(customToken(t, attacker, keyID)))
	if !errors.Is(err, session.ErrMethodUnavailable) {
		t.Fatalf("FromRequest() = %v, want %v", err, session.ErrMethodUnavailable)
	}

	if got := other.requests(); len(got) != 0 {
		t.Fatalf("the other host got %v, want no request", got)
	}

	if client.CheckRedirect != nil {
		t.Fatal("New changed the redirect policy of the client of the caller")
	}
}

// answerFrom is a RoundTripper that answers each request with body, as if
// it came from the URL from: a transport that follows a redirect itself.
type answerFrom struct {
	from *url.URL
	body []byte
}

func (a answerFrom) RoundTrip(req *http.Request) (*http.Response, error) {
	moved := req.Clone(req.Context())
	moved.URL = a.from

	return &http.Response{
		Status: "200 OK", StatusCode: http.StatusOK, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(a.body)), ContentLength: int64(len(a.body)),
		TransferEncoding: nil, Close: false, Uncompressed: false, Trailer: nil, Request: moved, TLS: nil,
	}, nil
}

// Layer 2, defense in depth: an answer whose request is not the key URL
// gives no keys, as if the redirect policy of the client had failed.
func TestAnAnswerFromAnotherURLGivesNoKeys(t *testing.T) {
	t.Parallel()

	attacker := newSigner(t, es256)
	from, err := url.Parse("https://evil.example.com/jwks")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	ks := newKeyServer(t, map[string][]byte{})
	cfg := jwtConfig(jwtSetup{preset: headerauth.PresetCustom, alg: es256, claim: ""}, ks, &atomic.Bool{})
	cfg.HTTPClient = &http.Client{Transport: answerFrom{from: from, body: jwksOf(t, attacker, keyID)}}
	m := newMethod(t, cfg)

	_, err = m.FromRequest(t.Context(), customFacts(customToken(t, attacker, keyID)))
	if !errors.Is(err, session.ErrMethodUnavailable) {
		t.Fatalf("FromRequest() = %v, want %v", err, session.ErrMethodUnavailable)
	}
}

// Layer 3, limit the effect: while a read of the key URL hangs, a token
// with a cached key signs in, and the requests that need the read wait for
// the one read.
func TestAHangingKeyReadDoesNotBlockACachedKey(t *testing.T) {
	t.Parallel()

	var now atomic.Int64

	now.Store(fixedNow().UnixNano())

	good := newSigner(t, es256)
	ks := newKeyServer(t, map[string][]byte{"/jwks": jwksOf(t, good, keyID)})
	cfg := jwtConfig(jwtSetup{preset: headerauth.PresetCustom, alg: es256, claim: ""}, ks, &atomic.Bool{})
	cfg.HTTPClient = ks.client(t, loopback())
	cfg.Now = func() time.Time { return time.Unix(0, now.Load()).UTC() }
	cfg.KeyTimeout = 2 * time.Second
	m := newMethod(t, cfg)

	known, unknown := customToken(t, good, keyID), customToken(t, good, "unknown")

	if _, err := m.FromRequest(t.Context(), customFacts(known)); err != nil {
		t.Fatalf("FromRequest() = %v", err)
	}

	<-ks.hits

	now.Add(int64(headerauth.KeyRetryAfter))
	ks.hang.Store(true)

	const waiters = 3

	results := make(chan error, waiters)
	for range waiters {
		go func() {
			_, err := m.FromRequest(t.Context(), customFacts(unknown))
			results <- err
		}()
	}

	<-ks.hits

	if _, err := m.FromRequest(t.Context(), customFacts(known)); err != nil {
		t.Fatalf("FromRequest() with a cached key = %v while a read hangs", err)
	}

	select {
	case err := <-results:
		t.Fatalf("a request ended with %v before the read ended: the cached key waited for the read", err)
	default:
	}

	for range waiters {
		if err := <-results; !errors.Is(err, session.ErrMethodUnavailable) && !errors.Is(err, session.ErrMethodAnswer) {
			t.Fatalf("FromRequest() with an unknown key = %v, want a refusal", err)
		}
	}

	if got := ks.requests(); len(got) != 2 {
		t.Fatalf("the key URL got %v, want 2 reads", got)
	}
}

// Layer 3: tokens with new key IDs of the AWS ALB read the key URL once in
// KeyRetryAfter, for all key IDs together.
func TestNewALBKeyIDsReadTheKeyURLOnceInTheRetryTime(t *testing.T) {
	t.Parallel()

	setup := jwtSetup{preset: headerauth.PresetAWSALB, alg: es256, claim: ""}
	f := jwtFixture(setup, newIssuerKeys(t, es256))(t)

	for _, kid := range []string{"new-1", "new-2", "new-3", keyID} {
		h := f.JWT.Header()
		h["kid"] = kid
		raw := f.JWT.Sign(t, h, f.JWT.Claims(alice()))

		_, err := f.Method.FromRequest(t.Context(), facts(map[string][]string{f.UserHeader: {raw}}))
		if !errors.Is(err, session.ErrMethodUnavailable) {
			t.Fatalf("kid %q: FromRequest() = %v, want %v", kid, err, session.ErrMethodUnavailable)
		}
	}

	if got := f.JWT.KeyRequests(); len(got) != 1 {
		t.Fatalf("the key URL got %v, want one read", got)
	}
}

// Layer 2: the client of the header of an AWS ALB token must be the
// configured client ID.
func TestAnALBClientThatIsNotTheConfiguredClientIsErrMethodAnswer(t *testing.T) {
	t.Parallel()

	f := jwtFixture(jwtSetup{preset: headerauth.PresetAWSALB, alg: es256, claim: ""}, newIssuerKeys(t, es256))(t)

	for _, client := range []any{"another-client", nil, 7} {
		h := f.JWT.Header()
		if client == nil {
			delete(h, "client")
		} else {
			h["client"] = client
		}

		raw := f.JWT.Sign(t, h, f.JWT.Claims(alice()))

		_, err := f.Method.FromRequest(t.Context(), facts(map[string][]string{f.UserHeader: {raw}}))
		if !errors.Is(err, session.ErrMethodAnswer) {
			t.Fatalf("client %v: FromRequest() = %v, want %v", client, err, session.ErrMethodAnswer)
		}
	}
}
