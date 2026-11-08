package gate_test

import (
	"context"
	"errors"
	"strings"
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
		{"installation scope", bad(func(g *gate.Gate) { g.Scope = gate.InstallationScope() }), gate.ErrInvalid},
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

	wide := gate.ObjectRef{Scope: gate.InstallationScope(), ID: "x", Version: 1}

	err := svc.DeleteGate(t.Context(), wide, "", callerEntry())
	if !errors.Is(err, gate.ErrInvalid) {
		t.Errorf("DeleteGate() at the installation = %v, want ErrInvalid", err)
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
			"installation scope",
			goodLimit(gate.InstallationScope()),
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
