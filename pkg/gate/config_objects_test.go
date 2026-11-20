package gate_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// objectStore implements the gate and the access limit ports of a gate.Store
// and records each write. Any other call panics.
type objectStore struct {
	gate.Store

	writes []write
	stored *gate.Gate
	gates  recordedGates
}

type recordedGates struct {
	gate.Objects[gate.Gate]

	store *objectStore
}

type recordedLimits struct {
	gate.Objects[gate.AccessLimit]

	store *objectStore
}

func (s *objectStore) Gates() gate.Objects[gate.Gate] { return recordedGates{Objects: nil, store: s} }

func (s *objectStore) AccessLimits() gate.Objects[gate.AccessLimit] {
	return recordedLimits{Objects: nil, store: s}
}

func (r recordedGates) Put(
	_ context.Context,
	g gate.Gate,
	prev int64,
	f namespace.Fence,
	e audit.Entry,
) (int64, error) {
	r.store.stored = &g
	r.store.writes = append(r.store.writes, write{fence: f, entry: e, prev: prev})

	return prev + 1, nil
}

func (r recordedGates) Get(context.Context, gate.Scope, gate.ID) (gate.Gate, error) {
	if r.store.stored == nil {
		return gate.Gate{}, gate.ErrNotFound
	}

	return *r.store.stored, nil
}

func (r recordedGates) List(context.Context, gate.Scope) ([]gate.Gate, error) {
	if r.store.stored == nil {
		return nil, nil
	}

	return []gate.Gate{*r.store.stored}, nil
}

func (r recordedGates) Delete(_ context.Context, ref gate.ObjectRef, f namespace.Fence, e audit.Entry) error {
	r.store.writes = append(r.store.writes, write{fence: f, entry: e, prev: ref.Version})

	return nil
}

func (r recordedLimits) Put(
	_ context.Context, _ gate.AccessLimit, prev int64, f namespace.Fence, e audit.Entry,
) (int64, error) {
	r.store.writes = append(r.store.writes, write{fence: f, entry: e, prev: prev})

	return prev + 1, nil
}

func (r recordedLimits) Get(context.Context, gate.Scope, gate.ID) (gate.AccessLimit, error) {
	return gate.AccessLimit{}, gate.ErrNotFound
}

func (r recordedLimits) List(context.Context, gate.Scope) ([]gate.AccessLimit, error) {
	return nil, nil
}

func (r recordedLimits) Delete(_ context.Context, ref gate.ObjectRef, f namespace.Fence, e audit.Entry) error {
	r.store.writes = append(r.store.writes, write{fence: f, entry: e, prev: ref.Version})

	return nil
}

// fakeEngine answers each check with its diagnostics, or its error.
type fakeEngine struct {
	gate.Engine

	diags         []gate.Diagnostic
	err           error
	membershipErr error
	checks        []gate.Policy
	schema        []gate.Schema
}

func (*fakeEngine) Version() string { return "fake/1" }

func (f *fakeEngine) CheckLimitMembership(context.Context, string) error {
	return f.membershipErr
}

func (f *fakeEngine) Check(_ context.Context, schema gate.Schema, ps []gate.Policy) ([]gate.Diagnostic, error) {
	f.checks = append(f.checks, ps...)
	f.schema = append(f.schema, schema)

	return f.diags, f.err
}

func newObjectService(t *testing.T, engine gate.Engine) (*gate.ConfigService, *objectStore) {
	t.Helper()

	st := &objectStore{Store: nil, writes: nil, stored: nil, gates: recordedGates{Objects: nil, store: nil}}

	svc, err := gate.NewConfigService(gate.ConfigServiceConfig{
		Store: st, Namespaces: fakeNamespaces{}, Approvers: fakeGroups{}, Engine: engine, Now: configNow,
	})
	if err != nil {
		t.Fatalf("NewConfigService() = %v", err)
	}

	return svc, st
}

func goodGate(scope gate.Scope) gate.Gate {
	return gate.Gate{
		Scope: scope, ID: "freeze", Version: 0, DecisionPoint: gate.PointStateWrite,
		Condition: `context.operation.kind == "write"`, Unless: "", Outcome: gate.OutcomeDeny,
		Message: "Writes are frozen", Mode: gate.ModeEnforce, OnError: gate.OnErrorFail, Final: false,
		CheckedWith: gate.CheckedWith{Engine: "caller", Schema: gate.SchemaVersion{Subject: "x", Major: 9, Minor: 9}},
		State:       gate.StateInvalid, Reads: []gate.ID{"caller"},
	}
}

func goodLimit(scope gate.Scope) gate.AccessLimit {
	return gate.AccessLimit{
		Scope: scope, ID: "no-writes", Version: 0,
		Policy: `forbid (principal, action == OpenDustpan::Action::"unit:write", resource);`,
	}
}

func TestPutGateStoresWhatTheEngineChecked(t *testing.T) {
	t.Parallel()

	engine := &fakeEngine{Engine: nil, diags: nil, err: nil, membershipErr: nil, checks: nil, schema: nil}
	svc, st := newObjectService(t, engine)
	scope := gate.NamespaceScope("acme")

	stored, err := svc.PutGate(t.Context(), goodGate(scope), 0, "", callerEntry())
	if err != nil {
		t.Fatalf("PutGate() = %v", err)
	}

	if stored.Version != 1 || stored.CheckedWith.Engine != "fake/1" ||
		stored.CheckedWith.Schema != (gate.SchemaVersion{Subject: "state.write", Major: 1, Minor: 0}) {
		t.Errorf("stored gate = %+v, want version 1 checked with fake/1 and the state.write schema", stored)
	}

	// The caller never sets the check state or the path sets that a gate reads.
	if st.stored.State != "" || st.stored.Reads != nil {
		t.Errorf("stored State %q, Reads %v; want both empty", st.stored.State, st.stored.Reads)
	}

	checkOneGateCheck(t, engine)

	checkSetGateEntry(t, st.writes[0].entry, scope)
}

// checkSetGateEntry fails unless e is the entry of a set-gate write in scope.
func checkSetGateEntry(t *testing.T, e audit.Entry, scope gate.Scope) {
	t.Helper()

	if e.Action != gate.AuditSetGate || e.Namespace != "acme" || e.Kind != audit.KindChange ||
		e.Object != scope.Key()+"/gates/freeze" || e.Subject != "bo" || len(e.Changes) != 1 {
		t.Errorf("audit entry = %+v, want the set-gate action, the namespace, the object and the caller", e)
	}
}

// checkOneGateCheck fails unless the engine checked one gate policy against
// the schema of state.write.
func checkOneGateCheck(t *testing.T, engine *fakeEngine) {
	t.Helper()

	if len(engine.checks) != 1 || engine.checks[0].Kind != gate.PolicyGate ||
		engine.schema[0].Version.Subject != "state.write" {
		t.Errorf("engine checks = %+v, %+v; want one gate policy against the state.write schema",
			engine.checks, engine.schema)
	}
}

func TestPutGateRefusals(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")
	bad := func(edit func(*gate.Gate)) gate.Gate {
		g := goodGate(scope)
		edit(&g)

		return g
	}

	tests := []struct {
		name string
		gate gate.Gate
		want error
	}{
		{"builtin id", bad(func(g *gate.Gate) { g.ID = "builtin/freeze" }), gate.ErrInvalid},
		{"upper-case id", bad(func(g *gate.Gate) { g.ID = "Freeze" }), gate.ErrInvalid},
		{"unknown point", bad(func(g *gate.Gate) { g.DecisionPoint = "state.read" }), gate.ErrInvalid},
		{"unknown outcome", bad(func(g *gate.Gate) { g.Outcome = "allow" }), gate.ErrInvalid},
		{
			"approval rule id that is reserved",
			bad(func(g *gate.Gate) { g.Outcome = "approval:default" }),
			gate.ErrInvalid,
		},
		{"unknown mode", bad(func(g *gate.Gate) { g.Mode = "" }), gate.ErrInvalid},
		{"unknown on-error", bad(func(g *gate.Gate) { g.OnError = "" }), gate.ErrInvalid},
		{"no message", bad(func(g *gate.Gate) { g.Message = " " }), gate.ErrInvalid},
		{
			"message too long",
			bad(func(g *gate.Gate) { g.Message = strings.Repeat("m", gate.MaxMessageBytes+1) }),
			gate.ErrInvalid,
		},
		{
			"condition too long",
			bad(func(g *gate.Gate) { g.Condition = strings.Repeat("c", gate.MaxConditionBytes+1) }),
			gate.ErrLimit,
		},
		{
			"unless too long",
			bad(func(g *gate.Gate) { g.Unless = strings.Repeat("c", gate.MaxConditionBytes+1) }),
			gate.ErrLimit,
		},
		{"profile scope", bad(func(g *gate.Gate) { g.Scope = profileScope(t) }), gate.ErrInvalid},
		{"empty scope", bad(func(g *gate.Gate) { g.Scope = gate.Scope{} }), gate.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			engine := &fakeEngine{Engine: nil, diags: nil, err: nil, membershipErr: nil, checks: nil, schema: nil}
			svc, st := newObjectService(t, engine)

			_, err := svc.PutGate(t.Context(), tt.gate, 0, "", callerEntry())
			if !errors.Is(err, tt.want) {
				t.Errorf("PutGate() = %v, want %v", err, tt.want)
			}

			if len(st.writes) != 0 || len(engine.checks) != 0 {
				t.Errorf("a refused gate reached the engine %d times and the store %d times, want 0 and 0",
					len(engine.checks), len(st.writes))
			}
		})
	}
}

func TestPutGateEngineFailsClosed(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")
	diag := gate.Diagnostic{Policy: gate.PolicyID{Scope: scope, ID: "freeze"}, Message: "unknown fact\nfoo"}

	tests := []struct {
		name   string
		engine gate.Engine
		want   error
	}{
		{
			"diagnostic",
			&fakeEngine{
				Engine:        nil,
				diags:         []gate.Diagnostic{diag},
				err:           nil,
				membershipErr: nil,
				checks:        nil,
				schema:        nil,
			},
			gate.ErrInvalid,
		},
		{
			"engine error",
			&fakeEngine{
				Engine:        nil,
				diags:         nil,
				err:           gate.ErrUnavailable,
				membershipErr: nil,
				checks:        nil,
				schema:        nil,
			},
			gate.ErrUnavailable,
		},
		{"no engine", nil, gate.ErrUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, st := newObjectService(t, tt.engine)

			_, err := svc.PutGate(t.Context(), goodGate(scope), 0, "", callerEntry())
			if !errors.Is(err, tt.want) {
				t.Fatalf("PutGate() = %v, want %v", err, tt.want)
			}

			if len(st.writes) != 0 {
				t.Errorf("a gate that no engine passed reached the store %d times, want 0", len(st.writes))
			}

			var checkErr *gate.CheckError
			if tt.name == "diagnostic" &&
				(!errors.As(err, &checkErr) || strings.ContainsAny(checkErr.Diagnostics[0].Message, "\n")) {
				t.Errorf("error = %v, want a CheckError whose diagnostic has no line break", err)
			}
		})
	}
}

func TestPutGateReasonAndModeSwitch(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")
	engine := &fakeEngine{Engine: nil, diags: nil, err: nil, membershipErr: nil, checks: nil, schema: nil}
	svc, st := newObjectService(t, engine)

	old := goodGate(scope)
	old.Mode = gate.ModeEvaluate
	old.Version = 3
	st.stored = &old

	if _, err := svc.PutGate(t.Context(), goodGate(scope), 3, "change freeze ends", callerEntry()); err != nil {
		t.Fatalf("PutGate() = %v", err)
	}

	changes := st.writes[0].entry.Changes
	keys := map[string]audit.Change{}

	for _, c := range changes {
		keys[c.Key] = c
	}

	mode, ok := keys["gate/freeze.mode"]
	if !ok || mode.Old == nil || *mode.Old != "evaluate" || mode.New != "enforce" {
		t.Errorf("changes = %+v, want the mode change from evaluate to enforce", changes)
	}

	if keys[gate.ReasonKey].New != "change freeze ends" || gate.Reason(st.writes[0].entry) != "change freeze ends" {
		t.Errorf("changes = %+v, want the reason under %q", changes, gate.ReasonKey)
	}

	if _, err := svc.PutGate(t.Context(), goodGate(scope), 3, "a\nb", callerEntry()); !errors.Is(err, gate.ErrInvalid) {
		t.Errorf("PutGate() with a line break in the reason = %v, want ErrInvalid", err)
	}
}

func TestDeleteGateWritesReasonAndAuditEntry(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")
	svc, st := newObjectService(t, nil)
	ref := gate.ObjectRef{Scope: scope, ID: "freeze", Version: 2}

	if err := svc.DeleteGate(t.Context(), ref, "no longer needed", callerEntry()); err != nil {
		t.Fatalf("DeleteGate() = %v", err)
	}

	e := st.writes[0].entry
	if e.Action != gate.AuditDeleteGate || e.Object != scope.Key()+"/gates/freeze" ||
		gate.Reason(e) != "no longer needed" ||
		st.writes[0].prev != 2 {
		t.Errorf(
			"entry = %+v, prev %d; want the delete-gate action, the object, the reason and version 2",
			e,
			st.writes[0].prev,
		)
	}

	wide := gate.ObjectRef{Scope: profileScope(t), ID: "x", Version: 1}

	err := svc.DeleteGate(t.Context(), wide, "", callerEntry())
	if !errors.Is(err, gate.ErrInvalid) {
		t.Errorf("DeleteGate() at a profile = %v, want ErrInvalid", err)
	}
}

func TestAccessLimitWrites(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")
	permit := gate.Diagnostic{Policy: gate.PolicyID{Scope: scope, ID: "no-writes"}, Message: "effect permit"}

	tests := []struct {
		name   string
		limit  gate.AccessLimit
		engine gate.Engine
		want   error
	}{
		{
			"good",
			goodLimit(scope),
			&fakeEngine{Engine: nil, diags: nil, err: nil, membershipErr: nil, checks: nil, schema: nil},
			nil,
		},
		{
			"permit refused by the engine",
			goodLimit(scope),
			&fakeEngine{
				Engine:        nil,
				diags:         []gate.Diagnostic{permit},
				err:           nil,
				membershipErr: nil,
				checks:        nil,
				schema:        nil,
			},
			gate.ErrInvalid,
		},
		{
			"membership test of a value that is not a group",
			goodLimit(scope),
			&fakeEngine{
				Engine:        nil,
				diags:         nil,
				err:           nil,
				membershipErr: gate.ErrLimitMembership,
				checks:        nil,
				schema:        nil,
			},
			gate.ErrLimitMembership,
		},
		{"no engine", goodLimit(scope), nil, gate.ErrUnavailable},
		{
			"empty policy",
			gate.AccessLimit{Scope: scope, ID: "x", Version: 0, Policy: " "},
			&fakeEngine{
				Engine:        nil,
				diags:         nil,
				err:           nil,
				membershipErr: nil,
				checks:        nil,
				schema:        nil,
			},
			gate.ErrInvalid,
		},
		{
			"policy too long",
			gate.AccessLimit{Scope: scope, ID: "x", Version: 0, Policy: strings.Repeat("p", gate.MaxConditionBytes+1)},
			&fakeEngine{Engine: nil, diags: nil, err: nil, membershipErr: nil, checks: nil, schema: nil}, gate.ErrLimit,
		},
		{
			"bad id",
			gate.AccessLimit{Scope: scope, ID: "Bad", Version: 0, Policy: goodLimit(scope).Policy},
			&fakeEngine{
				Engine:        nil,
				diags:         nil,
				err:           nil,
				membershipErr: nil,
				checks:        nil,
				schema:        nil,
			},
			gate.ErrInvalid,
		},
		{
			"profile scope",
			goodLimit(profileScope(t)),
			&fakeEngine{
				Engine:        nil,
				diags:         nil,
				err:           nil,
				membershipErr: nil,
				checks:        nil,
				schema:        nil,
			},
			gate.ErrInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, st := newObjectService(t, tt.engine)

			got, err := svc.PutAccessLimit(t.Context(), tt.limit, 0, callerEntry())
			if !errors.Is(err, tt.want) {
				t.Fatalf("PutAccessLimit() = %v, want %v", err, tt.want)
			}

			if tt.want != nil {
				if len(st.writes) != 0 {
					t.Errorf("a refused limit reached the store %d times, want 0", len(st.writes))
				}

				return
			}

			checkLimitRoundTrip(t, svc, st, got)
		})
	}
}

// checkLimitRoundTrip checks the entry of the write of got, then deletes it and
// checks the entry of the delete.
func checkLimitRoundTrip(t *testing.T, svc *gate.ConfigService, st *objectStore, got gate.AccessLimit) {
	t.Helper()

	e := st.writes[0].entry
	if got.Version != 1 || e.Action != gate.AuditSetAccessLimit ||
		e.Object != got.Scope.Key()+"/access-limits/no-writes" {
		t.Errorf("limit %+v, entry %+v; want version 1 and the set-access-limit entry", got, e)
	}

	err := svc.DeleteAccessLimit(t.Context(), got.Ref(), callerEntry())
	if err != nil || st.writes[1].entry.Action != gate.AuditDeleteAccessLimit {
		t.Errorf("DeleteAccessLimit() = %v, entry %+v; want the delete-access-limit entry", err, st.writes[1].entry)
	}
}

// profileScope returns a scope that holds no configuration through the service.
func profileScope(t *testing.T) gate.Scope {
	t.Helper()

	s, err := gate.ProfileScope("acme", "base")
	if err != nil {
		t.Fatalf("ProfileScope() = %v", err)
	}

	return s
}

// countingNamespaces answers like fakeNamespaces and counts each read.
type countingNamespaces struct {
	reads *atomic.Int64
}

func (c countingNamespaces) Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	c.reads.Add(1)

	return fakeNamespaces{}.Get(ctx, name)
}

// installationFixture is a service that reads its namespaces through a
// counter, with a store that records the writes of each kind.
type installationFixture struct {
	svc     *gate.ConfigService
	objects *objectStore
	rules   *recordingStore
	reads   *atomic.Int64
}

// installationStore joins the recorders of the three kinds. A call of any
// other port panics, which fails the test.
type installationStore struct {
	gate.Store

	objects *objectStore
	rules   *recordingStore
}

func (s installationStore) Gates() gate.Objects[gate.Gate] { return s.objects.Gates() }

func (s installationStore) AccessLimits() gate.Objects[gate.AccessLimit] {
	return s.objects.AccessLimits()
}

func (s installationStore) ApprovalRules() gate.Objects[gate.ApprovalRule] {
	return s.rules.ApprovalRules()
}

func newInstallationFixture(t *testing.T) installationFixture {
	t.Helper()

	f := installationFixture{
		svc: nil, reads: new(atomic.Int64),
		objects: &objectStore{Store: nil, writes: nil, stored: nil, gates: recordedGates{Objects: nil, store: nil}},
		rules:   &recordingStore{Store: nil, writes: nil, rule: nil, def: nil},
	}

	engine := &fakeEngine{Engine: nil, diags: nil, err: nil, membershipErr: nil, checks: nil, schema: nil}

	svc, err := gate.NewConfigService(gate.ConfigServiceConfig{
		Store:      installationStore{Store: nil, objects: f.objects, rules: f.rules},
		Namespaces: countingNamespaces{reads: f.reads}, Approvers: fakeGroups{}, Engine: engine, Now: configNow,
	})
	if err != nil {
		t.Fatalf("NewConfigService() = %v", err)
	}

	f.svc = svc

	return f
}

// writes returns the writes that reached the stores of the three kinds.
func (f installationFixture) writes() []write {
	return append(append([]write{}, f.objects.writes...), f.rules.writes...)
}

// TestConfigInstallationScope proves that the service reads, saves and
// deletes the gates, the access limits and the approval rules of the
// installation with the zero fence and the audit scope @server, and that it
// refuses the scopes that hold no such object.
func TestConfigInstallationScope(t *testing.T) {
	t.Parallel()

	inst := gate.InstallationScope()
	acme := gate.NamespaceScope("acme")

	t.Run("writes", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			scope  gate.Scope
			run    func(f installationFixture, s gate.Scope) error
			action string
			object string
		}{
			{"set gate", inst, func(f installationFixture, s gate.Scope) error {
				_, err := f.svc.PutGate(t.Context(), goodGate(s), 0, "", callerEntry())

				return err
			}, "installation:set-gate", "installation/gates/freeze"},
			{"delete gate", inst, func(f installationFixture, s gate.Scope) error {
				return f.svc.DeleteGate(
					t.Context(), gate.ObjectRef{Scope: s, ID: "freeze", Version: 1}, "", callerEntry(),
				)
			}, "installation:delete-gate", "installation/gates/freeze"},
			{"set access limit", inst, func(f installationFixture, s gate.Scope) error {
				_, err := f.svc.PutAccessLimit(t.Context(), goodLimit(s), 0, callerEntry())

				return err
			}, "installation:set-access-limit", "installation/access-limits/no-writes"},
			{"delete access limit", inst, func(f installationFixture, s gate.Scope) error {
				return f.svc.DeleteAccessLimit(
					t.Context(), gate.ObjectRef{Scope: s, ID: "no-writes", Version: 1}, callerEntry(),
				)
			}, "installation:delete-access-limit", "installation/access-limits/no-writes"},
			{"set approval rule", inst, func(f installationFixture, s gate.Scope) error {
				_, err := f.svc.PutApprovalRule(t.Context(), goodRule(s), 0, callerEntry())

				return err
			}, "installation:set-approval-rule", "installation/approval-rules/platform"},
			{"delete approval rule", inst, func(f installationFixture, s gate.Scope) error {
				return f.svc.DeleteApprovalRule(
					t.Context(), gate.ObjectRef{Scope: s, ID: "platform", Version: 1}, callerEntry(),
				)
			}, "installation:delete-approval-rule", "installation/approval-rules/platform"},
			// A write at a namespace never writes an installation: action.
			{"namespace set gate", acme, func(f installationFixture, s gate.Scope) error {
				_, err := f.svc.PutGate(t.Context(), goodGate(s), 0, "", callerEntry())

				return err
			}, "namespace:set-gate", "namespace/acme/gates/freeze"},
			{"namespace delete access limit", acme, func(f installationFixture, s gate.Scope) error {
				return f.svc.DeleteAccessLimit(
					t.Context(), gate.ObjectRef{Scope: s, ID: "no-writes", Version: 1}, callerEntry(),
				)
			}, "namespace:delete-access-limit", "namespace/acme/access-limits/no-writes"},
			{"namespace set approval rule", acme, func(f installationFixture, s gate.Scope) error {
				_, err := f.svc.PutApprovalRule(t.Context(), goodRule(s), 0, callerEntry())

				return err
			}, "namespace:set-approval-rule", "namespace/acme/approval-rules/platform"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				f := newInstallationFixture(t)
				if err := tt.run(f, tt.scope); err != nil {
					t.Fatalf("write = %v, want nil", err)
				}

				ws := f.writes()
				if len(ws) != 1 {
					t.Fatalf("stores got %d writes, want 1", len(ws))
				}

				w := ws[0]
				if w.entry.Action != tt.action || w.entry.Object != tt.object {
					t.Errorf("entry action %q object %q; want %q and %q",
						w.entry.Action, w.entry.Object, tt.action, tt.object)
				}

				if tt.scope.Level() == gate.LevelInstallation {
					want := namespace.Fence{Namespace: "", FailoverVersion: 0}
					if w.fence != want || w.entry.Namespace != audit.ServerScope || f.reads.Load() != 0 {
						t.Errorf("fence %+v, entry namespace %q, %d namespace reads; want the zero fence, @server and 0",
							w.fence, w.entry.Namespace, f.reads.Load())
					}

					return
				}

				if w.fence.Namespace != "acme" || w.entry.Namespace != "acme" ||
					strings.HasPrefix(w.entry.Action, "installation:") {
					t.Errorf("namespace write has fence %+v, namespace %q, action %q", w.fence, w.entry.Namespace, w.entry.Action)
				}
			})
		}
	})

	t.Run("reads", func(t *testing.T) {
		t.Parallel()

		f := newInstallationFixture(t)

		if _, err := f.svc.Gates(t.Context(), inst); err != nil {
			t.Errorf("Gates() = %v", err)
		}

		if _, err := f.svc.AccessLimits(t.Context(), inst); err != nil {
			t.Errorf("AccessLimits() = %v", err)
		}

		if _, err := f.svc.ApprovalRules(t.Context(), inst); err != nil {
			t.Errorf("ApprovalRules() = %v", err)
		}

		if _, err := f.svc.Gate(t.Context(), inst, "x"); !errors.Is(err, gate.ErrNotFound) {
			t.Errorf("Gate() = %v, want ErrNotFound", err)
		}

		if _, err := f.svc.AccessLimit(t.Context(), inst, "x"); !errors.Is(err, gate.ErrNotFound) {
			t.Errorf("AccessLimit() = %v, want ErrNotFound", err)
		}

		if _, err := f.svc.ApprovalRule(t.Context(), inst, "x"); !errors.Is(err, gate.ErrNotFound) {
			t.Errorf("ApprovalRule() = %v, want ErrNotFound", err)
		}

		if f.reads.Load() != 0 {
			t.Errorf("an installation read made %d namespace reads, want 0", f.reads.Load())
		}
	})

	t.Run("refusals", func(t *testing.T) {
		t.Parallel()

		blueprint, err := gate.BlueprintScope("web", 1)
		if err != nil {
			t.Fatalf("BlueprintScope() = %v", err)
		}

		profile, err := gate.ProfileScope("acme", "base")
		if err != nil {
			t.Fatalf("ProfileScope() = %v", err)
		}

		scopes := []struct {
			name  string
			scope gate.Scope
		}{{"blueprint", blueprint}, {"profile", profile}, {"empty", gate.Scope{}}}

		for _, sc := range scopes {
			tests := map[string]func(f installationFixture) error{
				"gates":         func(f installationFixture) error { _, err := f.svc.Gates(t.Context(), sc.scope); return err },
				"access limits": func(f installationFixture) error { _, err := f.svc.AccessLimits(t.Context(), sc.scope); return err },
				"approval rules": func(f installationFixture) error {
					_, err := f.svc.ApprovalRules(t.Context(), sc.scope)

					return err
				},
				"set gate": func(f installationFixture) error {
					_, err := f.svc.PutGate(t.Context(), goodGate(sc.scope), 0, "", callerEntry())

					return err
				},
				"set approval rule": func(f installationFixture) error {
					_, err := f.svc.PutApprovalRule(t.Context(), goodRule(sc.scope), 0, callerEntry())

					return err
				},
				"delete approval rule": func(f installationFixture) error {
					return f.svc.DeleteApprovalRule(
						t.Context(), gate.ObjectRef{Scope: sc.scope, ID: "platform", Version: 1}, callerEntry(),
					)
				},
			}

			for name, run := range tests {
				t.Run(sc.name+" "+name, func(t *testing.T) {
					t.Parallel()

					f := newInstallationFixture(t)
					if err := run(f); !errors.Is(err, gate.ErrInvalid) {
						t.Errorf("%s at a %s scope = %v, want ErrInvalid", name, sc.name, err)
					}

					if len(f.writes()) != 0 {
						t.Errorf("a refused call reached the store %d times, want 0", len(f.writes()))
					}
				})
			}
		}

		// The installation holds no approval default.
		defaults := map[string]func(f installationFixture) error{
			"read": func(f installationFixture) error { _, err := f.svc.ApprovalDefault(t.Context(), inst); return err },
			"set": func(f installationFixture) error {
				_, err := f.svc.PutApprovalDefault(t.Context(), goodDefault(inst), 0, callerEntry())

				return err
			},
			"delete": func(f installationFixture) error {
				return f.svc.DeleteApprovalDefault(t.Context(), inst, 1, callerEntry())
			},
		}

		for name, run := range defaults {
			t.Run("installation approval default "+name, func(t *testing.T) {
				t.Parallel()

				f := newInstallationFixture(t)
				if err := run(f); !errors.Is(err, gate.ErrInvalid) {
					t.Errorf("approval default %s at the installation = %v, want ErrInvalid", name, err)
				}

				if len(f.writes()) != 0 || f.reads.Load() != 0 {
					t.Errorf("a refused call made %d writes and %d namespace reads, want 0 and 0",
						len(f.writes()), f.reads.Load())
				}
			})
		}
	})
}
