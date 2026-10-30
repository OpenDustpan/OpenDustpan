package unitsettingssvc_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unitsettingssvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

const (
	connRW   = "gitconn-00000000000000000000000000000001"
	connRO   = "gitconn-00000000000000000000000000000002"
	poolJobs = "rp-jobs"
	poolSwp  = "rp-sweeps"
	poolFar  = "rp-other"
)

type rig struct {
	svc   *unitsettingssvc.Service
	store *memStore
	unit  identity.Object
}

func newRig(t *testing.T, withDefaultConn bool) *rig {
	t.Helper()

	ns := namespace.Name("acme")

	unit, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatal(err)
	}

	conns := memConnections{records: []forge.ConnectionRecord{
		{Namespace: ns, ID: connRW, Push: true, Default: withDefaultConn},
		{Namespace: ns, ID: connRO, Push: false},
	}}
	pools := memPools{pools: []runner.Pool{
		{ID: poolJobs, Namespace: ns, Purpose: runner.PurposeJobs},
		{ID: poolSwp, Namespace: ns, Purpose: runner.PurposeSweeps},
		{ID: poolFar, Namespace: "beta", Purpose: runner.PurposeJobs},
	}}
	store := newMemStore()

	svc, err := unitsettingssvc.New(unitsettingssvc.Config{
		Store: store, Fences: fixedFences{}, Connections: conns, Pools: pools, Now: fixedNow,
	})
	if err != nil {
		t.Fatal(err)
	}

	return &rig{svc: svc, store: store, unit: unit.WithUnitID("unit-1")}
}

func entry() audit.Entry {
	return audit.Entry{Subject: "ada", Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, Channel: audit.ChannelAPI}
}

func TestNewNeedsEveryDependency(t *testing.T) {
	t.Parallel()

	if _, err := unitsettingssvc.New(unitsettingssvc.Config{}); !errors.Is(err, unitsettingssvc.ErrMissingDependency) {
		t.Fatalf("New = %v, want ErrMissingDependency", err)
	}
}

func TestReadOfAUnitWithNoRecordShowsTheOriginOfEachField(t *testing.T) {
	t.Parallel()

	r := newRig(t, true)

	view, err := r.svc.Read(t.Context(), r.unit)
	if err != nil {
		t.Fatal(err)
	}

	e := view.Effective
	if view.Settings.Version != 0 || e.Connection.Value != connRW ||
		e.Connection.Origin != unitsettings.OriginNamespaceDefault ||
		e.Executor.Value != job.ExecutorRunner || e.Executor.Origin != unitsettings.OriginBuiltIn ||
		e.Directory.Value != "." {
		t.Fatalf("Read = %+v", view)
	}

	if got := e.Files.Value; len(got) != 1 || got[0] != "Pulumi.prod.yaml" {
		t.Fatalf("default files = %v, want the stack file of the tool", got)
	}
}

func TestDefaultFilesByTool(t *testing.T) {
	t.Parallel()

	ns := namespace.Name("acme")

	tofu, err := identity.UnitObject("opentofu", ns, "network")
	if err != nil {
		t.Fatal(err)
	}

	other, err := identity.UnitObject("future", ns, "x")
	if err != nil {
		t.Fatal(err)
	}

	if got := unitsettingssvc.DefaultFiles(tofu); len(got) != 1 || got[0] != "network.auto.tfvars.json" {
		t.Fatalf("opentofu files = %v", got)
	}

	if got := unitsettingssvc.DefaultFiles(other); got != nil {
		t.Fatalf("unknown tool files = %v, want none", got)
	}
}

func TestPutStampsTheEntryAndTheFence(t *testing.T) {
	t.Parallel()

	r := newRig(t, true)
	s := unitsettings.Settings{Unit: r.unit, CodeLocation: unitsettings.CodeLocation{Repository: "acme/infra", Ref: "main"}}

	view, err := r.svc.Put(t.Context(), &s, 0, entry())
	if err != nil {
		t.Fatal(err)
	}

	if view.Settings.Version != 1 || view.Effective.Repository.Origin != unitsettings.OriginUnit {
		t.Fatalf("Put = %+v", view)
	}

	e := r.store.entries[0]
	if e.Namespace != "acme" || e.Object != r.unit.String() || e.ObjectID != "unit-1" || e.FenceVersion != 7 ||
		!e.At.Equal(time.Unix(1_800_000_000, 0)) {
		t.Fatalf("entry = %+v, want the namespace, object, fence and time of the service", e)
	}

	if r.store.fences[0].FailoverVersion != 7 {
		t.Fatalf("fence = %+v", r.store.fences[0])
	}
}

func TestPutRefusesWhatThePlannerCouldNotUse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(s *unitsettings.Settings)
		want   error
	}{
		{"unknown connection", func(s *unitsettings.Settings) {
			s.CodeLocation.Connection = "gitconn-0000000000000000000000000000ffff"
		}, unitsettingssvc.ErrConnectionUnknown},
		{"read-only connection for direct push", func(s *unitsettings.Settings) {
			s.CodeLocation.Connection = connRO
			s.WriteBack.Delivery = job.DeliveryDirectPush
		}, unitsettingssvc.ErrConnectionLacksCapability},
		{"read-only connection for a review request", func(s *unitsettings.Settings) {
			s.CodeLocation.Connection = connRO
			s.Executor = job.ExecutorWriteBack
		}, unitsettingssvc.ErrConnectionLacksCapability},
		{"unknown pool", func(s *unitsettings.Settings) { s.Pool = "rp-nope" }, unitsettingssvc.ErrPoolUnknown},
		{"pool for sweeps", func(s *unitsettings.Settings) { s.Pool = poolSwp }, unitsettingssvc.ErrPoolUnknown},
		{"pool of another namespace", func(s *unitsettings.Settings) { s.Pool = poolFar }, unitsettingssvc.ErrPoolUnknown},
		{"invalid directory", func(s *unitsettings.Settings) { s.CodeLocation.Directory = "../x" }, unitsettings.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, true)
			s := unitsettings.Settings{Unit: r.unit}
			tc.mutate(&s)

			if _, err := r.svc.Put(t.Context(), &s, 0, entry()); !errors.Is(err, tc.want) {
				t.Fatalf("Put = %v, want %v", err, tc.want)
			}

			if len(r.store.entries) != 0 {
				t.Fatalf("a refused write wrote %d entries", len(r.store.entries))
			}
		})
	}
}

func TestPutAcceptsAReadWriteConnectionForWriteBack(t *testing.T) {
	t.Parallel()

	r := newRig(t, false)
	s := unitsettings.Settings{Unit: r.unit, Executor: job.ExecutorWriteBack}
	s.CodeLocation.Connection = connRW
	s.ReviewRequests.Enabled = true

	if _, err := r.svc.Put(t.Context(), &s, 0, entry()); err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func TestNeedsOf(t *testing.T) {
	t.Parallel()

	r := newRig(t, true)

	cur, err := r.svc.Read(t.Context(), r.unit)
	if err != nil {
		t.Fatal(err)
	}

	step := job.Step{Name: "x", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu"}}

	cases := []struct {
		name   string
		mutate func(s *unitsettings.Settings)
		want   unitsettingssvc.Needs
	}{
		{"nothing new", func(*unitsettings.Settings) {}, unitsettingssvc.Needs{}},
		{"names a connection", func(s *unitsettings.Settings) { s.CodeLocation.Connection = connRO },
			unitsettingssvc.Needs{Connection: connRO}},
		{"names the default connection", func(s *unitsettings.Settings) { s.CodeLocation.Connection = connRW },
			unitsettingssvc.Needs{Connection: connRW}},
		{"changes the steps", func(s *unitsettings.Settings) { s.Steps = []job.Step{step} },
			unitsettingssvc.Needs{Steps: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := unitsettings.Settings{Unit: r.unit}
			tc.mutate(&s)

			if got := unitsettingssvc.NeedsOf(&cur, &s); got != tc.want {
				t.Fatalf("NeedsOf = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestNeedsOfDropsToTheDefaultConnection(t *testing.T) {
	t.Parallel()

	r := newRig(t, true)
	s := unitsettings.Settings{Unit: r.unit}
	s.CodeLocation.Connection = connRO

	view, err := r.svc.Put(t.Context(), &s, 0, entry())
	if err != nil {
		t.Fatal(err)
	}

	// A write that clears the named connection moves the effective connection
	// to the default one, which is a new connection for the unit.
	next := unitsettings.Settings{Unit: r.unit}
	if got := unitsettingssvc.NeedsOf(&view, &next); got.Connection != connRW {
		t.Fatalf("NeedsOf = %+v, want the default connection", got)
	}
}

func TestResetAndDefaults(t *testing.T) {
	t.Parallel()

	r := newRig(t, true)
	s := unitsettings.Settings{Unit: r.unit, Pool: poolJobs}

	if _, err := r.svc.Put(t.Context(), &s, 0, entry()); err != nil {
		t.Fatal(err)
	}

	if err := r.svc.Reset(t.Context(), r.unit, 1, entry()); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	d, err := r.svc.SetDefaults(t.Context(), "acme", poolJobs, 0, entry())
	if err != nil || d.Pool != poolJobs || d.Version != 1 {
		t.Fatalf("SetDefaults = %+v, %v", d, err)
	}

	if _, err := r.svc.SetDefaults(t.Context(), "acme", poolSwp, 1, entry()); !errors.Is(err, unitsettingssvc.ErrPoolUnknown) {
		t.Fatalf("SetDefaults of a sweeps pool = %v, want ErrPoolUnknown", err)
	}

	view, err := r.svc.Read(t.Context(), r.unit)
	if err != nil || view.Effective.Pool.Value != poolJobs || view.Effective.Pool.Origin != unitsettings.OriginNamespaceDefault {
		t.Fatalf("Read = %+v, %v, want the pool from the namespace default", view, err)
	}
}

func TestCapabilitiesFollowThePushFlag(t *testing.T) {
	t.Parallel()

	if c := unitsettingssvc.Capabilities(&forge.ConnectionRecord{Push: false}); c.Has(forge.CapCommit) || c.Has(forge.CapReview) {
		t.Fatalf("a read-only connection has %v", c)
	}

	if c := unitsettingssvc.Capabilities(&forge.ConnectionRecord{Push: true}); !c.Has(forge.CapCommit) || !c.Has(forge.CapReview) {
		t.Fatalf("a push connection has %v", c)
	}
}
