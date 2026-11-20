package namespace_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestFilterCases checks what a store reads from each kind of filter: the
// zero Filter and an empty name list qualify no namespace, and only
// EveryNamespace reports every namespace, always with its left-out names.
func TestFilterCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		filter    namespace.Filter
		wantEvery bool
		wantLeft  []namespace.Name
		wantNames []namespace.Name
	}{
		{
			name: "the zero filter", filter: namespace.NamedNamespaces(), wantEvery: false, wantLeft: nil,
			wantNames: []namespace.Name{},
		},
		{
			name: "no name", filter: namespace.NamedNamespaces(), wantEvery: false, wantLeft: nil,
			wantNames: []namespace.Name{},
		},
		{
			name:      "named namespaces keep the first order once",
			filter:    namespace.NamedNamespaces("beta", "acme", "beta"),
			wantEvery: false,
			wantLeft:  nil,
			wantNames: []namespace.Name{"beta", "acme"},
		},
		{
			name: "every namespace", filter: namespace.EveryNamespace(), wantEvery: true,
			wantLeft: []namespace.Name{}, wantNames: []namespace.Name{},
		},
		{
			name: "every namespace but two, sorted once", filter: namespace.EveryNamespace("beta", "acme", "beta"),
			wantEvery: true, wantLeft: []namespace.Name{"acme", "beta"}, wantNames: []namespace.Name{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			left, every := tc.filter.Every()
			if every != tc.wantEvery || !slices.Equal(left, tc.wantLeft) {
				t.Fatalf("Every = %v, %t, want %v, %t", left, every, tc.wantLeft, tc.wantEvery)
			}

			names := tc.filter.Names()
			if names == nil || !slices.Equal(names, tc.wantNames) {
				t.Fatalf("Names = %#v, want %v", names, tc.wantNames)
			}
		})
	}
}

// TestFilterCopies checks that a change to the input or to a returned slice
// does not change the filter.
func TestFilterCopies(t *testing.T) {
	t.Parallel()

	input := []namespace.Name{"acme"}
	every := namespace.EveryNamespace(input...)
	named := namespace.NamedNamespaces(input...)
	input[0] = "beta"

	left, _ := every.Every()
	left[0] = "gamma"

	names := named.Names()
	names[0] = "gamma"

	if got, _ := every.Every(); !slices.Equal(got, []namespace.Name{"acme"}) {
		t.Fatalf("left-out names = %v, want [acme]", got)
	}

	if got := named.Names(); !slices.Equal(got, []namespace.Name{"acme"}) {
		t.Fatalf("names = %v, want [acme]", got)
	}
}

// TestFilterKey checks that the key follows the namespaces that a filter
// qualifies: the order of the names does not count, and the filters of every
// namespace, of every namespace but one, and of a namespace named "all" each
// give another key.
func TestFilterKey(t *testing.T) {
	t.Parallel()

	if namespace.NamedNamespaces("bravo", "alpha").Key() != namespace.NamedNamespaces("alpha", "bravo").Key() {
		t.Fatal("one name set in another order gives two keys")
	}

	if namespace.EveryNamespace("bravo", "alpha").Key() != namespace.EveryNamespace("alpha", "bravo").Key() {
		t.Fatal("one left-out set in another order gives two keys")
	}

	keys := []string{
		namespace.EveryNamespace().Key(),
		namespace.EveryNamespace("alpha").Key(),
		namespace.EveryNamespace("alpha", "bravo").Key(),
		namespace.NamedNamespaces().Key(),
		namespace.NamedNamespaces("alpha").Key(),
		namespace.NamedNamespaces("all").Key(),
		namespace.NamedNamespaces("alpha", "bravo").Key(),
	}

	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			t.Fatalf("two filters give the key %q", key)
		}

		seen[key] = true
	}
}
