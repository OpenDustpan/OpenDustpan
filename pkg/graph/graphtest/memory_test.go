package graphtest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/graph/graphtest"
)

func TestMemoryEdges(t *testing.T) {
	t.Parallel()

	graphtest.RunEdgeReader(t, func(_ *testing.T, edges []graph.Edge) graph.EdgeReader {
		return graphtest.MemoryEdges(edges)
	})
}
