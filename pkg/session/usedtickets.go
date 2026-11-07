package session

import (
	"context"
	"time"
)

// UsedTicketTTL is how long a record of a used CAS ticket lives. A CAS
// server gives a service ticket a life of seconds, so a ticket that is
// older than this is never valid again at the server.
const UsedTicketTTL = time.Hour

// UsedTickets records the CAS service tickets that the server used, so that
// a ticket gives one sign-in only. The key of a record is the provider ID
// and the ticket, so that two CAS servers that issue the same ticket do
// not refuse the sign-ins of each other. The CAS method calls Use before it
// sends the ticket to the CAS server. The conformance suite RunUsedTickets
// in package sessiontest checks an implementation.
type UsedTickets interface {
	// Use records the ticket of the provider until the time until. It
	// returns ErrReplay when a record of the same provider and ticket
	// exists and its until is after now, and ErrMethodInput for an empty
	// provider ID, an empty ticket or a zero until. It returns another error
	// when the record cannot be written. The method then fails closed with
	// ErrMethodUnavailable. After until, the record can go, and a new Use of
	// the same provider and ticket is recorded again.
	Use(ctx context.Context, providerID, ticket string, until time.Time) error
}
