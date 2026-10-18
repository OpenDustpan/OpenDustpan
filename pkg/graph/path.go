package graph

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// PathQuery is a search for a path between two units. MaxHops is the
// longest path, from 1 to MaxPathHops; 0 means MaxPathHops. Undirected
// follows edges in both directions. Else the path follows each edge from its
// source to its dependent, so it shows how the outputs of From reach To.
// Window is how far back an edge counts; 0 means DefaultWindow.
type PathQuery struct {
	From       Unit
	To         Unit
	MaxHops    int
	Window     time.Duration
	Undirected bool
}

// PathResult is the answer of a path search. When Found is true, Units holds
// the units of one shortest path from From to To, both included, and Edges
// holds the edge of each step. When two paths have the same length, the
// search takes the first edge in the order of the EdgeReader, so the same
// edges give the same answer. When Found is false and Complete is not complete,
// a path may exist through units that the caller cannot read, or beyond a
// limit.
type PathResult struct {
	Units    []Node
	Edges    []Edge
	Complete Completeness
	Found    bool
}

// Path searches for a shortest path between q.From and q.To, breadth first.
// It never passes through a unit that the caller cannot read. It reads at
// most MaxNodes units and MaxEdgesPerHop edges in each hop.
//
// It returns ErrInvalidUnit, ErrInvalidQuery for the same unit at both ends
// or a limit out of range, ErrNotFound when the caller cannot read q.From or
// q.To or one of them does not exist, and the errors of the ports.
func Path(ctx context.Context, s Sources, q PathQuery) (PathResult, error) {
	var res PathResult

	q, err := q.normalize()
	if err != nil {
		return res, err
	}

	from, err := s.startNode(ctx, q.From)
	if err != nil {
		return res, err
	}

	if _, err = s.startNode(ctx, q.To); err != nil {
		return res, err
	}

	res.Complete.Since = s.since(q.Window)
	p := &pathSearch{
		walker: newWalker(s, res.Complete.Since),
		nodes:  map[Unit]Node{q.From: from},
		parent: map[Unit]Edge{},
		c:      &res.Complete,
	}
	p.seen[q.From] = true

	frontier := []Unit{q.From}

	for hop := 1; hop <= q.MaxHops && len(frontier) > 0; hop++ {
		frontier, err = p.step(ctx, frontier, q.directions())
		if err != nil {
			return PathResult{}, err
		}

		if _, ok := p.nodes[q.To]; ok {
			res.Found = true

			break
		}

		if res.Complete.Truncated {
			break
		}
	}

	res.Complete.Hidden = len(p.hidden)

	if res.Found {
		res.Units, res.Edges = trace(q.From, q.To, p.nodes, p.parent)
	}

	return res, nil
}

func (q PathQuery) normalize() (PathQuery, error) {
	if q.MaxHops == 0 {
		q.MaxHops = MaxPathHops
	}

	switch {
	case q.MaxHops < 1 || q.MaxHops > MaxPathHops:
		return q, fmt.Errorf("%w: hops must be from 1 to %d", ErrInvalidQuery, MaxPathHops)
	case q.Window < 0:
		return q, fmt.Errorf("%w: the window must not be negative", ErrInvalidQuery)
	case q.From == q.To:
		return q, fmt.Errorf("%w: a path needs two units", ErrInvalidQuery)
	}

	return q, nil
}

// directions returns the directions that the search follows: downstream,
// and also upstream when q is undirected.
func (q PathQuery) directions() []Direction {
	if q.Undirected {
		return []Direction{Downstream, Upstream}
	}

	return []Direction{Downstream}
}

// pathSearch holds the state of one path search: the walk, the node and the
// parent edge of each unit that the search reached, and the completeness of
// the answer.
type pathSearch struct {
	*walker

	nodes  map[Unit]Node
	parent map[Unit]Edge
	c      *Completeness
}

// step reads one hop of a path search in dirs and records the parent edge of
// each new unit.
func (p *pathSearch) step(ctx context.Context, frontier []Unit, dirs []Direction) ([]Unit, error) {
	var found []Unit

	via := map[Unit]Edge{}

	for _, dir := range dirs {
		edges, truncated, err := p.read(ctx, frontier, dir)
		if err != nil {
			return nil, err
		}

		p.c.Truncated = p.c.Truncated || truncated

		for i := range edges {
			other := far(&edges[i], dir)
			if !p.seen[other] {
				p.seen[other] = true
				found = append(found, other)
				via[other] = edges[i]
			}
		}
	}

	visible, err := p.visible(ctx, found)
	if err != nil {
		return nil, err
	}

	var next []Unit

	for _, n := range visible {
		if len(p.nodes) > MaxNodes {
			p.c.Truncated = true

			break
		}

		p.nodes[n.Unit] = n
		p.parent[n.Unit] = via[n.Unit]
		next = append(next, n.Unit)
	}

	return next, nil
}

// trace walks the parent edges back from to and returns the path in order.
func trace(from, to Unit, nodes map[Unit]Node, parent map[Unit]Edge) ([]Node, []Edge) {
	units := []Node{nodes[to]}

	var edges []Edge

	for u := to; u != from; {
		e := parent[u]
		edges = append(edges, e)

		if e.Dependent == u {
			u = e.Source
		} else {
			u = e.Dependent
		}

		units = append(units, nodes[u])
	}

	slices.Reverse(units)
	slices.Reverse(edges)

	for i := range units {
		units[i].Hops = i
	}

	return units, edges
}
