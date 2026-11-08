package tofuunit_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var errStoreDown = errors.New("store down")

// rulesStub answers Place with a rule or an error, and records the lookup.
type rulesStub struct {
	rule collection.Rule
	err  error
	look [3]string
}

func (r *rulesStub) Place(
	_ context.Context, _ namespace.Name, tool, project, name string,
) (collection.Rule, error) {
	r.look = [3]string{tool, project, name}

	return r.rule, r.err
}

// collectionsStub answers the two collection reads.
type collectionsStub struct {
	id    string
	found bool
	err   error
}

func (c collectionsStub) CollectionID(context.Context, identity.Object) (string, bool, error) {
	return c.id, c.found, c.err
}

func (c collectionsStub) CollectionOf(context.Context, identity.Object) (string, bool, error) {
	return "default", c.found, c.err
}

// guardStub allows or denies every action, and records the last action.
type guardStub struct {
	err    error
	action identity.Action
}

func (g *guardStub) Authorize(
	_ context.Context, _ *http.Request, _ identity.Principal, action identity.Action, _ identity.Object,
) error {
	g.action = action

	return g.err
}

type namespacesStub struct{ err error }

func (n namespacesStub) WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	return n.Get(ctx, name)
}

func (n namespacesStub) Get(context.Context, namespace.Name) (namespace.Namespace, error) {
	var none namespace.Namespace

	return none, n.err
}

type workspacesStub struct{ exists bool }

func (w workspacesStub) Get(context.Context, workspace.Ref) (workspace.Workspace, error) {
	var none workspace.Workspace

	if w.exists {
		return none, nil
	}

	return none, workspace.ErrNotFound
}

func newRef(t *testing.T) workspace.Ref {
	t.Helper()

	name, err := workspace.ParseName("network")
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	return workspace.Ref{Namespace: "acme", Name: name}
}

func testRule() collection.Rule {
	var rule collection.Rule

	rule.ID = "rule-7"
	rule.Tool = "opentofu"
	rule.Collection = "platform"

	return rule
}

// newRules returns a rulesStub that answers rule and err.
func newRules(rule collection.Rule, err error) *rulesStub {
	return &rulesStub{rule: rule, err: err, look: [3]string{}}
}

// newGuard returns a guardStub that answers err.
func newGuard(err error) *guardStub {
	return &guardStub{err: err, action: ""}
}

// noRule is the zero rule that a lookup returns with an error.
func noRule() collection.Rule {
	var none collection.Rule

	return none
}

func noRuleError() error {
	return &collection.PlacementError{Namespace: "acme", Tool: "opentofu", Project: "*", Stack: "network"}
}

// request is what one Place call gets from a case.
type request struct {
	guard      *guardStub
	nsErr      error
	wsExists   bool
	placer     *tofuunit.Placer
	wantAction identity.Action
}

func placeOnce(t *testing.T, c request) (workspace.Placement, error) {
	t.Helper()

	var principal identity.Principal

	principal.Subject = "alice"

	httpReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(""))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	got, placeErr := c.placer.Place(t.Context(), tofuunit.PlaceRequest{
		Request:    httpReq,
		Principal:  principal,
		Ref:        newRef(t),
		Guard:      c.guard,
		Namespaces: namespacesStub{err: c.nsErr},
		Workspaces: workspacesStub{exists: c.wsExists},
	})

	if c.guard.action != c.wantAction {
		t.Errorf("authorized action = %q, want %q", c.guard.action, c.wantAction)
	}

	return got, placeErr
}

func TestPlaceChoosesTheCollectionOfTheRule(t *testing.T) {
	t.Parallel()

	rules := newRules(testRule(), nil)
	placer := &tofuunit.Placer{Rules: rules, Collections: collectionsStub{id: "c-1", found: true, err: nil}}

	got, err := placeOnce(t, request{
		guard: newGuard(nil), nsErr: nil, wsExists: false, placer: placer,
		wantAction: identity.ActionCollectionCreateUnit,
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	if rules.look != [3]string{"opentofu", "*", "network"} {
		t.Errorf("rule lookup = %v, want opentofu * network", rules.look)
	}

	if got.Collection.Name != "platform" || got.CollectionID != "c-1" || !got.Complete("acme") {
		t.Errorf("placement = %+v, want collection platform with ID c-1", got)
	}

	if !strings.Contains(got.Entry.Version, "to=platform;rule=rule-7") {
		t.Errorf("audit entry version = %q, want it to name the collection and the rule", got.Entry.Version)
	}
}

func TestPlaceRefusals(t *testing.T) {
	t.Parallel()

	rule := testRule()
	found := collectionsStub{id: "c-1", found: true, err: nil}
	create := identity.ActionCollectionCreateUnit

	cases := []struct {
		name       string
		rules      *rulesStub
		collection collectionsStub
		guardErr   error
		nsErr      error
		wsExists   bool
		wantErr    error
		wantAction identity.Action
	}{
		{
			"no rule matches",
			newRules(noRule(), noRuleError()), found, nil, nil, false, collection.ErrNoPlacement, "",
		},
		{
			"no rule matches and the namespace is gone",
			newRules(noRule(), noRuleError()), found, nil, namespace.ErrNotFound, false, namespace.ErrNotFound, "",
		},
		{
			"no rule matches and another installation owns the namespace",
			newRules(noRule(), noRuleError()), found, nil, namespace.ErrNotHome, false, namespace.ErrNotHome, "",
		},
		{
			"no rule matches and the workspace exists",
			newRules(noRule(), noRuleError()), found, nil, nil, true, workspace.ErrAlreadyExists, "",
		},
		{
			"the caller may not create in the collection",
			newRules(rule, nil), found, identity.ErrDenied, nil, false, identity.ErrDenied, create,
		},
		{
			"the rule names a collection that is gone",
			newRules(rule, nil),
			collectionsStub{id: "", found: false, err: nil},
			nil, nil, false,
			collection.ErrNotFound, create,
		},
		{
			"the collection read fails",
			newRules(rule, nil),
			collectionsStub{id: "", found: false, err: errStoreDown},
			nil, nil, false,
			errStoreDown, create,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := placeOnce(t, request{
				guard: newGuard(tc.guardErr), nsErr: tc.nsErr, wsExists: tc.wsExists,
				placer:     &tofuunit.Placer{Rules: tc.rules, Collections: tc.collection},
				wantAction: tc.wantAction,
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Place error = %v, want %v", err, tc.wantErr)
			}

			if got.CollectionID != "" || got.Collection.Name != "" {
				t.Fatalf("Place returned a placement %+v with an error", got)
			}
		})
	}
}

func TestPlaceWithoutPlacerRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		placer  *tofuunit.Placer
		nsErr   error
		wantErr error
	}{
		{"nil placer", nil, nil, tofuunit.ErrPlacementOff},
		{"placer with no rules", new(tofuunit.Placer), nil, tofuunit.ErrPlacementOff},
		{"nil placer and no namespace", nil, namespace.ErrNotFound, namespace.ErrNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := placeOnce(t, request{
				guard: newGuard(nil), nsErr: tc.nsErr, wsExists: false, placer: tc.placer, wantAction: "",
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Place error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRequire(t *testing.T) {
	t.Parallel()

	obj, err := tofuunit.Object(newRef(t))
	if err != nil {
		t.Fatalf("object: %v", err)
	}

	cases := []struct {
		name    string
		placer  *tofuunit.Placer
		wantErr bool
		wantIs  error
	}{
		{"unit in a collection", placerOf(collectionsStub{id: "", found: true, err: nil}), false, nil},
		{
			"unit in no collection",
			placerOf(collectionsStub{id: "", found: false, err: nil}), true, collection.ErrNoCollection,
		},
		{"store failure", placerOf(collectionsStub{id: "", found: false, err: errStoreDown}), true, errStoreDown},
		{"no collections port", new(tofuunit.Placer), true, tofuunit.ErrPlacementOff},
		{"nil placer", nil, true, tofuunit.ErrPlacementOff},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.placer.Require(t.Context(), obj)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Require = %v, want error %v", err, tc.wantErr)
			}

			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("Require = %v, want %v", err, tc.wantIs)
			}
		})
	}
}

// TestRequireNamesTheMoveCommand proves that the refusal names the command that
// puts the workspace in a collection.
func TestRequireNamesTheMoveCommand(t *testing.T) {
	t.Parallel()

	obj, err := tofuunit.Object(newRef(t))
	if err != nil {
		t.Fatalf("object: %v", err)
	}

	err = placerOf(collectionsStub{id: "", found: false, err: nil}).Require(t.Context(), obj)
	if err == nil || !strings.Contains(err.Error(), "PUT /api/inventory/unit-collection") {
		t.Fatalf("Require = %v, want it to name the move command", err)
	}
}

func placerOf(c collectionsStub) *tofuunit.Placer {
	return &tofuunit.Placer{Rules: nil, Collections: c}
}
