package job

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrServerRunsOff reports a start by a server-made principal in a namespace
// whose server-runs switch is off. The start makes no job.
var ErrServerRunsOff = errors.New("server-started runs are off for this namespace")

// ServerRuns is the server-runs switch of one namespace. When Off is true,
// the job service refuses each start whose actor is a server-made
// principal: every scheduled and every upstream run. Starts by a person, a
// service principal or a workload do not change. A namespace that never set
// the switch gives Off false. ChangedBy is the subject of the principal that
// set the switch last, and ChangedAt the time. Both are empty for a
// namespace that never set it. Reason is the text that the last change
// gave, and the API shows it with the refusal of a start.
type ServerRuns struct {
	ChangedAt time.Time
	ChangedBy string
	Reason    string
	Off       bool
}

// ServerRunsSettings keeps the server-runs switch of each namespace. A SQL
// adapter serves it from the database of the namespace.
//
// The job service reads the switch in the transaction of each start by a
// server-made principal, so that a start that begins after the switch
// commits is refused. It does not read it for any other actor.
type ServerRunsSettings interface {
	// ServerRuns returns the switch of the namespace ns. It returns
	// namespace.ErrNotFound for an unknown namespace, and ErrInvalid for a
	// namespace name that namespace.ParseName refuses and for a stored value
	// that the store cannot read, so that the caller refuses the start.
	ServerRuns(ctx context.Context, ns namespace.Name) (ServerRuns, error)
	// SetServerRuns stores s as the switch of the namespace ns and appends
	// the audit entry e in the transaction. The entry holds one change, and
	// the store sets its Old value to the value that the write replaces,
	// read in the same transaction. It returns ErrInvalid when ns fails
	// namespace.ParseName, namespace.ErrFenced when the fence is stale or
	// names another namespace, and namespace.ErrNotFound for an unknown
	// namespace. On every error the store changes nothing. A job that
	// started before the change keeps running.
	SetServerRuns(ctx context.Context, ns namespace.Name, s ServerRuns, fence namespace.Fence, e audit.Entry) error
}
