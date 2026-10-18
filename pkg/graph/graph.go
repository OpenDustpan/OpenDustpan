// Package graph answers questions about how units depend on each other:
// the dependents of a unit, the blast radius of a change within a number of
// hops, a path between two units, and the focus view of one unit.
//
// The graph records nothing. Its edges are the dependency edges that the
// output reads of each tool record, read through the EdgeReader port. Its
// nodes are units, with the facts that the console and an agent show next to
// a unit: the name, the collection and the owners. Each tool keeps its own
// model. A unit of one tool can depend on a unit of another tool, because the
// graph knows units only by namespace, tool and stable ID.
//
// Every query obeys the access rules. A walk never passes through a unit that
// the caller cannot read. It counts such units and says that the answer is
// not complete.
package graph

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors of the graph queries and ports.
var (
	// ErrInvalidUnit reports a unit reference that fails validation.
	ErrInvalidUnit = errors.New("invalid unit reference")
	// ErrInvalidQuery reports a query with a limit out of its range or with
	// the same unit at both ends of a path.
	ErrInvalidQuery = errors.New("invalid graph query")
	// ErrNotFound reports a start unit that does not exist, or that the
	// caller cannot read. The two cases give the same error, so a query does
	// not tell that a hidden unit exists.
	ErrNotFound = errors.New("unit not found")
)

// Limits of a query. A request above a maximum fails with ErrInvalidQuery.
// The limits keep the cost of one query bounded on a namespace with
// thousands of units.
const (
	// DefaultHops is the depth of a walk when the request gives none.
	DefaultHops = 2
	// MaxHops is the deepest walk.
	MaxHops = 5
	// MaxPathHops is the longest path that Path looks for.
	MaxPathHops = 6
	// DefaultNodes is the node limit of a walk when the request gives none.
	DefaultNodes = 200
	// MaxNodes is the most units that one answer holds, the start unit
	// excluded.
	MaxNodes = 500
	// MaxEdgesPerHop is the most edges that one hop of a walk reads. A hop
	// with more edges marks the answer as truncated.
	MaxEdgesPerHop = 5000
	// MaxBatch is the most units that one call to a port names.
	MaxBatch = 500
	// DefaultFocusNeighbors is the number of neighbors that a focus view
	// shows in each direction before it folds the others into their
	// collections.
	DefaultFocusNeighbors = 12
	// MaxFocusNeighbors is the highest value of FocusQuery.MaxNeighbors.
	MaxFocusNeighbors = 50
	// DefaultWindow is how far back in time an edge counts, from its last
	// time seen. It is the default dependents window of a safe delete, so
	// the graph and the delete check agree.
	DefaultWindow = 90 * 24 * time.Hour
)

// Limits of a unit reference.
const (
	maxToolLength = 100
	maxIDLength   = 100
)

// Unit names one unit: its namespace, its tool and its stable ID. The graph
// keeps the tool and the ID opaque. A rename of the unit keeps its ID, so the
// edges stay.
type Unit struct {
	Namespace namespace.Name
	Tool      string
	ID        string
}

// Validate reports whether u holds a valid namespace, tool and ID. It
// returns ErrInvalidUnit otherwise.
func (u Unit) Validate() error {
	if _, err := namespace.ParseName(u.Namespace.String()); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidUnit, err)
	}

	if !ident.Valid(u.Tool, maxToolLength) {
		return fmt.Errorf("%w: tool %q", ErrInvalidUnit, u.Tool)
	}

	if !ident.Valid(u.ID, maxIDLength) {
		return fmt.Errorf("%w: unit ID %q", ErrInvalidUnit, u.ID)
	}

	return nil
}

// String returns u as <tool>:<namespace>/<unit ID>. It is a key for logs and
// for the order of an answer, not the unit reference of a tool, which holds
// the tool's own name.
func (u Unit) String() string {
	return u.Tool + ":" + u.Namespace.String() + "/" + u.ID
}

// Direction is the way a walk follows an edge.
type Direction string

// The directions of a walk.
const (
	// Downstream follows an edge from its source to its dependent: the
	// units that read the outputs of a unit. A blast radius walks
	// downstream.
	Downstream Direction = "downstream"
	// Upstream follows an edge from its dependent to its source: the units
	// whose outputs a unit reads.
	Upstream Direction = "upstream"
)

// Valid reports whether d is a known direction.
func (d Direction) Valid() bool {
	return d == Downstream || d == Upstream
}

// Edge says that Dependent reads the outputs of Source. Kind and Evidence
// are the values that the dependency record holds, for example
// "output-read" and "job". The graph does not interpret them. Ambiguous is
// true for an inferred edge whose reader held more than one unit at the
// time.
type Edge struct {
	LastSeen  time.Time
	Source    Unit
	Dependent Unit
	Kind      string
	Evidence  string
	Ambiguous bool
}

// Facts are what the graph shows next to a unit. Name is the tool's own name
// of the unit. Collection is the name of the collection of the unit. Owners
// are the effective owners from the context of the unit, in order. Deleted is
// true for a unit in its recovery window.
type Facts struct {
	Name       string
	Collection string
	Owners     []string
	Deleted    bool
}

// Node is one unit in an answer, with its facts and its distance in hops
// from the start unit.
type Node struct {
	Facts Facts
	Unit  Unit
	Hops  int
}

// Completeness says how much of the graph an answer holds. Truncated is true
// when a limit cut the answer. Hidden counts the distinct units that the
// walk reached and that the caller cannot read; the walk did not pass
// through them. Since is the oldest last time seen of an edge that counts.
type Completeness struct {
	Since     time.Time
	Hidden    int
	Truncated bool
}

// Complete reports whether the answer holds every unit that the walk could
// reach within its limits.
func (c Completeness) Complete() bool {
	return !c.Truncated && c.Hidden == 0
}

// EdgeReader is the read port of the dependency edges. The output reads of
// each tool record the edges, and this port only reads them, so no edge is
// recorded twice. An adapter over the dependency store implements it.
type EdgeReader interface {
	// Edges returns the unit edges that touch units in dir: for Downstream
	// the edges whose source is in units, for Upstream the edges whose
	// dependent is in units. It returns only edges that name a dependent
	// unit and whose last time seen is at or after since. It returns at
	// most limit+1 edges, so that the caller sees that more exist. units
	// holds 1 to MaxBatch units. The order is the source, then the
	// dependent, then the kind and the evidence, by the String of each unit.
	// It returns ErrInvalidUnit for an invalid unit and ErrInvalidQuery for
	// an empty or too large batch, a limit below 1 or an unknown direction.
	// An edge whose other end is in another namespace is part of the answer.
	Edges(ctx context.Context, units []Unit, dir Direction, since time.Time, limit int) ([]Edge, error)
}

// UnitReader is the read port of the facts of units. The internal wiring
// implements it over the inventory, the collections and the context of
// units.
type UnitReader interface {
	// Describe returns the facts of each unit of units that exists, live or
	// in its recovery window. A purged unit or a unit that never existed
	// is not in the map, and is not an error. units holds at most MaxBatch
	// units. It returns ErrInvalidUnit for an invalid unit.
	Describe(ctx context.Context, units []Unit) (map[Unit]Facts, error)
}

// Access is the port of the access rules for one caller.
type Access interface {
	// Readable returns the units of units that the caller can read, with
	// the action unit:read. A unit not in the answer is hidden from the
	// caller. units holds at most MaxBatch units. The adapter filters in
	// the query, never with one check for each unit.
	Readable(ctx context.Context, units []Unit) (map[Unit]bool, error)
}

// Sources holds the ports that a query reads. Now gives the time of the
// query; nil means time.Now.
type Sources struct {
	Edges  EdgeReader
	Units  UnitReader
	Access Access
	Now    func() time.Time
}

func (s Sources) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}

	return s.Now()
}

// since returns the oldest last time seen that counts for a query with the
// window window. A zero window means DefaultWindow.
func (s Sources) since(window time.Duration) time.Time {
	if window <= 0 {
		window = DefaultWindow
	}

	return s.now().Add(-window)
}
