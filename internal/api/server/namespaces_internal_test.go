package server

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestNamespaceListFingerprint proves the fingerprint follows the filter
// only: the same names give one value, and another filter gives another.
func TestNamespaceListFingerprint(t *testing.T) {
	t.Parallel()

	all := namespace.Filter{All: true, Names: nil}
	two := namespace.Filter{All: false, Names: []namespace.Name{"bravo", "alpha"}}
	twoOtherOrder := namespace.Filter{All: false, Names: []namespace.Name{"alpha", "bravo"}}
	one := namespace.Filter{All: false, Names: []namespace.Name{"alpha"}}

	if namespaceListFingerprint(all) != namespaceListFingerprint(namespace.Filter{All: true, Names: nil}) {
		t.Fatal("one filter of all namespaces gives two fingerprints")
	}

	if namespaceListFingerprint(two) != namespaceListFingerprint(twoOtherOrder) {
		t.Fatal("one filter in another order gives two fingerprints")
	}

	if namespaceListFingerprint(two) == namespaceListFingerprint(one) {
		t.Fatal("two filters give one fingerprint")
	}

	if namespaceListFingerprint(all) == namespaceListFingerprint(one) {
		t.Fatal("the filter of all namespaces and a named filter give one fingerprint")
	}

	onlyAll := namespace.Filter{All: false, Names: []namespace.Name{"all"}}
	if namespaceListFingerprint(all) == namespaceListFingerprint(onlyAll) {
		t.Fatal("the filter of all namespaces and the filter of the namespace all give one fingerprint")
	}
}
