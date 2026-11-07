package dustpan

import (
	"opendustpan.org/opendustpan/pkg/principal"
)

// The errors of the read of the groups of an approver.
var (
	ErrNoPrincipalStore = errNoPrincipalStore
	ErrTooManyGroups    = errTooManyGroups
)

// JobGroupsPage is the size of one page of the read of the groups of a
// principal.
const JobGroupsPage = jobGroupsPage

// JobGroupsMaxPages is the most pages that the read covers.
const JobGroupsMaxPages = jobGroupsMaxPages

// JobGroups is the reader of the groups of an approver.
type JobGroups = jobGroups

// NewJobGroups returns the reader of the groups of an approver over store.
func NewJobGroups(store principal.Store) JobGroups {
	return jobGroups{principals: store}
}
