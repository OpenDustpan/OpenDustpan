package principal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// memberStore is a fake store that knows one principal of the namespace
// acme and accepts every membership. It records the memberships that reach
// it. Any other method panics.
type memberStore struct {
	principal.Store

	added *int
}

func (memberStore) GetPrincipal(_ context.Context, id string) (principal.Principal, error) {
	return principal.Principal{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), DisabledAt: time.Time{}, ID: id,
		Subject: "ci-acme", DisplayName: "", Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: "acme", GroupCount: 0, BreakGlass: false,
	}, nil
}

func (s memberStore) AddMember(context.Context, principal.Membership, audit.Entry) error {
	*s.added++

	return nil
}

// TestAddMemberRefusesNamespacePrincipal proves that the Service refuses a
// principal of a namespace as a member of a group before the store sees the
// change, so the refusal does not depend on the store.
func TestAddMemberRefusesNamespacePrincipal(t *testing.T) {
	t.Parallel()

	added := 0

	svc, err := principal.NewService(principal.Config{
		Store: memberStore{Store: nil, added: &added}, Guard: nil, Namespaces: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	caller := identity.Principal{
		Subject: "tester", DisplayName: "", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
	}

	_, err = svc.AddMember(t.Context(), testEntry(), caller, "grp-1", "prn-1")
	if !errors.Is(err, principal.ErrInvalid) {
		t.Fatalf("add a principal of a namespace to a group: err = %v, want ErrInvalid", err)
	}

	if added != 0 {
		t.Fatalf("the store got %d memberships, want none", added)
	}
}
