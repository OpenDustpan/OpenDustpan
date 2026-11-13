package dustpan_test

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// phaseJobStore answers Get with one job. Any other method of the port panics
// on the nil embedded interface, which proves the clock reads nothing else.
type phaseJobStore struct {
	job.Store

	j job.Job
}

func (s *phaseJobStore) Get(_ context.Context, id job.ID) (job.Job, error) {
	if id != s.j.ID {
		return job.Job{}, job.ErrNotFound
	}

	return s.j, nil
}

// TestManualLockServiceUsesTheJobPhaseClock proves that the server gives the
// manual lock service the phase clock of the job store: the service answers
// the start of the live phase of a job. Without a job store it answers the zero
// time, which passes no lock.
func TestManualLockServiceUsesTheJobPhaseClock(t *testing.T) {
	t.Parallel()

	ref := collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "network"}
	unit := manuallock.Unit{Ref: ref, NamespaceID: "ns-1", UnitID: "unt-1"}

	object, err := identity.UnitObject(ref.Tool, ref.Namespace, ref.Name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	object = object.WithUnitID(unit.UnitID)

	started := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var j job.Job

	j.ID = "job-1"
	j.NamespaceID = unit.NamespaceID
	j.State = job.StateRunning
	j.Spec.Unit = object
	j.Phases = make([]job.Phase, 1)
	j.Phases[0].StartedAt = started

	store := &phaseJobStore{Store: nil, j: j}

	tests := []struct {
		name  string
		jobID string
		opts  []dustpan.Option
		want  time.Time
	}{
		{"job store, live phase", "job-1", []dustpan.Option{dustpan.WithJobStore(store)}, started},
		{"job store, unknown job", "job-2", []dustpan.Option{dustpan.WithJobStore(store)}, time.Time{}},
		{"no job store", "job-1", nil, time.Time{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := openChainStore(t)
			opts := append([]dustpan.Option{dustpan.WithNamespaceStore(st)}, tc.opts...)

			got, phaseErr := dustpan.ManualLockPhaseStart(t.Context(), unit, tc.jobID, opts...)
			if phaseErr != nil {
				t.Fatalf("phase start: %v", phaseErr)
			}

			if !got.Equal(tc.want) {
				t.Errorf("phase start = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestManualLockPhaseStartAfterRename proves that the phase clock compares the
// stable namespace ID and unit ID. The job kept the old name of its unit at its
// start, and the request names the new name.
func TestManualLockPhaseStartAfterRename(t *testing.T) {
	t.Parallel()

	oldRef := collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "network"}
	newRef := collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "network-2"}
	unit := manuallock.Unit{Ref: newRef, NamespaceID: "ns-1", UnitID: "unt-1"}

	object, err := identity.UnitObject(oldRef.Tool, oldRef.Namespace, oldRef.Name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	object = object.WithUnitID(unit.UnitID)

	started := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var j job.Job

	j.ID = "job-1"
	j.NamespaceID = unit.NamespaceID
	j.State = job.StateRunning
	j.Spec.Unit = object
	j.Phases = make([]job.Phase, 1)
	j.Phases[0].StartedAt = started

	store := &phaseJobStore{Store: nil, j: j}
	st := openChainStore(t)

	got, phaseErr := dustpan.ManualLockPhaseStart(
		t.Context(), unit, "job-1", dustpan.WithNamespaceStore(st), dustpan.WithJobStore(store),
	)
	if phaseErr != nil {
		t.Fatalf("phase start: %v", phaseErr)
	}

	if !got.Equal(started) {
		t.Fatalf("phase start = %v, want %v", got, started)
	}
}
