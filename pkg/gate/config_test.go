package gate_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func configNow() time.Time { return time.Date(2026, time.October, 20, 9, 0, 0, 0, time.UTC) }

// fakeNamespaces holds one namespace with failover version 4.
type fakeNamespaces struct{}

func (fakeNamespaces) Get(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name != "acme" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		CreatedAt: configNow(), ID: "", Name: "acme", HomeRegion: "test",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 4,
	}, nil
}

// fakeGroups holds the groups platform (5 enabled members, 3 of them persons),
// ops (1 member, a person) and empty (no member). A name with a character that
// a group name cannot hold is not valid, and any other name names no group.
type fakeGroups struct{}

func (fakeGroups) GroupMembers(_ context.Context, name string) (gate.MemberCounts, error) {
	members := map[string]gate.MemberCounts{
		"platform": {Enabled: 5, Persons: 3},
		"ops":      {Enabled: 1, Persons: 1},
		"empty":    {Enabled: 0, Persons: 0},
	}

	if strings.ContainsAny(name, "<>/ ") {
		return gate.MemberCounts{}, gate.ErrInvalid
	}

	if n, ok := members[strings.ToLower(name)]; ok {
		return n, nil
	}

	return gate.MemberCounts{}, gate.ErrNotFound
}

// write records the last write that reached a store.
type write struct {
	fence namespace.Fence
	entry audit.Entry
	prev  int64
}

// recordingStore implements the settings and the approval rule ports of a
// gate.Store and records each write. Any other call panics.
type recordingStore struct {
	gate.Store

	writes []write
}

type recordingSettings struct {
	gate.Settings

	store *recordingStore
}

type recordingRules struct {
	gate.Objects[gate.ApprovalRule]

	store *recordingStore
}

//nolint:ireturn // The method implements gate.Store, which returns the port.
func (s *recordingStore) Settings() gate.Settings { return recordingSettings{Settings: nil, store: s} }

func (s *recordingStore) ApprovalRules() gate.Objects[gate.ApprovalRule] {
	return recordingRules{Objects: nil, store: s}
}

func (s recordingSettings) PutApprovalDefault(
	_ context.Context, _ gate.ApprovalDefault, prev int64, f namespace.Fence, e audit.Entry,
) (int64, error) {
	s.store.writes = append(s.store.writes, write{fence: f, entry: e, prev: prev})

	return prev + 1, nil
}

func (s recordingSettings) DeleteApprovalDefault(
	_ context.Context, _ gate.Scope, prev int64, f namespace.Fence, e audit.Entry,
) error {
	s.store.writes = append(s.store.writes, write{fence: f, entry: e, prev: prev})

	return nil
}

func (r recordingRules) Put(
	_ context.Context, _ gate.ApprovalRule, prev int64, f namespace.Fence, e audit.Entry,
) (int64, error) {
	r.store.writes = append(r.store.writes, write{fence: f, entry: e, prev: prev})

	return prev + 1, nil
}

func (r recordingRules) Delete(_ context.Context, ref gate.ObjectRef, f namespace.Fence, e audit.Entry) error {
	r.store.writes = append(r.store.writes, write{fence: f, entry: e, prev: ref.Version})

	return nil
}

func newConfigService(t *testing.T) (*gate.ConfigService, *recordingStore) {
	t.Helper()

	st := &recordingStore{Store: nil, writes: nil}

	svc, err := gate.NewConfigService(gate.ConfigServiceConfig{
		Store: st, Namespaces: fakeNamespaces{}, Approvers: fakeGroups{}, Now: configNow,
	})
	if err != nil {
		t.Fatalf("NewConfigService() = %v", err)
	}

	return svc, st
}

// callerEntry is the entry that an adapter hands over. It holds values that
// the service must replace.
func callerEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "evil", Subject: "bo", IDPSubject: "", TokenID: "", Action: "caller-action",
		Object: "caller-object", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindReadAccess, Outcome: audit.OutcomeDenied,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "",
		JobID: "", Reason: "",
	}
}

func goodDefault(scope gate.Scope) gate.ApprovalDefault {
	return gate.ApprovalDefault{
		Scope: scope, Version: 0, Count: 2, FreshAuth: gate.NoFreshAuth(),
		Approvers: gate.Approvers{Groups: []string{"platform"}, Users: nil, UnitOwners: false}, Unless: "", Reads: nil,
	}
}

func goodRule(scope gate.Scope) gate.ApprovalRule {
	return gate.ApprovalRule{
		Scope: scope, ID: "platform", Version: 0,
		Approvers: gate.Approvers{Groups: []string{"platform"}, Users: nil, UnitOwners: false}, Count: 1,
		AuthorMayApprove: false, FreshAuth: gate.NoFreshAuth(), Timeout: 0,
	}
}

// TestConfigServiceStampsTheEntry proves that every write carries the fence
// of the namespace and an entry that the service completes: the namespace,
// the time, the kind, the outcome, the object and the action come from the
// service, not from the caller.
func TestConfigServiceStampsTheEntry(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")
	wantFence := namespace.Fence{Namespace: "acme", FailoverVersion: 4}

	tests := []struct {
		name       string
		run        func(*gate.ConfigService) error
		wantAction string
		wantObject string
		wantPrev   int64
		wantChange string
	}{
		{
			"put default", func(s *gate.ConfigService) error {
				_, err := s.PutApprovalDefault(t.Context(), goodDefault(scope), 3, callerEntry())

				return err
			}, gate.AuditSetApprovalDefault, "namespace/acme/approval-default", 3, "3->4",
		},
		{
			"delete default", func(s *gate.ConfigService) error {
				return s.DeleteApprovalDefault(t.Context(), scope, 3, callerEntry())
			}, gate.AuditDeleteApprovalDefault, "namespace/acme/approval-default", 3, "3->deleted",
		},
		{
			"put rule", func(s *gate.ConfigService) error {
				_, err := s.PutApprovalRule(t.Context(), goodRule(scope), 0, callerEntry())

				return err
			}, gate.AuditSetApprovalRule, "namespace/acme/approval-rules/platform", 0, "new->1",
		},
		{
			"delete rule", func(s *gate.ConfigService) error {
				return s.DeleteApprovalRule(
					t.Context(),
					gate.ObjectRef{Scope: scope, ID: "platform", Version: 2},
					callerEntry(),
				)
			}, gate.AuditDeleteApprovalRule, "namespace/acme/approval-rules/platform", 2, "2->deleted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, st := newConfigService(t)
			if err := tt.run(svc); err != nil {
				t.Fatalf("write = %v", err)
			}

			if len(st.writes) != 1 {
				t.Fatalf("writes = %d, want 1", len(st.writes))
			}

			checkStamped(t, st.writes[0], stamped{
				fence:  wantFence,
				prev:   tt.wantPrev,
				action: tt.wantAction,
				object: tt.wantObject,
				change: tt.wantChange,
			})
		})
	}
}

// stamped is what a test expects of one write.
type stamped struct {
	fence  namespace.Fence
	prev   int64
	action string
	object string
	change string
}

// checkStamped checks the fence, the version and the entry of one write.
func checkStamped(t *testing.T, w write, want stamped) {
	t.Helper()

	if w.fence != want.fence || w.prev != want.prev {
		t.Fatalf("fence, prev = %+v, %d; want %+v, %d", w.fence, w.prev, want.fence, want.prev)
	}

	e := w.entry

	if e.Namespace != "acme" || !e.At.Equal(configNow()) || e.Kind != audit.KindChange ||
		e.Outcome != audit.OutcomeSuccess || e.Action != want.action || e.Object != want.object {
		t.Fatalf("entry = %+v, want namespace acme, the clock, change, success, %s on %s", e, want.action, want.object)
	}

	if e.Subject != "bo" || e.SourceIP != "192.0.2.1" {
		t.Fatalf("entry = %+v, want the caller fields kept", e)
	}

	checkVersionChange(t, e.Changes, want.change)
}

func checkVersionChange(t *testing.T, changes []audit.Change, want string) {
	t.Helper()

	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want one version change", changes)
	}

	old := "new"
	if changes[0].Old != nil {
		old = *changes[0].Old
	}

	if got := old + "->" + changes[0].New; got != want {
		t.Fatalf("version change = %s, want %s", got, want)
	}
}

// TestConfigServiceRefusesBeforeTheStore proves that an invalid object, a
// scope that holds no namespace configuration and an unknown namespace never
// reach the store.
func TestConfigServiceRefusesBeforeTheStore(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")

	badDefault := goodDefault(scope)
	badDefault.Count = 0

	badRule := goodRule(scope)
	badRule.Approvers = gate.Approvers{Groups: nil, Users: nil, UnitOwners: false}

	tests := []struct {
		name string
		run  func(*gate.ConfigService) error
		want error
	}{
		{"default count zero", func(s *gate.ConfigService) error {
			_, err := s.PutApprovalDefault(t.Context(), badDefault, 0, callerEntry())

			return err
		}, gate.ErrInvalid},
		{"rule without approver", func(s *gate.ConfigService) error {
			_, err := s.PutApprovalRule(t.Context(), badRule, 0, callerEntry())

			return err
		}, gate.ErrInvalid},
		{"default at the installation", func(s *gate.ConfigService) error {
			_, err := s.PutApprovalDefault(t.Context(), goodDefault(gate.InstallationScope()), 0, callerEntry())

			return err
		}, gate.ErrInvalid},
		{"rule at the installation", func(s *gate.ConfigService) error {
			_, err := s.PutApprovalRule(t.Context(), goodRule(gate.InstallationScope()), 0, callerEntry())

			return err
		}, gate.ErrInvalid},
		{"delete at the zero scope", func(s *gate.ConfigService) error {
			return s.DeleteApprovalDefault(t.Context(), gate.Scope{}, 1, callerEntry())
		}, gate.ErrInvalid},
		{"unknown namespace", func(s *gate.ConfigService) error {
			_, err := s.PutApprovalDefault(t.Context(), goodDefault(gate.NamespaceScope("zed")), 0, callerEntry())

			return err
		}, namespace.ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, st := newConfigService(t)

			if err := tt.run(svc); !errors.Is(err, tt.want) {
				t.Fatalf("write = %v, want %v", err, tt.want)
			}

			if len(st.writes) != 0 {
				t.Fatalf("a refused write reached the store: %d writes", len(st.writes))
			}
		})
	}
}

// TestNewConfigServiceNeedsPorts proves that a service without a port is
// refused.
func TestNewConfigServiceNeedsPorts(t *testing.T) {
	t.Parallel()

	_, err := gate.NewConfigService(
		gate.ConfigServiceConfig{Store: nil, Namespaces: fakeNamespaces{}, Approvers: fakeGroups{}, Now: nil},
	)
	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("no store = %v, want ErrInvalid", err)
	}

	st := &recordingStore{Store: nil, writes: nil}

	_, err = gate.NewConfigService(
		gate.ConfigServiceConfig{Store: st, Namespaces: nil, Approvers: fakeGroups{}, Now: nil},
	)
	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("no namespaces = %v, want ErrInvalid", err)
	}

	_, err = gate.NewConfigService(
		gate.ConfigServiceConfig{Store: st, Namespaces: fakeNamespaces{}, Approvers: nil, Now: nil},
	)
	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("no approver directory = %v, want ErrInvalid", err)
	}
}

// TestValidateApprovalFields proves the bounds of a rule and of a default.
func TestValidateApprovalFields(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")
	long := strings.Repeat("g", gate.MaxApproverBytes+1)
	many := make([]string, gate.MaxApproverEntries+1)

	for i := range many {
		many[i] = "g"
	}

	rule := func(mutate func(*gate.ApprovalRule)) gate.ApprovalRule {
		r := goodRule(scope)
		mutate(&r)

		return r
	}

	rules := map[string]struct {
		rule gate.ApprovalRule
		ok   bool
	}{
		"good":           {goodRule(scope), true},
		"count one":      {rule(func(r *gate.ApprovalRule) { r.Count = 1 }), true},
		"count ten":      {rule(func(r *gate.ApprovalRule) { r.Count = 10 }), true},
		"count zero":     {rule(func(r *gate.ApprovalRule) { r.Count = 0 }), false},
		"count eleven":   {rule(func(r *gate.ApprovalRule) { r.Count = 11 }), false},
		"count negative": {rule(func(r *gate.ApprovalRule) { r.Count = -1 }), false},
		"reserved ID":    {rule(func(r *gate.ApprovalRule) { r.ID = "default" }), false},
		"unit owners only": {
			rule(
				func(r *gate.ApprovalRule) { r.Approvers = gate.Approvers{Groups: nil, Users: nil, UnitOwners: true} },
			),
			true,
		},
		"user only": {
			rule(func(r *gate.ApprovalRule) {
				r.Approvers = gate.Approvers{Groups: nil, Users: []string{"bo"}, UnitOwners: false}
			}),
			true,
		},
		"empty group name": {rule(func(r *gate.ApprovalRule) { r.Approvers.Groups = []string{""} }), false},
		"long group name":  {rule(func(r *gate.ApprovalRule) { r.Approvers.Groups = []string{long} }), false},
		"too many groups":  {rule(func(r *gate.ApprovalRule) { r.Approvers.Groups = many }), false},
		"window every approval": {
			rule(func(r *gate.ApprovalRule) { r.FreshAuth = gate.FreshAuth{Set: true, Seconds: 0} }),
			true,
		},
		"window 60": {
			rule(func(r *gate.ApprovalRule) { r.FreshAuth = gate.FreshAuth{Set: true, Seconds: 60} }),
			true,
		},
		"window 59": {
			rule(func(r *gate.ApprovalRule) { r.FreshAuth = gate.FreshAuth{Set: true, Seconds: 59} }),
			false,
		},
		"window 901": {
			rule(func(r *gate.ApprovalRule) { r.FreshAuth = gate.FreshAuth{Set: true, Seconds: 901} }),
			false,
		},
		"timeout one hour":      {rule(func(r *gate.ApprovalRule) { r.Timeout = gate.MinApprovalTimeout }), true},
		"timeout under an hour": {rule(func(r *gate.ApprovalRule) { r.Timeout = gate.MinApprovalTimeout - 1 }), false},
		"timeout 30 days":       {rule(func(r *gate.ApprovalRule) { r.Timeout = gate.MaxApprovalTimeout }), true},
		"timeout over 30 days":  {rule(func(r *gate.ApprovalRule) { r.Timeout = gate.MaxApprovalTimeout + 1 }), false},
		"timeout negative":      {rule(func(r *gate.ApprovalRule) { r.Timeout = -time.Hour }), false},
	}

	for name, tt := range rules {
		err := tt.rule.Validate()
		if tt.ok != (err == nil) || (err != nil && !errors.Is(err, gate.ErrInvalid)) {
			t.Fatalf("rule %q: Validate() = %v, want ok %t and ErrInvalid otherwise", name, err, tt.ok)
		}
	}

	def := goodDefault(scope)
	if err := def.Validate(); err != nil {
		t.Fatalf("good default: Validate() = %v", err)
	}

	def.Approvers = gate.Approvers{Groups: nil, Users: nil, UnitOwners: false}
	if err := def.Validate(); err != nil {
		t.Fatalf("default with no approver: Validate() = %v, want it valid", err)
	}

	def.Count = 0
	if err := def.Validate(); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("default count 0: Validate() = %v, want ErrInvalid", err)
	}
}

// TestConfigServiceRefusesApproversThatCannotBeMet proves that a default and a
// rule whose approvers cannot give the count are refused before the store, and
// that the cases next to them are stored.
func TestConfigServiceRefusesApproversThatCannotBeMet(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")

	tests := []struct {
		name      string
		approvers gate.Approvers
		count     int
		refused   bool
	}{
		{
			"group name that is not valid",
			gate.Approvers{Groups: []string{"<b>x</b>"}, Users: nil, UnitOwners: false},
			1,
			true,
		},
		{
			"group that does not exist",
			gate.Approvers{Groups: []string{"nogroup"}, Users: nil, UnitOwners: false},
			1,
			true,
		},
		{
			"user with a control character",
			gate.Approvers{Groups: nil, Users: []string{"a\x07b"}, UnitOwners: false},
			1,
			true,
		},
		{
			"user with white space around it",
			gate.Approvers{Groups: nil, Users: []string{" alice"}, UnitOwners: false},
			1,
			true,
		},
		{"count above one user", gate.Approvers{Groups: nil, Users: []string{"alice"}, UnitOwners: false}, 2, true},
		{
			"a user named twice counts once",
			gate.Approvers{Groups: nil, Users: []string{"alice", "alice"}, UnitOwners: false},
			2,
			true,
		},
		{
			"count above the members of a group",
			gate.Approvers{Groups: []string{"ops"}, Users: nil, UnitOwners: false},
			2,
			true,
		},
		{"a group with no member", gate.Approvers{Groups: []string{"empty"}, Users: nil, UnitOwners: false}, 1, true},
		{"count within a group", gate.Approvers{Groups: []string{"platform"}, Users: nil, UnitOwners: false}, 5, false},
		{
			"a group named in another case counts once",
			gate.Approvers{Groups: []string{"ops", "OPS"}, Users: nil, UnitOwners: false},
			2,
			true,
		},
		{
			"count within users and groups",
			gate.Approvers{Groups: []string{"ops"}, Users: []string{"a", "b"}, UnitOwners: false},
			3,
			false,
		},
		{
			"count above the list with the unit owners",
			gate.Approvers{Groups: nil, Users: []string{"alice"}, UnitOwners: true},
			9,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, st := newConfigService(t)

			def := goodDefault(scope)
			def.Approvers, def.Count = tt.approvers, tt.count

			rule := goodRule(scope)
			rule.Approvers, rule.Count = tt.approvers, tt.count

			_, defErr := svc.PutApprovalDefault(t.Context(), def, 0, callerEntry())
			_, ruleErr := svc.PutApprovalRule(t.Context(), rule, 0, callerEntry())

			if tt.refused {
				wantRefused(t, defErr, ruleErr, len(st.writes))

				return
			}

			wantStored(t, defErr, ruleErr)
		})
	}
}

// wantStored fails the test unless the default and the rule were both stored.
func wantStored(t *testing.T, defErr, ruleErr error) {
	t.Helper()

	if defErr != nil || ruleErr != nil {
		t.Fatalf("default = %v, rule = %v, want both stored", defErr, ruleErr)
	}
}

// wantRefused fails the test unless the default and the rule were both refused
// with gate.ErrApprovers and reached no store.
func wantRefused(t *testing.T, defErr, ruleErr error, writes int) {
	t.Helper()

	if !errors.Is(defErr, gate.ErrApprovers) || !errors.Is(ruleErr, gate.ErrApprovers) {
		t.Fatalf("default = %v, rule = %v, want gate.ErrApprovers", defErr, ruleErr)
	}

	if writes != 0 {
		t.Fatalf("a refused write reached the store: %d writes", writes)
	}
}

// TestConfigServiceAnyApproveHolderHasNoBound proves that a default with no
// approver stays valid at every count: it means any holder of the approve
// action.
func TestConfigServiceAnyApproveHolderHasNoBound(t *testing.T) {
	t.Parallel()

	svc, _ := newConfigService(t)

	def := goodDefault(gate.NamespaceScope("acme"))
	def.Approvers, def.Count = gate.Approvers{Groups: nil, Users: nil, UnitOwners: false}, 10

	if _, err := svc.PutApprovalDefault(t.Context(), def, 0, callerEntry()); err != nil {
		t.Fatalf("default with no approver and count 10 = %v, want stored", err)
	}
}

// TestConfigServiceCountsOnlyPersonsForAFreshWindow proves which members count.
// A group of 5 enabled members, 3 of them persons, can give 5 approvals when the
// approval needs no fresh authentication, because a service principal with the
// approve action can sign. A window of any length, also 0, needs a fresh
// authentication, which only a person gives, so the group gives 3.
func TestConfigServiceCountsOnlyPersonsForAFreshWindow(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")
	windows := []struct {
		name  string
		fresh gate.FreshAuth
	}{
		{"none", gate.NoFreshAuth()},
		{"new sign-in at each approval", gate.FreshAuth{Set: true, Seconds: 0}},
		{"a window of 5 minutes", gate.FreshAuth{Set: true, Seconds: 300}},
	}

	for _, w := range windows {
		for _, count := range []int{3, 4, 5, 6} {
			t.Run(fmt.Sprintf("%s, count %d", w.name, count), func(t *testing.T) {
				t.Parallel()

				svc, _ := newConfigService(t)

				def := goodDefault(scope)
				def.Count, def.FreshAuth = count, w.fresh

				_, err := svc.PutApprovalDefault(t.Context(), def, 0, callerEntry())

				limit := 5
				if w.fresh.Set {
					limit = 3
				}

				if count <= limit && err != nil || count > limit && !errors.Is(err, gate.ErrApprovers) {
					t.Fatalf("count %d with %s = %v; the group gives %d", count, w.name, err, limit)
				}
			})
		}
	}
}

// TestConfigServiceApproversReasons proves that each refusal names its reason,
// and that a count refusal names the number of people who can approve.
func TestConfigServiceApproversReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		approvers gate.Approvers
		count     int
		reason    gate.ApproversReason
		people    int64
	}{
		{
			"group name not valid",
			gate.Approvers{Groups: []string{"<b>x</b>"}, Users: nil, UnitOwners: false},
			1,
			gate.ApproversGroupInvalid, 0,
		},
		{
			"group missing",
			gate.Approvers{Groups: []string{"nogroup"}, Users: nil, UnitOwners: false},
			1,
			gate.ApproversGroupMissing, 0,
		},
		{
			"user not valid",
			gate.Approvers{Groups: nil, Users: []string{"a\x07b"}, UnitOwners: false},
			1,
			gate.ApproversUserInvalid, 0,
		},
		{
			"count above",
			gate.Approvers{Groups: nil, Users: []string{"alice", "bob"}, UnitOwners: false},
			3,
			gate.ApproversCountAbove, 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, _ := newConfigService(t)

			def := goodDefault(gate.NamespaceScope("acme"))
			def.Approvers, def.Count = tt.approvers, tt.count

			_, err := svc.PutApprovalDefault(t.Context(), def, 0, callerEntry())

			got, ok := errors.AsType[*gate.ApproversError](err)
			if !ok || !errors.Is(err, gate.ErrApprovers) || got.Reason != tt.reason || got.People != tt.people {
				t.Fatalf("error = %v, want reason %d with %d people", err, tt.reason, tt.people)
			}
		})
	}
}
