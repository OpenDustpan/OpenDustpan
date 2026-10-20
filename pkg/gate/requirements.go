package gate

import (
	"fmt"
)

// DefaultRequirements returns the approval requirements that the approval
// defaults of the scopes of one unit give, with no signatures.
//
// A default that names no approvers merges with the others of its kind into
// one requirement, "default". Its count is the highest count of the merged
// defaults, so the strictest default wins. Its fresh authentication window is
// the shortest window that a merged default sets, and none when no merged
// default sets one. Its version is the version of the default that gives the
// count. Any holder of the approve action who is not the author may sign it.
//
// A default that names approvers is a requirement of its own,
// "default/<scope key>". The requirements come in this order: "default",
// then each named default in the order of defaults.
//
// It returns an error that wraps ErrInvalid for a default whose count or
// window is not valid (a row from another version, or from a restore), and an
// error that wraps ErrUnavailable for a default that holds an Unless body,
// because only the gate engine can decide an exemption. A caller that gets an
// error refuses the change: no default is skipped.
//
// Defense in depth: the service and the store validate a default at save.
// This check stays so that a stored row that skipped them still cannot lower
// a requirement.
func DefaultRequirements(defaults []ApprovalDefault) ([]Requirement, error) {
	var (
		merged Requirement
		named  []Requirement
		found  bool
	)

	for i := range defaults {
		d := defaults[i]

		if err := d.Validate(); err != nil {
			return nil, fmt.Errorf("the approval default of %s: %w", d.Scope.Key(), err)
		}

		if d.Unless != "" {
			return nil, fmt.Errorf("%w: the approval default of %s holds an unless body, and this version cannot "+
				"evaluate it; remove the unless body", ErrUnavailable, d.Scope.Key())
		}

		if !d.Approvers.Empty() {
			named = append(named, requirementOf(ScopedDefaultRequirement(d.Scope), d))

			continue
		}

		if !found {
			merged, found = requirementOf(DefaultRequirement(), d), true

			continue
		}

		merged = mergeDefault(merged, d)
	}

	if !found {
		return named, nil
	}

	return append([]Requirement{merged}, named...), nil
}

func requirementOf(name RequirementName, d ApprovalDefault) Requirement {
	var r Requirement
	r.Name, r.Version, r.Approvers, r.Count, r.FreshAuth = name, d.Version, d.Approvers, d.Count, d.FreshAuth
	r.TimeoutSeconds = int64(DefaultApprovalWait.Seconds())

	return r
}

// mergeDefault merges d into the requirement r of the defaults that name no
// approvers.
func mergeDefault(r Requirement, d ApprovalDefault) Requirement {
	if d.Count > r.Count {
		r.Count, r.Version = d.Count, d.Version
	}

	r.FreshAuth = shortestWindow(r.FreshAuth, d.FreshAuth)

	return r
}

// shortestWindow returns the shorter of two windows. None is longer than any
// window that is set.
func shortestWindow(a, b FreshAuth) FreshAuth {
	switch {
	case !a.Set:
		return b
	case !b.Set:
		return a
	case b.Seconds < a.Seconds:
		return b
	default:
		return a
	}
}
