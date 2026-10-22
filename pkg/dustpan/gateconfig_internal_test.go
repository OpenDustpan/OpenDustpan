package dustpan

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestGroupDirectory proves the answers of the group directory of the gate
// configuration over a real store: the members of a group by name without
// regard to case, a name that is not valid, a group that does not exist, and
// a server that keeps no groups.
func TestGroupDirectory(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st,
		Guard:      principal.AdminOnlyGuard{},
		Namespaces: st,
		Tokens:     nil,
		Now:        nil,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	entry := storetest.ServerChange(time.Time{}, "")
	entry.Subject, entry.Action = groupAdmin().Subject, string(identity.ActionServerManageGroups)

	seedGroup(t, svc, entry, "Platform", []string{"alice", "bob"})
	seedGroup(t, svc, entry, "Platform-extra", []string{"carol"})
	seedGroup(t, svc, entry, "empty", nil)

	tests := []struct {
		name    string
		dir     groupDirectory
		group   string
		members int64
		want    error
	}{
		{"members of a group", groupDirectory{groups: svc}, "Platform", 2, nil},
		{"name in another case", groupDirectory{groups: svc}, "platform", 2, nil},
		{"a longer name does not match", groupDirectory{groups: svc}, "Platform-ex", 0, gate.ErrNotFound},
		{"group with no member", groupDirectory{groups: svc}, "empty", 0, nil},
		{"group that does not exist", groupDirectory{groups: svc}, "nogroup", 0, gate.ErrNotFound},
		{"name that is not valid", groupDirectory{groups: svc}, "<b>x</b>", 0, gate.ErrInvalid},
		{"empty name", groupDirectory{groups: svc}, "", 0, gate.ErrInvalid},
		{"server with no groups", groupDirectory{groups: nil}, "Platform", 0, gate.ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.dir.GroupMembers(t.Context(), tt.group)
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("GroupMembers(%q) = %d, %v, want %v", tt.group, got, err, tt.want)
			}

			if got != tt.members {
				t.Fatalf("GroupMembers(%q) = %d, want %d members", tt.group, got, tt.members)
			}
		})
	}
}

// seedGroup makes the group name with one person for each subject.
func seedGroup(t *testing.T, svc *principal.Service, entry audit.Entry, name string, subjects []string) {
	t.Helper()

	g, err := svc.CreateGroup(t.Context(), entry, principal.GroupInput{Name: name, Description: ""})
	if err != nil {
		t.Fatalf("create group %q: %v", name, err)
	}

	for _, subject := range subjects {
		p, err := svc.CreatePrincipal(t.Context(), entry,
			principal.Input{Subject: subject, DisplayName: "", Kind: principal.KindPerson})
		if err != nil {
			t.Fatalf("create person %q: %v", subject, err)
		}

		if _, err := svc.AddMember(t.Context(), entry, groupAdmin(), g.ID, p.ID); err != nil {
			t.Fatalf("add %q to %q: %v", subject, name, err)
		}
	}
}

// groupAdmin is the bootstrap administrator, who holds every action.
func groupAdmin() identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "admin", DisplayName: "",
		Admin: true, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}
}
