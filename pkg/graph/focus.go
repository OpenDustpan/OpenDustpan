package graph

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// FocusQuery is the focus view of one unit. MaxNeighbors is the most
// neighbors that the view shows in each direction, from 1 to
// MaxFocusNeighbors; 0 means DefaultFocusNeighbors. Window is how far back
// an edge counts; 0 means DefaultWindow.
type FocusQuery struct {
	Unit         Unit
	MaxNeighbors int
	Window       time.Duration
}

// Fold is a collection that stands for units that a focus view does not show
// one by one. Units counts the distinct readable units that the fold holds.
// Links counts, for each shown unit, the edges between that unit and the
// units of the fold.
type Fold struct {
	Namespace  namespace.Name
	Collection string
	Links      []FoldLink
	Units      int
}

// FoldLink is the edges between one shown unit and the units of a fold.
// Direction is Downstream when the units of the fold read the outputs of
// Unit, and Upstream when Unit reads theirs.
type FoldLink struct {
	Unit      Unit
	Direction Direction
	Edges     int
}

// FocusView is a readable picture of the graph around one unit: the unit,
// its neighbors one hop upstream and one hop downstream, and the units one
// hop further folded into their collections. Edges holds each edge between
// two shown units. A whole graph of more than about 100 units is not
// readable, so the console shows this view.
type FocusView struct {
	Focus      Node
	Upstream   []Node
	Downstream []Node
	Folds      []Fold
	Edges      []Edge
	Complete   Completeness
}

// Focus returns the focus view of q.Unit. The rules:
//
//   - The view shows the readable neighbors one hop upstream and one hop
//     downstream, each ordered by the last time seen of their edge, newest
//     first, then by unit. Above q.MaxNeighbors in a direction, the other
//     neighbors fold into their collections.
//   - The readable units one hop beyond a shown neighbor fold into their
//     collections. The view reads no further.
//   - A unit that is both upstream and downstream shows in both lists.
//   - Units that the caller cannot read count in Completeness.Hidden and are
//     in no fold.
//
// It returns ErrInvalidUnit, ErrInvalidQuery, ErrNotFound and the errors of
// the ports.
func Focus(ctx context.Context, s Sources, q FocusQuery) (FocusView, error) {
	var v FocusView

	if q.MaxNeighbors == 0 {
		q.MaxNeighbors = DefaultFocusNeighbors
	}

	switch {
	case q.MaxNeighbors < 1 || q.MaxNeighbors > MaxFocusNeighbors:
		return v, fmt.Errorf("%w: neighbors must be from 1 to %d", ErrInvalidQuery, MaxFocusNeighbors)
	case q.Window < 0:
		return v, fmt.Errorf("%w: the window must not be negative", ErrInvalidQuery)
	}

	focus, err := s.startNode(ctx, q.Unit)
	if err != nil {
		return v, err
	}

	v.Focus = focus
	v.Complete.Since = s.since(q.Window)
	f := &focuser{walker: newWalker(s, v.Complete.Since), folds: newFolder(), c: &v.Complete}
	f.seen[q.Unit] = true

	for _, dir := range []Direction{Upstream, Downstream} {
		shown, err := f.neighbors(ctx, q.Unit, dir, q.MaxNeighbors)
		if err != nil {
			return FocusView{}, err
		}

		if dir == Upstream {
			v.Upstream = shown
		} else {
			v.Downstream = shown
		}
	}

	if err := f.foldBeyond(ctx, q.Unit, slices.Concat(v.Upstream, v.Downstream)); err != nil {
		return FocusView{}, err
	}

	v.Edges = edgesWithin(f.edges, f.inAnswer(q.Unit))
	v.Folds = f.folds.list()
	v.Complete.Hidden = len(f.hidden)

	return v, nil
}

// focuser holds the state of one focus view: the walk, the folds and the
// completeness of the view.
type focuser struct {
	*walker

	folds *folder
	c     *Completeness
}

// neighbors reads the neighbors of unit in dir, shows up to limit of them and
// folds the others.
func (f *focuser) neighbors(ctx context.Context, unit Unit, dir Direction, limit int) ([]Node, error) {
	edges, truncated, err := f.read(ctx, []Unit{unit}, dir)
	if err != nil {
		return nil, err
	}

	f.c.Truncated = f.c.Truncated || truncated
	f.edges = append(f.edges, edges...)

	lastSeen := map[Unit]time.Time{}

	var others []Unit

	for i := range edges {
		e := &edges[i]

		other := far(e, dir)
		if other == unit {
			continue
		}

		if _, ok := lastSeen[other]; !ok {
			others = append(others, other)
		}

		if e.LastSeen.After(lastSeen[other]) {
			lastSeen[other] = e.LastSeen
		}
	}

	nodes, err := f.visible(ctx, others)
	if err != nil {
		return nil, err
	}

	slices.SortStableFunc(nodes, func(a, b Node) int {
		return lastSeen[b.Unit].Compare(lastSeen[a.Unit])
	})

	if len(nodes) <= limit {
		for _, n := range nodes {
			f.shown[n.Unit] = true
		}

		return nodes, nil
	}

	for _, n := range nodes[limit:] {
		f.folds.nodes[n.Unit] = n
		f.folds.add(n, FoldLink{Unit: unit, Direction: dir, Edges: 1})
	}

	for _, n := range nodes[:limit] {
		f.shown[n.Unit] = true
	}

	return nodes[:limit], nil
}

// foldBeyond reads one hop beyond each shown neighbor, in both directions,
// and folds the readable units that it finds into their collections. It
// skips the focus unit, the shown units and the hidden units.
func (f *focuser) foldBeyond(ctx context.Context, focus Unit, shown []Node) error {
	if len(shown) == 0 {
		return nil
	}

	units := make([]Unit, 0, len(shown))
	for _, n := range shown {
		units = append(units, n.Unit)
	}

	units = sortedUnits(units)

	for _, dir := range []Direction{Upstream, Downstream} {
		if err := f.foldDirection(ctx, focus, units, dir); err != nil {
			return err
		}
	}

	return nil
}

// foldDirection reads one hop in dir from the shown units and folds the
// readable units that it finds into their collections.
func (f *focuser) foldDirection(ctx context.Context, focus Unit, units []Unit, dir Direction) error {
	edges, truncated, err := f.read(ctx, units, dir)
	if err != nil {
		return err
	}

	f.c.Truncated = f.c.Truncated || truncated

	beyond, near := f.beyond(edges, focus, dir)

	nodes, err := f.visible(ctx, beyond)
	if err != nil {
		return err
	}

	for _, n := range nodes {
		f.folds.nodes[n.Unit] = n
	}

	for _, other := range sortedUnits(mapKeys(near)) {
		n, ok := f.folds.nodes[other]
		if !ok {
			continue
		}

		for _, from := range near[other] {
			f.folds.add(n, FoldLink{Unit: from, Direction: dir, Edges: 1})
		}
	}

	return nil
}

// beyond returns the units that edges reach in dir and that the walk did not
// see before, and for each unit that edges reach, the shown units that reach
// it. It skips the focus unit, the shown units and the hidden units.
func (f *focuser) beyond(edges []Edge, focus Unit, dir Direction) ([]Unit, map[Unit][]Unit) {
	var beyond []Unit

	near := map[Unit][]Unit{}

	for i := range edges {
		e := &edges[i]

		from, other := e.Source, e.Dependent
		if dir == Upstream {
			from, other = e.Dependent, e.Source
		}

		if other == focus || f.shown[other] || f.hidden[other] {
			continue
		}

		if !f.seen[other] {
			f.seen[other] = true
			beyond = append(beyond, other)
		}

		near[other] = append(near[other], from)
	}

	return beyond, near
}

func mapKeys(m map[Unit][]Unit) []Unit {
	out := make([]Unit, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

// folder collects the folds of a focus view.
type folder struct {
	units map[foldKey]map[Unit]bool
	links map[foldKey]map[FoldLink]int
	nodes map[Unit]Node
}

type foldKey struct {
	namespace  namespace.Name
	collection string
}

func newFolder() *folder {
	return &folder{
		units: map[foldKey]map[Unit]bool{},
		links: map[foldKey]map[FoldLink]int{},
		nodes: map[Unit]Node{},
	}
}

func (f *folder) add(n Node, l FoldLink) {
	k := foldKey{namespace: n.Unit.Namespace, collection: n.Facts.Collection}

	units := f.units[k]
	if units == nil {
		units = map[Unit]bool{}
		f.units[k] = units
	}

	links := f.links[k]
	if links == nil {
		links = map[FoldLink]int{}
		f.links[k] = links
	}

	units[n.Unit] = true
	key := FoldLink{Unit: l.Unit, Direction: l.Direction, Edges: 0}
	links[key] += l.Edges
}

func (f *folder) list() []Fold {
	out := make([]Fold, 0, len(f.units))

	for k, units := range f.units {
		fold := Fold{Namespace: k.namespace, Collection: k.collection, Units: len(units), Links: nil}

		for l, n := range f.links[k] {
			l.Edges = n
			fold.Links = append(fold.Links, l)
		}

		slices.SortFunc(fold.Links, func(a, b FoldLink) int {
			return cmp.Or(cmp.Compare(a.Unit.String(), b.Unit.String()), cmp.Compare(a.Direction, b.Direction))
		})

		out = append(out, fold)
	}

	slices.SortFunc(out, func(a, b Fold) int {
		return cmp.Or(cmp.Compare(a.Namespace.String(), b.Namespace.String()), cmp.Compare(a.Collection, b.Collection))
	})

	return out
}
