package access

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errPrincipalRead is the error of the failed read of a principal.
var errPrincipalRead = errors.New("principal store is down")

// principalsOf is a principal port of the protected check: it answers one
// record per ID, or the error err.
type principalsOf struct {
	recs map[string]principal.Principal
	err  error
}

func (p principalsOf) GetPrincipal(_ context.Context, id string) (principal.Principal, error) {
	if p.err != nil {
		return principal.Principal{}, p.err
	}

	rec, ok := p.recs[id]
	if !ok {
		return principal.Principal{}, principal.ErrNotFound
	}

	return rec, nil
}

func (p principalsOf) ListNamespacePrincipals(
	context.Context, namespace.Name, principal.Query,
) ([]principal.Principal, int64, error) {
	return nil, 0, nil
}

// TestProtectedReadsTheObjectOfEachAction proves that the protected check
// reads the object of each action on its own: a reach that holds a sensitive
// action on a namespace, a collection or a unit, and on a principal of a
// namespace, gives no protection. The check is a defense-in-depth layer: it
// does not rely on the walker, which records no sensitive action on those
// objects.
func TestProtectedReadsTheObjectOfEachAction(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	install := principal.Principal{
		CreatedAt: at, UpdatedAt: at, ID: "prn-install", Subject: "ci-install", DisplayName: "ci-install",
		Kind: principal.KindService, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
		DisabledAt: time.Time{}, BreakGlass: false,
	}

	inNamespace := install
	inNamespace.ID = "prn-ns"
	inNamespace.Namespace = "acme"

	disabled := install
	disabled.ID = "prn-disabled"
	disabled.DisabledAt = at

	unit, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	ports := principalsOf{recs: map[string]principal.Principal{
		"prn-install": install, "prn-ns": inNamespace, "prn-disabled": disabled,
	}, err: nil}

	tests := []struct {
		port    PrincipalNamespaces
		wantErr error
		name    string
		act     identity.Action
		obj     identity.Object
		want    bool
	}{
		{
			name: "a sensitive action on the installation protects the group", wantErr: nil,
			obj: identity.ServerObject(), act: identity.ActionServerBackup, port: ports, want: true,
		},
		{
			name: "a data action on the installation gives no protection", wantErr: nil,
			obj: identity.ServerObject(), act: identity.ActionNamespaceRead, port: ports, want: false,
		},
		{
			name: "a sensitive action on a namespace gives no protection", wantErr: nil,
			obj: identity.NamespaceObject("acme"), act: identity.ActionServerBackup, port: ports, want: false,
		},
		{
			name: "a sensitive action on a collection gives no protection", wantErr: nil,
			obj: identity.CollectionObject("acme", "payments"), act: identity.ActionServerBackup,
			port: ports, want: false,
		},
		{
			name: "a sensitive action on a unit gives no protection", wantErr: nil,
			obj: unit, act: identity.ActionServerBackup, port: ports, want: false,
		},
		{
			name: "a sensitive action on a principal of the installation protects the group", wantErr: nil,
			obj: identity.PrincipalObject("prn-install"), act: identity.ActionPrincipalManageTokens,
			port: ports, want: true,
		},
		{
			name: "a sensitive action on a principal of a namespace gives no protection", wantErr: nil,
			obj: identity.PrincipalObject("prn-ns"), act: identity.ActionPrincipalManageTokens,
			port: ports, want: false,
		},
		{
			name: "a sensitive action on a disabled principal of the installation protects the group", wantErr: nil,
			obj: identity.PrincipalObject("prn-disabled"), act: identity.ActionPrincipalManageTokens,
			port: ports, want: true,
		},
		{
			name: "a read-only administration action on the installation gives no protection", wantErr: nil,
			obj: identity.ServerObject(), act: identity.ActionServerReadAccess, port: ports, want: false,
		},
		{
			name: "an unknown principal gives no protection", wantErr: nil,
			obj: identity.PrincipalObject("prn-missing"), act: identity.ActionPrincipalManageTokens,
			port: ports, want: false,
		},
		{
			name: "a failed read of a principal fails closed", wantErr: errPrincipalRead,
			obj: identity.PrincipalObject("prn-install"), act: identity.ActionPrincipalManageTokens,
			port: principalsOf{recs: nil, err: errPrincipalRead}, want: false,
		},
		{
			name: "a missing principal port fails closed", wantErr: ErrMissingDependency,
			obj: identity.PrincipalObject("prn-install"), act: identity.ActionPrincipalManageTokens,
			port: nil, want: false,
		},
		{
			name: "an object with no known kind fails closed", wantErr: ErrInvalid,
			obj: identity.CallerObject(), act: identity.ActionServerBackup,
			port: ports, want: false,
		},
		{
			name: "an action that no family classifies fails closed", wantErr: ErrUnknownAction,
			obj: identity.ServerObject(), act: identity.Action("future:read-installation"),
			port: ports, want: false,
		},
	}

	for i := range tests {
		tc := &tests[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := Reach{
				entries: map[string]*reachEntry{
					"target": {object: tc.obj, actions: map[identity.Action]bool{tc.act: true}},
				},
				collections: nil,
				units:       nil,
				principals:  tc.port,
			}

			got, err := r.protected(t.Context())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("protected of %s on %q = %v, want %v", tc.act, tc.obj.String(), err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("protected of %s on %q = %t, want %t", tc.act, tc.obj.String(), got, tc.want)
			}
		})
	}
}

// TestProtectedKindRefusesAnUnknownKind proves that the protected check
// fails closed on a kind that it does not know: KindOf returns no such kind
// today, and a kind that it returns tomorrow must give an error, not a
// false flag.
func TestProtectedKindRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()

	r := Reach{entries: nil, collections: nil, units: nil, principals: nil}

	got, err := r.protectedKind(t.Context(), identity.ServerObject(), ObjectKind("future-kind"))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("protected of an unknown kind = %v, want ErrInvalid", err)
	}

	if got {
		t.Error("protected of an unknown kind = true, want false")
	}
}

// TestProtectedSkipsANilEntry proves that a reach with a nil entry holds
// nothing and answers no error: the walker records no nil entry, and the
// check must not panic on one.
func TestProtectedSkipsANilEntry(t *testing.T) {
	t.Parallel()

	r := Reach{entries: map[string]*reachEntry{"nil": nil}, collections: nil, units: nil, principals: nil}

	got, err := r.protected(t.Context())
	if err != nil {
		t.Fatalf("protected with a nil entry = %v, want no error", err)
	}

	if got {
		t.Error("protected with a nil entry = true, want false")
	}
}
