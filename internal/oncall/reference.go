package oncall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Ref names an on-call schedule from the on-call field of a context.
type Ref struct {
	Namespace namespace.Name
	Name      string
}

// RefPrefix starts an on-call field that names a schedule.
const RefPrefix = "schedule:"

// ParseRef reads an on-call field. "schedule:<name>" names a schedule of
// the namespace ns of the object, and "schedule:<namespace>/<name>" a
// schedule of another namespace. Any other text is not a reference, and
// the context shows it as it is: ParseRef then returns false.
func ParseRef(field string, ns namespace.Name) (Ref, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(field), RefPrefix)
	if !ok {
		return noRef(), false
	}

	if n, name, found := strings.Cut(rest, "/"); found {
		parsed, err := namespace.ParseName(n)
		if err != nil || !validName(name) {
			return noRef(), false
		}

		return Ref{Namespace: parsed, Name: name}, true
	}

	if ns == "" || !validName(rest) {
		return noRef(), false
	}

	return Ref{Namespace: ns, Name: rest}, true
}

// noRef returns the Ref of a field that names no schedule.
func noRef() Ref {
	return Ref{Namespace: "", Name: ""}
}

// Getter reads one schedule. Store implements it.
type Getter interface {
	// Get returns the schedule ref names, or ErrNotFound.
	Get(ctx context.Context, ref Ref) (Schedule, error)
}

// CanRead reports whether the caller holds the read action of the
// namespace ns (namespace:read). The caller of Resolve gives it, from the
// access rules of the caller.
type CanRead func(ctx context.Context, ns namespace.Name) (bool, error)

// Status tells what Resolve found for an on-call field.
type Status string

// Values of Status.
const (
	// StatusText is a field that names no schedule. The context shows
	// the text as it is.
	StatusText Status = "text"
	// StatusResolved is a field that names a schedule, with the person
	// on call now.
	StatusResolved Status = "resolved"
	// StatusDenied is a field that names a schedule of another namespace
	// that the caller cannot read. The context shows the text as it is,
	// with no person.
	StatusDenied Status = "denied"
	// StatusMissing is a field that names a schedule that does not exist.
	// The context shows the text as it is, with a warning.
	StatusMissing Status = "missing"
	// StatusNobody is a field that names a schedule in which nobody is on
	// call now.
	StatusNobody Status = "nobody"
)

// Resolution is what an on-call field resolves to. Ref is set for every
// status except StatusText. Current and Next are set for StatusResolved
// only; Next is nil when nobody comes in MaxRange. Stale is true for the
// copy of an external schedule that is older than StaleAfter, and Synced
// is then the time of that copy.
type Resolution struct {
	Ref     Ref
	Status  Status
	Current *Shift
	Next    *Shift
	Stale   bool
	Synced  time.Time
}

// Resolver resolves on-call fields for one caller. Get reads the
// schedules. CanRead answers the access rules of the caller; a nil
// CanRead answers false for every namespace.
type Resolver struct {
	Get     Getter
	CanRead CanRead
}

// Resolve resolves the on-call field of an object of the namespace own at
// now. A schedule of own resolves for every caller, as the on-call text
// does. A schedule of another namespace resolves only when CanRead
// answers true for that namespace; else Resolve answers StatusDenied and
// does not read the schedule. Resolve returns the errors of CanRead and
// of Get other than ErrNotFound, and the errors of CurrentAndNext other
// than ErrNobodyOnCall.
func (r Resolver) Resolve(ctx context.Context, field string, own namespace.Name, now time.Time) (Resolution, error) {
	ref, ok := ParseRef(field, own)
	if !ok {
		return Resolution{
			Ref:     noRef(),
			Status:  StatusText,
			Current: nil,
			Next:    nil,
			Stale:   false,
			Synced:  time.Time{},
		}, nil
	}

	res := Resolution{Ref: ref, Status: StatusDenied, Current: nil, Next: nil, Stale: false, Synced: time.Time{}}

	if ref.Namespace != own {
		if r.CanRead == nil {
			return res, nil
		}

		allowed, err := r.CanRead(ctx, ref.Namespace)
		if err != nil {
			return res, fmt.Errorf("resolve %s/%s: %w", ref.Namespace, ref.Name, err)
		}

		if !allowed {
			return res, nil
		}
	}

	s, err := r.Get.Get(ctx, ref)
	if errors.Is(err, ErrNotFound) {
		res.Status = StatusMissing

		return res, nil
	}

	if err != nil {
		return res, fmt.Errorf("resolve %s/%s: %w", ref.Namespace, ref.Name, err)
	}

	if s.Source == SourceExternal && now.Sub(s.Synced) > StaleAfter {
		res.Stale, res.Synced = true, s.Synced
	}

	cur, next, err := s.CurrentAndNext(now)
	if errors.Is(err, ErrNobodyOnCall) {
		res.Status = StatusNobody

		return res, nil
	}

	if err != nil {
		return res, fmt.Errorf("resolve %s/%s: %w", ref.Namespace, ref.Name, err)
	}

	res.Status, res.Current, res.Next = StatusResolved, &cur, next

	return res, nil
}
