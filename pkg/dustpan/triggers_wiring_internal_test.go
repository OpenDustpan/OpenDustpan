package dustpan

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
	"opendustpan.org/opendustpan/pkg/trigger/triggertest"
)

// finalsStore is a job.Store with the final version reader. Its embedded store
// is nil: the wiring under test calls no method of it except FinalVersion.
type finalsStore struct {
	job.Store

	reader *fakeReader
}

// FinalVersion implements job.FinalVersionReader.
func (s finalsStore) FinalVersion(ctx context.Context, ns namespace.Name, unitID string,
) (job.FinalVersion, bool, error) {
	return s.reader.FinalVersion(ctx, ns, unitID)
}

// plainStore is a job.Store without the final version reader.
type plainStore struct {
	job.Store
}

// noTriggers is a trigger.Store with no followers of any unit.
type noTriggers struct {
	trigger.Store
}

// Followers implements trigger.Store.
func (noTriggers) Followers(context.Context, graph.Unit) ([]trigger.Trigger, error) {
	return nil, nil
}

// noAudit is an audit.Store that the dispatcher only needs as an appender.
type noAudit struct {
	audit.Store
}

// noStarter is a trigger.Starter. It starts nothing, because the test has no
// followers to start.
type noStarter struct {
	trigger.Starter
}

// noFences is the trigger.Fences of a namespace with fence zero.
type noFences struct{}

// Fence implements trigger.Fences.
func (noFences) Fence(context.Context, namespace.Name) (namespace.Fence, error) {
	var f namespace.Fence

	return f, nil
}

// noJobStarter is the job.Starter of a server that starts no job. The test
// never starts one: it has no followers.
type noJobStarter struct {
	job.Starter
}

// TestNewTriggerManagerReadsFinalVersionFromJobStore goes through
// newTriggerManager. The dispatcher that the manager builds reads the last final
// state version of a unit from the job store, and it fails closed without the
// port.
func TestNewTriggerManagerReadsFinalVersionFromJobStore(t *testing.T) {
	t.Parallel()

	unit := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "u-1"}
	row := job.FinalVersion{UnitID: "u-1", VersionID: "sv-9", Commit: "", Job: "", At: time.Time{}}

	tests := []struct {
		name    string
		hasPort bool
		version string
		wantErr error
	}{
		{
			name:    "the last final version is accepted",
			hasPort: true,
			version: "sv-9",
			wantErr: nil,
		},
		{
			name:    "an older version is refused",
			hasPort: true,
			version: "sv-8",
			wantErr: trigger.ErrVersionMismatch,
		},
		{
			name:    "a store without the port fails closed",
			hasPort: false,
			version: "sv-9",
			wantErr: trigger.ErrVersionMismatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var js job.Store = plainStore{Store: nil}
			if tc.hasPort {
				js = finalsStore{Store: nil, reader: &fakeReader{
					row: row, ok: true, err: nil, gotNS: "", gotID: "",
				}}
			}

			// The dispatcher that the manager builds is kept here, so that the test
			// can call its Upstream.
			var built trigger.Dispatcher

			capture := func(cfg trigger.DispatcherConfig) (trigger.Dispatcher, error) {
				d, err := trigger.NewDispatcher(cfg)
				built = d

				return d, err
			}

			cfg := new(settings)
			cfg.triggerStore = noTriggers{Store: nil}
			cfg.jobStore = js
			cfg.authorizer = identity.AdminOnly{}
			cfg.logger = slog.New(slog.DiscardHandler)
			ports := &triggerPorts{records: nil, entries: noAudit{Store: nil}}

			var svc services

			manager, err := newTriggerManager(svc, cfg, ports, noJobStarter{Starter: nil}, capture)
			if err != nil {
				t.Fatalf("newTriggerManager: %v", err)
			}

			if manager == nil || built == nil {
				t.Fatal("newTriggerManager built no dispatcher")
			}

			_, err = built.Upstream(t.Context(), trigger.Applied{Unit: unit, Version: tc.version})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Upstream error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestTriggerDispatcherReadsFinalVersionFromJobStore checks the wiring of the
// dispatcher: the version gate of an upstream event reads the last final state
// version from the job store. Without the wiring, every upstream event fails
// closed with trigger.ErrVersionMismatch.
func TestTriggerDispatcherReadsFinalVersionFromJobStore(t *testing.T) {
	t.Parallel()

	unit := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "u-1"}
	row := job.FinalVersion{UnitID: "u-1", VersionID: "sv-9", Commit: "", Job: "", At: time.Time{}}

	tests := []struct {
		name    string
		hasPort bool
		version string
		wantErr error
	}{
		{
			name:    "the last final version is accepted",
			hasPort: true,
			version: "sv-9",
			wantErr: nil,
		},
		{
			name:    "an older version is refused",
			hasPort: true,
			version: "sv-8",
			wantErr: trigger.ErrVersionMismatch,
		},
		{
			name:    "a store without the port fails closed",
			hasPort: false,
			version: "sv-9",
			wantErr: trigger.ErrVersionMismatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Each case has its own reader: the fake records the arguments of
			// its calls.
			var js job.Store = plainStore{Store: nil}
			if tc.hasPort {
				js = finalsStore{Store: nil, reader: &fakeReader{
					row: row, ok: true, err: nil, gotNS: "", gotID: "",
				}}
			}

			doubles := triggertest.NewDoubles()
			cfg := new(settings)
			cfg.triggerStore = noTriggers{Store: nil}
			cfg.jobStore = js
			cfg.logger = slog.New(slog.DiscardHandler)
			ports := &triggerPorts{records: nil, entries: noAudit{Store: nil}}
			parts := triggerParts{
				starter: noStarter{Starter: nil}, savers: doubles, units: doubles, fences: noFences{},
			}

			dispatcher, err := trigger.NewDispatcher(triggerDispatcherConfig(cfg, ports, parts))
			if err != nil {
				t.Fatalf("NewDispatcher: %v", err)
			}

			_, err = dispatcher.Upstream(t.Context(), trigger.Applied{Unit: unit, Version: tc.version})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Upstream error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
