package route_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var errAuthzDown = errors.New("authorizer failed")

// tokens accepts the token "good" only, and records each token it saw.
type tokens struct {
	identity.Authenticator

	seen *[]string
}

func (t tokens) AuthenticateToken(_ context.Context, raw string) (identity.Principal, error) {
	*t.seen = append(*t.seen, raw)

	var p identity.Principal

	if raw != "good" {
		return p, identity.ErrUnauthenticated
	}

	p.Subject = "ana"

	return p, nil
}

// authorizer allows the actions in allow, answers err for the rest when it
// is set, and denies the rest otherwise.
type authorizer struct {
	identity.Authorizer

	allow map[identity.Action]bool
	err   error
	asked *[]identity.Action
}

func (a authorizer) Authorize(
	_ context.Context,
	_ identity.Principal,
	action identity.Action,
	_ identity.Object,
) error {
	*a.asked = append(*a.asked, action)

	switch {
	case a.allow[action]:
		return nil
	case a.err != nil:
		return a.err
	default:
		return identity.ErrDenied
	}
}

// auditStore accepts each refusal entry and keeps none of them.
type auditStore struct {
	audit.Store
}

func (auditStore) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	return e, nil
}

func guard(t *testing.T, seen *[]string, authz authorizer) route.Guard {
	t.Helper()

	recorder, err := refusal.New(authz, auditStore{Store: nil}, nil, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatalf("refusal.New: %v", err)
	}

	return route.Guard{
		Tokens:   tokens{Authenticator: nil, seen: seen},
		Sessions: nil,
		Refusals: recorder,
	}
}

func newAuthorizer(allow map[identity.Action]bool, err error) authorizer {
	var a authorizer

	a.allow, a.err, a.asked = allow, err, new([]identity.Action)

	return a
}

// TestAuthenticateSchemes sends each credential straight to Authenticate.
func TestAuthenticateSchemes(t *testing.T) {
	t.Parallel()

	cases := []schemeCase{
		{"token scheme", "token good", nil, true},
		{"bearer scheme", "Bearer good", nil, true},
		{"wrong token", "Bearer bad", identity.ErrUnauthenticated, true},
		{"basic scheme", "Basic Z29vZA==", identity.ErrUnauthenticated, false},
		{"empty token", "token ", identity.ErrUnauthenticated, false},
		{"lower-case bearer", "bearer good", identity.ErrUnauthenticated, false},
		{"no space", "Bearergood", identity.ErrUnauthenticated, false},
		{"no header and no session checker", "", identity.ErrUnauthenticated, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkScheme(t, tc)
		})
	}
}

// schemeCase is one credential: the Authorization header, the error that
// Authenticate must return, and whether the token must reach the
// authenticator.
type schemeCase struct {
	name    string
	header  string
	want    error
	reached bool
}

// checkScheme authenticates one request with header and checks the error,
// whether the token reached the authenticator, and that no token request
// reads as a session.
func checkScheme(t *testing.T, tc schemeCase) {
	t.Helper()

	var seen []string

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
	if tc.header != "" {
		r.Header.Set("Authorization", tc.header)
	}

	p, caller, err := guard(t, &seen, newAuthorizer(nil, nil)).Authenticate(r)
	if !errors.Is(err, tc.want) {
		t.Fatalf("Authenticate(%q) error = %v, want %v", tc.header, err, tc.want)
	}

	if tc.want == nil && p.Subject != "ana" {
		t.Fatalf("Authenticate(%q) principal = %q, want ana", tc.header, p.Subject)
	}

	if (len(seen) > 0) != tc.reached {
		t.Fatalf("Authenticate(%q) asked the authenticator %d times, want reached %t", tc.header, len(seen), tc.reached)
	}

	if caller != nil || sessionauth.FromSession(route.WithCaller(r.Context(), caller)) {
		t.Fatalf("Authenticate(%q) marked a token request as a session", tc.header)
	}
}

// TestAuthorizeAll checks that every action is asked in order, and that the
// first refusal stops the walk, fails closed for an authorizer error, and
// names the action.
func TestAuthorizeAll(t *testing.T) {
	t.Parallel()

	obj := identity.NamespaceObject(namespace.Name("acme"))
	actions := []identity.Action{identity.ActionNamespaceRead, identity.ActionNamespaceManageNotifications}

	var p identity.Principal

	p.Subject = "ana"

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)

	all := newAuthorizer(map[identity.Action]bool{actions[0]: true, actions[1]: true}, nil)
	if err := guard(t, new([]string), all).AuthorizeAll(r, p, obj, actions); err != nil || len(*all.asked) != 2 {
		t.Fatalf("AuthorizeAll = %v after %d checks, want nil after 2", err, len(*all.asked))
	}

	first := newAuthorizer(nil, nil)

	err := guard(t, new([]string), first).AuthorizeAll(r, p, obj, actions)
	if err == nil || !errors.Is(err, identity.ErrDenied) || len(*first.asked) != 1 ||
		!strings.Contains(err.Error(), string(actions[0])) {
		t.Fatalf("AuthorizeAll = %v after %d checks, want the first refusal after 1", err, len(*first.asked))
	}

	down := newAuthorizer(nil, errAuthzDown)
	if err := guard(t, new([]string), down).AuthorizeAll(r, p, obj, actions); !errors.Is(err, errAuthzDown) {
		t.Fatalf("AuthorizeAll with a failing authorizer = %v, want its error", err)
	}
}

// TestDecodeBody sends each hostile body straight to DecodeBody.
func TestDecodeBody(t *testing.T) {
	t.Parallel()

	type doc struct {
		Name string `json:"name"`
	}

	cases := []struct {
		name string
		body string
		ok   bool
		text string
	}{
		{"one object", `{"name":"a"}`, true, ""},
		{"unknown field", `{"name":"a","x":1}`, false, "known fields"},
		{"two values", `{"name":"a"}{"name":"b"}`, false, "known fields"},
		{"not JSON", `name=a`, false, "known fields"},
		{"over the limit", `{"name":"` + strings.Repeat("a", 2048) + `"}`, false, "over 1 KiB"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", strings.NewReader(tc.body))

			var got doc

			err := route.DecodeBody(httptest.NewRecorder(), r, 1<<10, &got)
			if (err == nil) != tc.ok {
				t.Fatalf("DecodeBody(%q) = %v, want ok %t", tc.body, err, tc.ok)
			}

			if err != nil && (!strings.Contains(err.Error(), tc.text) || strings.Contains(err.Error(), "aaaa")) {
				t.Fatalf("DecodeBody error %q, want the fixed text %q and no part of the body", err, tc.text)
			}

			if err != nil {
				checkBadBodyCode(t, err)
			}
		})
	}
}

// checkBadBodyCode checks that err answers 400 with the code of the catalog
// for a body that is not valid.
func checkBadBodyCode(t *testing.T, err error) {
	t.Helper()

	rec := httptest.NewRecorder()
	problem.Writer{Logger: slog.New(slog.DiscardHandler), Classify: nil}.Write(t.Context(), rec, err)

	var answer problem.Body
	if decodeErr := json.Unmarshal(rec.Body.Bytes(), &answer); decodeErr != nil {
		t.Fatalf("decode %q: %v", rec.Body, decodeErr)
	}

	if rec.Code != http.StatusBadRequest || answer.ErrorCode != errcode.RequestBodyInvalid {
		t.Fatalf("answer %d %+v, want 400 %s", rec.Code, answer, errcode.RequestBodyInvalid)
	}
}

// TestMuxRecordsEveryPattern checks that both ways to register record the
// pattern and serve it.
func TestMuxRecordsEveryPattern(t *testing.T) {
	t.Parallel()

	m := route.NewMux()
	m.HandleFunc("GET /a", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	m.Handle("GET /b", http.NotFoundHandler())

	if got := strings.Join(m.Patterns(), ","); got != "GET /a,GET /b" {
		t.Fatalf("patterns = %q, want both in order", got)
	}

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/a", http.NoBody))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("GET /a = %d, want the registered handler", rec.Code)
	}
}

// TestWithCaller checks that only a session marks the context as a session
// and carries its sign-in.
func TestWithCaller(t *testing.T) {
	t.Parallel()

	if sessionauth.FromSession(route.WithCaller(t.Context(), nil)) {
		t.Fatal("WithCaller(nil) marked the context as a session")
	}

	var caller sessionauth.Caller

	caller.Match.Session.Provider = "corp"

	ctx := route.WithCaller(t.Context(), &caller)
	if !sessionauth.FromSession(ctx) {
		t.Fatal("WithCaller(session) did not mark the context as a session")
	}

	if in, ok := sessionauth.SignInFrom(ctx); !ok || in.Provider != "corp" {
		t.Fatalf("sign-in = %+v %t, want the provider of the session", in, ok)
	}
}
