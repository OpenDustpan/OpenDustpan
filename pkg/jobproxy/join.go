package jobproxy

import (
	"fmt"
	"slices"
)

// Join joins the proxy rule sets of the scopes of a unit: the namespace, the
// blueprint version and the unit. A proxy rule only allows, so the joined
// set allows each request that one of the sets allows, and no other.
//
// It returns ErrInvalid when a set fails Validate, when two sets use one ID,
// and when two sets give one host and port different modes. So a decision
// names one proxy rule. The joined set can hold more than MaxRules proxy
// rules: the limit applies to each scope.
func Join(sets ...RuleSet) (RuleSet, error) {
	out := RuleSet{Rules: []Rule{}}
	ids := map[string]bool{}
	modes := map[string]Mode{}

	for _, set := range sets {
		// Defense in depth: Parse checks each set already; this check stays
		// so that a set that a caller builds by hand cannot skip it.
		if err := set.Validate(); err != nil {
			return RuleSet{}, err
		}

		for i := range set.Rules {
			r := &set.Rules[i]

			if ids[r.ID] {
				return RuleSet{}, fmt.Errorf("%w: proxy rule %s: %s", ErrInvalid, r.ID, reasonIDTwice)
			}

			key := hostPort(r.Host, r.Port)
			if m, ok := modes[key]; ok && m != r.Mode {
				return RuleSet{}, fmt.Errorf("%w: proxy rule %s: %s", ErrInvalid, r.ID, reasonModeTwice)
			}

			ids[r.ID] = true
			modes[key] = r.Mode

			out.Rules = append(out.Rules, cloneRule(*r))
		}
	}

	return out, nil
}

// cloneRule returns a copy of r that shares no list with r.
func cloneRule(r Rule) Rule {
	r.Operations = slices.Clone(r.Operations)
	r.Methods = slices.Clone(r.Methods)
	r.Paths = slices.Clone(r.Paths)

	return r
}
