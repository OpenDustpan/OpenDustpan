package trigger

import (
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/cron"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrNotDue reports a claim of a slot that the trigger does not owe: the slot
// is not after the last slot that a claim took, or it is later than the time of
// the claim. The error is the normal answer to the second instance that claims
// a slot that a first instance took.
var ErrNotDue = errors.New("trigger owes no run for this slot")

// slotWindows returns the spans that LatestSlot scans back from the time of the
// claim, the shortest first. The last one is the cycle of the calendar (28
// years of 366 days), inside which every schedule that cron.Parse accepts has a
// slot. A dense schedule finds its latest slot in the first window, so the work
// of a claim stays small.
func slotWindows() [4]time.Duration {
	return [4]time.Duration{24 * time.Hour, 8 * 24 * time.Hour, 366 * 24 * time.Hour, 28 * 366 * 24 * time.Hour}
}

// maxSlotSteps bounds the slots that one scan of LatestSlot counts. The span of
// the longest window holds fewer slots than this, because the shortest period
// is cron.MinInterval.
const maxSlotSteps = int(28*366*24*time.Hour/cron.MinInterval) + 1

// schedule parses the frequency of t for its unit. It returns an error that
// wraps ErrInvalid for a trigger that is not a schedule trigger.
func (t Trigger) schedule() (cron.Schedule, error) {
	if t.Kind != KindSchedule {
		return cron.Schedule{}, fmt.Errorf("%w: only a schedule trigger has slots", ErrInvalid)
	}

	expr, err := t.Frequency.Expression(t.Unit)
	if err != nil {
		return cron.Schedule{}, err
	}

	s, err := cron.Parse(expr)
	if err != nil {
		return cron.Schedule{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return s, nil
}

// NextSlot returns the first slot of the schedule trigger t after the time
// after, in UTC, on a whole minute. It returns an error that wraps ErrInvalid
// for a trigger that is not a valid schedule trigger. Whether t is on does not
// matter: the caller decides when a trigger has a next slot.
func NextSlot(t Trigger, after time.Time) (time.Time, error) {
	s, err := t.schedule()
	if err != nil {
		return time.Time{}, err
	}

	next, ok := s.Next(after)
	if !ok {
		return time.Time{}, fmt.Errorf("%w: the schedule has no slot", ErrInvalid)
	}

	return next, nil
}

// IsSlot reports whether slot is a time that the schedule of t gives: a whole
// minute in UTC that matches the frequency. A trigger that is not a valid
// schedule trigger has no slot.
func IsSlot(t Trigger, slot time.Time) bool {
	if slot.IsZero() || !slot.Equal(slot.UTC().Truncate(time.Minute)) {
		return false
	}

	next, err := NextSlot(t, slot.Add(-time.Minute))

	return err == nil && next.Equal(slot)
}

// LatestSlot returns the latest slot of t that is not after now, and false
// when there is none after from. from is the first slot that the trigger owes
// (the next slot that the store keeps). After an outage this is the one run
// that the catch-up starts: the slots between from and the result are skipped.
// It returns an error that wraps ErrInvalid for a trigger that is not a valid
// schedule trigger.
func LatestSlot(t Trigger, from, now time.Time) (time.Time, bool, error) {
	if _, err := t.schedule(); err != nil {
		return time.Time{}, false, err
	}

	for _, w := range slotWindows() {
		start, last := now.Add(-w), false
		if !from.Before(start) {
			start, last = from, true
		}

		latest, ok, err := scanSlots(t, start, now)
		if err != nil {
			return time.Time{}, false, err
		}

		if ok || last {
			return latest, ok, nil
		}
	}

	return time.Time{}, false, nil
}

// scanSlots returns the latest slot of t in the span from start to now, both
// ends included, and false when the span holds none.
func scanSlots(t Trigger, start, now time.Time) (time.Time, bool, error) {
	var latest time.Time

	cur := start.Add(-time.Minute)

	for range maxSlotSteps {
		next, err := NextSlot(t, cur)
		if err != nil {
			return time.Time{}, false, err
		}

		if next.After(now) {
			break
		}

		latest, cur = next, next
	}

	return latest, !latest.IsZero(), nil
}

// MaxClaimSkew is how far after the clock of the store a claim may name a slot.
// It is the room that two clocks need to agree. A slot later than the clock of
// the store plus MaxClaimSkew is not due, so one instance with a clock that runs
// ahead cannot move the next slot into the future and stop the schedule.
const MaxClaimSkew = 2 * time.Minute

// Claim names one slot of a schedule trigger that an instance takes. The store
// decides from its own clock whether the slot is due: the caller gives no time.
type Claim struct {
	// Namespace is the namespace of the trigger.
	Namespace namespace.Name
	// Trigger is the ID of the schedule trigger.
	Trigger ID
	// Slot is the slot to take. It is a time that the schedule gives (IsSlot).
	Slot time.Time
}

// Validate returns an error that wraps ErrInvalid when c is not valid. The
// store checks that Slot is a time that the schedule of the stored trigger gives
// and that it is not later than its clock plus MaxClaimSkew.
func (c Claim) Validate() error {
	if _, err := namespace.ParseName(string(c.Namespace)); err != nil {
		return fmt.Errorf("%w: namespace: %w", ErrInvalid, err)
	}

	if err := c.Trigger.Validate(); err != nil {
		return err
	}

	switch {
	case c.Slot.IsZero():
		return fmt.Errorf("%w: a claim names a slot", ErrInvalid)
	case !c.Slot.Equal(c.Slot.UTC().Truncate(time.Minute)):
		return fmt.Errorf("%w: a slot is a whole minute", ErrInvalid)
	default:
		return nil
	}
}

// CheckClaim applies the rules of a claim to the stored trigger t, whose next
// slot is next, at the time now of the clock of the store. It returns the slot
// that follows c.Slot, which becomes the next slot. It returns:
//   - ErrInvalid when c fails Validate, when t is not a valid schedule trigger,
//     when next is the zero time (an enabled schedule trigger always has a
//     slot), and when c.Slot is not a time that the schedule gives,
//   - ErrDisabled when t is off,
//   - ErrInvalid when c.Slot is later than now plus MaxClaimSkew,
//   - ErrNotDue when c.Slot is before next: a claim took that slot or a later
//     slot already.
//
// Every store adapter calls it in the transaction of ClaimDue, over the row
// that it reads in that transaction, so all adapters apply one rule. It is a
// layer of its own: the dispatcher checks the slot again before it starts a
// plan, and the idempotency key of the start is a third layer.
func CheckClaim(c Claim, t Trigger, next, now time.Time) (time.Time, error) {
	if err := c.Validate(); err != nil {
		return time.Time{}, err
	}

	if t.Unit.Namespace != c.Namespace || t.ID != c.Trigger {
		return time.Time{}, fmt.Errorf("%w: the trigger is not the one that the claim names", ErrInvalid)
	}

	if err := t.Validate(); err != nil {
		return time.Time{}, err
	}

	if t.Kind != KindSchedule {
		return time.Time{}, fmt.Errorf("%w: only a schedule trigger has slots", ErrInvalid)
	}

	if !t.Enabled {
		return time.Time{}, ErrDisabled
	}

	// An enabled schedule trigger always has a next slot: Put sets it, and the
	// store refuses a row without one. A zero value here is a row that fails its
	// checks, so the claim fails closed.
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("%w: the trigger has no next slot", ErrInvalid)
	}

	if !IsSlot(t, c.Slot) {
		return time.Time{}, fmt.Errorf("%w: the time is not a slot of the schedule", ErrInvalid)
	}

	// Defense in depth: the clock is the one of the store, in the transaction of
	// the claim. A slot far in the future would move the next slot there and stop
	// the schedule, so the claim fails closed, whatever the caller says.
	if c.Slot.After(now.Add(MaxClaimSkew)) {
		return time.Time{}, fmt.Errorf("%w: the slot is later than the clock of the store", ErrInvalid)
	}

	if c.Slot.Before(next) {
		return time.Time{}, ErrNotDue
	}

	return NextSlot(t, c.Slot)
}

// PutSlot returns the next slot that a Put of t at the time at records. It is
// the zero time for a trigger that is not an enabled schedule trigger. A replace
// of an enabled schedule trigger that keeps its frequency keeps prevNext, so a
// save that changes nothing about the schedule never skips or repeats a slot.
// A new trigger, a trigger that was off, a trigger with another frequency, and a
// trigger with a zero prevNext get the first slot after at. prev is the stored
// trigger that t replaces, and nil when there is none. Every store adapter calls
// it in the transaction of Put.
func PutSlot(t Trigger, at time.Time, prev *Trigger, prevNext time.Time) (time.Time, error) {
	if t.Kind != KindSchedule || !t.Enabled {
		return time.Time{}, nil
	}

	if prev != nil && prev.Kind == KindSchedule && prev.Enabled && prev.Frequency == t.Frequency && !prevNext.IsZero() {
		return prevNext, nil
	}

	return NextSlot(t, at)
}
