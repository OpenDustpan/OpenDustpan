// Package dependency records which dependents read the outputs of a unit,
// so that a delete of the unit can check what depends on it first.
//
// An edge links a source unit to a dependent: another unit when the server
// knows it, or only the principal that read. The server records an edge as a
// side effect of an output read, at most one time for each RecordInterval,
// and a failed write never fails the read. Edges are tool-neutral: a Pulumi
// stack can depend on an OpenTofu workspace.
package dependency

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors of the dependency ports.
var (
	// ErrHasDependents reports a delete or a purge that is not forced, of a
	// unit with a blocking edge.
	ErrHasDependents = errors.New("unit has dependents")
	// ErrInvalidEdge reports an edge that fails validation.
	ErrInvalidEdge = errors.New("invalid dependency edge")
)

// RecordInterval is the shortest time between two writes of one edge. A
// read within it after the last write changes nothing.
const RecordInterval = time.Hour

// DefaultWindow is how long an edge blocks a delete after the last time
// seen. The setting --dependents-window changes it.
const DefaultWindow = 90 * 24 * time.Hour

// Kind is what an edge records.
type Kind string

// The kinds of an edge.
const (
	// KindOutputRead is a read of the outputs or the state of the source
	// by the dependent.
	KindOutputRead Kind = "output-read"
)

// Evidence is how the server knows the dependent of an edge.
type Evidence string

// The evidence of an edge.
const (
	// EvidenceJob is a read with the token of a job that names its unit.
	EvidenceJob Evidence = "job"
	// EvidenceLock is a read by a principal that held the update lease or
	// the lock of another unit at the time. It is an inference.
	EvidenceLock Evidence = "lock"
	// EvidencePrincipal is a read by a principal that held nothing. The edge
	// names the principal only. It never blocks a delete.
	EvidencePrincipal Evidence = "principal"
	// EvidenceDeclared is a link between units that a feature of the server
	// declares, for example a trigger.
	EvidenceDeclared Evidence = "declared"
)

// Edge says that a dependent reads the outputs of the source unit.
// Dependent is nil when the evidence is EvidencePrincipal. Ambiguous is true
// for a lock edge when the principal held more than one unit at the time.
// Reads counts the reads that the edge records, at most one for each
// RecordInterval.
type Edge struct {
	FirstSeen time.Time
	LastSeen  time.Time
	Dependent *history.Unit
	Source    history.Unit
	Principal string
	Kind      Kind
	Evidence  Evidence
	Reads     int64
	Ambiguous bool
}

// Validate reports whether e is an edge that a store can record. It returns
// ErrInvalidEdge otherwise.
func (e Edge) Validate() error {
	if err := e.Source.Validate(); err != nil {
		return errors.Join(ErrInvalidEdge, err)
	}

	switch {
	case e.Kind != KindOutputRead:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidEdge, e.Kind)
	case e.Principal == "" && e.Evidence != EvidenceDeclared:
		return fmt.Errorf("%w: a read edge names its principal", ErrInvalidEdge)
	case e.FirstSeen.IsZero() || e.LastSeen.Before(e.FirstSeen):
		return fmt.Errorf("%w: the last time seen is before the first", ErrInvalidEdge)
	}

	return e.validateDependent()
}

// validateDependent checks the dependent of e against its evidence.
func (e Edge) validateDependent() error {
	switch e.Evidence {
	case EvidencePrincipal:
		if e.Dependent != nil {
			return fmt.Errorf("%w: a principal edge names no unit", ErrInvalidEdge)
		}

		return nil
	case EvidenceJob, EvidenceLock, EvidenceDeclared:
		if e.Dependent == nil {
			return fmt.Errorf("%w: a unit edge names its unit", ErrInvalidEdge)
		}

		if err := e.Dependent.Validate(); err != nil {
			return errors.Join(ErrInvalidEdge, err)
		}

		if *e.Dependent == e.Source {
			return fmt.Errorf("%w: a unit does not depend on itself", ErrInvalidEdge)
		}

		return nil
	default:
		return fmt.Errorf("%w: unknown evidence %q", ErrInvalidEdge, e.Evidence)
	}
}

// SameEdge reports whether a and b are the same edge: the same source, the
// same dependent unit or principal, the same kind and the same evidence. A
// store keeps one row for each edge.
func SameEdge(a, b Edge) bool {
	sameDependent := (a.Dependent == nil) == (b.Dependent == nil) &&
		(a.Dependent == nil || *a.Dependent == *b.Dependent)

	return a.Source == b.Source && sameDependent && a.Principal == b.Principal && a.Kind == b.Kind &&
		a.Evidence == b.Evidence
}

// Due reports whether a read at now must write the stored edge again: the
// last write is RecordInterval or more ago.
func Due(stored Edge, now time.Time) bool {
	return !now.Before(stored.LastSeen.Add(RecordInterval))
}

// Blocks reports whether e blocks a delete that is not forced at now, with
// the dependents window window: its evidence is a job, a lock or a
// declaration, and its last time seen is within the window.
func Blocks(e Edge, now time.Time, window time.Duration) bool {
	if e.Evidence == EvidencePrincipal {
		return false
	}

	return now.Before(e.LastSeen.Add(window))
}

// Blocking returns the edges of edges that block a delete at now, in their
// order.
func Blocking(edges []Edge, now time.Time, window time.Duration) []Edge {
	return slices.DeleteFunc(slices.Clone(edges), func(e Edge) bool { return !Blocks(e, now, window) })
}

// Store is the persistence port of edges.
type Store interface {
	// RecordEdge writes e: it stores a new edge as it is, and for a stored
	// edge (SameEdge) it keeps FirstSeen, sets LastSeen to the later time,
	// adds e.Reads to the count, and sets Ambiguous when e has it. The write
	// carries the fence of the namespace of the source. It returns the error
	// of Edge.Validate, and namespace.ErrFenced when the fence is stale. The
	// caller skips the write when the stored edge is not Due.
	RecordEdge(ctx context.Context, e Edge, fence namespace.Fence) error
	// Dependents returns the edges whose source is source and whose last
	// time seen is at or after since, ordered by the last time seen, newest
	// first. A soft-deleted source keeps its edges.
	Dependents(ctx context.Context, source history.Unit, since time.Time) ([]Edge, error)
	// Sources returns the edges whose dependent unit is dependent and whose
	// last time seen is at or after since, ordered by the last time seen,
	// newest first.
	Sources(ctx context.Context, dependent history.Unit, since time.Time) ([]Edge, error)
	// DropUnit deletes, for the purge of unit, every edge whose source is
	// unit and every edge of the same namespace whose dependent is unit. An
	// edge of another namespace with unit as its dependent stays, because
	// the fence of unit does not cover it, and leaves the window in time. A
	// unit with no edge is not an error. It returns namespace.ErrFenced when
	// the fence of the namespace of unit is stale.
	DropUnit(ctx context.Context, unit history.Unit, fence namespace.Fence) error
}
