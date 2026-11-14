// Package unmanaged models the sweep store: sweep targets and sweeps.
//
// A sweep target names one operator pool with the sweep purpose. A sweep is
// one run of a target. Every write carries the fence of its namespace, and
// the store checks it in the same transaction as the write.
package unmanaged

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Bounds of the sweep store.
const (
	// MaxTargets bounds the sweep targets of a namespace.
	MaxTargets = 100
	// MaxPage bounds a page of sweeps.
	MaxPage = 500
	// PurposeSweeps is the purpose of a pool that runs sweeps only.
	PurposeSweeps = "sweeps"
	// PurposeJobs is the purpose of a pool that runs jobs only.
	PurposeJobs = "jobs"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrInvalidTarget reports a sweep target that fails validation, or that
	// names a pool that is not an operator pool with the sweep purpose.
	ErrInvalidTarget = errors.New("invalid sweep target")
	// ErrAlreadyExists reports a sweep target with a name that is taken.
	ErrAlreadyExists = errors.New("sweep target already exists")
	// ErrNotFound reports a sweep target or a sweep that does not exist.
	ErrNotFound = errors.New("sweep target or sweep not found")
	// ErrLimit reports a write above a limit of the namespace.
	ErrLimit = errors.New("limit reached")
	// ErrScanBusy reports the start of a sweep for a target that has a sweep
	// that did not end.
	ErrScanBusy = errors.New("a sweep of this target did not end")
	// ErrTransition reports an event that the state table does not allow.
	ErrTransition = errors.New("transition not allowed")
	// ErrStatusChanged reports a sweep whose status changed after the caller
	// read it.
	ErrStatusChanged = errors.New("the sweep changed")
	// ErrPoolInUse reports a pool that a sweep target still names.
	ErrPoolInUse = errors.New("runner pool is still named by a sweep target")
)

// Target is one sweep target of a namespace. PoolID is the stable ID of the
// runner pool of the target.
type Target struct {
	Namespace namespace.Name
	Name      string
	PoolID    string
	Every     time.Duration
	Timeout   time.Duration
	Enabled   bool
}

// Validate reports whether t is a valid target.
func (t Target) Validate() error {
	if t.Namespace == "" {
		return fmt.Errorf("%w: empty namespace", ErrInvalidTarget)
	}

	if t.Name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidTarget)
	}

	if t.PoolID == "" {
		return fmt.Errorf("%w: empty pool", ErrInvalidTarget)
	}

	return nil
}

// SweepStatus is the status of a sweep.
type SweepStatus string

// The statuses of a sweep.
const (
	SweepQueued    SweepStatus = "queued"
	SweepRunning   SweepStatus = "running"
	SweepSucceeded SweepStatus = "succeeded"
	SweepFailed    SweepStatus = "failed"
	SweepCanceled  SweepStatus = "canceled"
	SweepExpired   SweepStatus = "expired"
)

// Terminal reports whether s ends its sweep.
func (s SweepStatus) Terminal() bool {
	switch s {
	case SweepSucceeded, SweepFailed, SweepCanceled, SweepExpired:
		return true
	default:
		return false
	}
}

// SweepEvent is one event that advances a sweep.
type SweepEvent string

// The events that advance a sweep.
const (
	EventStart   SweepEvent = "start"
	EventSucceed SweepEvent = "succeed"
	EventFail    SweepEvent = "fail"
	EventCancel  SweepEvent = "cancel"
	EventExpire  SweepEvent = "expire"
)

// NextSweep returns the status after ev in status s. It returns ErrTransition
// for an event that the table does not allow.
func NextSweep(s SweepStatus, ev SweepEvent) (SweepStatus, error) {
	switch s {
	case "":
		if ev == EventStart {
			return SweepRunning, nil
		}
	case SweepQueued:
		switch ev {
		case EventStart:
			return SweepRunning, nil
		case EventSucceed:
			return SweepSucceeded, nil
		case EventFail:
			return SweepFailed, nil
		case EventCancel:
			return SweepCanceled, nil
		case EventExpire:
			return SweepExpired, nil
		}
	case SweepRunning:
		switch ev {
		case EventSucceed:
			return SweepSucceeded, nil
		case EventFail:
			return SweepFailed, nil
		case EventCancel:
			return SweepCanceled, nil
		case EventExpire:
			return SweepExpired, nil
		}
	}

	return "", fmt.Errorf("%w: %s on a sweep that is %q", ErrTransition, ev, s)
}

// Sweep is one run of a sweep target.
type Sweep struct {
	ID        string
	Namespace namespace.Name
	Target    string
	Status    SweepStatus
	CreatedAt time.Time
	EndedAt   time.Time
	Reason    string
}

// SweepAdvance is one advance of a sweep: the sweep, the event and the reason
// of the event.
type SweepAdvance struct {
	Namespace namespace.Name
	ID        string
	Event     SweepEvent
	Reason    string
}

// SweepStore is the persistence port of sweep targets and sweeps. Every write
// checks the fence of the namespace in the same transaction. Every adapter
// runs the suite in unmanagedtest.
type SweepStore interface {
	// CreateTarget creates t. It returns ErrInvalidTarget, also for a pool
	// of another namespace or a pool whose purpose is jobs, not sweeps.
	// It returns ErrAlreadyExists, ErrLimit above MaxTargets, and
	// namespace.ErrFenced.
	CreateTarget(ctx context.Context, t Target, fence namespace.Fence) error
	// UpdateTarget replaces the target of the same name. It returns
	// ErrInvalidTarget, also for a pool as CreateTarget refuses it,
	// ErrNotFound and namespace.ErrFenced.
	UpdateTarget(ctx context.Context, t Target, fence namespace.Fence) error
	// DeleteTarget deletes the target name of ns. It refuses a target with
	// a sweep that did not end. It returns ErrNotFound and
	// namespace.ErrFenced.
	DeleteTarget(ctx context.Context, ns namespace.Name, name string, fence namespace.Fence) error
	// Targets returns the targets of ns ordered by name.
	Targets(ctx context.Context, ns namespace.Name) ([]Target, error)
	// StartSweep stores s with the status SweepQueued. It returns
	// ErrScanBusy when the target has a sweep that did not end, ErrNotFound
	// for an unknown target, and namespace.ErrFenced.
	StartSweep(ctx context.Context, s Sweep, fence namespace.Fence) (Sweep, error)
	// AdvanceSweep applies a.Event with a.Reason to the sweep a.ID of
	// a.Namespace, by NextSweep. It returns the new sweep, ErrNotFound,
	// ErrTransition and namespace.ErrFenced.
	AdvanceSweep(ctx context.Context, a SweepAdvance, fence namespace.Fence) (Sweep, error)
	// GetSweep returns the sweep id of ns, or ErrNotFound.
	GetSweep(ctx context.Context, ns namespace.Name, id string) (Sweep, error)
	// ListSweeps returns the sweeps of ns for target, newest first, or of
	// every target when target is empty. It starts after the sweep ID after
	// and returns at most limit, from 1 to MaxPage.
	ListSweeps(ctx context.Context, ns namespace.Name, target, after string, limit int) ([]Sweep, error)
}
