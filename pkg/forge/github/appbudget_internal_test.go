package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
)

// budgetRig is an Opener over a TLS server that counts its requests, with a
// budget of signed calls of the app.
type budgetRig struct {
	opener *Opener
	budget *forge.AppBudget
	url    string
	calls  *atomic.Int64
	pem    []byte
}

func newBudgetRig(t *testing.T, limit int) budgetRig {
	t.Helper()

	return newBudgetRigWith(t, limit, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
}

// newBudgetRigWith is newBudgetRig over a server that answers each request
// with handle.
func newBudgetRigWith(t *testing.T, limit int, handle http.HandlerFunc) budgetRig {
	t.Helper()

	calls := new(atomic.Int64)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handle(w, r)
	}))
	t.Cleanup(srv.Close)

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	budget, err := forge.NewAppBudget(limit, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// A fixed clock, so that the window of the budget does not run on the
	// wall clock of the test.
	now := time.UnixMilli(1_700_000_000_000)

	o, err := NewOpener(Config{
		Rules: egress.Rules{Allow: nil, LoopbackForTests: true}, RootCAs: pool,
		Now: func() time.Time { return now }, Prover: nil, AppBudget: budget,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(o.CloseIdleConnections)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	block := pem.EncodeToMemory(
		&pem.Block{Type: "RSA PRIVATE KEY", Headers: nil, Bytes: x509.MarshalPKCS1PrivateKey(key)},
	)

	return budgetRig{opener: o, budget: budget, url: srv.URL, calls: calls, pem: block}
}

// open returns the adapter of an app connection of the app appID to the
// installation installationID.
func (r budgetRig) open(t *testing.T, appID, installationID string) *gitHub {
	t.Helper()

	fg, err := r.opener.Open(t.Context(), forge.Connection{
		ID: "c-" + installationID, Kind: forge.KindGitHub, Auth: forge.AuthApp, BaseURL: r.url, Push: true,
		Params: map[string]string{forge.ParamAppID: appID, forge.ParamInstallationID: installationID},
	}, forge.Credential{SetID: "s1", Secret: r.pem, CreatedAt: time.Time{}})
	if err != nil {
		t.Fatal(err)
	}

	f, ok := fg.(*gitHub)
	if !ok {
		t.Fatalf("Open gave %T", fg)
	}

	return f
}

// signedCalls are the calls of the adapter that the key of the app signs.
func signedCalls() map[string]func(ctx context.Context, f *gitHub) error {
	return map[string]func(ctx context.Context, f *gitHub) error{
		"the installation read of a check": func(ctx context.Context, f *gitHub) error {
			_, err := f.checkApp(ctx)

			return err
		},
		"the token probe of a check": func(ctx context.Context, f *gitHub) error {
			return f.probeToken(ctx, "/app/installations/"+f.installationID, "jwt")
		},
		"the read of the installation owner": func(ctx context.Context, f *gitHub) error {
			_, err := f.installationOwner(ctx)

			return err
		},
		"an installation token": func(ctx context.Context, f *gitHub) error {
			_, err := f.mintToken(ctx, "infra", "read")

			return err
		},
		"the read of the app login": func(ctx context.Context, f *gitHub) error {
			_, err := f.appLogin(ctx)

			return err
		},
	}
}

// TestEverySignedCallSpendsTheAppBudget sends each signed call of the adapter
// with the budget of the app spent: each one fails with ErrAppBudget,
// ErrUnavailable and a wait, and makes no request. With budget left, each one makes its request.
func TestEverySignedCallSpendsTheAppBudget(t *testing.T) {
	t.Parallel()

	for name, call := range signedCalls() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newBudgetRig(t, 1)
			f := r.open(t, "12345", "101")

			// The budget has room: the call makes its request. The server
			// answers 500, which is no refusal of the budget: it has no wait.
			if wait := budgetWait(call(t.Context(), f)); wait > 0 {
				t.Fatalf("a call with budget left was refused with a wait of %s", wait)
			}

			if got := r.calls.Load(); got != 1 {
				t.Fatalf("requests with budget left = %d, want 1", got)
			}

			// The budget is spent: no request.
			err := call(t.Context(), f)
			wait, ok := forge.RetryAfter(err)
			if !errors.Is(err, forge.ErrAppBudget) || !errors.Is(err, forge.ErrUnavailable) || !ok || wait <= 0 {
				t.Fatalf("call over the budget = %v, want ErrAppBudget and ErrUnavailable with a wait", err)
			}

			if got := r.calls.Load(); got != 1 {
				t.Errorf("requests over the budget = %d, want still 1", got)
			}
		})
	}
}

// budgetWait returns the wait of err when the adapter refused a call over the
// budget, and 0 for any other error.
func budgetWait(err error) time.Duration {
	if wait, ok := forge.RetryAfter(err); ok {
		return wait
	}

	return 0
}

// writeAnswer writes one JSON answer of the fake server.
func writeAnswer(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")

	if _, err := io.WriteString(w, body); err != nil {
		return
	}
}

// TestOneFullCheckSpendsTwoCallsOfTheAppBudget runs one full check against a
// server that answers the calls of the check. The check spends two calls of
// the budget, the read of the installation and the token request. A budget of
// two is spent by the one check, and the next check is refused with no new
// request. The revoke of the token does not spend the budget.
func TestOneFullCheckSpendsTwoCallsOfTheAppBudget(t *testing.T) {
	t.Parallel()

	r := newBudgetRigWith(t, 2, func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/app/installations/101":
			writeAnswer(w, `{"account":{"id":12345,"login":"acme"},"permissions":{"contents":"write"}}`)
		case req.Method == http.MethodPost && req.URL.Path == "/app/installations/101/access_tokens":
			writeAnswer(w, `{"token":"test-token"}`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})

	f := r.open(t, "12345", "101")

	if _, err := f.Check(t.Context()); err != nil {
		t.Fatalf("the one check = %v, want success", err)
	}

	// Three requests, two of them signed by the key of the app.
	if got := r.calls.Load(); got != 3 {
		t.Fatalf("requests of one check = %d, want 3", got)
	}

	// The budget of two is spent by the one check.
	if _, err := f.Check(t.Context()); !errors.Is(err, forge.ErrAppBudget) {
		t.Fatalf("the check over the budget = %v, want ErrAppBudget", err)
	}

	if got := r.calls.Load(); got != 3 {
		t.Errorf("requests over the budget = %d, want still 3", got)
	}
}

// TestTheAppBudgetIsSharedByItsInstallations spends the budget of an app
// through one installation. A connection of another installation of the same
// app gets no call, and a connection of another app keeps its own budget.
func TestTheAppBudgetIsSharedByItsInstallations(t *testing.T) {
	t.Parallel()

	r := newBudgetRig(t, 2)
	first, second := r.open(t, "12345", "101"), r.open(t, "12345", "202")
	other := r.open(t, "67890", "303")

	for range 2 {
		if _, err := first.installationOwner(t.Context()); budgetWait(err) > 0 {
			t.Fatalf("a call with budget left was refused: %v", err)
		}
	}

	if _, err := second.installationOwner(t.Context()); !errors.Is(err, forge.ErrAppBudget) {
		t.Fatalf("another installation of the app = %v, want ErrAppBudget", err)
	}

	if got := r.calls.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}

	if _, err := other.installationOwner(t.Context()); budgetWait(err) > 0 || r.calls.Load() != 3 {
		t.Fatalf("another app = %v with %d requests, want a request of its own", err, r.calls.Load())
	}
}
