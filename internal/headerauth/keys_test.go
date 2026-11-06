package headerauth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/headerauth"
	"opendustpan.org/opendustpan/pkg/session"
)

// jwksOf returns a JWKS that holds the public key of s with the key ID of
// the tests.
func jwksOf(t *testing.T, s signer) []byte {
	t.Helper()

	set, err := json.Marshal(map[string]any{"keys": []any{jwkOf(t, s.public, keyID, s.alg)}})
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
	other := newKeyServer(t, map[string][]byte{"/jwks": jwksOf(t, attacker)})
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
	cfg.HTTPClient = &http.Client{Transport: answerFrom{from: from, body: jwksOf(t, attacker)}}
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
	ks := newKeyServer(t, map[string][]byte{"/jwks": jwksOf(t, good)})
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

// Layer 3: tokens with new key IDs of the AWS ALB read the key URL at most
// three times in KeyRetryAfter, for all new key IDs together.
func TestNewALBKeyIDsHaveAReadBudget(t *testing.T) {
	t.Parallel()

	setup := jwtSetup{preset: headerauth.PresetAWSALB, alg: es256, claim: ""}
	f := jwtFixture(setup, newIssuerKeys(t, es256))(t)

	for _, kid := range []string{"new-1", "new-2", "new-3", "new-4", "new-5"} {
		h := f.JWT.Header()
		h["kid"] = kid
		raw := f.JWT.Sign(t, h, f.JWT.Claims(alice()))

		_, err := f.Method.FromRequest(t.Context(), facts(map[string][]string{f.UserHeader: {raw}}))
		if !errors.Is(err, session.ErrMethodUnavailable) {
			t.Fatalf("kid %q: FromRequest() = %v, want %v", kid, err, session.ErrMethodUnavailable)
		}
	}

	if got := f.JWT.KeyRequests(); len(got) != 3 {
		t.Fatalf("the key URL got %v, want 3 reads", got)
	}
}

// albClock is an ALB test provider with two real keys and a clock that the
// test moves.
type albClock struct {
	method *headerauth.Method
	ks     *keyServer
	keys   map[string]signer
	now    *atomic.Int64
}

// newALBClock returns an ALB test provider whose key server serves the
// keys key-1 and key-2.
func newALBClock(t *testing.T) albClock {
	t.Helper()

	keys := map[string]signer{keyID: newSigner(t, es256), "key-2": newSigner(t, es256)}
	body := map[string][]byte{}

	for kid, s := range keys {
		body["/alb/"+kid] = encodeKey(t, s.public).pem
	}

	now := &atomic.Int64{}
	now.Store(fixedNow().UnixNano())

	ks := newKeyServer(t, body)
	cfg := jwtConfig(jwtSetup{preset: headerauth.PresetAWSALB, alg: es256, claim: ""}, ks, &atomic.Bool{})
	cfg.HTTPClient = ks.client(t, loopback())
	cfg.Now = func() time.Time { return time.Unix(0, now.Load()).UTC() }

	return albClock{method: newMethod(t, cfg), ks: ks, keys: keys, now: now}
}

// signIn sends a token with the key ID kid, signed with the key of kid or,
// for a key ID with no key, with key-1, at the clock of a.
func (a albClock) signIn(t *testing.T, kid string) error {
	t.Helper()

	s, ok := a.keys[kid]
	if !ok {
		s = a.keys[keyID]
	}

	at := time.Unix(0, a.now.Load()).UTC()
	shape := jwtShapes(jwtSetup{preset: headerauth.PresetAWSALB, alg: es256, claim: ""})
	h := shape.header()
	h["kid"], h["exp"] = kid, at.Add(time.Minute).Unix()
	raw := encodeToken(t, s, h, shape.claims(alice()), base64.URLEncoding)

	_, err := a.method.FromRequest(t.Context(), facts(map[string][]string{headerauth.AWSALBHeader: {raw}}))

	return err
}

// Layer 3: a stream of tokens with random key IDs, one in each
// KeyRetryAfter less one second, does not stop the sign-in with the key in
// use, also after KeyMaxAge.
func TestAStreamOfRandomALBKeyIDsDoesNotStopTheKeyInUse(t *testing.T) {
	t.Parallel()

	a := newALBClock(t)

	if err := a.signIn(t, keyID); err != nil {
		t.Fatalf("signIn() = %v", err)
	}

	step := headerauth.KeyRetryAfter - time.Second
	for i := 0; time.Duration(i)*step <= 2*headerauth.KeyMaxAge; i++ {
		a.now.Add(int64(step))

		if err := a.signIn(t, "random-"+strconv.Itoa(i)); !errors.Is(err, session.ErrMethodUnavailable) {
			t.Fatalf("random key ID: signIn() = %v, want %v", err, session.ErrMethodUnavailable)
		}

		if err := a.signIn(t, keyID); err != nil {
			t.Fatalf("after %s: signIn() with the key in use = %v", time.Duration(i)*step, err)
		}
	}
}

// Layer 3: a read of a random key ID does not block the first read of a
// real new key of the load balancer.
func TestARandomALBKeyIDDoesNotBlockARealNewKey(t *testing.T) {
	t.Parallel()

	a := newALBClock(t)

	if err := a.signIn(t, "random-1"); !errors.Is(err, session.ErrMethodUnavailable) {
		t.Fatalf("random key ID: signIn() = %v, want %v", err, session.ErrMethodUnavailable)
	}

	if err := a.signIn(t, "key-2"); err != nil {
		t.Fatalf("signIn() with a real new key = %v", err)
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

// Layer 3: the request that starts a read leaves when its own context
// ends, and does not wait for the timeout of the read.
func TestTheFirstRequestLeavesWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	good := newSigner(t, es256)
	ks := newKeyServer(t, map[string][]byte{"/jwks": jwksOf(t, good)})
	ks.hang.Store(true)

	cfg := jwtConfig(jwtSetup{preset: headerauth.PresetCustom, alg: es256, claim: ""}, ks, &atomic.Bool{})
	cfg.HTTPClient = ks.client(t, loopback())
	cfg.KeyTimeout = time.Minute
	m := newMethod(t, cfg)

	ctx, cancel := context.WithCancel(t.Context())
	raw := customToken(t, good, keyID)
	result := make(chan error, 1)

	go func() {
		_, err := m.FromRequest(ctx, customFacts(raw))
		result <- err
	}()

	<-ks.hits
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, session.ErrMethodUnavailable) {
			t.Fatalf("FromRequest() = %v, want %v", err, session.ErrMethodUnavailable)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the first request waited for the read after its context ended")
	}
}
