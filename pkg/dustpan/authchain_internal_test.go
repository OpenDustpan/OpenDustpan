package dustpan

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/devsignin"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// chainTestToken is the token that the recording authenticator accepts.
const chainTestToken = "admin-token-for-the-authenticator-chain-test-of-the-server"

// callRecorder keeps, for each call, the methods named method on the stack
// above the caller of record, innermost first.
type callRecorder struct {
	method string
	mu     sync.Mutex
	calls  [][]string
	// stacks holds, for each call, every function on the stack above the
	// recording method, innermost first. The list of the callers that use the
	// bare port on purpose reads it.
	stacks [][]string
}

// record adds the methods named r.method on the stack of the caller.
func (r *callRecorder) record() {
	pcs := make([]uintptr, 128)
	// Skip runtime.Callers and record: the recording method is the first.
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])

	var chain, stack []string

	for {
		frame, more := frames.Next()
		name := frame.Function[strings.LastIndex(frame.Function, "/")+1:]
		stack = append(stack, name)

		if wrapper, ok := strings.CutSuffix(name, "."+r.method); ok {
			chain = append(chain, wrapper)
		}

		if !more {
			break
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, chain)
	r.stacks = append(r.stacks, stack)
}

// recordedStacks returns a copy of the stacks of the recorded calls, in the
// order of recorded.
func (r *callRecorder) recordedStacks() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.stacks)
}

// recorded returns a copy of the recorded calls.
func (r *callRecorder) recorded() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.calls)
}

// recordingAuthenticator accepts chainTestToken as an administrator and
// records the wrappers that call it.
type recordingAuthenticator struct {
	calls *callRecorder
}

func (a recordingAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	a.calls.record()

	if token != chainTestToken {
		var none identity.Principal

		return none, identity.ErrUnauthenticated
	}

	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "admin", DisplayName: "admin",
		Admin: true, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
		JobUnit: identity.Object{},
	}, nil
}

// recordingAuthorizer allows each action and records the wrappers that call
// it.
type recordingAuthorizer struct {
	identity.AdminOnly

	calls *callRecorder
}

func (a recordingAuthorizer) Authorize(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	a.calls.record()

	return a.AdminOnly.Authorize(ctx, p, action, obj)
}

// TestNewServerAuthenticatorChain proves the order of the wrappers that
// NewServer puts around the configured authenticator and the configured
// authorizer, as a request through the handler of the server meets them. It
// also proves that the console sign-in of the server has a failure limit on
// its sign-in routes and on its password routes. A nil limit means no limit,
// so a wrong order of the assembly must fail here.
func TestNewServerAuthenticatorChain(t *testing.T) {
	t.Parallel()

	authn := &callRecorder{method: "AuthenticateToken", mu: sync.Mutex{}, calls: nil, stacks: nil}
	authz := &callRecorder{method: "Authorize", mu: sync.Mutex{}, calls: nil, stacks: nil}

	w := newWiredSignIn(t, func(st *sqlite.Store) []Option {
		return []Option{
			WithAuthenticator(recordingAuthenticator{calls: authn}),
			WithAuthorizer(recordingAuthorizer{AdminOnly: identity.AdminOnly{}, calls: authz}),
			WithIPAllowlists(true), WithIPAllowlistStore(st),
		}
	})

	if err := w.srv.Start(t.Context()); err != nil {
		t.Fatalf("start the server: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/principals", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+chainTestToken)

	rec := httptest.NewRecorder()
	w.srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/server/principals = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Innermost first: the configured authenticator is the first member of
	// the chain, before the stored access tokens, so the bootstrap admin
	// tokens never meet the failure budget.
	wantAuthn := []string{
		"dustpan.recordingAuthenticator",
		"identity.Chain",
		"tempadmin.(*Decorator)",
		"principal.DevGuard",
		"principal.NamespaceServiceGuard",
	}
	assertSomeCall(t, "authenticator", authn.recorded(), wantAuthn)

	wantAuthz := []string{
		"dustpan.recordingAuthorizer",
		"identity.SelfAllowed",
		"identity.NamespaceServiceRule",
		"identity.JobTokenRule",
		"ipallow.Gate",
	}
	// The manual lock service is reachable with no route in front of it. Each
	// of its checks must come through the whole chain: it is not on the list
	// of the callers that use the bare port.
	ctx := clientip.WithAddr(t.Context(), netip.MustParseAddr("198.51.100.7"))
	admin := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "admin", DisplayName: "admin",
		Admin: true, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
		JobUnit: identity.Object{},
	}

	locks := w.srv.ManualLocks()
	if locks == nil {
		t.Fatal("the server has no manual lock service")
	}

	target := manuallock.NamespaceTarget("acme")
	// The namespace does not exist, so the read fails after the check. The
	// check itself must allow the administrator.
	if _, err := locks.Get(ctx, admin, target); err == nil || errors.Is(err, identity.ErrDenied) {
		t.Fatalf("manual lock read = %v, want an error that is not a denial", err)
	}

	assertSomeCall(t, "authorizer", authz.recorded(), wantAuthz)
	assertEveryCall(t, "authorizer", authz, wantAuthz, bareAuthorizerCallers())

	in := w.srv.mounted.signIn
	if in == nil {
		t.Fatal("the server has no console sign-in")
	}

	if in.Limiter == nil {
		t.Error("the sign-in routes have no failure limit")
	}

	if in.PasswordLimiter == nil {
		t.Error("the password routes have no failure limit")
	}
}

// bareAuthorizerCallers names the code that calls the configured authorizer
// with no wrapper of the chain, on purpose. The key is the prefix of the name
// of the function that calls the authorizer. The value is the reason.
//
// The entries are declared, and this test does not exercise them: with the
// recording authorizer, these callers build their own *access.Authorizer, so
// no call of theirs reaches the recorder. A stale entry is not found by the
// test.
//
// Both callers hold the bare port because they run before NewServer writes
// the chain into the settings (newIdentityServices). This layer has no IP
// allowlist gate and no namespace service rule. The routes in front of them
// use the chain, but this layer does not rely on that.
func bareAuthorizerCallers() map[string]string {
	return map[string]string{
		// The grant check of the access service needs the grant model of the
		// concrete *access.Authorizer (the stored roles and grants). It is a
		// check of the escalation of a grant, and not a check of a request.
		"access.(*Service)": "escalation check of a grant needs the concrete *access.Authorizer",
		// The access token service gets that authorizer of the access service
		// for the escalation check of a token for another principal.
		"accesstoken.(*Service)": "escalation check of a token needs the concrete *access.Authorizer",
	}
}

// assertEveryCall fails t when a call of rec does not start with want, unless
// the function that makes the call starts with a key of bare. It checks only
// the calls that the test makes: a service that holds the configured port and
// that the test does not drive is not found here. NewServer closes that
// window in its structure: it writes the chain into the settings before
// newServices, so a later constructor cannot read the bare port. The manual
// lock service is not in bare, so a call of it that skips the chain fails.
func assertEveryCall(t *testing.T, name string, rec *callRecorder, want []string, bare map[string]string) {
	t.Helper()

	calls, stacks := rec.recorded(), rec.recordedStacks()
	if len(calls) == 0 {
		t.Fatalf("the %s recorded no call", name)
	}

	for i, call := range calls {
		if len(call) >= len(want) && slices.Equal(call[:len(want)], want) {
			continue
		}

		if onBareList(stacks[i], bare) {
			continue
		}

		t.Errorf("a call of the %s did not come through the wrappers %v; call: %v; stack: %v",
			name, want, call, stacks[i])
	}
}

// onBareList reports whether the function that makes the call starts with a
// key of bare. stack starts with the recording method. The function that makes
// the call is the first one after it that is not an Authorize method of a
// wrapper. A frame further up the stack does not count, so a named caller
// cannot exempt the call of another service that it calls.
func onBareList(stack []string, bare map[string]string) bool {
	rest := stack
	if len(rest) > 0 {
		rest = rest[1:]
	}

	for len(rest) > 0 && strings.HasSuffix(rest[0], ".Authorize") {
		rest = rest[1:]
	}

	if len(rest) == 0 {
		return false
	}

	for prefix := range bare {
		if strings.HasPrefix(rest[0], prefix) {
			return true
		}
	}

	return false
}

// assertSomeCall fails t unless one of calls starts with want. It proves that
// the wrappers are in the order of want; assertEveryCall proves that no call
// skips them.
func assertSomeCall(t *testing.T, name string, calls [][]string, want []string) {
	t.Helper()

	for _, call := range calls {
		if len(call) >= len(want) && slices.Equal(call[:len(want)], want) {
			return
		}
	}

	t.Errorf("no call of the %s came through the wrappers %v; calls: %v", name, want, calls)
}

// TestGatedAuthenticatorPutsTheConfiguredOneFirst proves that the chain of
// gatedAuthenticator asks the configured authenticator before the gate of the
// stored access tokens, so the bootstrap admin tokens never meet the failure
// budget.
func TestGatedAuthenticatorPutsTheConfiguredOneFirst(t *testing.T) {
	t.Parallel()

	f := newBudgetFixture(t)

	chain, ok := f.chain.(identity.Chain)
	if !ok || len(chain) != 2 {
		t.Fatalf("gatedAuthenticator = %T %v, want an identity.Chain of 2", f.chain, f.chain)
	}

	if _, first := chain[0].(refuseAll); !first {
		t.Errorf("chain[0] = %T, want the configured authenticator", chain[0])
	}

	if _, second := chain[1].(*authgate.Gate); !second {
		t.Errorf("chain[1] = %T, want *authgate.Gate", chain[1])
	}
}

// TestNewSignInRefusesTokensWithoutGates proves the fail-closed layer of
// newSignIn on its own: when the store keeps access tokens, a sign-in with a
// missing failure gate does not start. A nil limit would mean no limit.
func TestNewSignInRefusesTokensWithoutGates(t *testing.T) {
	t.Parallel()

	f := newBudgetFixture(t)
	f.cfg.publicURL = "https://dustpan.example"
	f.cfg.devSignIn = &devsignin.Config{Code: nil, Admin: noDevAdmin}

	policies, err := newSessionPolicyService(f.cfg)
	if err != nil {
		t.Fatalf("session policies: %v", err)
	}

	noSignIn, noPassword := f.gates, f.gates
	noSignIn.signInGate, noPassword.passwordGate = nil, nil

	tests := []struct {
		name  string
		gates signInGates
		want  error
	}{
		{name: "no gate", gates: noSignInGates(), want: errSignInUngated},
		{name: "no sign-in gate", gates: noSignIn, want: errSignInUngated},
		{name: "no password gate", gates: noPassword, want: errSignInUngated},
		{name: "every gate", gates: f.gates, want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in, err := newSignIn(f.cfg, policies, f.tokens, tc.gates)
			if !errors.Is(err, tc.want) {
				t.Fatalf("newSignIn = %v, want %v", err, tc.want)
			}

			if tc.want != nil && in != nil {
				t.Fatal("newSignIn returned a sign-in with its error")
			}
		})
	}
}
