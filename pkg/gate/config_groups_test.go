package gate_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
)

// idGroups is a group directory whose groups can be deleted, made again and
// renamed. Each group made gets a new ID, as the principal store gives.
type idGroups struct {
	mu     sync.Mutex
	next   int
	groups map[string]idGroup
}

type idGroup struct {
	name   string
	counts gate.MemberCounts
}

func newIDGroups() *idGroups {
	return &idGroups{mu: sync.Mutex{}, next: 0, groups: map[string]idGroup{}}
}

func (d *idGroups) ResolveGroup(_ context.Context, name string) (gate.GroupRef, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for id, g := range d.groups {
		if strings.EqualFold(g.name, name) {
			return gate.GroupRef{ID: id, Name: g.name}, nil
		}
	}

	return gate.GroupRef{ID: "", Name: ""}, gate.ErrNotFound
}

func (d *idGroups) GroupByID(_ context.Context, id string) (gate.GroupRef, gate.MemberCounts, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	g, ok := d.groups[id]
	if !ok {
		return gate.GroupRef{ID: "", Name: ""}, gate.MemberCounts{Enabled: 0, Persons: 0}, gate.ErrNotFound
	}

	return gate.GroupRef{ID: id, Name: g.name}, g.counts, nil
}

// make adds a group with count enabled members, all persons, and returns its ID.
func (d *idGroups) make(name string, count int64) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.next++
	id := fmt.Sprintf("grp-%d", d.next)
	d.groups[id] = idGroup{name: name, counts: gate.MemberCounts{Enabled: count, Persons: count}}

	return id
}

func (d *idGroups) delete(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.groups, id)
}

func (d *idGroups) rename(id, name string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	g := d.groups[id]
	g.name = name
	d.groups[id] = g
}

func newGroupService(t *testing.T, dir gate.ApproverDirectory) (*gate.ConfigService, *recordingStore) {
	t.Helper()

	st := &recordingStore{Store: nil, writes: nil, rule: nil, def: nil}

	svc, err := gate.NewConfigService(gate.ConfigServiceConfig{
		Store: st, Namespaces: fakeNamespaces{}, Approvers: dir, Engine: nil, Now: configNow,
	})
	if err != nil {
		t.Fatalf("NewConfigService() = %v", err)
	}

	return svc, st
}

// approversOf returns approvers that name groups and hold ids.
func approversOf(groups, ids []string) gate.Approvers {
	return gate.Approvers{Groups: groups, GroupIDs: ids, Users: nil, UnitOwners: false}
}

// ruleOf returns a rule that names groups.
func ruleOf(groups ...string) gate.ApprovalRule {
	r := goodRule(gate.NamespaceScope("acme"))
	r.Approvers = approversOf(groups, nil)

	return r
}

// TestConfigServiceStoresGroupIDs proves that a save resolves each group name
// to the ID of the group, stores the ID with the current name, keeps each
// group once, and ignores a group ID that the caller gives.
func TestConfigServiceStoresGroupIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		in        gate.Approvers
		wantNames []string
		wantIDs   []string
	}{
		{"a name in another case", approversOf([]string{"NetOps"}, nil), []string{"netops"}, []string{"grp-1"}},
		{
			"the same group twice in two cases", approversOf([]string{"netops", "NETOPS", "dba"}, nil),
			[]string{"netops", "dba"},
			[]string{"grp-1", "grp-2"},
		},
		{
			"an ID from the caller", approversOf([]string{"dba"}, []string{"grp-1"}),
			[]string{"dba"},
			[]string{"grp-2"},
		},
		{
			"no group",
			gate.Approvers{Groups: nil, GroupIDs: nil, Users: []string{"alice"}, UnitOwners: false},
			[]string{},
			[]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := newIDGroups()
			dir.make("netops", 3)
			dir.make("dba", 3)

			svc, st := newGroupService(t, dir)
			r := goodRule(gate.NamespaceScope("acme"))
			r.Approvers = tt.in

			got, err := svc.PutApprovalRule(t.Context(), r, 0, callerEntry())
			if err != nil {
				t.Fatalf("PutApprovalRule() = %v", err)
			}

			for _, a := range []gate.Approvers{got.Approvers, st.rule.Approvers} {
				if !slices.Equal(a.Groups, tt.wantNames) || !slices.Equal(a.GroupIDs, tt.wantIDs) {
					t.Fatalf("approvers = %+v, want names %v and IDs %v", a, tt.wantNames, tt.wantIDs)
				}
			}
		})
	}
}

// TestConfigServiceGroupLifecycle proves the read of a rule after its group
// changes: a rename shows the new name, a group that was deleted keeps its
// stored name, and a group that was made again with the same name does not
// take the place of the group that was deleted. A rule saved with a group
// that does not exist is refused with the coded error.
func TestConfigServiceGroupLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		change    func(d *idGroups, id string)
		wantNames []string
		wantIDs   []string
	}{
		{"nothing changed", func(*idGroups, string) {}, []string{"netops"}, []string{"grp-1"}},
		{
			"the group was renamed", func(d *idGroups, id string) { d.rename(id, "network-ops") },
			[]string{"network-ops"},
			[]string{"grp-1"},
		},
		{
			"the group was deleted", func(d *idGroups, id string) { d.delete(id) },
			[]string{"netops"},
			[]string{"grp-1"},
		},
		{
			"the group was deleted and made again", func(d *idGroups, id string) { d.delete(id); d.make("netops", 9) },
			[]string{"netops"},
			[]string{"grp-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := newIDGroups()
			id := dir.make("netops", 3)
			svc, _ := newGroupService(t, dir)

			if _, err := svc.PutApprovalRule(t.Context(), ruleOf("netops"), 0, callerEntry()); err != nil {
				t.Fatalf("PutApprovalRule() = %v", err)
			}

			tt.change(dir, id)

			got, err := svc.ApprovalRule(t.Context(), gate.NamespaceScope("acme"), "platform")
			if err != nil {
				t.Fatalf("ApprovalRule() = %v", err)
			}

			if !slices.Equal(got.Approvers.Groups, tt.wantNames) || !slices.Equal(got.Approvers.GroupIDs, tt.wantIDs) {
				t.Fatalf("approvers = %+v, want names %v and IDs %v", got.Approvers, tt.wantNames, tt.wantIDs)
			}
		})
	}
}

// TestConfigServiceRefusesAnUnknownGroup proves that a save that names a group
// that does not exist, also one that was deleted, is refused with
// ApproversGroupMissing and stores nothing.
func TestConfigServiceRefusesAnUnknownGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(d *idGroups)
		group string
	}{
		{"a group that never existed", func(*idGroups) {}, "ghost"},
		{"a group that was deleted", func(d *idGroups) { d.delete(d.make("old", 3)) }, "old"},
		{"the old name of a renamed group", func(d *idGroups) { d.rename(d.make("old", 3), "new") }, "old"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := newIDGroups()
			tt.setup(dir)

			svc, st := newGroupService(t, dir)
			_, err := svc.PutApprovalRule(t.Context(), ruleOf(tt.group), 0, callerEntry())

			var ae *gate.ApproversError
			if !errors.As(err, &ae) || ae.Reason != gate.ApproversGroupMissing {
				t.Fatalf("PutApprovalRule() = %v, want reason %v", err, gate.ApproversGroupMissing)
			}

			if len(st.writes) != 0 {
				t.Fatalf("%d writes reached the store, want none", len(st.writes))
			}
		})
	}
}

// TestConfigServiceKeepsARuleWithNoGroupIDs proves that a read leaves a rule
// that holds names and no IDs as it is: the names are display only, and no
// name is looked up.
func TestConfigServiceKeepsARuleWithNoGroupIDs(t *testing.T) {
	t.Parallel()

	dir := newIDGroups()
	dir.make("netops", 3)

	svc, st := newGroupService(t, dir)
	rule := ruleOf("netops")
	st.rule = &rule

	got, err := svc.ApprovalRule(t.Context(), gate.NamespaceScope("acme"), "platform")
	if err != nil {
		t.Fatalf("ApprovalRule() = %v", err)
	}

	if !slices.Equal(got.Approvers.Groups, []string{"netops"}) || len(got.Approvers.GroupIDs) != 0 {
		t.Fatalf("approvers = %+v, want the stored names and no IDs", got.Approvers)
	}
}
