package dustpan

import (
	"opendustpan.org/opendustpan/pkg/principal"
)

// ErrNoPrincipalStore is the error of a reader with no principal store.
var ErrNoPrincipalStore = errNoPrincipalStore

// JobGroups is the reader of the groups of an approver.
type JobGroups = jobGroups

// NewJobGroups returns the reader of the groups of an approver over store.
func NewJobGroups(store principal.Store) JobGroups {
	return jobGroups{principals: store}
}
