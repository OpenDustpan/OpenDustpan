// Package drift is the tool-neutral model of drift detection: the result of
// one drift check, the status that follows from it, the schedule setting that
// turns the checks on, and the ports that store them.
//
// A drift check is a job of the operation refresh with one phase, plan. It
// changes nothing. The runner reduces its plan to a list of resource
// addresses, and this package keeps that list. The package never holds an
// attribute value and never reads the state of a tool, so it stays apart from
// the models of the tools.
//
// The status values are final. Their wire spelling is the work of the unit
// status vocabulary and can change.
package drift

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
)

// Sentinel errors of the drift model and its ports.
var (
	// ErrInvalid reports a value that fails validation: a result, a setting or
	// a scope. The error text names the field.
	ErrInvalid = errors.New("invalid drift value")
	// ErrNotFound reports that no latest drift result or no setting exists for
	// the object that a call names. A reader treats a missing result as the
	// status StatusUnknown.
	ErrNotFound = errors.New("drift value not found")
	// ErrStale reports a result that is not newer than the stored one. The
	// store changes nothing.
	ErrStale = errors.New("drift result is not newer than the stored result")
	// ErrDisabled reports a scheduled check for a unit whose policy is off.
	// The job service starts nothing.
	ErrDisabled = errors.New("drift checks are off for the unit")
	// ErrJobMismatch reports a result whose job does not belong to the unit,
	// is not a drift check, or did not succeed. The store changes nothing.
	ErrJobMismatch = errors.New("job is not a finished drift check of the unit")
)

// Bounds of a drift result.
const (
	// MaxResources is the most addresses in one result. A runner that finds
	// more cuts the list and sets Truncated.
	MaxResources = 1000
	// MaxAddressBytes is the longest address.
	MaxAddressBytes = 1024
)

// Status is the drift status of a unit. The values follow the status words
// that the owner decided for the unit status vocabulary. The wire spelling is
// not final.
type Status string

// The drift statuses.
const (
	// StatusDrifted means that the latest check found at least one difference.
	StatusDrifted Status = "drifted"
	// StatusInSync means that the latest check found no difference.
	StatusInSync Status = "in-sync"
	// StatusUnknown means that no check ran, or that a later change made the
	// latest result old.
	StatusUnknown Status = "unknown"
)

// Statuses returns the drift statuses, in the order of the design table.
// Each call returns a new slice.
func Statuses() []Status {
	return []Status{StatusDrifted, StatusInSync, StatusUnknown}
}

// Valid reports whether s is a drift status.
func (s Status) Valid() bool {
	return slices.Contains(Statuses(), s)
}

// Kind is the kind of difference at one address. A resource that no state
// holds is not drift. It is an unmanaged resource.
type Kind string

// The kinds of difference.
const (
	// KindChanged means that the real resource differs from the state.
	KindChanged Kind = "changed"
	// KindDeleted means that the real resource is gone.
	KindDeleted Kind = "deleted"
)

// Kinds returns the kinds of difference. Each call returns a new slice.
func Kinds() []Kind {
	return []Kind{KindChanged, KindDeleted}
}

// Valid reports whether k is a kind of difference.
func (k Kind) Valid() bool {
	return slices.Contains(Kinds(), k)
}

// Resource is one resource that differs. Address is the string that the tool
// uses for the resource, for example "aws_s3_bucket.logs" or
// "urn:pulumi:prod::web::aws:s3/bucket:Bucket::logs". The package keeps it
// opaque. It holds no attribute name and no attribute value.
type Resource struct {
	Address string
	Kind    Kind
}

// Result is what one finished drift check reports. A runner produces it, so
// the store treats it as untrusted input and calls Validate first.
type Result struct {
	// CheckedAt is the end of the plan phase of the job. The server sets it
	// from the job and never takes it from the runner, because a runner is not
	// trusted. The store refuses a time outside the run of the job.
	CheckedAt time.Time
	Unit      graph.Unit
	JobID     job.ID
	Resources []Resource
	// Truncated is true when the runner cut Resources at MaxResources.
	Truncated bool
}

// Status returns the drift status that the result gives: StatusDrifted when
// it lists a resource, else StatusInSync.
func (r Result) Status() Status {
	if len(r.Resources) > 0 {
		return StatusDrifted
	}

	return StatusInSync
}

// Validate reports whether r is a valid result, with an allowlist, and rejects
// a value that is not valid. It never cleans a value. It returns an error
// that wraps ErrInvalid.
func (r Result) Validate() error {
	if err := r.Unit.Validate(); err != nil {
		return fmt.Errorf("%w: unit: %w", ErrInvalid, err)
	}

	if !validJobID(r.JobID) {
		return fmt.Errorf("%w: job ID", ErrInvalid)
	}

	if r.CheckedAt.IsZero() {
		return fmt.Errorf("%w: checked time", ErrInvalid)
	}

	if len(r.Resources) > MaxResources {
		return fmt.Errorf("%w: %d resources, the limit is %d", ErrInvalid, len(r.Resources), MaxResources)
	}

	// A runner cuts the list only at the limit, so a shorter list cannot be
	// truncated.
	if r.Truncated && len(r.Resources) != MaxResources {
		return fmt.Errorf("%w: a truncated result lists %d resources, not %d",
			ErrInvalid, len(r.Resources), MaxResources)
	}

	seen := make(map[string]bool, len(r.Resources))

	for i, res := range r.Resources {
		if err := res.validate(); err != nil {
			return fmt.Errorf("resource %d: %w", i, err)
		}

		if seen[res.Address] {
			return fmt.Errorf("%w: resource %d: address listed twice", ErrInvalid, i)
		}

		seen[res.Address] = true
	}

	return nil
}

// validate reports whether res has a valid address and a known kind.
func (res Resource) validate() error {
	if !res.Kind.Valid() {
		return fmt.Errorf("%w: kind %q", ErrInvalid, res.Kind)
	}

	return validateAddress(res.Address)
}

// validateAddress reports whether address is a valid UTF-8 string of 1 to
// MaxAddressBytes bytes with no control character, no format character and no
// line or paragraph separator. Format characters include the bidirectional
// controls and the zero-width characters that make one address look like
// another.
func validateAddress(address string) error {
	switch {
	case address == "" || len(address) > MaxAddressBytes:
		return fmt.Errorf("%w: address length %d", ErrInvalid, len(address))
	case !utf8.ValidString(address):
		return fmt.Errorf("%w: address is not valid UTF-8", ErrInvalid)
	}

	return hiddenRuneError("address", address)
}

// validateText reports whether text is valid UTF-8 with no control character,
// no format character and no line or paragraph separator. An empty text is
// valid.
func validateText(text string) error {
	if !utf8.ValidString(text) {
		return fmt.Errorf("%w: text is not valid UTF-8", ErrInvalid)
	}

	return hiddenRuneError("text", text)
}

// hiddenRuneError returns an error for the first rune of s that is a control
// character, a format character, a line separator or a paragraph separator.
// It returns nil for a string with no such rune.
func hiddenRuneError(what, s string) error {
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Zl, r):
			return fmt.Errorf("%w: %s has a line separator", ErrInvalid, what)
		case unicode.Is(unicode.Zp, r):
			return fmt.Errorf("%w: %s has a paragraph separator", ErrInvalid, what)
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return fmt.Errorf("%w: %s has a control or format character", ErrInvalid, what)
		}
	}

	return nil
}

// validJobID reports whether id has the prefix of a job ID and a body.
func validJobID(id job.ID) bool {
	body, ok := strings.CutPrefix(string(id), job.IDPrefix)

	return ok && body != "" && len(id) <= 100
}

// Latest is the latest drift result of a unit and the status that follows
// from it. It is what the inventory projection, the context read API and the
// gate facts read.
type Latest struct {
	CheckedAt time.Time
	Unit      graph.Unit
	JobID     job.ID
	Status    Status
	Resources []Resource
	Truncated bool
}

// ResourceCount returns the number of listed addresses. It is the field
// resources of the fact group drift of the gate model.
func (l Latest) ResourceCount() int {
	return len(l.Resources)
}

// Age returns the time from the check to now, never below zero. It is the
// field ageSeconds of the fact group drift.
func (l Latest) Age(now time.Time) time.Duration {
	return max(now.Sub(l.CheckedAt), 0)
}

// Transition is the change of the drift status that a stored result made. The
// store returns it from Record. The store also appends the notification of
// the change in the transaction of Record, so the caller sends nothing.
type Transition struct {
	At    time.Time
	Unit  graph.Unit
	JobID job.ID
	From  Status
	To    Status
}

// Changed reports whether the status changed.
func (t Transition) Changed() bool {
	return t.From != t.To
}

// BecameDrifted reports whether the unit was not drifted and now is. It is the
// condition of the notification type unit.drifted.
func (t Transition) BecameDrifted() bool {
	return t.To == StatusDrifted && t.From != StatusDrifted
}

// Cleared reports whether a clean check ended a drift. It is the condition of
// the notification type unit.drift-cleared.
func (t Transition) Cleared() bool {
	return t.From == StatusDrifted && t.To == StatusInSync
}
