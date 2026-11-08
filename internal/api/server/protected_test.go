package server_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// errGrantRead is the error of a failed read of the grants of a reach check.
var errGrantRead = errors.New("grant store is down")

// failingGrantReads fails each read of the grants of a reach check.
type failingGrantReads struct {
	access.GrantLookups

	err error
}

func (f failingGrantReads) GrantsOf(context.Context, access.Subject) ([]access.Grant, error) {
	return nil, f.err
}

func (f failingGrantReads) GrantsOfSubjects(context.Context, []access.Subject) ([]access.Grant, error) {
	return nil, f.err
}

// TestGroupProtectedFlag proves that the group list and the group get answer
// the protected flag of each group, and that a check that cannot read answers
// an error, never protected: false.
func TestGroupProtectedFlag(t *testing.T) {
	t.Parallel()

	svc, st := newTestPrincipalStore(t)

	handler := newReachPrincipalHandler(t, svc, st, routeReach(st))

	var protected, open groupDoc

	mustCall(t, handler, apiCall{
		http.MethodPost, "/api/server/groups", `{"name":"admins"}`, http.StatusCreated,
	}, &protected)
	mustCall(t, handler, apiCall{
		http.MethodPost, "/api/server/groups", `{"name":"readers"}`, http.StatusCreated,
	}, &open)

	// The first group holds an operator grant on the installation.
	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: "grt-protected",
		Role: access.RoleInstallationOperator, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectGroup, ID: protected.ID},
	}
	fence, e := storetest.GrantWrite(t, st, grt.Object, at, "grant:"+grt.ID)

	if err := st.CreateGrant(t.Context(), grt, fence, e, nil); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	want := map[string]bool{protected.ID: true, open.ID: false}

	var list groupListDoc

	mustCall(t, handler, apiCall{http.MethodGet, "/api/server/groups", "", http.StatusOK}, &list)

	if len(list.Groups) != len(want) {
		t.Fatalf("group list holds %d groups, want %d", len(list.Groups), len(want))
	}

	for i := range list.Groups {
		g := list.Groups[i]

		wantProtected, ok := want[g.ID]
		if !ok {
			t.Fatalf("group list holds the unknown group %q", g.ID)
		}

		if g.Protected != wantProtected {
			t.Errorf("group list: protected of %q = %t, want %t", g.ID, g.Protected, wantProtected)
		}
	}

	for id, wantProtected := range want {
		var got groupDoc

		mustCall(t, handler, apiCall{http.MethodGet, "/api/server/groups/" + id, "", http.StatusOK}, &got)

		if got.Protected != wantProtected {
			t.Errorf("protected of group %q = %t, want %t", id, got.Protected, wantProtected)
		}
	}

	// A check that cannot read the grants answers an error, never a false
	// flag.
	failing := newReachPrincipalHandler(t, svc, st, access.ReachConfig{
		Grants:      failingGrantReads{GrantLookups: st, err: errGrantRead},
		Roles:       st,
		Callers:     st,
		Principals:  st,
		Collections: nil,
		Units:       st,
	})

	var person principalDoc

	mustCall(t, handler, apiCall{
		http.MethodPost, "/api/server/principals",
		`{"subject":"alice@example.com","displayName":"Alice","kind":"person"}`, http.StatusCreated,
	}, &person)

	// The person joins the protected group, so that the groups of a
	// principal hold a group for the failing check to read.
	mustCall(t, handler, apiCall{
		http.MethodPost, "/api/server/groups/" + protected.ID + "/members",
		`{"principalID":"` + person.ID + `"}`, http.StatusCreated,
	}, nil)

	failingCalls := []apiCall{
		{http.MethodGet, "/api/server/groups", "", 0},
		{http.MethodGet, "/api/server/groups/" + protected.ID, "", 0},
		{http.MethodPost, "/api/server/groups", `{"name":"hidden"}`, 0},
		{http.MethodPatch, "/api/server/groups/" + protected.ID, `{"name":"renamed"}`, 0},
		{http.MethodGet, "/api/server/principals/" + person.ID + "/groups", "", 0},
	}

	for _, c := range failingCalls {
		status, body := call(t, failing, c)

		if status != http.StatusInternalServerError {
			t.Errorf("%s %s over a failed check answers %d, want 500 (%s)", c.method, c.path, status, body)
		}

		if strings.Contains(body, `"protected"`) {
			t.Errorf("%s %s over a failed check answers a protected flag: %s", c.method, c.path, body)
		}
	}
}
