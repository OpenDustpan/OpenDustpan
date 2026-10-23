package pulumi_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// denyUnitReadOnBeta denies unit:read on the namespace beta and allows every
// other action, as a custom role with namespace:read and without unit:read on
// beta does.
type denyUnitReadOnBeta struct{}

func (denyUnitReadOnBeta) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionUnitRead && obj.Namespace().String() == "beta" {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (denyUnitReadOnBeta) ListObjects(
	_ context.Context, p identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// failUnitRead fails each check of unit:read with an error that is not a
// denial, and allows every other action.
type failUnitRead struct{}

var errAuthorizerDown = errors.New("authorizer is not available")

func (failUnitRead) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, _ identity.Object,
) error {
	if action == identity.ActionUnitRead {
		return errAuthorizerDown
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (failUnitRead) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// TestListStacksAllNeedsUnitRead proves that the stack list with no
// organization holds the stacks of a namespace only when the caller holds
// unit:read on it, as the list with that organization does.
func TestListStacksAllNeedsUnitRead(t *testing.T) {
	t.Parallel()

	handler := manyStacks(t, 2, denyUnitReadOnBeta{}, "acme", "beta")

	keys, _ := walkStacks(t, handler, "/api/user/stacks?x=1")
	if len(keys) != 2 || keys[0][:5] != "acme/" || keys[1][:5] != "acme/" {
		t.Fatalf("stacks = %v, want the 2 stacks of acme and none of beta", keys)
	}

	if rec := get(t, handler, "/api/user/stacks?organization=beta"); rec.Code != http.StatusForbidden {
		t.Fatalf("GET with organization beta answers %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

// TestListStacksAllWithNoUnitReadIsEmpty proves that a caller with
// namespace:read and without unit:read on every namespace gets an empty list.
func TestListStacksAllWithNoUnitReadIsEmpty(t *testing.T) {
	t.Parallel()

	handler := manyStacks(t, 3, selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitRead: true}},
		"acme", "beta")

	page := readStackPage(t, handler, "/api/user/stacks?x=1")
	if len(page.Stacks) != 0 || page.ContinuationToken != nil {
		t.Fatalf("page = %+v, want no stack and no token", page)
	}
}

// TestListStacksAllFailsClosedOnUnitRead proves that an error of the
// authorizer for unit:read that is not a denial answers an error and no
// stack.
func TestListStacksAllFailsClosedOnUnitRead(t *testing.T) {
	t.Parallel()

	handler := manyStacks(t, 2, failUnitRead{}, "acme")

	rec := get(t, handler, "/api/user/stacks?x=1")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET answers %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}
