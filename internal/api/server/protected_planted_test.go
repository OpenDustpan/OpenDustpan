package server_test

import (
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestGroupProtectedFlagIgnoresPlantedNamespacePrincipalGrant proves that the
// protected flag of a group agrees with the authorizer for a planted row: a
// group with a namespace-wide act-as edge on a namespace whose service
// principal holds a planted grant on the installation is not protected,
// because the authorizer honors no such grant.
func TestGroupProtectedFlagIgnoresPlantedNamespacePrincipalGrant(t *testing.T) {
	t.Parallel()

	svc, st := newTestPrincipalStore(t)
	handler := newReachPrincipalHandler(t, svc, st, routeReach(st))
	ctx := t.Context()
	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	var group groupDoc

	mustCall(t, handler, apiCall{
		http.MethodPost, "/api/server/groups", `{"name":"edge"}`, http.StatusCreated,
	}, &group)

	// The planted grant is written before the principal belongs to a
	// namespace, as a restore of an older backup holds it.
	planted := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: "grt-planted",
		Role: access.RoleSiteAdministrator, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-ci-acme"},
	}
	fence, e := storetest.GrantWrite(t, st, planted.Object, at, "grant:"+planted.ID)

	if err := st.CreateGrant(ctx, planted, fence, e, nil); err != nil {
		t.Fatalf("plant grant: %v", err)
	}

	err := st.CreateNamespace(ctx, namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: at, Name: "acme", PrimaryInstallation: "",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	bot := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-ci-acme", Subject: "ci-acme",
		DisplayName: "ci-acme", Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: "acme", GroupCount: 0, BreakGlass: false,
	}
	fence, e = storetest.GrantWrite(t, st, identity.NamespaceObject("acme"), at, principal.Ref(bot.ID))

	if err = st.CreateNamespacePrincipal(ctx, bot, fence, e); err != nil {
		t.Fatalf("create namespace principal: %v", err)
	}

	role := access.Role{
		CreatedAt: at, UpdatedAt: at, ID: "rol-ns-tokens", Name: "ns-tokens", Description: "", BuiltIn: false,
		Actions: []identity.Action{identity.ActionNamespaceManagePrincipalTokens},
	}
	if err = st.CreateRole(ctx, role, storetest.ServerChange(at, "role:"+role.ID)); err != nil {
		t.Fatalf("create role: %v", err)
	}

	edge := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.NamespaceObject("acme"), ID: "grt-edge",
		Role: role.ID, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectGroup, ID: group.ID},
	}
	fence, e = storetest.GrantWrite(t, st, edge.Object, at, "grant:"+edge.ID)

	if err = st.CreateGrant(ctx, edge, fence, e, nil); err != nil {
		t.Fatalf("create edge grant: %v", err)
	}

	var got groupDoc

	mustCall(t, handler, apiCall{http.MethodGet, "/api/server/groups/" + group.ID, "", http.StatusOK}, &got)

	if got.Protected {
		t.Fatal("protected = true, want false: the planted grant of a namespace principal gives no power")
	}
}
