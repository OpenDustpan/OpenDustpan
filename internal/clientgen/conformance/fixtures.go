package conformance

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
)

// Namespace is the namespace that `opendustpan dev` makes. The cases use it.
const Namespace = "dev"

// The operations that the cases use by name.
const (
	opCapabilities = "getCapabilities"
	opListSinks    = "listNotificationSinks"
	opGetSink      = "getNotificationSink"
	opPreferences  = "getPreferences"
	opGetApp       = "getGitHostApp"
	opPutApp       = "putGitHostApp"
)

// The paths that the harness reads directly.
const (
	sinksPath = "/api/dustpan/v1/namespaces/" + Namespace + "/sinks"
	appPath   = "/api/dustpan/v1/git-host-apps/" + gitHostKind
)

// gitHostKind is the kind of Git host app that the cases write.
const gitHostKind = "github"

// Sizes of the Git host app that the cases write.
const (
	appKeyBits       = 2048
	appSecretLength  = 40
	staleVersionETag = `"999999"`
)

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
	return callOf(opListSinks, map[string]string{"namespace": Namespace})
}

// sinkCall makes the call of getNotificationSink.
func sinkCall(id string) CallParams {
	return callOf(opGetSink, map[string]string{"namespace": Namespace, "sink_id": id})
}

// appCall makes a call whose only path parameter is the kind of the app.
func appCall(op string) CallParams {
	return callOf(op, map[string]string{"kind": gitHostKind})
}

// faultOf makes a fault with no setting but its kind and its operation.
func faultOf(kind FaultKind, op string) Fault {
	return Fault{
		Kind: kind, Operation: op, Status: 0, Body: nil, ContentType: "", RetryAfter: "", Field: "", Value: nil,
	}
}

// appFixture makes the body of putGitHostApp. The key takes a moment to make,
// so a run makes it once.
type appFixture struct {
	once sync.Once
	body json.RawMessage
	err  error
}

// appBody returns the body of putGitHostApp.
func (e Env) appBody() (json.RawMessage, error) {
	app := e.app
	if app == nil {
		app = new(appFixture)
	}

	app.once.Do(func() { app.body, app.err = makeAppBody() })

	return app.body, app.err
}

func makeAppBody() (json.RawMessage, error) {
	key, err := rsa.GenerateKey(rand.Reader, appKeyBits)
	if err != nil {
		return nil, fmt.Errorf("make a key: %w", err)
	}

	block := &pem.Block{Type: "RSA PRIVATE KEY", Headers: nil, Bytes: x509.MarshalPKCS1PrivateKey(key)}

	body, err := json.Marshal(map[string]any{
		"apiAddress": "https://api.github.com", "appID": "123",
		"eventSecret": strings.Repeat("s", appSecretLength), "privateKey": string(pem.EncodeToMemory(block)),
	})
	if err != nil {
		return nil, fmt.Errorf("encode the body: %w", err)
	}

	return body, nil
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
	// withToken sends the token of the server.
	withToken authMode = iota
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

// ensureApp writes the Git host app with the harness, so that the cases that
// read it find one. It returns the ETag of the app.
func ensureApp(ctx context.Context, env Env) (string, error) {
	body, err := env.appBody()
	if err != nil {
		return "", err
	}

	put := directRequest{
		method:      http.MethodPut,
		path:        appPath,
		auth:        withToken,
		contentType: "application/json",
		body:        body,
	}

	got, err := direct(ctx, env, put)
	if err != nil {
		return "", err
	}

	if got.status != http.StatusOK && got.status != http.StatusCreated {
		return "", failf("the harness could not write the Git host app: %d %s", got.status, got.body)
	}

	return got.header.Get("ETag"), nil
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
}

// answerFixture expects an answer, and compares its body with a direct read.
func answerFixture(params CallParams, status int, compare string) fixture {
	return fixture{params: params, status: status, errorCode: "", compare: compare, anonymous: false}
}

// errorFixture expects an error answer.
func errorFixture(params CallParams, status int, errorCode string) fixture {
	return fixture{params: params, status: status, errorCode: errorCode, compare: "", anonymous: false}
}

// fixtures returns the call of each operation that the suite knows.
func fixtures(env Env) (map[string]fixture, error) {
	body, err := env.appBody()
	if err != nil {
		return nil, err
	}

	put := appCall(opPutApp)
	put.Body = body

	public := answerFixture(callOf(opCapabilities, nil), http.StatusOK, capabilitiesPath)
	public.anonymous = true

	return map[string]fixture{
		opCapabilities: public,
		opListSinks:    answerFixture(sinksCall(), http.StatusOK, sinksPath),
		opGetSink:      errorFixture(sinkCall("missing"), http.StatusNotFound, "sink.not-found"),
		// The token of the dev server is not a person, so the server refuses.
		opPreferences: errorFixture(callOf(opPreferences, nil), http.StatusForbidden, "preferences.person-required"),
		opGetApp:      answerFixture(appCall(opGetApp), http.StatusOK, appPath),
		opPutApp:      answerFixture(put, 0, ""),
	}, nil
}

// checkFixture runs one fixture.
func checkFixture(ctx context.Context, env Env, op string, fx fixture) error {
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
