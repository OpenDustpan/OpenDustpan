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

// Store is the persistence port for calendars and their assignments.
//
// Every write carries a fence for the scope of its row (Scope.WriteScope):
// the scope of the owner of the calendar, or the scope of the assignment.
// A row of a namespace needs the fence of that namespace, at its current
// failover version, and the store checks the fence in the transaction of
// the write. A row of a group or of the installation is in
// audit.ServerScope and needs the zero fence. A zero fence for a row of a
// namespace, the fence of another namespace, and a fence other than the
// zero fence for a row in audit.ServerScope are refused: each method names
// the error. A stale fence returns namespace.ErrFenced, and the fence of an
// unknown namespace returns namespace.ErrNotFound.
//
// Every write through the API appends the audit entry e in its
// transaction, and a failed write changes nothing and appends nothing. The
// writes of a scheduled feed read (ReplaceFeedHolidays and
// RecordFeedStatus) append no entry. A write refuses an audit entry whose
// Namespace is not the scope of its row, with the error of a fence that
// does not belong to that scope, so that a write cannot append to the
// audit chain of another namespace.
//
// Get and Resolve return the full URL of each feed, for the server and the
// HolidaySource adapter. A read through the API returns Calendar.Masked.
// Their holidays hold the manual holidays and the holidays of each feed,
// ordered by date.
type Store interface {
	// Create persists c in the scope c.Owner, at version 1, with the
	// manual holidays of c and a zero status for each feed. It returns
	// ErrInvalid for a calendar that fails Validate and for a fence that
	// does not belong to the scope of c.Owner, ErrExists for a used ID and
	// for DefaultID, and namespace.ErrFenced for a stale fence.
	Create(ctx context.Context, c Calendar, fence namespace.Fence, e audit.Entry) error
	// Get returns the calendar id of scope, the write scope of its owner
	// (Scope.WriteScope): the namespace of a calendar of a namespace, or
	// audit.ServerScope for a calendar of a group or of the installation.
	// It returns ErrNotFound for an unknown ID and for a calendar of
	// another scope, so that a read in one namespace does not tell whether
	// a calendar of another scope exists. Get of DefaultID in
	// audit.ServerScope returns Default until an administrator changes it.
	Get(ctx context.Context, scope namespace.Name, id ID) (Calendar, error)
	// Update replaces the name, the zone, the working days, the working
	// hours, the manual holidays and the feeds of c.ID when c.Version is
	// the stored version, and increments the version. The feeds follow
	// MergeFeeds: a feed with its stored ID and URL, or the masked URL,
	// keeps its URL, its status and its holidays; the store drops the
	// holidays of a feed that is gone or has a new URL. The status in c is
	// ignored. It returns ErrNotFound for an unknown ID and for a fence that
	// does not belong to the scope of the stored calendar, ErrInvalid for a
	// calendar that fails Validate or a changed Owner, ErrConflict for an
	// older version, and namespace.ErrFenced for a stale fence.
	Update(ctx context.Context, c Calendar, fence namespace.Fence, e audit.Entry) error
	// Delete deletes the calendar id and the holidays of its feeds. It
	// returns ErrInvalid for DefaultID, ErrNotFound for an unknown ID and
	// for a fence that does not belong to the scope of the calendar,
	// ErrInUse when an assignment names it, and namespace.ErrFenced for a
	// stale fence.
	Delete(ctx context.Context, id ID, fence namespace.Fence, e audit.Entry) error
	// Assign binds a.Scope to a.Calendar, or clears the binding when
	// a.Calendar is empty. A scope can use a calendar of its own
	// namespace, of a group, or of the installation, not a calendar of
	// another namespace. It returns ErrNotFound for an unknown calendar,
	// ErrInvalid for a scope that fails Validate, for a calendar of
	// another namespace and for a fence that does not belong to the scope
	// of a.Scope, and namespace.ErrFenced for a stale fence.
	Assign(ctx context.Context, a Assignment, fence namespace.Fence, e audit.Entry) error
	// Resolve returns the calendar of the first scope of chain that has an
	// assignment, and the default calendar when none has one. The order of
	// chain is the order of the search. It never returns ErrNotFound.
	Resolve(ctx context.Context, chain []Scope) (Resolved, error)
	// ReplaceFeedHolidays records a good read of the feed read.Feed of the
	// calendar id: it replaces the holidays of the feed with read.Holidays
	// and the status of the feed with read.Status, with Status.Holidays set
	// to the number of holidays, in one transaction. It returns ErrNotFound for
	// an unknown calendar or feed and for a fence that does not belong to
	// the scope of the calendar, ErrConflict when read.URL is not the
	// stored URL of the feed (an update changed the URL during the read),
	// ErrInvalid for more than MaxHolidays holidays, for a holiday that
	// fails Holiday.Validate or is not of SourceFeed with the feed
	// read.Feed, and for a status with an error, and namespace.ErrFenced
	// for a stale fence.
	ReplaceFeedHolidays(ctx context.Context, id ID, read FeedRead, fence namespace.Fence) error
	// RecordFeedStatus records a read of the feed read.Feed of the
	// calendar id that gave no new holidays: a read that failed
	// (read.Status.Error holds the class), or a read that answered
	// ErrNotModified (read.Status.Error is empty). It sets ReadAt and Error
	// of the stored status from read.Status, and keeps the holidays of the
	// feed and the Holidays, Skipped and ETag of the stored status. It
	// returns ErrNotFound for an unknown calendar or feed and for a fence
	// that does not belong to the scope of the calendar, ErrConflict when
	// read.URL is not the stored URL of the feed, ErrInvalid for an error
	// that is not a valid FeedError and for a read with holidays, and
	// namespace.ErrFenced for a stale fence.
	RecordFeedStatus(ctx context.Context, id ID, read FeedRead, fence namespace.Fence) error
}

// FeedRead is one read of one feed: a good read for
// Store.ReplaceFeedHolidays, or a read with no new holidays for
// Store.RecordFeedStatus.
type FeedRead struct {
	// Feed is the ID of the feed.
	Feed string
	// URL is the URL that the read fetched. The store refuses the read
	// when the feed now has another URL, so that the holidays or the
	// status of an old URL never go to a new one.
	URL string
	// Holidays are the holidays of the read, of SourceFeed with the ID
	// Feed.
	Holidays []Holiday
	// Status is the status of the read, with no error.
	Status FeedStatus
}

// DateRange is a range of dates, From included and To excluded.
type DateRange struct {
	From Date
	To   Date
}

// FetchResult is what one good read of a feed gives.
type FetchResult struct {
	// Holidays are the holidays of the feed in the window, one for each
	// date, of SourceFeed with the ID of the feed.
	Holidays []Holiday
	// Skipped counts the events that the source did not import: timed
	// events, canceled events, and events outside the window.
	Skipped int
	// ETag is the validator of the answer, for the next conditional read.
	ETag string
	// ReadAt is the time of the read.
	ReadAt time.Time
}

// HolidaySource reads holidays from an iCal feed (RFC 5545).
//
// Egress: an adapter reads the feed URL with a client of the egresshttp
// adapter, made with egress.SameHost and egress.HTTPSOnly. So it follows a
// redirect only to the same host and never to http, sends no user
// information and no other credential, dials no address that
// egress.CheckAddr refuses (this check runs on the resolved address of
// each connection), reads at most egress.MaxBodyBytes after decompression,
// and stops after egress.Timeout. The server also runs egress.CheckURL
// (through Calendar.Validate) when a principal creates or changes a feed,
// and on each test of a feed through the API.
//
// Import rules:
//
//   - An event with a DTSTART of the type DATE is a whole-day event. Its
//     last day is the day before DTEND, or DTSTART plus DURATION less one
//     day. With neither, the event is one day.
//   - An event with a time of day (a DTSTART of the type DATE-TIME) is
//     skipped and counted, with one exception: an event from 00:00 to
//     00:00 of a later day (as some calendar programs write a whole-day
//     event) is a whole-day event of those dates. The source reads the
//     time in the zone of its TZID, in UTC when it ends in "Z", and as a
//     wall clock time when it is a floating time.
//   - RRULE and RDATE add occurrences in the window; EXDATE removes them.
//     An event with a RECURRENCE-ID replaces the occurrence of that date.
//   - An event or an occurrence that its STATUS property marks as
//     canceled is skipped and counted.
//   - Two events on one date give one holiday, with the first name in the
//     order of the feed.
//   - More than MaxHolidays holidays in the window is ErrFeedInvalid.
type HolidaySource interface {
	// Fetch reads feed and returns the holidays in window. It sends the
	// ETag of the last good read, when feed.Status holds one. It returns
	// ErrNotModified when the feed did not change (a 304 answer),
	// ErrFeedRefused for a URL or an address that the egress rules refuse
	// (the error also matches egress.ErrRefused), ErrFeedUnreachable for a
	// host that it cannot resolve or reach, ErrFeedTimeout after
	// egress.Timeout, ErrFeedHTTPStatus for a status other than 200 and
	// 304, ErrFeedTooLarge above egress.MaxBodyBytes, and ErrFeedInvalid
	// for a body that is not iCal. FeedErrorOf gives the class that the
	// store records.
	Fetch(ctx context.Context, feed Feed, window DateRange) (FetchResult, error)
}

// HolidaySource errors.
var (
	// ErrNotModified reports a feed that did not change since its last
	// good read.
	ErrNotModified = errors.New("feed not modified")
	// ErrFeedRefused reports a feed URL or address that the egress rules
	// refuse.
	ErrFeedRefused = errors.New("feed address refused")
	// ErrFeedUnreachable reports a feed host that the source cannot
	// resolve or reach.
	ErrFeedUnreachable = errors.New("feed unreachable")
	// ErrFeedTimeout reports a read that took longer than egress.Timeout.
	ErrFeedTimeout = errors.New("feed read timed out")
	// ErrFeedHTTPStatus reports an answer with a status other than 200 or
	// 304.
	ErrFeedHTTPStatus = errors.New("feed answered with an error status")
	// ErrFeedTooLarge reports a feed above egress.MaxBodyBytes.
	ErrFeedTooLarge = errors.New("feed too large")
	// ErrFeedInvalid reports a feed body that is not iCal, or that gives
	// more than MaxHolidays holidays.
	ErrFeedInvalid = errors.New("feed is not valid iCal")
)
