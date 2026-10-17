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

func (s memberStore) AddMember(context.Context, principal.Membership, audit.Entry, principal.MemberAudit) error {
	*s.added++

	return nil
}

// captureMemberStore is a fake store that knows one person of the
// installation and records the entry of its membership add.
type captureMemberStore struct {
	principal.Store

	captured *audit.Entry
	actsAs   []string
}

// fixedActs answers ActsAs with a fixed list.
type fixedActs []string

func (f fixedActs) ActsAs(context.Context, string) ([]string, error) {
	return f, nil
}

func (captureMemberStore) GetPrincipal(_ context.Context, id string) (principal.Principal, error) {
	return principal.Principal{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), DisabledAt: time.Time{}, ID: id,
		Subject: "alice", DisplayName: "", Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}, nil
}

func (s captureMemberStore) AddMember(
	ctx context.Context, _ principal.Membership, e audit.Entry, more principal.MemberAudit,
) error {
	if more != nil {
		changes, err := more(ctx, fixedActs(s.actsAs))
		if err != nil {
			return err
		}

		e.Changes = append(e.Changes, changes...)
	}

	*s.captured = e

	return nil
}

// TestAddMemberRecordsMembershipFields proves that the entry of a membership
// add names the source, source ID, state and pinned values.
func TestAddMemberRecordsMembershipFields(t *testing.T) {
	t.Parallel()

	var captured audit.Entry

	svc, err := principal.NewService(principal.Config{
		Store: captureMemberStore{
			Store:    nil,
			captured: &captured,
			actsAs:   nil,
		},
		Guard:      nil,
		Namespaces: nil,
		Tokens:     nil,
		Now:        nil,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	caller := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "tester", DisplayName: "", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
	}

	if _, err := svc.AddMember(t.Context(), testEntry(), caller, "grp-1", "prn-1"); err != nil {
		t.Fatalf("add member: %v", err)
	}

	changes := make(map[string]string, len(captured.Changes))
	for _, c := range captured.Changes {
		changes[c.Key] = c.New
	}

	if changes["source"] != "local" || changes["sourceID"] != "" ||
		changes["state"] != "active" || changes["pinned"] != "false" {
		t.Fatalf("changes = %+v, want source local, empty source ID, state active and pinned false", captured.Changes)
	}
}

// TestAddMemberRefusesNamespacePrincipal proves that the Service refuses a
// principal of a namespace as a member of a group before the store sees the
// change, so the refusal does not depend on the store.
func TestAddMemberRefusesNamespacePrincipal(t *testing.T) {
	t.Parallel()

	added := 0

	svc, err := principal.NewService(principal.Config{
		Store: memberStore{Store: nil, added: &added}, Guard: nil, Namespaces: nil, Tokens: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	caller := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
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

// TestAddMemberNamesWhatTheMemberActsAs proves that the entry of a
// membership add names, in one change "actsAs" with one line each, what
// the grants of the group let the new member act as, and that a group that
// gives no act-as right adds no such change.
func TestAddMemberNamesWhatTheMemberActsAs(t *testing.T) {
	t.Parallel()

	caller := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "tester", DisplayName: "", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
	}

	for _, tc := range []struct {
		name   string
		actsAs []string
		want   string
		found  bool
	}{
		{
			name: "every form", actsAs: []string{"@server", "acme", "principal:prn-a", "principal:prn-b"},
			want: "@server\nacme\nprincipal:prn-a\nprincipal:prn-b", found: true,
		},
		{name: "nothing", actsAs: nil, want: "", found: false},
	} {
		var captured audit.Entry

		svc, err := principal.NewService(principal.Config{
			Store: captureMemberStore{Store: nil, captured: &captured, actsAs: tc.actsAs}, Guard: nil,
			Namespaces: nil, Tokens: nil, Now: nil, NewID: nil,
		})
		if err != nil {
			t.Fatalf("service: %v", err)
		}

		if _, err := svc.AddMember(t.Context(), testEntry(), caller, "grp-1", "prn-1"); err != nil {
			t.Fatalf("%s: add member: %v", tc.name, err)
		}

		got, found := "", false

		for _, c := range captured.Changes {
			if c.Key == "actsAs" {
				got, found = c.New, true
			}
		}

		if got != tc.want || found != tc.found {
			t.Fatalf("%s: actsAs = %q (found %t), want %q (found %t)", tc.name, got, found, tc.want, tc.found)
		}
	}
}
