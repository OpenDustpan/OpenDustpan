package graph

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"
)

// WalkQuery is a walk from one unit. Hops is the depth, from 1 to MaxHops; 0
// means DefaultHops. MaxNodes bounds the units of the answer, from 1 to
// MaxNodes; 0 means DefaultNodes. Window is how far back an edge counts; 0
// means DefaultWindow.
type WalkQuery struct {
	Direction Direction
	Hops      int
	MaxNodes  int
	Window    time.Duration
}

func (q WalkQuery) normalize() (WalkQuery, error) {
	if !q.Direction.Valid() {
		return q, fmt.Errorf("%w: direction %q", ErrInvalidQuery, q.Direction)
	}

	if q.Hops == 0 {
		q.Hops = DefaultHops
	}

	if q.MaxNodes == 0 {
		q.MaxNodes = DefaultNodes
	}

	switch {
	case q.Hops < 1 || q.Hops > MaxHops:
		return q, fmt.Errorf("%w: hops must be from 1 to %d", ErrInvalidQuery, MaxHops)
	case q.MaxNodes < 1 || q.MaxNodes > MaxNodes:
		return q, fmt.Errorf("%w: the node limit must be from 1 to %d", ErrInvalidQuery, MaxNodes)
	case q.Window < 0:
		return q, fmt.Errorf("%w: the window must not be negative", ErrInvalidQuery)
	}

	return q, nil
}

// WalkResult is the answer of a walk. Nodes holds the units that the walk
// reached, the start unit excluded, ordered by hops and then by unit. Edges
// holds each edge between two units of the answer, the start unit included.
type WalkResult struct {
	Start    Node
	Nodes    []Node
	Edges    []Edge
	Complete Completeness
}

// Dependents returns the units that read the outputs of start, one hop
// downstream.
func Dependents(ctx context.Context, s Sources, start Unit) (WalkResult, error) {
	return Walk(ctx, s, start, WalkQuery{Direction: Downstream, Hops: 1, MaxNodes: MaxNodes, Window: 0})
}

// BlastRadius returns the units that a change of start can reach within
// hops: the walk downstream.
func BlastRadius(ctx context.Context, s Sources, start Unit, hops, maxNodes int) (WalkResult, error) {
	return Walk(ctx, s, start, WalkQuery{Direction: Downstream, Hops: hops, MaxNodes: maxNodes, Window: 0})
}

// Walk follows the edges of q.Direction from start, breadth first, hop by
// hop. It never passes through a unit that the caller cannot read, and counts
// it in Completeness.Hidden. A unit that the facts port does not know, for
// example a purged unit of another namespace, is not in the answer and is not
// counted. When a hop reads more than MaxEdgesPerHop edges, or the answer
// reaches q.MaxNodes, the walk stops and marks the answer as truncated.
//
// It returns ErrInvalidUnit, ErrInvalidQuery, ErrNotFound when the caller
// cannot read start or start does not exist, and the errors of the ports.
func Walk(ctx context.Context, s Sources, start Unit, q WalkQuery) (WalkResult, error) {
	var res WalkResult

	q, err := q.normalize()
	if err != nil {
		return res, err
	}

	startNode, err := s.startNode(ctx, start)
	if err != nil {
		return res, err
	}

	res.Start = startNode
	res.Complete.Since = s.since(q.Window)

	w := newWalker(s, res.Complete.Since)
	w.seen[start] = true
	frontier := []Unit{start}

	for hop := 1; hop <= q.Hops && len(frontier) > 0 && !res.Complete.Truncated; hop++ {
		frontier, err = w.step(ctx, frontier, q, hop, &res)
		if err != nil {
			return WalkResult{}, err
		}
	}

	res.Edges = edgesWithin(w.edges, w.inAnswer(start))
	res.Complete.Hidden = len(w.hidden)

	return res, nil
}

// walker holds the state of one walk.
type walker struct {
	seen   map[Unit]bool
	hidden map[Unit]bool
	shown  map[Unit]bool
	since  time.Time
	s      Sources
	edges  []Edge
}

func newWalker(s Sources, since time.Time) *walker {
	return &walker{
		s:      s,
		since:  since,
		seen:   map[Unit]bool{},
		hidden: map[Unit]bool{},
		shown:  map[Unit]bool{},
		edges:  nil,
	}
}

// step reads one hop from frontier in q.Direction, adds the readable units
// that it reaches to res, up to q.MaxNodes, and returns the next frontier.
func (w *walker) step(ctx context.Context, frontier []Unit, q WalkQuery, hop int, res *WalkResult) ([]Unit, error) {
	edges, truncated, err := w.read(ctx, frontier, q.Direction)
	if err != nil {
		return nil, err
	}

	if truncated {
		res.Complete.Truncated = true
	}

	w.edges = append(w.edges, edges...)

	var candidates []Unit

	for i := range edges {
		other := far(&edges[i], q.Direction)
		if !w.seen[other] {
			w.seen[other] = true
			candidates = append(candidates, other)
		}
	}

	visible, err := w.visible(ctx, candidates)
	if err != nil {
		return nil, err
	}

	var next []Unit

	for _, n := range visible {
		if len(res.Nodes) >= q.MaxNodes {
			res.Complete.Truncated = true

			break
		}

		n.Hops = hop
		res.Nodes = append(res.Nodes, n)
		w.shown[n.Unit] = true
		next = append(next, n.Unit)
	}

	return next, nil
}

// read reads the edges of dir from units, in batches of MaxBatch, and cuts
// them at MaxEdgesPerHop.
func (w *walker) read(ctx context.Context, units []Unit, dir Direction) ([]Edge, bool, error) {
	var out []Edge

	for batch := range slices.Chunk(units, MaxBatch) {
		limit := MaxEdgesPerHop - len(out)

		got, err := w.s.Edges.Edges(ctx, batch, dir, w.since, limit)
		if err != nil {
			return nil, false, fmt.Errorf("read edges: %w", err)
		}

		if len(got) > limit {
			return append(out, got[:limit]...), true, nil
		}

		out = append(out, got...)
	}

	return out, false, nil
}

// visible returns the nodes of the units that the caller can read and that
// exist, ordered by unit. It records the other readable-check failures as
// hidden.
func (w *walker) visible(ctx context.Context, units []Unit) ([]Node, error) {
	var out []Node

	for batch := range slices.Chunk(sortedUnits(units), MaxBatch) {
		readable, err := w.s.Access.Readable(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("check access: %w", err)
		}

		allowed := w.allowed(batch, readable)
		if len(allowed) == 0 {
			continue
		}

		facts, err := w.s.Units.Describe(ctx, allowed)
		if err != nil {
			return nil, fmt.Errorf("describe units: %w", err)
		}

		for _, u := range allowed {
			if f, ok := facts[u]; ok {
				out = append(out, Node{Unit: u, Facts: f, Hops: 0})
			}
		}
	}

	return out, nil
}

// allowed returns the units of batch that readable holds, in order. It
// records the other units as hidden.
func (w *walker) allowed(batch []Unit, readable map[Unit]bool) []Unit {
	out := make([]Unit, 0, len(batch))

	for _, u := range batch {
		if readable[u] {
			out = append(out, u)
		} else {
			w.hidden[u] = true
		}
	}

	return out
}

func (w *walker) inAnswer(start Unit) func(Unit) bool {
	return func(u Unit) bool { return u == start || w.shown[u] }
}

// startNode checks start and returns its node. A start that the caller
// cannot read, or that does not exist, is ErrNotFound.
func (s Sources) startNode(ctx context.Context, start Unit) (Node, error) {
	if err := start.Validate(); err != nil {
		return Node{}, err
	}

	readable, err := s.Access.Readable(ctx, []Unit{start})
	if err != nil {
		return Node{}, fmt.Errorf("check access: %w", err)
	}

	if !readable[start] {
		return Node{}, ErrNotFound
	}

	facts, err := s.Units.Describe(ctx, []Unit{start})
	if err != nil {
		return Node{}, fmt.Errorf("describe units: %w", err)
	}

	f, ok := facts[start]
	if !ok {
		return Node{}, ErrNotFound
	}

	return Node{Unit: start, Facts: f, Hops: 0}, nil
}

// far returns the end of e that a walk in dir reaches.
func far(e *Edge, dir Direction) Unit {
	if dir == Downstream {
		return e.Dependent
	}

	return e.Source
}

// edgesWithin returns the distinct edges whose two ends satisfy in, ordered
// by source, dependent, kind and evidence.
func edgesWithin(edges []Edge, in func(Unit) bool) []Edge {
	out := slices.DeleteFunc(slices.Clone(edges), func(e Edge) bool {
		return !in(e.Source) || !in(e.Dependent)
	})

	slices.SortFunc(out, compareEdges)

	return slices.CompactFunc(out, func(a, b Edge) bool { return compareEdges(a, b) == 0 })
}

func compareEdges(a, b Edge) int {
	return cmp.Or(
		cmp.Compare(a.Source.String(), b.Source.String()),
		cmp.Compare(a.Dependent.String(), b.Dependent.String()),
		cmp.Compare(a.Kind, b.Kind),
		cmp.Compare(a.Evidence, b.Evidence),
	)
}

func sortedUnits(units []Unit) []Unit {
	out := slices.Clone(units)
	slices.SortFunc(out, func(a, b Unit) int { return cmp.Compare(a.String(), b.String()) })

	return slices.Compact(out)
}
