// Package graphtest is the conformance suite of the graph ports. Each adapter
// of graph.EdgeReader runs RunEdgeReader.
package graphtest

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// NewEdgeReader returns an adapter that holds edges and nothing else. The
// harness of a store adapter writes them through the port that records
// dependency edges, so the suite also checks that the two ports agree.
type NewEdgeReader func(t *testing.T, edges []graph.Edge) graph.EdgeReader

// base returns the time of the fixtures. Each edge is seen at or before it.
func base() time.Time {
	return time.UnixMilli(1_800_000_000_000).UTC()
}

// Unit returns a valid unit of tool "pulumi" in namespace ns with ID id.
func Unit(ns, id string) graph.Unit {
	return graph.Unit{Namespace: namespace.Name(ns), Tool: "pulumi", ID: id}
}

// Edge returns an edge from src to dep, last seen age before the base time.
func Edge(src, dep graph.Unit, age time.Duration) graph.Edge {
	return graph.Edge{
		Source: src, Dependent: dep, Kind: "output-read", Evidence: "job",
		LastSeen:  base().Add(-age),
		Ambiguous: false,
	}
}

// RunEdgeReader runs the conformance suite of graph.EdgeReader.
func RunEdgeReader(t *testing.T, newReader NewEdgeReader) {
	t.Helper()

	t.Run("Directions", func(t *testing.T) { t.Parallel(); testDirections(t, newReader) })
	t.Run("Window", func(t *testing.T) { t.Parallel(); testWindow(t, newReader) })
	t.Run("LimitPlusOne", func(t *testing.T) { t.Parallel(); testLimit(t, newReader) })
	t.Run("AcrossNamespaces", func(t *testing.T) { t.Parallel(); testAcross(t, newReader) })
	t.Run("InvalidCalls", func(t *testing.T) { t.Parallel(); testInvalid(t, newReader) })
}

func testDirections(t *testing.T, newReader NewEdgeReader) {
	t.Helper()

	a, b, c := Unit("acme", "a"), Unit("acme", "b"), Unit("acme", "c")
	r := newReader(t, []graph.Edge{Edge(a, b, time.Hour), Edge(b, c, time.Hour), Edge(a, c, time.Hour)})

	cases := []struct {
		name  string
		dir   graph.Direction
		units []graph.Unit
		want  [][2]graph.Unit
	}{
		{"downstream of a", graph.Downstream, []graph.Unit{a}, [][2]graph.Unit{{a, b}, {a, c}}},
		{"upstream of c", graph.Upstream, []graph.Unit{c}, [][2]graph.Unit{{a, c}, {b, c}}},
		{"downstream of a batch", graph.Downstream, []graph.Unit{b, a}, [][2]graph.Unit{{a, b}, {a, c}, {b, c}}},
		{"downstream of a leaf", graph.Downstream, []graph.Unit{c}, nil},
	}

	for _, tc := range cases {
		got, err := r.Edges(t.Context(), tc.units, tc.dir, base().Add(-24*time.Hour), 100)
		if err != nil {
			t.Fatalf("%s: Edges: %v", tc.name, err)
		}

		if pairs := pairsOf(got); !slices.Equal(pairs, tc.want) {
			t.Errorf("%s: edges %v, want %v", tc.name, pairs, tc.want)
		}
	}
}

func testWindow(t *testing.T, newReader NewEdgeReader) {
	t.Helper()

	a, b, c := Unit("acme", "a"), Unit("acme", "b"), Unit("acme", "c")
	r := newReader(t, []graph.Edge{Edge(a, b, time.Hour), Edge(a, c, 100*24*time.Hour)})

	got, err := r.Edges(t.Context(), []graph.Unit{a}, graph.Downstream, base().Add(-graph.DefaultWindow), 100)
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}

	if pairs := pairsOf(got); !slices.Equal(pairs, [][2]graph.Unit{{a, b}}) {
		t.Errorf("edges %v, want only the edge seen within the window", pairs)
	}
}

func testLimit(t *testing.T, newReader NewEdgeReader) {
	t.Helper()

	src := Unit("acme", "src")

	edges := make([]graph.Edge, 0, 5)
	for i := range 5 {
		edges = append(edges, Edge(src, Unit("acme", fmt.Sprintf("d%d", i)), time.Hour))
	}

	r := newReader(t, edges)

	got, err := r.Edges(t.Context(), []graph.Unit{src}, graph.Downstream, base().Add(-time.Hour*24), 2)
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}

	if len(got) != 3 {
		t.Errorf("edges %d, want the limit plus one (3)", len(got))
	}
}

func testAcross(t *testing.T, newReader NewEdgeReader) {
	t.Helper()

	a, b := Unit("acme", "a"), Unit("other", "b")
	r := newReader(t, []graph.Edge{Edge(a, b, time.Hour)})

	got, err := r.Edges(t.Context(), []graph.Unit{b}, graph.Upstream, base().Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}

	if pairs := pairsOf(got); !slices.Equal(pairs, [][2]graph.Unit{{a, b}}) {
		t.Errorf("edges %v, want the edge from another namespace", pairs)
	}
}

func testInvalid(t *testing.T, newReader NewEdgeReader) {
	t.Helper()

	r := newReader(t, nil)
	ok := []graph.Unit{Unit("acme", "a")}
	tooMany := make([]graph.Unit, graph.MaxBatch+1)

	for i := range tooMany {
		tooMany[i] = Unit("acme", fmt.Sprintf("u%d", i))
	}

	cases := []struct {
		want  error
		name  string
		dir   graph.Direction
		units []graph.Unit
		limit int
	}{
		{graph.ErrInvalidQuery, "empty batch", graph.Downstream, nil, 10},
		{graph.ErrInvalidQuery, "batch too large", graph.Downstream, tooMany, 10},
		{graph.ErrInvalidQuery, "limit zero", graph.Downstream, ok, 0},
		{graph.ErrInvalidQuery, "unknown direction", graph.Direction("sideways"), ok, 10},
		{
			graph.ErrInvalidUnit,
			"invalid unit",
			graph.Downstream,
			[]graph.Unit{{Namespace: "acme", Tool: "pulumi", ID: ""}},
			10,
		},
	}

	for _, tc := range cases {
		_, err := r.Edges(t.Context(), tc.units, tc.dir, base(), tc.limit)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: error %v, want %v", tc.name, err, tc.want)
		}
	}
}

func pairsOf(edges []graph.Edge) [][2]graph.Unit {
	out := make([][2]graph.Unit, 0, len(edges))
	for i := range edges {
		out = append(out, [2]graph.Unit{edges[i].Source, edges[i].Dependent})
	}

	return out
}
