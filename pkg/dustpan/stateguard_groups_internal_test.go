package dustpan

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// groupsGuard is a gate service over a real store whose access limits name
// the groups ops and sre.
type groupsGuard struct {
	svc  *gateService
	unit identity.Object
}

// newGroupsGuard seeds the groups ops and sre with active and suspended
// members, stores the access limit and returns the gate service.
func newGroupsGuard(t *testing.T, limit gate.AccessLimit) groupsGuard {
	t.Helper()

	ctx := t.Context()

	st := openGateStore(t)

	nss, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	createPrincipals(t, st, time.Unix(0, 0), "ops-member", "non-member", "sre-active", "sre-suspended")

	n, err := nss.Create(ctx, "acme")
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	groups := []principal.Group{
		{
			CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), ID: "grp-ops", Name: "ops",
			Description: "", Source: principal.SourceLocal, MemberCount: 0,
			ClosedToIdentityProvider: false,
		},
		{
			CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), ID: "grp-sre", Name: "sre",
			Description: "", Source: principal.SourceLocal, MemberCount: 0,
			ClosedToIdentityProvider: false,
		},
	}
	for i := range groups {
		g := &groups[i]
		if err = st.CreateGroup(ctx, *g, storetest.ServerChange(time.Unix(0, 0), "group:"+g.ID)); err != nil {
			t.Fatalf("create group %q: %v", g.Name, err)
		}
	}

	members := []principal.Membership{
		{
			AddedAt: time.Unix(0, 0), SuspendedAt: time.Time{}, GroupID: "grp-ops",
			PrincipalID: "prn-ops-member", Source: principal.MembershipSourceLocal, SourceID: "",
			State: principal.MembershipStateActive, Pinned: false,
		},
		{
			AddedAt: time.Unix(0, 0), SuspendedAt: time.Time{}, GroupID: "grp-sre",
			PrincipalID: "prn-sre-active", Source: principal.MembershipSourceLocal, SourceID: "",
			State: principal.MembershipStateActive, Pinned: false,
		},
		{
			AddedAt: time.Unix(0, 0), SuspendedAt: time.Unix(1, 0), GroupID: "grp-sre",
			PrincipalID: "prn-sre-suspended", Source: principal.MembershipSourceLocal, SourceID: "",
			State: principal.MembershipStateSuspended, Pinned: false,
		},
	}

	for i := range members {
		m := &members[i]
		entry := storetest.ServerChange(time.Unix(0, 0), "principal:"+m.PrincipalID)

		err = st.AddMember(t.Context(), *m, entry, nil)
		if err != nil {
			t.Fatalf("add %q to %q: %v", m.PrincipalID, m.GroupID, err)
		}
	}

	w := workspace.Workspace{
		CreatedAt:   time.Unix(0, 0),
		UpdatedAt:   time.Unix(0, 0),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          "ws-web",
		NamespaceID: n.ID,
		Ref:         workspace.Ref{Namespace: "acme", Name: "web"},
		Overrides:   namespace.Overrides{Forced: false, Unlocked: false},
		Revision:    1,
	}
	if err = st.CreateWorkspace(ctx, w, n.Fence()); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	cfg := new(settings)
	cfg.namespaceStore, cfg.inventoryReader = st, st
	cfg.now = func() time.Time { return time.Unix(1_700_000_000, 0) }

	gates, err := newStateWriteGates(cfg, nss)
	if err != nil || gates.svc == nil {
		t.Fatalf("newStateWriteGates() = %v, %v", gates.svc, err)
	}

	_, err = st.AccessLimits().Put(ctx, limit, 0, n.Fence(), deletedGuardEntry("namespace:manage-gates"))
	if err != nil {
		t.Fatalf("put access limit %q: %v", limit.ID, err)
	}

	unit, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	return groupsGuard{svc: gates.svc, unit: unit}
}

// TestGuardReadsOnlyActiveGroups proves, over a real store, that the gates
// read the active groups of a principal and not its suspended groups.
func TestGuardReadsOnlyActiveGroups(t *testing.T) {
	t.Parallel()

	opsOnly := gate.AccessLimit{
		Scope: gate.NamespaceScope("acme"), ID: "ops-only", Version: 0,
		Policy: `forbid (principal, action, resource) when { principal in OpenDustpan::Group::"ops" };`,
	}
	sreUnless := gate.AccessLimit{
		Scope: gate.NamespaceScope("acme"), ID: "sre-exempt", Version: 0,
		Policy: `forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" };`,
	}

	cases := []struct {
		name      string
		subject   string
		limit     gate.AccessLimit
		wantLimit string
	}{
		{"a member of ops is refused", "ops-member", opsOnly, "ops-only"},
		{"a principal outside ops passes", "non-member", opsOnly, ""},
		{"an active member of sre is exempt", "sre-active", sreUnless, ""},
		{"a suspended member of sre is not exempt", "sre-suspended", sreUnless, "sre-exempt"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := newGroupsGuard(t, tc.limit)

			_, err := g.svc.GuardStateWrite(t.Context(), gate.StateWrite{
				Principal: gatePrincipal(tc.subject, identity.KindPerson, identity.CredentialAccessToken),
				Unit:      g.unit,
				Operation: gate.OperationLock,
				Action:    identity.ActionUnitLock,
				Source:    gate.SourceAPI,
				PlanLock:  false,
				State:     nil,
			})

			if tc.wantLimit == "" {
				if err != nil {
					t.Fatalf("GuardStateWrite() = %v, want nil", err)
				}

				return
			}

			refusal, ok := errors.AsType[*gate.RefusalError](err)
			if !ok || !errors.Is(err, gate.ErrLimited) {
				t.Fatalf("GuardStateWrite() = %v, want a refusal that wraps gate.ErrLimited", err)
			}

			if !slices.ContainsFunc(refusal.Messages, func(m string) bool {
				return strings.Contains(m, tc.wantLimit)
			}) {
				t.Fatalf("messages = %q, want the limit %q", refusal.Messages, tc.wantLimit)
			}
		})
	}
}
