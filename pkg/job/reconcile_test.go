package job_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// stubStore is a job.Store that the config checks never call.
type stubStore struct {
	job.Store
}

// stubNamespaces is a job.NamespaceReader that the config checks never call.
type stubNamespaces struct{}

// GetNamespace implements job.NamespaceReader.
func (stubNamespaces) GetNamespace(context.Context, namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{}, namespace.ErrNotFound
}

// reconcilerConfig returns a valid config that change then edits.
func reconcilerConfig(change func(*job.ReconcilerConfig)) job.ReconcilerConfig {
	cfg := job.ReconcilerConfig{
		Store: stubStore{Store: nil}, Namespaces: stubNamespaces{}, QueueTimeout: 0, DispatchTimeout: 0,
		StateTimeout: 0, MaxPlanAge: 0, Approvers: gate.ApproverPolicy{Production: true, IsDev: nil}, Batch: 0,
		Now: nil, Log: nil,
	}

	change(&cfg)

	return cfg
}

func TestNewReconcilerChecksItsConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*job.ReconcilerConfig)
		valid  bool
	}{
		{"defaults", func(*job.ReconcilerConfig) {}, true},
		{"no store", func(c *job.ReconcilerConfig) { c.Store = nil }, false},
		{"no namespace reader", func(c *job.ReconcilerConfig) { c.Namespaces = nil }, false},
		{"negative queue timeout", func(c *job.ReconcilerConfig) { c.QueueTimeout = -time.Second }, false},
		{"negative dispatch timeout", func(c *job.ReconcilerConfig) { c.DispatchTimeout = -time.Second }, false},
		{"negative state timeout", func(c *job.ReconcilerConfig) { c.StateTimeout = -time.Second }, false},
		{"negative plan age", func(c *job.ReconcilerConfig) { c.MaxPlanAge = -time.Second }, false},
		{"negative batch", func(c *job.ReconcilerConfig) { c.Batch = -1 }, false},
		{"set timeouts", func(c *job.ReconcilerConfig) { c.QueueTimeout = time.Hour; c.Batch = 5 }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := job.NewReconciler(reconcilerConfig(tt.change))

			if tt.valid && (err != nil || r == nil) {
				t.Fatalf("NewReconciler = %v, %v; want a reconciler", r, err)
			}

			if !tt.valid && !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("NewReconciler = %v, want ErrInvalid", err)
			}
		})
	}
}
