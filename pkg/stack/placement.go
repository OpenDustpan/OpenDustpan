package stack

import "opendustpan.org/opendustpan/pkg/namespace"

// Complete reports whether p names a collection in ns and the checked ID of it.
func (p Placement) Complete(ns namespace.Name) bool {
	return p.Collection.Name != "" && p.CollectionID != "" && p.Collection.Namespace == ns
}
