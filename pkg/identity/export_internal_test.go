package identity

import (
	"slices"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// LeftOutNamesForTest returns a copy of the namespaces that s leaves out, so a
// test can check the stored order and the names that Every removes.
func LeftOutNamesForTest(s Scope) []namespace.Name {
	return slices.Clone(s.every.except)
}
