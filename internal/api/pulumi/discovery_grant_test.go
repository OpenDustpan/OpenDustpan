package pulumi_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// errGrantList is the grant read error of the fakes of these tests.
var errGrantList = errors.New("grant list failed")

// headerGrantAuthorizer denies every action but caller:read, and names the
// grant grt-test of a person on collection team-a of acme to
// DiscoveryGrantID. A namespace other than acme gets no grant. The empty id
// names no grant: the namespace stays hidden.
type headerGrantAuthorizer struct {
	id  string
	err error
}

func (headerGrantAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionCallerRead {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (headerGrantAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, action identity.Action,
) (identity.Scope, error) {
	none := identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}

	if action != identity.ActionUnitRead && action != identity.ActionUnitReadOutputs {
		return none, nil
	}

	ns, err := namespace.ParseName("acme")
	if err != nil {
		return none, err
	}

	return identity.Scope{
		Namespaces:  nil,
		Collections: []identity.Object{identity.CollectionObject(ns, "team-a")},
		Units:       nil,
		Principals:  nil,
	}, nil
}

func (a headerGrantAuthorizer) DiscoveryGrantID(
	_ context.Context, _ identity.Principal, ns namespace.Name,
) (string, error) {
	if ns.String() == "acme" {
		return a.id, a.err
	}

	return "", nil
}

// headerAllowAllAuthorizer allows every action: the authorizer, not a grant,
// allows the answer.
type headerAllowAllAuthorizer struct{}

func (headerAllowAllAuthorizer) Authorize(
	context.Context, identity.Principal, identity.Action, identity.Object,
) error {
	return nil
}

func (headerAllowAllAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// namespaceScopeAuthorizer denies every action but caller:read and lists the
// namespace acme itself in the scope of the unit read actions. It cannot name
// a grant: a grant on the namespace alone must not open discovery.
type namespaceScopeAuthorizer struct{}

func (namespaceScopeAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionCallerRead {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (namespaceScopeAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{
		Namespaces:  []identity.Object{identity.NamespaceObject("acme")},
		Collections: nil,
		Units:       nil,
		Principals:  nil,
	}, nil
}

// TestDefaultOrganizationAnswerNamesTheGrant proves that the default
// organization answer that a grant allowed carries the stable ID of the
// grant in the response header.
func TestDefaultOrganizationAnswerNamesTheGrant(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme", "beta")

	handler := newUserHandler(t, nsStore, stStore, headerGrantAuthorizer{id: "grt-test-1", err: nil})

	rec := get(t, handler, "/api/user/organizations/default")
	if rec.Code != http.StatusOK {
		t.Fatalf("default organization answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := rec.Header().Get(access.HeaderDiscoveryGrant); got != "grt-test-1" {
		t.Fatalf("%s = %q, want grt-test-1", access.HeaderDiscoveryGrant, got)
	}
}

// TestUserAnswerNamesTheGrant proves that the answer of GET /api/user names
// the grant that allowed the namespace in the list.
func TestUserAnswerNamesTheGrant(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme", "beta")

	handler := newUserHandler(t, nsStore, stStore, headerGrantAuthorizer{id: "grt-test-1", err: nil})

	rec := get(t, handler, "/api/user")
	if rec.Code != http.StatusOK {
		t.Fatalf("user answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := rec.Header().Get(access.HeaderDiscoveryGrant); got != "grt-test-1" {
		t.Fatalf("%s = %q, want grt-test-1", access.HeaderDiscoveryGrant, got)
	}
}

// TestOrganizationAnswersWithoutGrantNameNoGrant proves that an answer that
// no grant allowed carries no grant header: the authorizer allowed it, no
// grant allows it, the caller is unauthorized, or the grant read failed.
func TestOrganizationAnswersWithoutGrantNameNoGrant(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		authz      identity.Authorizer
		target     string
		seed       []string
		want       int
		wantDenied int
	}{
		{
			"authorizer allowed, user",
			headerAllowAllAuthorizer{},
			"/api/user",
			[]string{"acme", "beta"},
			http.StatusOK,
			0,
		},
		{
			"authorizer allowed, default organization",
			headerAllowAllAuthorizer{},
			"/api/user/organizations/default",
			[]string{"acme"},
			http.StatusOK,
			0,
		},
		{
			"no grant allows, user",
			headerGrantAuthorizer{id: "", err: nil},
			"/api/user",
			[]string{"acme", "beta"},
			http.StatusOK,
			0,
		},
		{
			"no grant allows, default organization",
			headerGrantAuthorizer{id: "", err: nil},
			"/api/user/organizations/default",
			[]string{"acme", "beta"},
			http.StatusNotFound,
			0,
		},
		{
			"no grant anywhere, user",
			discoveryEmptyAuthorizer{},
			"/api/user",
			[]string{"acme", "beta"},
			http.StatusOK,
			0,
		},
		{
			"no grant anywhere, default organization",
			discoveryEmptyAuthorizer{},
			"/api/user/organizations/default",
			[]string{"acme", "beta"},
			http.StatusNotFound,
			0,
		},
		{
			"grant read failed, user",
			headerGrantAuthorizer{id: "grt-test-1", err: errGrantList},
			"/api/user",
			[]string{"acme", "beta"},
			http.StatusInternalServerError,
			1,
		},
		{
			"grant ID is a role name, default organization",
			headerGrantAuthorizer{id: "admins", err: nil},
			"/api/user/organizations/default",
			[]string{"acme", "beta"},
			http.StatusNotFound,
			1,
		},
		{
			"grant ID has a header line break, default organization",
			headerGrantAuthorizer{id: "grt-a\r\nX-A: 1", err: nil},
			"/api/user/organizations/default",
			[]string{"acme", "beta"},
			http.StatusNotFound,
			1,
		},
		{
			"grant ID has 65 bytes, user",
			headerGrantAuthorizer{id: "grt-" + strings.Repeat("a", 61), err: nil},
			"/api/user",
			[]string{"acme", "beta"},
			http.StatusOK,
			1,
		},
		{
			"grant read failed with a generic invalid error, user",
			headerGrantAuthorizer{id: "", err: fmt.Errorf("%w: another check failed", access.ErrInvalid)},
			"/api/user",
			[]string{"acme", "beta"},
			http.StatusInternalServerError,
			1,
		},
		{
			"grant read failed, default organization",
			headerGrantAuthorizer{id: "grt-test-1", err: errGrantList},
			"/api/user/organizations/default",
			[]string{"acme", "beta"},
			http.StatusInternalServerError,
			1,
		},
		{
			"namespace-level scope only, default organization",
			namespaceScopeAuthorizer{},
			"/api/user/organizations/default",
			[]string{"acme"},
			http.StatusNotFound,
			0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
			stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
			seedNamespaces(t, nsStore, tc.seed...)

			entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}
			handler := newUserHandlerWithOptions(t, nsStore, stStore, tc.authz, userHandlerOptions{
				sessions: nil, entries: entries,
			})

			rec := get(t, handler, tc.target)
			if rec.Code != tc.want {
				t.Fatalf("%s answers %d, want %d (%s)", tc.target, rec.Code, tc.want, rec.Body.String())
			}

			if got := rec.Header().Get(access.HeaderDiscoveryGrant); got != "" {
				t.Fatalf("%s = %q, want no grant header", access.HeaderDiscoveryGrant, got)
			}

			if got := len(deniedEntries(entries)); got != tc.wantDenied {
				t.Fatalf("%s writes %d denial entries, want %d", tc.target, got, tc.wantDenied)
			}
		})
	}
}

// perNamespaceGrantAuthorizer denies every action but caller:read and names
// one grant ID for each namespace in ids to DiscoveryGrantID.
type perNamespaceGrantAuthorizer struct {
	ids map[string]string
	err error
}

func (perNamespaceGrantAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionCallerRead {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (perNamespaceGrantAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

func (a perNamespaceGrantAuthorizer) DiscoveryGrantID(
	_ context.Context, _ identity.Principal, ns namespace.Name,
) (string, error) {
	return a.ids[ns.String()], a.err
}

// TestInvalidGrantIDSkipsOnlyItsNamespace proves that a grant with an invalid
// ID in one namespace hides that namespace only: the answer is 200, lists the
// namespace with a valid grant, omits the other one, names the valid grant
// and writes one denial entry for the omitted namespace.
func TestInvalidGrantIDSkipsOnlyItsNamespace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		invalid string
	}{
		{"role name", "admins"},
		{"header line break", "grt-a\r\nX-A: 1"},
		{"65 bytes", "grt-" + strings.Repeat("a", 61)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
			stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
			seedNamespaces(t, nsStore, "alpha", "beta")

			entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}
			ids := map[string]string{"alpha": "grt-good-1", "beta": tc.invalid}
			authz := perNamespaceGrantAuthorizer{ids: ids, err: nil}
			handler := newUserHandlerWithOptions(t, nsStore, stStore, authz, userHandlerOptions{
				sessions: nil, entries: entries,
			})

			checkInvalidGrantAnswer(t, handler, entries, "/api/user")
			checkInvalidGrantAnswer(t, handler, entries, "/api/user/organizations/default")
		})
	}
}

// checkInvalidGrantAnswer checks one answer of the invalid grant ID test. The
// limiter folds a repeat of the same denial in its window, so only the first
// request of a handler is counted: the caller passes /api/user first.
func checkInvalidGrantAnswer(t *testing.T, handler *pulumi.Handler, entries *memAuditStore, target string) {
	t.Helper()

	before := len(deniedEntries(entries))
	rec := get(t, handler, target)

	if rec.Code != http.StatusOK {
		t.Fatalf("%s answers %d, want 200 (%s)", target, rec.Code, rec.Body.String())
	}

	if got := rec.Header().Get(access.HeaderDiscoveryGrant); got != "grt-good-1" {
		t.Fatalf("%s = %q, want grt-good-1", access.HeaderDiscoveryGrant, got)
	}

	if target != "/api/user" {
		return
	}

	if got := len(deniedEntries(entries)) - before; got != 1 {
		t.Fatalf("%s writes %d denial entries, want 1", target, got)
	}

	orgs := decodeDiscoveryOrganizations(t, rec.Body.String())
	if !discoveryContains(orgs, "alpha") || discoveryContains(orgs, "beta") {
		t.Fatalf("organizations = %+v, want alpha only", orgs)
	}
}
