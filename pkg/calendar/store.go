package calendar

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Assignment binds a scope to a calendar. A scope has at most one
// calendar. A scope with no assignment uses the next scope of its chain
// (see Store.Resolve).
type Assignment struct {
	Scope    Scope
	Calendar ID
}

// Resolved is the calendar that applies to a chain of scopes, and the
// scope that gave it, so that a reader can see why a deadline falls where
// it does.
type Resolved struct {
	Calendar Calendar
	// From is the scope whose assignment gave the calendar, or
	// the installation scope for the default.
	From Scope
}

// Store is the persistence port for calendars and their assignments. A
// calendar of a namespace and the assignment of a namespace are writes in
// that namespace: they carry its fence, and the store checks the fence in
// the transaction of the write. A calendar of a group or of the
// installation carries no fence. Every write appends the audit entry e in
// its transaction, and a failed write appends nothing.
//
// Holidays returned by Get and Resolve hold the manual holidays and the
// holidays of each feed, ordered by date.
type Store interface {
	// Create persists c in scope owner. It returns ErrInvalid for a
	// calendar that fails Validate, ErrExists for a used ID, and
	// namespace.ErrFenced for a stale fence.
	Create(ctx context.Context, owner Scope, c Calendar, fence namespace.Fence, e audit.Entry) error
	// Get returns the calendar id, or ErrNotFound.
	Get(ctx context.Context, id ID) (Calendar, error)
	// Update replaces the fields of c.ID, except the holidays of its
	// feeds, when c.Version is the stored version, and increments the
	// version. It returns ErrNotFound, ErrInvalid, ErrConflict for an older
	// version, and namespace.ErrFenced for a stale fence.
	Update(ctx context.Context, c Calendar, fence namespace.Fence, e audit.Entry) error
	// Delete deletes the calendar id. It returns ErrNotFound, ErrInUse when
	// an assignment names it, ErrInvalid for DefaultID, and
	// namespace.ErrFenced for a stale fence.
	Delete(ctx context.Context, id ID, fence namespace.Fence, e audit.Entry) error
	// Assign binds a.Scope to a.Calendar, or clears the binding when
	// a.Calendar is empty. It returns ErrNotFound for an unknown calendar,
	// ErrInvalid for a scope that fails Validate or for a calendar of
	// another namespace, and namespace.ErrFenced for a stale fence.
	Assign(ctx context.Context, a Assignment, fence namespace.Fence, e audit.Entry) error
	// Resolve returns the calendar of the first scope of chain that has an
	// assignment, and the default calendar when none has one. The order of
	// chain is the order of the search. It never returns ErrNotFound.
	Resolve(ctx context.Context, chain []Scope) (Resolved, error)
	// ReplaceFeedHolidays replaces the holidays of the feed feedID of the
	// calendar id with holidays, and records status, in one transaction.
	// A failed read calls it with nil holidays and a status that holds the
	// error: the store then keeps the holidays of the last good read and
	// records only the status. It returns ErrNotFound for an unknown
	// calendar or feed, and ErrInvalid for more than MaxHolidays holidays.
	// The server writes no audit entry for a scheduled read.
	ReplaceFeedHolidays(ctx context.Context, id ID, feedID string, holidays []Holiday, status FeedStatus) error
}

// DateRange is a range of dates, From included and To excluded.
type DateRange struct {
	From Date
	To   Date
}

// FetchResult is what one read of a feed gives.
type FetchResult struct {
	// Holidays are the whole-day events of the feed in the window, one
	// for each date. Recurring events are expanded in the window.
	Holidays []Holiday
	// Skipped counts the events that the source did not import: timed
	// events, and events outside the window.
	Skipped int
	// ETag is the validator of the answer, for the next conditional read.
	ETag string
	// ReadAt is the time of the read.
	ReadAt time.Time
}

// HolidaySource reads holidays from an iCal feed (RFC 5545). An adapter
// reads the feed URL over https, through the egress rules of the
// installation, with no credential. It refuses a private, loopback or
// link-local address, a redirect to another host, and a body above its
// size bound. It parses VEVENT entries. An event with a DTSTART of the
// type DATE is a whole-day event, and DTEND is the first day after it. An
// event with a time of day is skipped and counted.
type HolidaySource interface {
	// Fetch reads feed and returns the holidays in window. It sends the
	// ETag of the last good read, when feed.Status holds one. It returns
	// ErrNotModified when the feed did not change, ErrFeedUnreachable for
	// an address that it refuses or cannot reach, ErrFeedTooLarge above
	// its size bound, and ErrFeedInvalid for a body that is not iCal.
	Fetch(ctx context.Context, feed Feed, window DateRange) (FetchResult, error)
}

// HolidaySource errors.
var (
	// ErrNotModified reports a feed that did not change since its last
	// good read.
	ErrNotModified = errors.New("feed not modified")
	// ErrFeedUnreachable reports a feed URL that the source refuses or
	// cannot reach.
	ErrFeedUnreachable = errors.New("feed unreachable")
	// ErrFeedTooLarge reports a feed above the size bound of the source.
	ErrFeedTooLarge = errors.New("feed too large")
	// ErrFeedInvalid reports a feed body that is not iCal.
	ErrFeedInvalid = errors.New("feed is not valid iCal")
)
