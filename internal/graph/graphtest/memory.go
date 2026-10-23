package graphtest

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/graph"
)

// MemoryEdges is a reference graph.EdgeReader that holds its edges in
// memory. It runs the suite in this package, and the tests of the graph
// queries read it.
type MemoryEdges []graph.Edge

// Edges implements graph.EdgeReader.
func (m MemoryEdges) Edges(
	_ context.Context, units []graph.Unit, dir graph.Direction, since time.Time, limit int,
) ([]graph.Edge, error) {
	switch {
	case len(units) == 0 || len(units) > graph.MaxBatch:
		return nil, fmt.Errorf("%w: batch of %d units", graph.ErrInvalidQuery, len(units))
	case limit < 1:
		return nil, fmt.Errorf("%w: limit %d", graph.ErrInvalidQuery, limit)
	case !dir.Valid():
		return nil, fmt.Errorf("%w: direction %q", graph.ErrInvalidQuery, dir)
	}

	in := map[graph.Unit]bool{}

	for _, u := range units {
		if err := u.Validate(); err != nil {
			return nil, err
		}

		in[u] = true
	}

	var out []graph.Edge

	for i := range m {
		end := m[i].Source
		if dir == graph.Upstream {
			end = m[i].Dependent
		}

		if in[end] && !m[i].LastSeen.Before(since) {
			out = append(out, m[i])
		}
	}

	slices.SortFunc(out, func(a, b graph.Edge) int {
		return cmp.Or(
			cmp.Compare(a.Source.String(), b.Source.String()),
			cmp.Compare(a.Dependent.String(), b.Dependent.String()),
			cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.Evidence, b.Evidence),
		)
	})

	if len(out) > limit+1 {
		out = out[:limit+1]
	}

	return out, nil
}
