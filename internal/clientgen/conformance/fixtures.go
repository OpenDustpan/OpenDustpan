package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
)

// Namespace is the namespace that `opendustpan dev` makes. The cases use it.
const Namespace = "dev"

// The operations that the cases use by name.
const (
	opCapabilities = "getCapabilities"
	opListSinks    = "listNotificationSinks"
	opGetSink      = "getNotificationSink"
	opPreferences  = "getPreferences"
	opGetProfile   = "getProfile"
	opCreate       = "createProfile"
	opRevise       = "createProfileRevision"
	opPutApp       = "putGitHostApp"
)

// pathNamespace is the name of the namespace parameter in the paths.
const pathNamespace = "namespace"

// The paths that the harness reads directly.
const (
	sinksPath    = "/api/dustpan/v1/namespaces/" + Namespace + "/sinks"
	profilesPath = "/api/dustpan/v1/namespaces/" + Namespace + "/profiles"
)

// seedProfile is the name of the profile that the harness writes, so that the
// cases that read or revise a profile find one.
const seedProfile = "conformance-seed"

// staleVersionETag is an ETag that no profile has: a profile has one version
// for each write, so a version far ahead of it is never current.
const staleVersionETag = `"999999"`

// ErrExpectation means that a client did not do what the case expects.
var ErrExpectation = errors.New("conformance: the client did not do what the case expects")

// failf returns ErrExpectation with the text of what went wrong.
func failf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrExpectation, fmt.Sprintf(format, args...))
}

// callOf makes the parameters of a call with no query, no body and no headers.
func callOf(op string, path map[string]string) CallParams {
	return CallParams{Operation: op, Path: path, Query: nil, Body: nil, IdempotencyKey: "", IfMatch: ""}
}

// sinksCall makes the call of listNotificationSinks.
func sinksCall() CallParams {
	return callOf(opListSinks, map[string]string{pathNamespace: Namespace})
}

// sinkCall makes the call of getNotificationSink.
func sinkCall(id string) CallParams {
	return callOf(opGetSink, map[string]string{pathNamespace: Namespace, "sink_id": id})
}

// profileCall makes a call whose only path parameter, besides the namespace, is
// the profile.
func profileCall(op, profile string) CallParams {
	return callOf(op, map[string]string{pathNamespace: Namespace, "profile": profile})
}

// createCall makes the call of createProfile with a profile that no earlier
// call made.
func createCall(env Env, key string) CallParams {
	p := callOf(opCreate, map[string]string{pathNamespace: Namespace})
	p.Body, p.IdempotencyKey = env.profileBody(env.seed.next("conformance-new-"), "schemaVersion: 1\n"), key

	return p
}

// reviseCall makes the call of createProfileRevision. The document differs from
// the document of each earlier call.
func reviseCall(env Env, ifMatch string) CallParams {
	doc := "schemaVersion: 1\nvalues:\n  region: " + env.seed.next("eu-west-")
	p := profileCall(opRevise, seedProfile)
	p.Body, p.IfMatch = env.reviseBody(doc), ifMatch

	return p
}

// faultOf makes a fault with no setting but its kind and its operation.
func faultOf(kind FaultKind, op string) Fault {
	return Fault{
		Kind: kind, Operation: op, Status: 0, Body: nil, ContentType: "", RetryAfter: "", Field: "", Value: nil,
	}
}

// seedState holds what one run of the harness shares: the profile that the
// harness writes until one write succeeds, and a counter that makes names and
// documents.
type seedState struct {
	// mu serializes the seed write.
	mu sync.Mutex
	// written says that a seed write succeeded on a server at this URL. A
	// failed write leaves it false, so that the next call tries again. The
	// write reads the profile again before it trusts this value.
	written bool
	n       atomic.Int64
}

// fallbackSeed counts for an Env that no run made.
//
//nolint:gochecknoglobals // One shared counter, so that two such Envs never make the same name.
var fallbackSeed seedState

// fallbackSeeds holds the seed state of each server URL, for an Env that no run
// made. A server that gets the URL of an earlier, closed server finds the old
// state; the write reads the profile from it and writes the profile again when
// the server does not have it.
//
//nolint:gochecknoglobals // One state for each server, so that a second call finds the profile.
var (
	fallbackSeedsMu sync.Mutex
	fallbackSeeds   = map[string]*seedState{}
)

// fallbackSeedFor returns the shared seed state of a server address.
func fallbackSeedFor(addr string) *seedState {
	fallbackSeedsMu.Lock()
	defer fallbackSeedsMu.Unlock()

	s, ok := fallbackSeeds[addr]
	if !ok {
		s = new(seedState)
		fallbackSeeds[addr] = s
	}

	return s
}

// write writes the seed profile when no write has succeeded, or when the server
// does not have the profile any more (a 404 answer). It does not keep the error
// of a failed call, so that the next call tries again.
func (s *seedState) write(ctx context.Context, env Env) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.written {
		got, err := get(ctx, env, profilesPath+"/"+seedProfile, withToken)
		if err != nil {
			return err
		}

		switch got.status {
		case http.StatusOK:
			return nil
		case http.StatusNotFound:
			// Defense in depth: the server does not have the profile, so a new
			// server has this URL. Write the profile again.
			s.written = false
		default:
			return failf("the harness could not read the seed profile: %d %s", got.status, got.body)
		}
	}

	if err := writeSeed(ctx, env); err != nil {
		return err
	}

	s.written = true

	return nil
}

// next returns prefix and a number that no earlier call of next returned.
func (s *seedState) next(prefix string) string {
	if s == nil {
		s = &fallbackSeed
	}

	return prefix + strconv.FormatInt(s.n.Add(1), 10)
}

// profileBody returns the body of createProfile.
func (Env) profileBody(name, doc string) json.RawMessage {
	return mustJSON(map[string]string{"name": name, "document": doc})
}

// reviseBody returns the body of createProfileRevision.
func (Env) reviseBody(doc string) json.RawMessage {
	return mustJSON(map[string]string{"document": doc, "reason": "conformance"})
}

// mustJSON encodes a map of strings. Such a map always encodes.
func mustJSON(v map[string]string) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("conformance: encode a map of strings: %v", err))
	}

	return b
}

// answer is what the harness reads from the server.
type answer struct {
	status int
	header http.Header
	body   []byte
}

// authMode says whether a direct request carries the token.
type authMode int

const (
	_ authMode = iota // Zero means that nobody chose.
	// withToken sends the token of the server.
	withToken
	// withoutToken sends no Authorization header.
	withoutToken
)

// directRequest is a request that the harness sends to the server, not through
// the proxy.
type directRequest struct {
	method      string
	path        string
	auth        authMode
	contentType string
	body        []byte
}

// get reads a path from the server, with the token or without it.
func get(ctx context.Context, env Env, path string, auth authMode) (answer, error) {
	return direct(ctx, env, directRequest{method: http.MethodGet, path: path, auth: auth, contentType: "", body: nil})
}

// direct sends a request to the server, as the harness.
func direct(ctx context.Context, env Env, dr directRequest) (answer, error) {
	var out answer

	req, err := http.NewRequestWithContext(ctx, dr.method, env.Server.URL()+dr.path, bytes.NewReader(dr.body))
	if err != nil {
		return out, fmt.Errorf("new request: %w", err)
	}

	if dr.auth == withToken {
		req.Header.Set(authorizationHeader, "Bearer "+env.Server.Token())
	}

	if dr.contentType != "" {
		req.Header.Set("Content-Type", dr.contentType)
	}

	client := loopbackClient(DefaultCallTimeout)
	defer client.CloseIdleConnections()

	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("%s %s: %w", dr.method, dr.path, err)
	}

	defer resp.Body.Close() //nolint:errcheck // The body is read below.

	out.status, out.header = resp.StatusCode, resp.Header

	if out.body, err = readLimited(resp.Body); err != nil {
		return out, fmt.Errorf("read the answer: %w", err)
	}

	return out, nil
}

// ensureProfile writes the seed profile with the harness, so that the cases
// that read or revise it find one. It returns the current ETag of the profile.
func ensureProfile(ctx context.Context, env Env) (string, error) {
	seed := env.seed
	if seed == nil {
		// No run made this Env, so the seed of the server is the shared one for
		// its URL. A fresh state here would write the profile on each call.
		seed = fallbackSeedFor(env.Server.URL())
	}

	if err := seed.write(ctx, env); err != nil {
		return "", err
	}

	got, err := get(ctx, env, profilesPath+"/"+seedProfile, withToken)
	if err != nil {
		return "", err
	}

	if got.status != http.StatusOK || got.header.Get("ETag") == "" {
		return "", failf("the harness could not read the seed profile: %d %s", got.status, got.body)
	}

	return got.header.Get("ETag"), nil
}

// writeSeed makes the seed profile. A conflict means that another writer made
// the profile first. The write then succeeds when the server has the profile
// and answers with a usable ETag.
func writeSeed(ctx context.Context, env Env) error {
	post := directRequest{
		method:      http.MethodPost,
		path:        profilesPath,
		auth:        withToken,
		contentType: mediaJSON,
		body:        env.profileBody(seedProfile, "schemaVersion: 1\n"),
	}

	got, err := direct(ctx, env, post)
	if err != nil {
		return err
	}

	if got.status == http.StatusConflict {
		existing, readErr := get(ctx, env, profilesPath+"/"+seedProfile, withToken)
		if readErr != nil {
			return readErr
		}

		// Defense in depth: ensureProfile reads the profile again and checks the
		// same condition. This check stays so that a conflict is never counted as
		// success without a readable profile, even if a caller skips that read.
		if existing.status != http.StatusOK || existing.header.Get("ETag") == "" {
			return failf("the harness could not read the seed profile: %d %s", existing.status, existing.body)
		}

		return nil
	}

	if got.status != http.StatusOK && got.status != http.StatusCreated {
		return failf("the harness could not write the seed profile: %d %s", got.status, got.body)
	}

	return nil
}

// sameJSON says that two JSON documents have the same value.
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}

	return reflect.DeepEqual(x, y)
}

// fixture is one call of every-operation-is-called and what it expects.
type fixture struct {
	params CallParams
	// status is the expected status, or 0 for any 2xx status.
	status int
	// errorCode is the expected errorCode of an error answer.
	errorCode string
	// compare is a path that the harness reads directly, and whose JSON must
	// equal the body that the client returned.
	compare string
	// anonymous says that the harness reads compare with no token. A public
	// operation is compared like that, because the server answers a signed-in
	// caller with more members.
	anonymous bool
	// injected is an answer that the proxy gives in place of the server, for an
	// operation that the dev server cannot answer, for example one that needs
	// a Git host. The client must still send the request and check the answer.
	injected *Fault
}

// answerFixture expects an answer, and compares its body with a direct read.
func answerFixture(params CallParams, status int, compare string) fixture {
	return fixture{params: params, status: status, errorCode: "", compare: compare, anonymous: false, injected: nil}
}

// errorFixture expects an error answer.
func errorFixture(params CallParams, status int, errorCode string) fixture {
	return fixture{params: params, status: status, errorCode: errorCode, compare: "", anonymous: false, injected: nil}
}

// injectedFixture expects the answer that the proxy gives in place of the
// server.
func injectedFixture(params CallParams, status int, body string) fixture {
	f := faultOf(FaultStatus, params.Operation)
	f.Status, f.ContentType, f.Body = status, mediaJSON, []byte(body)

	return fixture{params: params, status: status, errorCode: "", compare: "", anonymous: false, injected: &f}
}

// fixtures returns the call of each operation that the suite knows.
func fixtures(ctx context.Context, env Env) (map[string]fixture, error) {
	etag, err := ensureProfile(ctx, env)
	if err != nil {
		return nil, err
	}

	public := answerFixture(callOf(opCapabilities, nil), http.StatusOK, capabilitiesPath)
	public.anonymous = true

	return map[string]fixture{
		opCapabilities: public,
		opListSinks:    answerFixture(sinksCall(), http.StatusOK, sinksPath),
		opGetSink:      errorFixture(sinkCall("missing"), http.StatusNotFound, "sink.not-found"),
		// The token of the dev server is not a person, so the server refuses.
		opPreferences: errorFixture(callOf(opPreferences, nil), http.StatusForbidden, "preferences.person-required"),
		opGetProfile: answerFixture(
			profileCall(opGetProfile, seedProfile), http.StatusOK, profilesPath+"/"+seedProfile,
		),
		opCreate: answerFixture(createCall(env, ""), 0, ""),
		opRevise: answerFixture(reviseCall(env, etag), 0, ""),
		// The dev server has no Git host, so the proxy answers in its place.
		opPutApp: injectedFixture(putAppCall(), http.StatusCreated, gitHostAppAnswer),
	}, nil
}

// checkFixture runs one fixture.
func checkFixture(ctx context.Context, env Env, op string, fx fixture) error {
	if fx.injected != nil {
		env.Proxy.Inject(*fx.injected)
	}

	res, err := env.Adapter.Call(ctx, fx.params)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	if statusErr := checkStatus(op, res, fx); statusErr != nil {
		return statusErr
	}

	if fx.compare == "" {
		return nil
	}

	auth := withToken
	if fx.anonymous {
		auth = withoutToken
	}

	want, err := get(ctx, env, fx.compare, auth)
	if err != nil {
		return err
	}

	if !sameJSON(res.Body, want.body) {
		return failf("%s: the body of the client is %s and the body of the server is %s", op, res.Body, want.body)
	}

	return nil
}

// checkStatus checks the status and the error of a result against a fixture.
func checkStatus(op string, res CallResult, fx fixture) error {
	if fx.status == 0 {
		if res.Status < http.StatusOK || res.Status >= http.StatusMultipleChoices || res.Error != nil {
			return failf("%s: want a 2xx status, got %d %+v", op, res.Status, res.Error)
		}

		return nil
	}

	if res.Status != fx.status {
		return failf("%s: want status %d, got %d %+v", op, fx.status, res.Status, res.Error)
	}

	if fx.errorCode == "" {
		return nil
	}

	e := res.Error
	if e == nil || e.ErrorCode != fx.errorCode || e.Code != fx.status || e.Message == "" {
		return failf("%s: want error %d %s with a message, got %+v", op, fx.status, fx.errorCode, e)
	}

	return nil
}

// escapedSegment is the path segment that the path-parameter case sends.
func escapedSegment() string { return url.PathEscape("a/b c%") }
