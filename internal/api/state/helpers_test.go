package state_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/state"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	patToken         = "pat-token"
	installationName = "eu-west"
	// stackURL is the route of the stack web/dev in the namespace acme.
	stackURL = "/api/dustpan/v1/units/pulumi:acme%2Fweb%2Fdev/state"
	// workspaceURL is the route of the workspace network in the namespace acme.
	workspaceURL = "/api/dustpan/v1/units/opentofu:acme%2Fnetwork/state"
	// secretText is the plaintext of a secret value in the tests. An answer
	// must never hold it.
	secretText = "s3cr3t"
)

var (
	// errNoListing is the answer of ListObjects: no route of the read lists.
	errNoListing = errors.New("no route lists objects")
	// errUnknown is an error that no class of the handler knows. Its text
	// holds the secret text, which no answer may repeat.
	errUnknown = errors.New(secretText)
	// errNoDeadline is the answer of a fake read that runs with no deadline
	// within the timeout of the handler.
	errNoDeadline = errors.New("the read has no deadline within the timeout")
)

// person returns the principal pat.
func person() identity.Principal {
	return identity.Principal{
		Subject: "pat", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: identity.CredentialAccessToken, JobID: "",
	}
}

// tokens signs in pat for patToken.
type tokens struct{}

func (tokens) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != patToken {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return person(), nil
}

// grants allows each action that it lists for the wire form of an object, and
// denies the rest. It checks the object, not only the action.
type grants map[string][]identity.Action

func (g grants) Authorize(_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object) error {
	if slices.Contains(g[obj.String()], action) {
		return nil
	}

	return fmt.Errorf("%w: %s on %s", identity.ErrDenied, action, obj)
}

func (grants) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, errNoListing
}

// readerOf returns the grants of namespace:read on acme and unit:read on each
// unit of units.
func readerOf(units ...string) grants {
	g := grants{"acme": {identity.ActionNamespaceRead}}
	for _, u := range units {
		g[u] = []identity.Action{identity.ActionUnitRead}
	}

	return g
}

// acme returns the namespace acme of the tests.
func acme() namespace.Namespace {
	return namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme",
		PrimaryInstallation: installationName, FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
}

// namespaces answers the namespace acme and no other.
type namespaces struct{ ns namespace.Namespace }

func (n namespaces) Get(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name != n.ns.Name {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return n.ns, nil
}

// memAudit keeps the entries of a handler in memory and checks each one, as
// the stores do.
type memAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

var (
	_ audit.Store       = (*memAudit)(nil)
	_ audit.ServerStore = (*memAudit)(nil)
)

func (a *memAudit) Append(_ context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	if e.Namespace != fence.Namespace {
		return audit.Entry{}, audit.ErrInvalidEntry
	}

	return a.keep(e), nil
}

func (a *memAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	return a.keep(e), nil
}

func (a *memAudit) List(context.Context, namespace.Name, audit.Page) (audit.Listing, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return audit.Listing{Entries: slices.Clone(a.entries), More: false}, nil
}

func (a *memAudit) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

func (a *memAudit) keep(e audit.Entry) audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()

	e.Sequence = int64(len(a.entries)) + 1
	a.entries = append(a.entries, e)

	return e
}

// count returns the number of entries.
func (a *memAudit) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	return len(a.entries)
}

// units fakes the stack and workspace services. Each read runs the read
// function of its tool, and counts the call.
type units struct {
	mu         sync.Mutex
	reads      int
	readStack  func(ctx context.Context, args stack.ReadMaskedArgs) (stack.MaskedPage, error)
	readTofu   func(ctx context.Context, args workspace.ReadMaskedArgs) (workspace.MaskedPage, error)
	stackError error
}

func (u *units) Get(_ context.Context, ref stack.Ref) (stack.Stack, error) {
	if u.stackError != nil {
		return stack.Stack{}, u.stackError
	}

	var s stack.Stack

	s.Ref, s.ID = ref, "stk-web-dev"

	return s, nil
}

func (u *units) ReadMasked(ctx context.Context, args stack.ReadMaskedArgs) (stack.MaskedPage, error) {
	u.mu.Lock()
	u.reads++
	u.mu.Unlock()

	return u.readStack(ctx, args)
}

// count returns the number of reads of both tools.
func (u *units) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()

	return u.reads
}

// tofu is the workspace side of units.
type tofu struct{ u *units }

func (t tofu) Get(_ context.Context, ref workspace.Ref) (workspace.Workspace, error) {
	var w workspace.Workspace

	w.Ref, w.ID = ref, "ws-network"

	return w, nil
}

func (t tofu) ReadMasked(ctx context.Context, args workspace.ReadMaskedArgs) (workspace.MaskedPage, error) {
	t.u.mu.Lock()
	t.u.reads++
	t.u.mu.Unlock()

	return t.u.readTofu(ctx, args)
}

// stackPage answers one page with two markers.
func stackPage(_ context.Context, _ stack.ReadMaskedArgs) (stack.MaskedPage, error) {
	paths := []string{"/resources/0/inputs/password", "/resources/0/outputs/result"}

	return stack.MaskedPage{
		Version: "1", ValuePaths: paths, NextCursor: "",
		Markers: []stack.MaskedMarker{{Secret: true, Path: paths[0]}, {Secret: true, Path: paths[1]}},
	}, nil
}

// tofuPage answers one page with one marker.
func tofuPage(_ context.Context, _ workspace.ReadMaskedArgs) (workspace.MaskedPage, error) {
	paths := []string{"/outputs/password/value"}

	return workspace.MaskedPage{
		Version: "sv-1", ValuePaths: paths, NextCursor: "",
		Markers: []workspace.MaskedMarker{{Secret: true, Path: paths[0]}},
	}, nil
}

// newUnits returns the fake services that answer one page for each tool.
func newUnits() *units {
	return &units{
		mu: sync.Mutex{}, reads: 0, readStack: stackPage, readTofu: tofuPage, stackError: nil,
	}
}

// env is a handler over fake services.
type env struct {
	handler *state.Handler
	units   *units
	audit   *memAudit
}

// setup holds what a test changes in the env.
type setup struct {
	authz   identity.Authorizer
	units   *units
	limiter *denylimit.Limiter
	timeout time.Duration
}

// newEnv returns a handler with the authorizer, the fake services, the
// limiter and the timeout of s.
func newEnv(t *testing.T, s setup) env {
	t.Helper()

	a := &memAudit{mu: sync.Mutex{}, entries: nil}

	h, err := state.NewHandler(config(s, a))
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	return env{handler: h, units: s.units, audit: a}
}

// config returns the Config of s, with the audit store a.
func config(s setup, a *memAudit) state.Config {
	return state.Config{
		Namespaces: namespaces{ns: acme()}, Stacks: s.units, StackStates: s.units, Workspaces: tofu{u: s.units},
		Authenticator: tokens{}, Authorizer: s.authz, Audit: a, OnAuditFailure: nil,
		DenialLimiter: s.limiter, Sessions: nil, CursorKey: bytes.Repeat([]byte{0x2a}, state.MinCursorKeyBytes),
		Logger: slog.New(slog.DiscardHandler), Timeout: s.timeout,
	}
}

// get sends GET target as pat and returns the status and the body.
func (e env) get(t *testing.T, target string) (int, []byte) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+patToken)

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.Bytes()
}

// errorBody is the error answer of the tool-neutral APIs.
type errorBody struct {
	Message   string `json:"message"`
	Code      int    `json:"code"`
	ErrorCode string `json:"errorCode"`
}

// wantError fails the test unless the answer has the status and the code,
// and holds no secret bytes.
func wantError(t *testing.T, status int, body []byte, wantStatus int, wantCode string) {
	t.Helper()

	if bytes.Contains(body, []byte(secretText)) {
		t.Fatalf("the answer holds secret bytes: %s", body)
	}

	var got errorBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	if status != wantStatus || got.ErrorCode != wantCode {
		t.Fatalf("answer = %d %q (%s), want %d %q", status, got.ErrorCode, got.Message, wantStatus, wantCode)
	}
}
