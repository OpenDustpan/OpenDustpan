package conformance_test

import (
	"regexp"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen/conformance"
)

var caseName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// TestCaseNames checks that each case has a known group and a kebab-case name
// that is unique in its group, so that a report names each case once.
func TestCaseNames(t *testing.T) {
	t.Parallel()

	groups := conformance.Groups()
	seen := make(map[string]bool)

	for _, c := range conformance.Cases() {
		if !slices.Contains(groups, c.Group) {
			t.Errorf("case %q: group %q is not in Groups", c.Name, c.Group)
		}

		if !caseName.MatchString(c.Name) {
			t.Errorf("case %q: name is not kebab-case", c.Name)
		}

		key := string(c.Group) + "/" + c.Name
		if seen[key] {
			t.Errorf("case %q is in the list two times", key)
		}

		seen[key] = true
	}
}

// TestEveryGroupHasACase checks that no group is empty.
func TestEveryGroupHasACase(t *testing.T) {
	t.Parallel()

	cases := conformance.Cases()
	for _, g := range conformance.Groups() {
		if !slices.ContainsFunc(cases, func(c conformance.Case) bool { return c.Group == g }) {
			t.Errorf("group %q has no case", g)
		}
	}
}
