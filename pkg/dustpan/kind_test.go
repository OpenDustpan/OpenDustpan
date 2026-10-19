package dustpan_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/devcode"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// devPublicURL is the public URL of the servers that the tests give a dev
// sign-in: a loopback address, which the console sign-in accepts with HTTP.
const devPublicURL = "http://127.0.0.1:8080"

// TestNewServerKindOptions proves which combinations of the installation
// kind options NewServer accepts.
func TestNewServerKindOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// opts returns the options on top of the full server options.
		opts    func(t *testing.T) []dustpan.Option
		wantErr error
	}{
		{name: "no kind option", opts: func(*testing.T) []dustpan.Option { return nil }, wantErr: nil},
		{name: "dev", opts: kindOptions(installation.KindDev), wantErr: nil},
		{name: "production", opts: kindOptions(installation.KindProduction), wantErr: nil},
		{name: "unknown kind", opts: kindOptions("staging"), wantErr: dustpan.ErrInvalidOption},
		{name: "dev sign-in with no kind", opts: devSignInOptions(""), wantErr: dustpan.ErrInvalidOption},
		{
			name: "dev sign-in on production", opts: devSignInOptions(installation.KindProduction),
			wantErr: dustpan.ErrInvalidOption,
		},
		{
			name:    "dev sign-in on dev with a loopback public URL",
			opts:    devSignInOptions(installation.KindDev),
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)
			opts := append(fullServerOptions(t, st, blobs, authn), tt.opts(t)...)

			_, err := dustpan.NewServer(opts...)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewServer: err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func kindOptions(kind installation.Kind) func(*testing.T) []dustpan.Option {
	return func(*testing.T) []dustpan.Option {
		return []dustpan.Option{dustpan.WithInstallationKind(kind)}
	}
}

// devSignInOptions answers the options of a server with a dev sign-in, the
// public URL devPublicURL and the kind, when the kind is not empty.
func devSignInOptions(kind installation.Kind) func(*testing.T) []dustpan.Option {
	return func(t *testing.T) []dustpan.Option {
		t.Helper()

		code, _, err := devcode.Make(nil)
		if err != nil {
			t.Fatalf("make code: %v", err)
		}

		opts := []dustpan.Option{dustpan.WithDevSignIn(code), dustpan.WithPublicURL(devPublicURL)}
		if kind != "" {
			opts = append(opts, dustpan.WithInstallationKind(kind))
		}

		return opts
	}
}

// TestNewServerDevSignInNeedsPublicURL proves that a dev sign-in with no
// public URL stops NewServer: the cookie check needs the origin.
func TestNewServerDevSignInNeedsPublicURL(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	opts := fullServerOptions(t, st, blobs, authn)

	code, _, err := devcode.Make(nil)
	if err != nil {
		t.Fatalf("make code: %v", err)
	}

	opts = append(opts, dustpan.WithInstallationKind(installation.KindDev), dustpan.WithDevSignIn(code))

	if _, newErr := dustpan.NewServer(opts...); newErr == nil {
		t.Fatal("NewServer with a dev sign-in and no public URL succeeded")
	}
}

// runKindCase describes one start of a server for TestRunClaimsKind.
type runKindCase struct {
	name string
	// claimed is the kind that claimed the database before the start.
	claimed installation.Kind
	// kind is the kind option of the server, empty for none.
	kind installation.Kind
	// withStore tells whether the server gets the kind store.
	withStore bool
	wantErr   error
	// wantStored is the kind that the database holds after the start.
	wantStored installation.Kind
}

// startKindServer builds a server for the case and runs it. It answers how
// many times the function of WithBeforeServe ran, and the error of Run.
func startKindServer(t *testing.T, st *sqlite.Store, tt runKindCase, serverOpts []dustpan.Option) (int32, error) {
	t.Helper()

	ln := openTestListener(t)

	var served atomic.Int32

	reached := make(chan struct{})

	opts := slices.Concat(serverOpts, []dustpan.Option{
		dustpan.WithListener(ln),
		dustpan.WithBeforeServe(func(context.Context) error {
			if served.Add(1) == 1 {
				close(reached)
			}

			return nil
		}),
	})
	if tt.kind != "" {
		opts = append(opts, dustpan.WithInstallationKind(tt.kind))
	}

	if tt.withStore {
		opts = append(opts, dustpan.WithInstallationKindStore(st))
	}

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := runServer(ctx, srv)

	if tt.wantErr == nil {
		// The function of WithBeforeServe runs after the claim.
		select {
		case <-reached:
		case <-time.After(10 * time.Second):
			t.Fatal("the server did not reach the function of WithBeforeServe")
		}

		cancel()
	}

	runErr := <-done

	return served.Load(), runErr
}

// TestRunClaimsKind proves that Run claims the installation kind of the
// server before it serves, and that a database of another kind stops the
// start before the listen and before the function of WithBeforeServe.
func TestRunClaimsKind(t *testing.T) {
	t.Parallel()

	tests := []runKindCase{
		{
			name: "new database, dev", claimed: "", kind: installation.KindDev, withStore: true, wantErr: nil,
			wantStored: installation.KindDev,
		},
		{
			name: "new database, no kind option", claimed: "", kind: "", withStore: true, wantErr: nil,
			wantStored: installation.KindProduction,
		},
		{
			name: "database of the same kind", claimed: installation.KindDev, kind: installation.KindDev,
			withStore: true, wantErr: nil, wantStored: installation.KindDev,
		},
		{
			name: "dev database, production server", claimed: installation.KindDev, kind: installation.KindProduction,
			withStore: true, wantErr: installation.ErrKindMismatch, wantStored: installation.KindDev,
		},
		{
			name: "production database, dev server", claimed: installation.KindProduction, kind: installation.KindDev,
			withStore: true, wantErr: installation.ErrKindMismatch, wantStored: installation.KindProduction,
		},
		{
			name: "no kind store claims nothing", claimed: "", kind: installation.KindDev, withStore: false,
			wantErr: nil, wantStored: installation.KindProduction,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkRunClaimsKind(t, tt)
		})
	}
}

// checkRunClaimsKind runs one case of TestRunClaimsKind.
func checkRunClaimsKind(t *testing.T, tt runKindCase) {
	t.Helper()

	st, blobs, authn := requireServerPorts(t)

	if tt.claimed != "" {
		if _, err := st.ClaimKind(t.Context(), tt.claimed); err != nil {
			t.Fatalf("claim %q: %v", tt.claimed, err)
		}
	}

	served, runErr := startKindServer(t, st, tt, fullServerOptions(t, st, blobs, authn))

	if !errors.Is(runErr, tt.wantErr) {
		t.Fatalf("Run: err = %v, want %v", runErr, tt.wantErr)
	}

	if tt.wantErr != nil && served != 0 {
		t.Fatal("the server called the function of WithBeforeServe after a kind mismatch")
	}

	stored, err := st.ClaimKind(t.Context(), installation.KindProduction)
	if err != nil || stored != tt.wantStored {
		t.Fatalf("stored kind = %q, %v, want %q", stored, err, tt.wantStored)
	}
}

// mountedRouteCase describes one request for TestInstallationRoutesOnTheMux.
type mountedRouteCase struct {
	name string
	opts func(t *testing.T) []dustpan.Option
	// method, path and token describe the request.
	method string
	path   string
	token  string
	want   int
	// wantBody is text that the answer holds, or empty.
	wantBody string
	// refuseBody is text that the answer must not hold, or empty.
	refuseBody string
}

// answerOnTheMux builds a server with the options of the case and serves the
// request of the case through its handler.
func answerOnTheMux(t *testing.T, tt mountedRouteCase) *httptest.ResponseRecorder {
	t.Helper()

	st, blobs, authn := requireServerPorts(t)
	opts := append(fullServerOptions(t, st, blobs, authn), tt.opts(t)...)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader("{}"))
	if tt.token != "" {
		req.Header.Set("Authorization", "Bearer "+tt.token)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	return rec
}

// TestInstallationRoutesOnTheMux proves that the capabilities document and
// the dev sign-in answer through the mux of the server: the document is
// public and tells the kind to a signed-in caller only, and the dev sign-in
// exists only when the server got a one-time code.
func TestInstallationRoutesOnTheMux(t *testing.T) {
	t.Parallel()

	tests := []mountedRouteCase{
		{
			name: "capabilities with no sign-in", opts: kindOptions(installation.KindDev), method: http.MethodGet,
			path: "/api/dustpan/v1/capabilities", token: "", want: http.StatusOK, wantBody: `"apiVersion":"v1"`,
			refuseBody: "installationKind",
		},
		{
			name: "capabilities of a dev server for an administrator", opts: kindOptions(installation.KindDev),
			method: http.MethodGet, path: "/api/dustpan/v1/capabilities", token: adminToken, want: http.StatusOK,
			wantBody: `"installationKind":"dev"`, refuseBody: "",
		},
		{
			name: "capabilities of a server with no kind option for an administrator",
			opts: func(*testing.T) []dustpan.Option { return nil }, method: http.MethodGet,
			path: "/api/dustpan/v1/capabilities", token: adminToken, want: http.StatusOK,
			wantBody: `"installationKind":"production"`, refuseBody: "",
		},
		{
			name: "dev sign-in with a code", opts: devSignInOptions(installation.KindDev), method: http.MethodPost,
			path: "/api/dustpan/v1/sign-in/dev-code", token: "", want: http.StatusForbidden,
			wantBody: "sign-in code is not valid", refuseBody: "",
		},
		{
			name: "dev sign-in with no code", opts: kindOptions(installation.KindDev), method: http.MethodPost,
			path: "/api/dustpan/v1/sign-in/dev-code", token: "", want: http.StatusNotFound, wantBody: "",
			refuseBody: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := answerOnTheMux(t, tt)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body)
			}

			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body %q does not hold %q", rec.Body, tt.wantBody)
			}

			if tt.refuseBody != "" && strings.Contains(rec.Body.String(), tt.refuseBody) {
				t.Fatalf("body %q holds %q", rec.Body, tt.refuseBody)
			}
		})
	}
}
