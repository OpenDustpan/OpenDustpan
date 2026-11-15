package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
// in package sessiontest checks an implementation. StoreTickets is the
// implementation over a TicketStore, which the SQL stores implement, so that
// all servers of an installation share the records.
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

// The limits of the input of a record of a used ticket. A CAS ticket is short,
// so a longer value is not a ticket.
const (
	// MaxTicketLength is the largest length, in bytes, of a CAS ticket that
	// a record accepts.
	MaxTicketLength = 4096
	// MaxProviderIDLength is the largest length, in bytes, of a provider ID
	// that a record accepts.
	MaxProviderIDLength = 256
	// maxUntilAhead is how far after now the until time of a record may be.
	maxUntilAhead = 2 * UsedTicketTTL
)

// TicketDigest is what a TicketStore keeps in place of a CAS ticket: the
// SHA-256 of the ticket.
type TicketDigest [sha256.Size]byte

// DigestOfTicket returns the digest of the ticket.
func DigestOfTicket(ticket string) TicketDigest {
	return sha256.Sum256([]byte(ticket))
}

// String returns the digest in lower-case hex, the form a store keeps.
func (d TicketDigest) String() string {
	return hex.EncodeToString(d[:])
}

// TicketStore is the persistence port for the records of used CAS tickets.
// All servers of an installation share it, so a ticket that one server used
// is refused at every other server, and after a restart. A store keeps the
// digest of a ticket, never the ticket. A record belongs to the installation,
// so a write carries no namespace fence. The conformance suite RunTicketStore
// in package sessiontest checks an implementation.
type TicketStore interface {
	// UseTicket records the ticket with the digest d of the provider
	// providerID until the time until, in one atomic step. It returns
	// ErrReplay, and changes nothing, when a record of the same provider and
	// digest exists and its until is after at. A record whose until is at or
	// before at is replaced. It returns ErrMethodInput, and writes nothing,
	// for an empty or too long provider ID, a zero digest, an until that is
	// not after at, or an until that is more than two UsedTicketTTL after at.
	// Of any number of concurrent calls for one provider and digest, one
	// returns nil.
	UseTicket(ctx context.Context, providerID string, d TicketDigest, at, until time.Time) error
	// PruneTickets deletes the records whose until is at or before at, and
	// returns how many it deleted. A record that is past its until refuses
	// nothing, so a prune changes no answer of UseTicket.
	PruneTickets(ctx context.Context, at time.Time) (int64, error)
}

// ValidateTicketUse applies the input rules of TicketStore.UseTicket and
// returns ErrMethodInput for a value that fails them. Each store calls it at
// its own boundary, before it writes.
func ValidateTicketUse(providerID string, d TicketDigest, at, until time.Time) error {
	switch {
	case providerID == "" || len(providerID) > MaxProviderIDLength:
		return fmt.Errorf("%w: the provider ID is empty or longer than %d bytes", ErrMethodInput, MaxProviderIDLength)
	case d == TicketDigest{}:
		return fmt.Errorf("%w: the ticket digest is zero", ErrMethodInput)
	case until.IsZero() || !until.After(at):
		return fmt.Errorf("%w: the until time is not after now", ErrMethodInput)
	case until.After(at.Add(maxUntilAhead)):
		return fmt.Errorf("%w: the until time is more than %s after now", ErrMethodInput, maxUntilAhead)
	}

	return nil
}

// StoreTickets is the UsedTickets of a TicketStore.
type StoreTickets struct {
	store TicketStore
	now   func() time.Time
}

var _ UsedTickets = (*StoreTickets)(nil)

// NewStoreTickets returns the UsedTickets that records in store. The clock now
// gives the time at which a record starts. A nil store or a nil clock returns
// ErrMethodInput.
func NewStoreTickets(store TicketStore, now func() time.Time) (*StoreTickets, error) {
	if store == nil || now == nil {
		return nil, fmt.Errorf("%w: the store and the clock of used tickets are required", ErrMethodInput)
	}

	return &StoreTickets{store: store, now: now}, nil
}

// Use implements UsedTickets. It checks the input before it calls the store,
// and the store checks it again.
func (s *StoreTickets) Use(ctx context.Context, providerID, ticket string, until time.Time) error {
	// Defense in depth: the store checks the same limits at its own boundary;
	// this check stays so that a store that omits them cannot take a hostile
	// value, and it does not rely on the CAS method for the same limits.
	if providerID == "" || len(providerID) > MaxProviderIDLength || ticket == "" || len(ticket) > MaxTicketLength ||
		until.IsZero() {
		return fmt.Errorf("%w: the provider ID, the ticket and the until time are required and limited", ErrMethodInput)
	}

	err := s.store.UseTicket(ctx, providerID, DigestOfTicket(ticket), s.now(), until)
	if err != nil {
		return fmt.Errorf("use ticket: %w", err)
	}

	return nil
}
