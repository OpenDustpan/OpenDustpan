package unmanaged

import (
	"slices"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Holder is one unit whose latest state holds a resource key, from the
// resource index. Resource is the key of the resource in the state: the URN
// or the resource instance address. Deleted is true for a unit in its
// recovery window.
type Holder struct {
	Resource string
	Type     string
	Unit     graph.Unit
	Deleted  bool
}

// Class is the outcome of the classification of one resource key in one
// namespace. The three kinds of unmanaged resource are classes. The other
// classes make no finding.
type Class string

// The classes that make no finding.
const (
	// ClassManaged is a resource that exactly one live unit of the
	// namespace manages, and no live unit of another namespace.
	ClassManaged Class = "managed"
	// ClassElsewhere is a resource that units of linked namespaces hold and
	// no unit of this namespace holds. The namespace of those units owns
	// the question. The answer to this namespace does not name them.
	ClassElsewhere Class = "elsewhere"
	// ClassNone is a resource that no unit holds and that no sweep saw.
	ClassNone Class = "none"
)

// The classes of the kinds of unmanaged resource.
const (
	ClassNotInAnyUnit = Class(KindNotInAnyUnit)
	ClassLeftBehind   = Class(KindLeftBehind)
	ClassManagedTwice = Class(KindManagedTwice)
)

// Finding reports whether c makes a finding, and its kind.
func (c Class) Finding() (Kind, bool) {
	k := Kind(c)

	return k, k.Valid()
}

// Classify returns the class of one resource key in namespace ns. observed
// is true when a sweep saw the resource. linked holds the namespaces that an
// installation administrator linked with ns. holders is the holders of the
// key; Classify drops each holder whose namespace is neither ns nor linked,
// so a unit of a namespace that is not linked never changes the class. The
// rules, in order:
//
//  1. Two or more distinct live units hold the key, and at least one is in
//     ns: managed twice. Two resources of one unit are one holder, so a
//     unit that names one cloud resource two times is not managed twice.
//  2. Exactly one live unit of ns holds the key: managed.
//  3. Live units hold the key, none of them in ns: elsewhere.
//  4. Only deleted units of ns hold the key: left behind.
//  5. Only deleted units of linked namespaces hold the key: elsewhere.
//  6. No unit holds the key, and a sweep saw it: not in any unit.
//  7. Else: none.
//
// A data source, which reads a resource and does not manage it, is never a
// holder: the resource index leaves it out.
func Classify(ns namespace.Name, linked []namespace.Name, observed bool, holders []Holder) Class {
	c := countHolders(ns, linked, holders)

	switch {
	case len(c.live) >= 2 && c.liveHere:
		return ClassManagedTwice
	case len(c.live) == 1 && c.liveHere:
		return ClassManaged
	case len(c.live) > 0:
		return ClassElsewhere
	case c.deletedHere:
		return ClassLeftBehind
	case c.deletedElsewhere:
		return ClassElsewhere
	case observed:
		return ClassNotInAnyUnit
	default:
		return ClassNone
	}
}

// holderCount is what Classify needs to know of the holders of a key: the
// distinct live units, whether one of them is in the namespace, and whether
// a deleted unit in the namespace or in a linked namespace holds the key.
type holderCount struct {
	live             map[graph.Unit]bool
	liveHere         bool
	deletedHere      bool
	deletedElsewhere bool
}

func countHolders(ns namespace.Name, linked []namespace.Name, holders []Holder) holderCount {
	c := holderCount{live: map[graph.Unit]bool{}, liveHere: false, deletedHere: false, deletedElsewhere: false}

	for _, h := range holders {
		here := h.Unit.Namespace == ns
		if !here && !slices.Contains(linked, h.Unit.Namespace) {
			continue
		}

		switch {
		case !h.Deleted:
			c.live[h.Unit] = true
			c.liveHere = c.liveHere || here
		case here:
			c.deletedHere = true
		default:
			c.deletedElsewhere = true
		}
	}

	return c
}
