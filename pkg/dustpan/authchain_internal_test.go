package dustpan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/identity"
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
}

// record adds the methods named r.method on the stack of the caller.
func (r *callRecorder) record() {
	pcs := make([]uintptr, 128)
	// Skip runtime.Callers and record: the recording method is the first.
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])

	var chain []string

	for {
		frame, more := frames.Next()
		name := frame.Function[strings.LastIndex(frame.Function, "/")+1:]

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

	authn := &callRecorder{method: "AuthenticateToken", mu: sync.Mutex{}, calls: nil}
	authz := &callRecorder{method: "Authorize", mu: sync.Mutex{}, calls: nil}

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
		"ipallow.Gate",
	}
	assertSomeCall(t, "authorizer", authz.recorded(), wantAuthz)

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

// assertSomeCall fails t unless one of calls starts with want. The services
// that NewServer builds before the wrappers hold the configured port itself,
// so not each call comes through the wrappers.
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
