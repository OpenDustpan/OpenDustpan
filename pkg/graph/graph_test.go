package graph_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/graph/graphtest"
	"opendustpan.org/opendustpan/pkg/graph"
)

//nolint:gochecknoglobals // A fixed test time that the fixture and the tests read.
var now = time.UnixMilli(1_800_000_000_000).UTC()

// world is a fixture graph:
//
//	net: n1
//	app: a1, a2, a3 (hidden)
//	data: d1, d2, d3
//
//	n1 -> a1, n1 -> a2, n1 -> a3, a1 -> d1, a2 -> d1, d1 -> d2, a3 -> d3
type world struct {
	collection map[graph.Unit]string
	hidden     map[graph.Unit]bool
	edges      graphtest.MemoryEdges
}

//nolint:gochecknoglobals // The fixed units of the fixture graph that the tests read.
var (
	n1 = graphtest.Unit("acme", "n1")
	a1 = graphtest.Unit("acme", "a1")
	a2 = graphtest.Unit("acme", "a2")
	a3 = graphtest.Unit("acme", "a3")
	d1 = graphtest.Unit("acme", "d1")
	d2 = graphtest.Unit("acme", "d2")
	d3 = graphtest.Unit("acme", "d3")
)

func newWorld() *world {
	return &world{
		collection: map[graph.Unit]string{
			n1: "net", a1: "app", a2: "app", a3: "app", d1: "data", d2: "data", d3: "data",
		},
		hidden: map[graph.Unit]bool{a3: true},
		edges: graphtest.MemoryEdges{
			graphtest.Edge(n1, a1, time.Hour), graphtest.Edge(n1, a2, 2*time.Hour),
			graphtest.Edge(n1, a3, time.Hour), graphtest.Edge(a1, d1, time.Hour),
			graphtest.Edge(a2, d1, time.Hour), graphtest.Edge(d1, d2, time.Hour),
			graphtest.Edge(a3, d3, time.Hour),
		},
	}
}

func (w *world) Describe(_ context.Context, units []graph.Unit) (map[graph.Unit]graph.Facts, error) {
	out := map[graph.Unit]graph.Facts{}

	for _, u := range units {
		if c, ok := w.collection[u]; ok {
			out[u] = graph.Facts{Name: u.ID, Collection: c, Owners: nil, Deleted: false}
		}
	}

	return out, nil
}

func (w *world) Readable(_ context.Context, units []graph.Unit) (map[graph.Unit]bool, error) {
	out := map[graph.Unit]bool{}

	for _, u := range units {
		if !w.hidden[u] {
			out[u] = true
		}
	}

	return out, nil
}

func (w *world) sources() graph.Sources {
	return graph.Sources{Edges: w.edges, Units: w, Access: w, Now: func() time.Time { return now }}
}

func ids(nodes []graph.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Unit.ID)
	}

	return out
}

type walkCase struct {
	name      string
	start     graph.Unit
	query     graph.WalkQuery
	want      []string
	hops      []int
	edges     int
	hidden    int
	truncated bool
}

func (tc *walkCase) check(t *testing.T, res *graph.WalkResult) {
	t.Helper()

	if got := ids(res.Nodes); !slices.Equal(got, tc.want) {
		t.Errorf("nodes %v, want %v", got, tc.want)
	}

	hops := make([]int, 0, len(res.Nodes))
	for _, n := range res.Nodes {
		hops = append(hops, n.Hops)
	}

	if !slices.Equal(hops, tc.hops) {
		t.Errorf("hops %v, want %v", hops, tc.hops)
	}

	if len(res.Edges) != tc.edges {
		t.Errorf("edges %d, want %d", len(res.Edges), tc.edges)
	}

	if res.Complete.Hidden != tc.hidden || res.Complete.Truncated != tc.truncated {
		t.Errorf("completeness %+v, want hidden %d, truncated %v", res.Complete, tc.hidden, tc.truncated)
	}
}

func TestWalk(t *testing.T) {
	t.Parallel()

	cases := []walkCase{
		{
			name: "dependents", start: n1,
			query: graph.WalkQuery{Direction: graph.Downstream, Hops: 1, MaxNodes: 0, Window: 0},
			want:  []string{"a1", "a2"}, hops: []int{1, 1}, edges: 2, hidden: 1,
			truncated: false,
		},
		{
			name: "blast radius never passes a hidden unit", start: n1,
			query: graph.WalkQuery{Direction: graph.Downstream, Hops: 5, MaxNodes: 0, Window: 0},
			want:  []string{"a1", "a2", "d1", "d2"}, hops: []int{1, 1, 2, 3}, edges: 5, hidden: 1,
			truncated: false,
		},
		{
			name: "node limit truncates", start: n1,
			query: graph.WalkQuery{Direction: graph.Downstream, Hops: 3, MaxNodes: 2, Window: 0},
			want:  []string{"a1", "a2"}, hops: []int{1, 1}, edges: 2, hidden: 1, truncated: true,
		},
		{
			name: "upstream", start: d2,
			query: graph.WalkQuery{Direction: graph.Upstream, Hops: 2, MaxNodes: 0, Window: 0},
			want:  []string{"d1", "a1", "a2"}, hops: []int{1, 2, 2}, edges: 3,
			hidden:    0,
			truncated: false,
		},
		{
			name: "edges outside the window do not count", start: n1,
			query: graph.WalkQuery{Direction: graph.Downstream, Hops: 1, Window: 90 * time.Minute, MaxNodes: 0},
			want:  []string{"a1"}, hops: []int{1}, edges: 1, hidden: 1,
			truncated: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := graph.Walk(t.Context(), newWorld().sources(), tc.start, tc.query)
			if err != nil {
				t.Fatalf("Walk: %v", err)
			}

			tc.check(t, &res)
		})
	}
}

func TestWalkErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want  error
		name  string
		start graph.Unit
		query graph.WalkQuery
	}{
		{
			graph.ErrNotFound,
			"hidden start",
			a3,
			graph.WalkQuery{Direction: graph.Downstream, Hops: 0, MaxNodes: 0, Window: 0},
		},
		{
			graph.ErrNotFound,
			"unknown start",
			graphtest.Unit("acme", "zz"),
			graph.WalkQuery{Direction: graph.Downstream, Hops: 0, MaxNodes: 0, Window: 0},
		},
		{
			graph.ErrInvalidUnit,
			"invalid start",
			graph.Unit{Namespace: "acme", Tool: "", ID: ""},
			graph.WalkQuery{Direction: graph.Downstream, Hops: 0, MaxNodes: 0, Window: 0},
		},
		{
			graph.ErrInvalidQuery,
			"too many hops",
			n1,
			graph.WalkQuery{Direction: graph.Downstream, Hops: graph.MaxHops + 1, MaxNodes: 0, Window: 0},
		},
		{
			graph.ErrInvalidQuery,
			"too many nodes",
			n1,
			graph.WalkQuery{Direction: graph.Downstream, MaxNodes: graph.MaxNodes + 1, Hops: 0, Window: 0},
		},
		{graph.ErrInvalidQuery, "no direction", n1, graph.WalkQuery{Direction: "", Hops: 0, MaxNodes: 0, Window: 0}},
	}

	for _, tc := range cases {
		_, err := graph.Walk(t.Context(), newWorld().sources(), tc.start, tc.query)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: error %v, want %v", tc.name, err, tc.want)
		}
	}
}

type pathCase struct {
	name   string
	query  graph.PathQuery
	want   []string
	found  bool
	hidden bool
}

func (tc *pathCase) check(t *testing.T, res *graph.PathResult) {
	t.Helper()

	if res.Found != tc.found || !slices.Equal(ids(res.Units), tc.want) {
		t.Errorf("path %v found %v, want %v found %v", ids(res.Units), res.Found, tc.want, tc.found)
	}

	if res.Found && len(res.Edges) != len(res.Units)-1 {
		t.Errorf("edges %d for %d units", len(res.Edges), len(res.Units))
	}

	if tc.hidden && res.Complete.Hidden == 0 {
		t.Errorf("completeness %+v, want hidden units counted", res.Complete)
	}
}

func TestPath(t *testing.T) {
	t.Parallel()

	cases := []pathCase{
		{
			"follows outputs",
			graph.PathQuery{From: n1, To: d2, MaxHops: 0, Window: 0, Undirected: false},
			[]string{"n1", "a1", "d1", "d2"},
			true,
			false,
		},
		{
			"directed has no way back",
			graph.PathQuery{From: d2, To: n1, MaxHops: 0, Window: 0, Undirected: false},
			nil,
			false,
			false,
		},
		{
			"undirected finds a way back",
			graph.PathQuery{From: d2, To: n1, Undirected: true, MaxHops: 0, Window: 0},
			[]string{"d2", "d1", "a1", "n1"},
			true,
			false,
		},
		{"too short", graph.PathQuery{From: n1, To: d2, MaxHops: 2, Window: 0, Undirected: false}, nil, false, true},
		{
			"not through a hidden unit",
			graph.PathQuery{From: n1, To: d3, MaxHops: 0, Window: 0, Undirected: false},
			nil,
			false,
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := graph.Path(t.Context(), newWorld().sources(), tc.query)
			if err != nil {
				t.Fatalf("Path: %v", err)
			}

			tc.check(t, &res)
		})
	}
}

func TestPathErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want  error
		name  string
		query graph.PathQuery
	}{
		{
			graph.ErrInvalidQuery,
			"same unit",
			graph.PathQuery{From: n1, To: n1, MaxHops: 0, Window: 0, Undirected: false},
		},
		{
			graph.ErrInvalidQuery,
			"too long",
			graph.PathQuery{From: n1, To: d1, MaxHops: graph.MaxPathHops + 1, Window: 0, Undirected: false},
		},
		{graph.ErrNotFound, "hidden end", graph.PathQuery{From: n1, To: a3, MaxHops: 0, Window: 0, Undirected: false}},
	}

	for _, tc := range cases {
		_, err := graph.Path(t.Context(), newWorld().sources(), tc.query)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: error %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestFocus(t *testing.T) {
	t.Parallel()

	v, err := graph.Focus(t.Context(), newWorld().sources(), graph.FocusQuery{Unit: a1, MaxNeighbors: 0, Window: 0})
	if err != nil {
		t.Fatalf("Focus: %v", err)
	}

	if got := ids(v.Upstream); !slices.Equal(got, []string{"n1"}) {
		t.Errorf("upstream %v, want [n1]", got)
	}

	if got := ids(v.Downstream); !slices.Equal(got, []string{"d1"}) {
		t.Errorf("downstream %v, want [d1]", got)
	}

	want := []graph.Fold{
		{Namespace: "acme", Collection: "app", Units: 1, Links: []graph.FoldLink{
			{Unit: d1, Direction: graph.Upstream, Edges: 1},
			{Unit: n1, Direction: graph.Downstream, Edges: 1},
		}},
		{Namespace: "acme", Collection: "data", Units: 1, Links: []graph.FoldLink{
			{Unit: d1, Direction: graph.Downstream, Edges: 1},
		}},
	}

	if !foldsEqual(v.Folds, want) {
		t.Errorf("folds %+v, want %+v", v.Folds, want)
	}

	if len(v.Edges) != 2 {
		t.Errorf("edges %d, want the 2 edges between shown units", len(v.Edges))
	}

	if v.Complete.Hidden != 1 {
		t.Errorf("hidden %d, want 1", v.Complete.Hidden)
	}
}

func TestFocusFoldsOverflow(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.hidden = nil

	v, err := graph.Focus(t.Context(), w.sources(), graph.FocusQuery{Unit: n1, MaxNeighbors: 2, Window: 0})
	if err != nil {
		t.Fatalf("Focus: %v", err)
	}

	if got := ids(v.Downstream); !slices.Equal(got, []string{"a1", "a3"}) {
		t.Errorf("downstream %v, want the 2 newest neighbors [a1 a3]", got)
	}

	app := slices.IndexFunc(v.Folds, func(f graph.Fold) bool { return f.Collection == "app" })
	if app < 0 || v.Folds[app].Units != 1 {
		t.Fatalf("folds %+v, want a2 folded into app", v.Folds)
	}
}

func TestFocusErrors(t *testing.T) {
	t.Parallel()

	tooMany := graph.FocusQuery{Unit: a1, MaxNeighbors: graph.MaxFocusNeighbors + 1, Window: 0}

	_, err := graph.Focus(t.Context(), newWorld().sources(), tooMany)
	if !errors.Is(err, graph.ErrInvalidQuery) {
		t.Errorf("error %v, want ErrInvalidQuery", err)
	}

	hidden := graph.FocusQuery{Unit: a3, MaxNeighbors: 0, Window: 0}

	_, err = graph.Focus(t.Context(), newWorld().sources(), hidden)
	if !errors.Is(err, graph.ErrNotFound) {
		t.Errorf("error %v, want ErrNotFound", err)
	}
}

func foldsEqual(a, b []graph.Fold) bool {
	return slices.EqualFunc(a, b, func(x, y graph.Fold) bool {
		return x.Namespace == y.Namespace && x.Collection == y.Collection && x.Units == y.Units &&
			slices.Equal(x.Links, y.Links)
	})
}

func TestUnitValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		unit graph.Unit
		ok   bool
	}{
		{graphtest.Unit("acme", "stk-1"), true},
		{graph.Unit{Namespace: "acme", Tool: "pulumi", ID: ""}, false},
		{graph.Unit{Namespace: "acme", ID: "x", Tool: ""}, false},
		{graph.Unit{Namespace: "", Tool: "pulumi", ID: "x"}, false},
	}

	for _, tc := range cases {
		if err := tc.unit.Validate(); (err == nil) != tc.ok {
			t.Errorf("Validate(%+v) = %v, want ok %v", tc.unit, err, tc.ok)
		}
	}
}
