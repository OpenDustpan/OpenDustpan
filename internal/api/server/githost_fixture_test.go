package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	hostAppPath           = "/api/dustpan/v1/git-host-apps/github"
	hostInstallationsPath = "/api/dustpan/v1/git-host-installations"
	hostConnectionsPath   = "/api/dustpan/v1/namespaces/acme/git-host-connections"
	hostKeyCanary         = "HOSTKEYCANARY-private-key-bytes"
	hostSecretCanary      = "HOSTEVENTCANARY-0123456789abcdef0123456789"
	hostAPIAddress        = "https://api.github.com"
	hostAppID             = "12345"
	hostRemote            = "192.0.2.10:1234"
)

// hostAuthorizer allows the Git host actions of the installation, and the
// actions on a namespace only on acme. An action in deny is refused everywhere.
type hostAuthorizer struct {
	deny map[identity.Action]bool
	// anyNamespace lets the namespace actions pass on every namespace, so that a
	// test reaches the handler with the ID of a connection of another one.
	anyNamespace bool
}

func (a hostAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	if a.deny[action] {
		return identity.ErrDenied
	}

	//nolint:exhaustive // The authorizer allows four actions and refuses every other one.
	switch action {
	case identity.ActionServerReadGitHost, identity.ActionServerManageGitHost:
		if obj.IsServer() {
			return nil
		}
	case identity.ActionNamespaceReadGitHostConnections, identity.ActionNamespaceManageGitHostConnections:
		if obj.Namespace() == "acme" || a.anyNamespace {
			return nil
		}
	default:
	}

	return identity.ErrDenied
}

func (hostAuthorizer) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// errHostAddress is the error of an address that the rules refuse.
var errHostAddress = errors.New("the address rules refuse the address")

// errHostBinding is the error of a value that does not open with its binding.
var errHostBinding = errors.New("the value does not open with this binding")

// hostSealer reverses the bytes of a value, so that a sealed value differs from
// its plaintext.
type hostSealer struct{}

func (hostSealer) SealBound(_ context.Context, _ string, binding, plaintext []byte) ([]byte, error) {
	out := slices.Clone(plaintext)
	slices.Reverse(out)

	return append([]byte("sealed:"+string(binding)+":"), out...), nil
}

func (hostSealer) OpenBound(_ context.Context, _ string, binding, ciphertext []byte) ([]byte, error) {
	rest, ok := strings.CutPrefix(string(ciphertext), "sealed:"+string(binding)+":")
	if !ok {
		return nil, errHostBinding
	}

	out := []byte(rest)
	slices.Reverse(out)

	return out, nil
}

// hostOpener is a forge.Opener whose Forge answers what the test sets.
type hostOpener struct {
	mu       sync.Mutex
	opened   int
	checkErr error
	openErr  error
}

func (o *hostOpener) Kind() forge.Kind { return forge.KindGitHub }

//nolint:ireturn // The port is the interface.
func (o *hostOpener) Open(context.Context, forge.Connection, forge.Credential) (forge.Forge, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.openErr != nil {
		return nil, o.openErr
	}

	o.opened++

	return hostForge{Forge: nil, o: o}, nil
}

func (o *hostOpener) openedCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.opened
}

func (o *hostOpener) setOpenErr(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.openErr = err
}

func (o *hostOpener) setCheckErr(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.checkErr = err
}

type hostForge struct {
	forge.Forge

	o *hostOpener
}

func (hostForge) Capabilities() forge.Capabilities { return nil }

func (f hostForge) Check(context.Context) (forge.Account, error) {
	f.o.mu.Lock()
	defer f.o.mu.Unlock()

	if f.o.checkErr != nil {
		return forge.Account{}, f.o.checkErr
	}

	return forge.Account{
		ID: "99", Login: "acme-org", ExpiresAt: time.Time{}, NoExpiry: false, Scopes: []string{"contents:read"},
	}, nil
}

// hostEnv is a handler over a fresh store with acme and beta.
type hostEnv struct {
	handler testHandler
	store   *sqlite.Store
	opener  *hostOpener
	// refuseAddress makes the address rules of the service refuse every address.
	refuseAddress *atomic.Bool
	mu            sync.Mutex
	refused       []string
}

// refusals returns the reasons that the service counted, in order.
func (e *hostEnv) refusals() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	return slices.Clone(e.refused)
}

// hostResponse is the answer of one request.
type hostResponse struct {
	header http.Header
	body   string
	status int
}

// hostCall is one request of a test.
type hostCall struct {
	headers map[string]string
	method  string
	target  string
	body    string
}

func newHostEnv(t *testing.T, authz identity.Authorizer) *hostEnv {
	t.Helper()

	return newHostEnvWith(t, authz, withKeys)
}

// keyMode says whether the handler of a test has the Idempotency-Key middleware.
type keyMode int

const (
	withKeys keyMode = iota
	withoutKeys
)

// newHostEnvWith builds the environment of newHostEnv. With keys false the
// handler has no Idempotency-Key middleware.
func newHostEnvWith(t *testing.T, authz identity.Authorizer, keys keyMode) *hostEnv {
	t.Helper()

	// The limits are wide, so that a test can check a connection many times.
	// The tests of the limit set their own.
	limits := forge.CheckLimits{
		PerConnection: time.Nanosecond, PerInstallation: 1000, PerApp: 1000, Window: time.Minute,
	}

	return newHostEnvLimited(t, authz, keys, &limits)
}

// newHostEnvLimited builds the environment of newHostEnvWith with the limits of
// Check that limits holds.
func newHostEnvLimited(t *testing.T, authz identity.Authorizer, keys keyMode, limits *forge.CheckLimits) *hostEnv {
	t.Helper()

	return newHostEnvWrapped(t, authz, keys, limits, func(s forge.Store) forge.Store { return s })
}

// newHostEnvWrapped builds the environment of newHostEnvLimited over the store
// that wrap returns for the store of the Git host.
func newHostEnvWrapped(
	t *testing.T, authz identity.Authorizer, keys keyMode, limits *forge.CheckLimits,
	wrap func(forge.Store) forge.Store,
) *hostEnv {
	t.Helper()

	st, nss := openIPAllowFixture(t)
	env := &hostEnv{
		handler: testHandler{
			Handler: nil,
			served:  nil,
		},
		store:         st,
		opener:        nil,
		refuseAddress: &atomic.Bool{},
		mu:            sync.Mutex{},
		refused:       nil,
	}
	opener := &hostOpener{mu: sync.Mutex{}, opened: 0, checkErr: nil, openErr: nil}

	rules := egress.Rules{Allow: nil, LoopbackForTests: false}

	svc, err := forge.NewService(forge.Config{
		Store: wrap(st.GitHost()), Namespaces: st, Secrets: hostSealer{}, Audit: st, Openers: []forge.Opener{opener},
		CheckURL: func(raw string) error {
			if env.refuseAddress.Load() {
				return errHostAddress
			}

			return rules.CheckURL(raw, egress.HTTPSOnly)
		},
		OnAuditFailure: nil,
		OnRefused: func(reason string) {
			env.mu.Lock()
			defer env.mu.Unlock()

			env.refused = append(env.refused, reason)
		},
		CheckLimits: limits, Logger: nil, Now: nil,
	})
	if err != nil {
		t.Fatalf("git host service: %v", err)
	}

	var mw *idempotent.Middleware

	if keys == withKeys {
		mw = newHostMiddleware(t, st)
	}

	built := buildHostEnv(t, hostParts{st: st, nss: nss, svc: svc, mw: mw, opener: opener}, authz)
	env.handler, env.opener = built.handler, built.opener

	return env
}

func newHostMiddleware(t *testing.T, st *sqlite.Store) *idempotent.Middleware {
	t.Helper()

	mw, err := idempotent.New(idempotent.Config{
		Store: st.Idempotency(idempotencytest.TestWrapper(t, 1)), Logger: slog.New(slog.DiscardHandler), Now: nil,
		MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("idempotency middleware: %v", err)
	}

	return mw
}

// hostParts are the parts that buildHostEnv puts in a handler.
type hostParts struct {
	st     *sqlite.Store
	nss    *namespace.Service
	svc    *forge.Service
	mw     *idempotent.Middleware
	opener *hostOpener
}

func buildHostEnv(t *testing.T, parts hostParts, authz identity.Authorizer) *hostEnv {
	t.Helper()

	st, nss, svc, mw, opener := parts.st, parts.nss, parts.svc, parts.mw, parts.opener

	// A write of the registration or of an installation needs a console
	// session with a recent sign-in: an access token never gives one.
	signIn, sessions := signInOf(t, st)

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          svc,
		Idempotency:      mw,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       authz,
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       nss,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           st,
		Journal:          nil,
		Principals:       sessions.principals,
		Access:           nil,
		Tokens:           nil,
		Audit:            st,
		OnAuditFailure:   nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		SignIn:           signIn,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	rememberSessions(handler, sessions)

	return &hostEnv{
		handler: handler, store: st, opener: opener, refuseAddress: nil, mu: sync.Mutex{}, refused: nil,
	}
}

func (e *hostEnv) do(t *testing.T, c hostCall) hostResponse {
	t.Helper()

	var body io.Reader = http.NoBody
	if c.body != "" {
		body = strings.NewReader(c.body)
	}

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.target, body)
	req.RemoteAddr = hostRemote

	if needsFreshHostSession(c.method, c.target) {
		startSession(t, e.handler, req, 0)
	} else {
		req.Header.Set("Authorization", "Bearer test-token")
	}

	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return hostResponse{header: rec.Header(), body: rec.Body.String(), status: rec.Code}
}

// needsFreshHostSession reports whether method and target are a write of the
// registration of the app or of an installation, which needs a console
// session with a recent sign-in.
func needsFreshHostSession(method, target string) bool {
	path, _, _ := strings.Cut(target, "?")

	return (method == http.MethodPut || method == http.MethodDelete) &&
		(strings.HasPrefix(path, "/api/dustpan/v1/git-host-apps/") || strings.HasPrefix(path, hostInstallationsPath))
}

// send makes a request with no extra header.
func (e *hostEnv) send(t *testing.T, method, target, body string) hostResponse {
	t.Helper()

	return e.do(t, hostCall{headers: nil, method: method, target: target, body: body})
}

func hostAppBody() string {
	return `{"appID":"` + hostAppID + `","apiAddress":"` + hostAPIAddress + `","privateKey":"` + hostKeyCanary +
		`","eventSecret":"` + hostSecretCanary + `"}`
}

func decodeHost[T any](t *testing.T, r hostResponse) T {
	t.Helper()

	var out T
	if err := json.Unmarshal([]byte(r.body), &out); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}

	return out
}

func wantHost(t *testing.T, r hostResponse, want int) {
	t.Helper()

	if r.status != want {
		t.Fatalf("status = %d, want %d: %s", r.status, want, r.body)
	}
}

// register registers the app and assigns installation 101 to acme.
func (e *hostEnv) register(t *testing.T) {
	t.Helper()

	wantHost(t, e.send(t, http.MethodPut, hostAppPath, hostAppBody()), http.StatusCreated)
	wantHost(t, e.send(t, http.MethodPut, hostInstallationsPath+"/101", `{"namespace":"acme"}`), http.StatusCreated)
}

// connect makes a connection to the installation and returns its ID.
func (e *hostEnv) connect(t *testing.T, installationID string) string {
	t.Helper()

	r := e.send(t, http.MethodPost, hostConnectionsPath, createBody(installationID))
	wantHost(t, r, http.StatusCreated)

	return decodeHost[connectionDoc](t, r).ID
}

func createBody(installationID string) string {
	return `{"kind":"github","auth":"app","installationID":"` + installationID + `","push":true}`
}

type credentialDoc struct {
	SetID     string `json:"setID"`
	CreatedAt string `json:"createdAt"`
	Set       bool   `json:"set"`
}

type appDoc struct {
	Kind        string        `json:"kind"`
	AppID       string        `json:"appID"`
	APIAddress  string        `json:"apiAddress"`
	HookID      string        `json:"hookID"`
	PrivateKey  credentialDoc `json:"privateKey"`
	EventSecret credentialDoc `json:"eventSecret"`
	Version     int64         `json:"version"`
}

type installationDoc struct {
	InstallationID string `json:"installationID"`
	Namespace      string `json:"namespace"`
	Version        int64  `json:"version"`
}

type installationsDoc struct {
	Installations []installationDoc `json:"installations"`
}

type connectionDoc struct {
	ID             string   `json:"id"`
	Namespace      string   `json:"namespace"`
	Kind           string   `json:"kind"`
	Auth           string   `json:"auth"`
	AppScope       string   `json:"appScope"`
	InstallationID string   `json:"installationID"`
	AppID          string   `json:"appID"`
	APIAddress     string   `json:"apiAddress"`
	HookID         string   `json:"hookID"`
	CredentialSets []string `json:"credentialSets"`
	Version        int64    `json:"version"`
	Push           bool     `json:"push"`
	Default        bool     `json:"default"`
}

type connectionsDoc struct {
	Connections []connectionDoc `json:"connections"`
}

type accountDoc struct {
	ID       string   `json:"id"`
	Login    string   `json:"login"`
	Scopes   []string `json:"scopes"`
	NoExpiry bool     `json:"noExpiry"`
}

type checkDoc struct {
	Account      accountDoc          `json:"account"`
	Capabilities []string            `json:"capabilities"`
	Missing      map[string][]string `json:"missing"`
}

// auditEntries returns every audit entry of a scope, oldest first.
func (e *hostEnv) auditEntries(t *testing.T, scope namespace.Name) []audit.Entry {
	t.Helper()

	listing, err := e.store.List(t.Context(), scope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit of %s: %v", scope, err)
	}

	out := slices.Clone(listing.Entries)
	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out
}
