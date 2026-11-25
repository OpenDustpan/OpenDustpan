package access

import (
	"opendustpan.org/opendustpan/pkg/principal"
)

// HousekeepingSubject reports whether s is the subject of a housekeeping
// principal: a principal whose ID is the ID of a purpose of
// principal.HousekeepingPurposes.
func HousekeepingSubject(s Subject) bool {
	if s.Kind != SubjectPrincipal {
		return false
	}

	for _, p := range principal.HousekeepingPurposes() {
		if p.ID() == s.ID {
			return true
		}
	}

	return false
}
