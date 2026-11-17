package pulumi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// discoveryCollectionAuthorizer denies namespace:read but reports a grant on
// one collection of acme for the unit read actions. It allows caller:read so
// the wrapper reaches the handler.
type discoveryCollectionAuthorizer struct{}

func (discoveryCollectionAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionCallerRead {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (discoveryCollectionAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, action identity.Action,
) (identity.Scope, error) {
	if action != identity.ActionUnitRead && action != identity.ActionUnitReadOutputs {
		return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
	}

	ns, err := namespace.ParseName("acme")
	if err != nil {
		return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, err
	}

	return identity.Scope{
		Namespaces:  nil,
		Collections: []identity.Object{identity.CollectionObject(ns, "team-a")},
		Units:       nil,
		Principals:  nil,
	}, nil
}

// discoveryUnitAuthorizer denies namespace:read but reports a grant on one
// unit of acme. It allows caller:read so the wrapper reaches the handler.
type discoveryUnitAuthorizer struct{}

func (discoveryUnitAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionCallerRead {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (discoveryUnitAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, action identity.Action,
) (identity.Scope, error) {
	none := identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}

	if action != identity.ActionUnitRead && action != identity.ActionUnitReadOutputs {
		return none, nil
	}

	ns, parseErr := namespace.ParseName("acme")
	if parseErr != nil {
		return none, parseErr
	}

	unit, unitErr := identity.UnitObject("pulumi", ns, "scope/a")
	if unitErr != nil {
		return none, unitErr
	}

	return identity.Scope{
		Namespaces:  nil,
		Collections: nil,
		Units:       []identity.Object{unit},
		Principals:  nil,
	}, nil
}

// discoveryEmptyAuthorizer denies every action but caller:read and reports no
// object.
type discoveryEmptyAuthorizer struct{}

func (discoveryEmptyAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionCallerRead {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (discoveryEmptyAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// discoveryEveryAuthorizer denies every action but caller:read but reports an
// every-object scope: without the gated namespace check this scope alone
// would open discovery.
type discoveryEveryAuthorizer struct{}

func (discoveryEveryAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionCallerRead {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (discoveryEveryAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Every(), nil
}

// discoveryBlockedAuthorizer refuses namespace:read with an IP allowlist
// refusal that also matches identity.ErrDenied, and reports a grant on one
// collection of acme for the unit read actions.
type discoveryBlockedAuthorizer struct{}

func (discoveryBlockedAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionCallerRead {
		return nil
	}

	if action == identity.ActionNamespaceRead {
		return fmt.Errorf(
			"%w: %w: %s may not %s on %q",
			identity.ErrDenied,
			ipallow.ErrAddressNotAllowed,
			p.Subject,
			action,
			obj.String(),
		)
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (discoveryBlockedAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	ns, err := namespace.ParseName("acme")
	if err != nil {
		return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, err
	}

	return identity.Scope{
		Namespaces:  nil,
		Collections: []identity.Object{identity.CollectionObject(ns, "team-a")},
		Units:       nil,
		Principals:  nil,
	}, nil
}

// discoveryRevocableAuthorizer denies namespace:read and reports a collection
// grant until Revoke runs; after that it reports no object. It reads the
// flag at each request, so a revoked grant is gone on the next request.
type discoveryRevocableAuthorizer struct {
	revoked atomic.Bool
}

func (a *discoveryRevocableAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionCallerRead {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (a *discoveryRevocableAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	none := identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}

	if a.revoked.Load() {
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

// Revoke ends the grant for the next request.
func (a *discoveryRevocableAuthorizer) Revoke() {
	a.revoked.Store(true)
}

func decodeDiscoveryOrganizations(t *testing.T, body string) []organizationBody {
	t.Helper()

	var got userBody
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode user: %v (%s)", err, body)
	}

	return got.Organizations
}

func discoveryContains(orgs []organizationBody, name string) bool {
	for i := range orgs {
		if orgs[i].Name == name {
			return true
		}
	}

	return false
}

// TestCollectionGrantSeesNamespace proves a holder of a grant on a collection
// or on a unit in the namespace sees that namespace in GET /api/user, and no
// other namespace.
func TestCollectionGrantSeesNamespace(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		authz identity.Authorizer
	}{
		{"collection grant", discoveryCollectionAuthorizer{}},
		{"unit grant", discoveryUnitAuthorizer{}},
		{"grant for the unit read action only", discoveryActionAuthorizer{action: identity.ActionUnitRead}},
		{"grant for the output read action only", discoveryActionAuthorizer{action: identity.ActionUnitReadOutputs}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
			stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
			seedNamespaces(t, nsStore, "acme", "beta")

			handler := newUserHandler(t, nsStore, stStore, tc.authz)

			rec := get(t, handler, "/api/user")
			if rec.Code != http.StatusOK {
				t.Fatalf("user answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
			}

			orgs := decodeDiscoveryOrganizations(t, rec.Body.String())
			if !discoveryContains(orgs, "acme") {
				t.Fatalf("organizations = %+v, want acme", orgs)
			}

			if discoveryContains(orgs, "beta") {
				t.Fatalf("organizations = %+v, want no beta", orgs)
			}
		})
	}
}

// TestNoGrantSeesNoNamespace proves a principal with no grant in the
// namespace sees no namespace in GET /api/user.
func TestNoGrantSeesNoNamespace(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme", "beta")

	handler := newUserHandler(t, nsStore, stStore, discoveryEmptyAuthorizer{})

	rec := get(t, handler, "/api/user")
	if rec.Code != http.StatusOK {
		t.Fatalf("user answers %d, want %d", rec.Code, http.StatusOK)
	}

	if orgs := decodeDiscoveryOrganizations(t, rec.Body.String()); len(orgs) != 0 {
		t.Fatalf("organizations = %+v, want none", orgs)
	}
}

// TestEveryScopeSeesNoNamespace proves an every-object scope alone opens no
// namespace: the caller is denied namespace:read and holds no named
// collection or unit in acme.
func TestEveryScopeSeesNoNamespace(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme")

	handler := newUserHandler(t, nsStore, stStore, discoveryEveryAuthorizer{})

	rec := get(t, handler, "/api/user")
	if rec.Code != http.StatusOK {
		t.Fatalf("user answers %d, want %d", rec.Code, http.StatusOK)
	}

	if orgs := decodeDiscoveryOrganizations(t, rec.Body.String()); len(orgs) != 0 {
		t.Fatalf("organizations = %+v, want none", orgs)
	}
}

// TestRevokedGrantLosesNamespace proves a revoked grant loses the namespace
// on the next request: the check reads the grants at each request.
func TestRevokedGrantLosesNamespace(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme")

	authz := &discoveryRevocableAuthorizer{revoked: atomic.Bool{}}
	handler := newUserHandler(t, nsStore, stStore, authz)

	rec := get(t, handler, "/api/user")
	if rec.Code != http.StatusOK {
		t.Fatalf("user before the revoke answers %d, want %d", rec.Code, http.StatusOK)
	}

	if orgs := decodeDiscoveryOrganizations(t, rec.Body.String()); !discoveryContains(orgs, "acme") {
		t.Fatalf("organizations before the revoke = %+v, want acme", orgs)
	}

	authz.Revoke()

	rec = get(t, handler, "/api/user")
	if rec.Code != http.StatusOK {
		t.Fatalf("user after the revoke answers %d, want %d", rec.Code, http.StatusOK)
	}

	if orgs := decodeDiscoveryOrganizations(t, rec.Body.String()); len(orgs) != 0 {
		t.Fatalf("organizations after the revoke = %+v, want none", orgs)
	}
}

// TestBlockedAddressSeesNoNamespace proves a request refused by the IP
// allowlist never falls through to the grant fallback: even with a grant on
// a collection of acme, the caller sees no namespace, and the request still
// answers 200.
func TestBlockedAddressSeesNoNamespace(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme")

	handler := newUserHandler(t, nsStore, stStore, discoveryBlockedAuthorizer{})

	rec := get(t, handler, "/api/user")
	if rec.Code != http.StatusOK {
		t.Fatalf("user outside the allowlist answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	if body := rec.Body.String(); containsOrganization(body, "acme") {
		t.Fatalf("blocked body holds acme, want no namespace: %s", body)
	}
}

// TestBlockedNamespaceKeepsOthers proves that one namespace refused by the IP
// allowlist does not fail the list: the other readable namespace stays in it.
func TestBlockedNamespaceKeepsOthers(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme", "beta")

	handler := newUserHandler(t, nsStore, stStore, discoveryBlockedOneAuthorizer{})

	rec := get(t, handler, "/api/user")
	if rec.Code != http.StatusOK {
		t.Fatalf("user answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	orgs := decodeDiscoveryOrganizations(t, rec.Body.String())
	if discoveryContains(orgs, "acme") {
		t.Fatalf("organizations = %+v, want no acme", orgs)
	}

	if !discoveryContains(orgs, "beta") {
		t.Fatalf("organizations = %+v, want beta", orgs)
	}
}

// discoveryBlockedOneAuthorizer refuses namespace:read on acme with an IP
// allowlist refusal, allows it on every other namespace, and reports a
// collection grant on acme.
type discoveryBlockedOneAuthorizer struct{}

func (discoveryBlockedOneAuthorizer) Authorize(
	ctx context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionNamespaceRead && obj.Namespace().String() == "acme" {
		return discoveryBlockedAuthorizer{}.Authorize(ctx, p, action, obj)
	}

	return nil
}

func (discoveryBlockedOneAuthorizer) ListObjects(
	ctx context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	return discoveryBlockedAuthorizer{}.ListObjects(ctx, p, action)
}

// discoveryLookupFailAuthorizer denies namespace:read, fails the grant lookup
// for unit:read, and reports a collection grant on acme for the output read
// action. A lookup error that is skipped instead of returned would list acme.
type discoveryLookupFailAuthorizer struct{}

func (discoveryLookupFailAuthorizer) Authorize(
	ctx context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	return discoveryCollectionAuthorizer{}.Authorize(ctx, p, action, obj)
}

func (discoveryLookupFailAuthorizer) ListObjects(
	ctx context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	if action == identity.ActionUnitRead {
		return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, errDiscoveryStore
	}

	return discoveryCollectionAuthorizer{}.ListObjects(ctx, p, action)
}

// TestFailedGrantLookupSeesNoNamespace proves that an error of the grant
// lookup fails the request closed: no namespace is listed.
func TestFailedGrantLookupSeesNoNamespace(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme")

	handler := newUserHandler(t, nsStore, stStore, discoveryLookupFailAuthorizer{})

	rec := get(t, handler, "/api/user")
	if rec.Code == http.StatusOK {
		t.Fatalf("user answers %d with a failed grant lookup, want an error status", rec.Code)
	}

	if body := rec.Body.String(); containsOrganization(body, "acme") {
		t.Fatalf("failed body holds acme, want no namespace: %s", body)
	}
}

// discoveryNamespaceGrantAuthorizer denies namespace:read and reports a
// grant of the unit read actions on the namespace acme itself.
type discoveryNamespaceGrantAuthorizer struct{}

func (discoveryNamespaceGrantAuthorizer) Authorize(
	ctx context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	return discoveryCollectionAuthorizer{}.Authorize(ctx, p, action, obj)
}

func (discoveryNamespaceGrantAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, action identity.Action,
) (identity.Scope, error) {
	if action != identity.ActionUnitRead && action != identity.ActionUnitReadOutputs {
		return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
	}

	ns, err := namespace.ParseName("acme")
	if err != nil {
		return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, err
	}

	return identity.Scope{
		Namespaces:  []identity.Object{identity.NamespaceObject(ns)},
		Collections: nil,
		Units:       nil,
		Principals:  nil,
	}, nil
}

// TestNamespaceGrantSeesNamespace proves a unit read grant on the namespace
// itself opens discovery of that namespace, the same as the grant on a
// collection of it, and of no other namespace.
func TestNamespaceGrantSeesNamespace(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme", "beta")

	handler := newUserHandler(t, nsStore, stStore, discoveryNamespaceGrantAuthorizer{})

	rec := get(t, handler, "/api/user")
	if rec.Code != http.StatusOK {
		t.Fatalf("user answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	orgs := decodeDiscoveryOrganizations(t, rec.Body.String())
	if !discoveryContains(orgs, "acme") || discoveryContains(orgs, "beta") {
		t.Fatalf("organizations = %+v, want acme only", orgs)
	}
}

func containsOrganization(body, name string) bool {
	var got userBody
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		return body != "" && strings.Contains(body, name)
	}

	for i := range got.Organizations {
		if got.Organizations[i].Name == name {
			return true
		}
	}

	return false
}

// discoveryActionAuthorizer denies namespace:read and reports a grant on one
// collection of acme for the one action in action only. For every other action
// it reports no object.
type discoveryActionAuthorizer struct {
	action identity.Action
}

func (discoveryActionAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionCallerRead {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (a discoveryActionAuthorizer) ListObjects(
	ctx context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	if action != a.action {
		return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
	}

	return discoveryCollectionAuthorizer{}.ListObjects(ctx, p, action)
}

// discoveryFailingAuthorizer fails namespace:read with an error that is not a
// refusal, and reports a grant on one collection of acme.
type discoveryFailingAuthorizer struct{}

var errDiscoveryStore = errors.New("grant store is down")

func (discoveryFailingAuthorizer) Authorize(
	_ context.Context,
	_ identity.Principal,
	action identity.Action,
	_ identity.Object,
) error {
	if action == identity.ActionCallerRead {
		return nil
	}

	return errDiscoveryStore
}

func (discoveryFailingAuthorizer) ListObjects(
	ctx context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	return discoveryCollectionAuthorizer{}.ListObjects(ctx, p, action)
}

// TestFailedAuthorizerSeesNoNamespace proves that an error of the Authorizer
// that is not a refusal fails the request closed: the grant fallback does not
// run, and the body holds no namespace.
func TestFailedAuthorizerSeesNoNamespace(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme")

	handler := newUserHandler(t, nsStore, stStore, discoveryFailingAuthorizer{})

	rec := get(t, handler, "/api/user")
	if rec.Code == http.StatusOK {
		t.Fatalf("user answers %d with a failed Authorizer, want an error status", rec.Code)
	}

	if body := rec.Body.String(); containsOrganization(body, "acme") {
		t.Fatalf("failed body holds acme, want no namespace: %s", body)
	}
}
