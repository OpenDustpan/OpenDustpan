package trigger_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

type countingStarter struct{ started int }

func (s *countingStarter) StartPlan(context.Context, job.Actor, graph.Unit, trigger.PlanRequest) (job.ID, error) {
	s.started++

	return "job-1", nil
}

type failingAudit struct{ calls int }

func (a *failingAudit) Append(context.Context, namespace.Fence, audit.Entry) (audit.Entry, error) {
	a.calls++

	return audit.Entry{}, errors.New("audit store is down")
}

type dispatcherRig struct {
	d       trigger.Dispatcher
	starter *countingStarter
	audit   *failingAudit
	savers  *fakeSaverCheck
	store   *fakeStore
}

func newDispatcherRig(t *testing.T, versions trigger.Versions) *dispatcherRig {
	t.Helper()

	r := &dispatcherRig{
		starter: &countingStarter{}, audit: &failingAudit{}, savers: &fakeSaverCheck{}, store: newStore(),
	}

	d, err := trigger.NewDispatcher(trigger.DispatcherConfig{
		Store: r.store, Starter: r.starter, Savers: r.savers, Units: newUnits(), Audit: r.audit,
		Fences: &fakeFences{}, Versions: versions, Now: func() time.Time { return time.UnixMilli(1_800_000_000_000) },
	})
	if err != nil {
		t.Fatalf("NewDispatcher = %v", err)
	}

	r.d = d
	r.store.triggers["s1"] = schedule("s1", trigger.Frequency{Preset: "daily", Cron: ""})

	return r
}

func TestNewDispatcherNeedsPorts(t *testing.T) {
	t.Parallel()

	full := trigger.DispatcherConfig{
		Store: newStore(), Starter: &countingStarter{}, Savers: &fakeSaverCheck{}, Units: newUnits(),
		Audit: &failingAudit{}, Fences: &fakeFences{}, Versions: nil, Now: nil,
	}

	for name, mutate := range map[string]func(*trigger.DispatcherConfig){
		"no store":   func(c *trigger.DispatcherConfig) { c.Store = nil },
		"no starter": func(c *trigger.DispatcherConfig) { c.Starter = nil },
		"no savers":  func(c *trigger.DispatcherConfig) { c.Savers = nil },
		"no units":   func(c *trigger.DispatcherConfig) { c.Units = nil },
		"no audit":   func(c *trigger.DispatcherConfig) { c.Audit = nil },
		"no fences":  func(c *trigger.DispatcherConfig) { c.Fences = nil },
	} {
		cfg := full
		mutate(&cfg)

		if _, err := trigger.NewDispatcher(cfg); !errors.Is(err, trigger.ErrInvalid) {
			t.Errorf("%s: NewDispatcher = %v, want ErrInvalid", name, err)
		}
	}

	if _, err := trigger.NewDispatcher(full); err != nil {
		t.Errorf("a dispatcher with no versions and no clock: NewDispatcher = %v", err)
	}
}

func TestDispatcherUpstreamWithoutVersionsFailsClosed(t *testing.T) {
	t.Parallel()

	r := newDispatcherRig(t, nil)

	_, err := r.d.Upstream(t.Context(), trigger.Applied{Unit: unit("a"), Version: "v-1"})
	wantErr(t, "Upstream", err, trigger.ErrVersionMismatch)

	if r.starter.started != 0 {
		t.Errorf("started %d plans, want none", r.starter.started)
	}
}

func TestDispatcherRefusalIsAuditedAndFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		saverErr    error
		wantAudit   int
		wantDenied  bool
		wantAuditIn bool
	}{
		{"a saver that lost access, with a failing audit store", trigger.ErrSaverDenied, 1, true, true},
		{"a port that fails writes no denial entry", errors.New("grant store is down"), 0, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newDispatcherRig(t, nil)
			r.savers.err = tt.saverErr

			_, err := r.d.Scheduled(t.Context(), acme, "s1", time.UnixMilli(1_800_000_000_000))
			if err == nil {
				t.Fatal("Scheduled = nil, want a refusal")
			}

			if errors.Is(err, trigger.ErrSaverDenied) != tt.wantDenied {
				t.Errorf("Scheduled = %v, want denied %v", err, tt.wantDenied)
			}

			if r.audit.calls != tt.wantAudit || r.starter.started != 0 {
				t.Errorf("audit calls %d, starts %d; want %d and 0", r.audit.calls, r.starter.started, tt.wantAudit)
			}
		})
	}
}

type onlyVersion struct{ id string }

func (v onlyVersion) LastFinal(context.Context, graph.Unit) (string, bool, error) {
	return v.id, v.id != "", nil
}

func TestDispatcherUpstreamUsesTheLastFinalVersion(t *testing.T) {
	t.Parallel()

	r := newDispatcherRig(t, onlyVersion{id: "v-2"})
	r.store.triggers["f1"] = follows("f1", "b", "a")

	// An event for an older version starts nothing, and so does an unknown one.
	for _, version := range []string{"v-1", "v-3"} {
		_, err := r.d.Upstream(t.Context(), trigger.Applied{Unit: unit("a"), Version: version})
		wantErr(t, "Upstream "+version, err, trigger.ErrVersionMismatch)
	}

	out, err := r.d.Upstream(t.Context(), trigger.Applied{Unit: unit("a"), Version: "v-2"})
	if err != nil || len(out) != 1 || out[0].Err != nil || out[0].Job == "" {
		t.Errorf("Upstream = %+v, %v; want one started plan", out, err)
	}
}
