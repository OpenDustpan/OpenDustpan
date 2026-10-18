package oncall

import (
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/calendar"
)

// Timeline returns the shifts of s in the range that starts at from and
// ends at to, ordered by start. It
// cuts the first and the last shift at the range. Two shifts of one person
// that touch become one. A time that nobody covers is a gap between two
// shifts. It returns ErrInvalid for a schedule that fails Validate, or for
// a range that is empty or longer than MaxRange.
func (s Schedule) Timeline(from, to time.Time) ([]Shift, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}

	if !to.After(from) || to.Sub(from) > MaxRange {
		return nil, fmt.Errorf("%w: a timeline covers more than 0 and at most %s", ErrInvalid, MaxRange)
	}

	loc, err := calendar.LoadZone(s.Zone)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	cuts := s.boundaries(from, to, loc)

	var out []Shift

	for i := range len(cuts) - 1 {
		sh, ok := s.pick(cuts[i], loc)
		if !ok {
			continue
		}

		sh.Start, sh.End = cuts[i], cuts[i+1]

		if len(out) > 0 {
			if last := &out[len(out)-1]; last.End.Equal(sh.Start) && same(*last, sh) {
				last.End = sh.End

				continue
			}
		}

		out = append(out, sh)
	}

	return out, nil
}

// At returns the shift that covers t, with its real start and end when
// they are less than MaxRange away. It returns ErrNobodyOnCall when nobody
// covers t, and the errors of Timeline.
func (s Schedule) At(t time.Time) (Shift, error) {
	after, err := s.Timeline(t, t.Add(MaxRange))
	if err != nil {
		return Shift{}, err
	}

	if len(after) == 0 || after[0].Start.After(t) {
		return Shift{}, fmt.Errorf("%w: schedule %s at %s", ErrNobodyOnCall, s.Name, t.UTC().Format(time.RFC3339))
	}

	cur := after[0]

	before, err := s.Timeline(t.Add(-MaxRange), t)
	if err != nil {
		return Shift{}, err
	}

	if len(before) > 0 {
		if last := before[len(before)-1]; last.End.Equal(t) && same(last, cur) {
			cur.Start = last.Start
		}
	}

	return cur, nil
}

// CurrentAndNext returns the shift at now and the next shift of another
// person after it. This is the line "you are on call until Monday 09:00,
// next: <person>". next is nil when nobody comes in MaxRange. It returns
// the errors of At.
func (s Schedule) CurrentAndNext(now time.Time) (Shift, *Shift, error) {
	cur, err := s.At(now)
	if err != nil {
		return Shift{}, nil, err
	}

	later, err := s.Timeline(cur.End, cur.End.Add(MaxRange))
	if err != nil {
		return Shift{}, nil, err
	}

	for i := range later {
		if !same(later[i], cur) {
			return cur, &later[i], nil
		}
	}

	return cur, nil, nil
}

// same reports whether a and b name the same person.
func same(a, b Shift) bool {
	if a.Principal != "" || b.Principal != "" {
		return a.Principal == b.Principal
	}

	return a.Display == b.Display
}

// pick returns the person on call at t, with no start or end: the last
// override that covers t, else the last layer that covers t, else the
// copied shift of an external schedule.
func (s Schedule) pick(t time.Time, loc *time.Location) (Shift, bool) {
	if sh, ok := s.pickOverride(t); ok {
		return sh, true
	}

	if s.Source == SourceExternal {
		return s.pickCopied(t)
	}

	return s.pickLayer(t, loc)
}

// pickOverride returns the person of the last override that covers t.
func (s Schedule) pickOverride(t time.Time) (Shift, bool) {
	for i := len(s.Overrides) - 1; i >= 0; i-- {
		if o := &s.Overrides[i]; !t.Before(o.Start) && t.Before(o.End) {
			return s.shift(o.Principal, OriginOverride), true
		}
	}

	var none Shift

	return none, false
}

// pickCopied returns the copied shift of an external schedule that covers
// t.
func (s Schedule) pickCopied(t time.Time) (Shift, bool) {
	for _, c := range s.Copied {
		if !t.Before(c.Start) && t.Before(c.End) {
			c.Schedule, c.Layer = s.ID, OriginExternal

			return c, true
		}
	}

	var none Shift

	return none, false
}

// pickLayer returns the person of the last layer that covers t.
func (s Schedule) pickLayer(t time.Time, loc *time.Location) (Shift, bool) {
	for _, l := range slices.Backward(s.Layers) {
		if !l.covers(t, loc) {
			continue
		}

		if tn, ok := l.turnAt(t, loc); ok {
			return s.shift(tn.member, l.Name), true
		}
	}

	var none Shift

	return none, false
}

// shift returns a shift of s for principal from the origin layer, with no
// start or end.
func (s Schedule) shift(principal, layer string) Shift {
	return Shift{
		Schedule: s.ID, Principal: principal, Display: "",
		Start: time.Time{}, End: time.Time{}, Layer: layer,
	}
}

// boundaries returns the sorted times in the range that starts at from
// and ends at to where the person on call may change, with from and to.
func (s Schedule) boundaries(from, to time.Time, loc *time.Location) []time.Time {
	cuts := []time.Time{from, to}
	add := func(t time.Time) {
		if t.After(from) && t.Before(to) {
			cuts = append(cuts, t)
		}
	}

	for i := range s.Overrides {
		add(s.Overrides[i].Start)
		add(s.Overrides[i].End)
	}

	for _, c := range s.Copied {
		add(c.Start)
		add(c.End)
	}

	for _, l := range s.Layers {
		add(l.Start)

		// Before the start of the layer there is no turn, so the walk
		// starts at the later of from and the start of the layer.
		first := from
		if l.Start.After(first) {
			first = l.Start
		}

		for tn, ok := l.turnAt(first, loc); ok && tn.end.Before(to); tn, ok = l.turnAt(tn.end, loc) {
			add(tn.end)
		}

		if l.Hours == nil {
			continue
		}

		last := calendar.DateOf(to, loc)
		for d := calendar.DateOf(from, loc); !last.Before(d); d = d.AddDays(1) {
			add(clockAt(d, l.Hours.From, loc))
			add(clockAt(d, l.Hours.To, loc))
		}
	}

	slices.SortFunc(cuts, func(a, b time.Time) int { return a.Compare(b) })

	return slices.CompactFunc(cuts, func(a, b time.Time) bool { return a.Equal(b) })
}

// covers reports whether the hours of l cover t. A layer with no hours
// covers every time.
func (l Layer) covers(t time.Time, loc *time.Location) bool {
	if l.Hours == nil {
		return true
	}

	lt := t.In(loc)
	if !l.Hours.Days.Has(lt.Weekday()) {
		return false
	}

	c := calendar.ClockOf(lt.Hour(), lt.Minute())

	return c >= l.Hours.From && c < l.Hours.To
}

// turn is the turn of one member of a layer, from start to end.
type turn struct {
	member string
	start  time.Time
	end    time.Time
}

// turnAt returns the turn that covers t. It returns false before the start
// of the layer. Turns follow the wall clock of loc: turn k starts at the
// wall time of Start plus k turns, resolved with inZone.
func (l Layer) turnAt(t time.Time, loc *time.Location) (turn, bool) {
	if t.Before(l.Start) {
		return turn{member: "", start: time.Time{}, end: time.Time{}}, false
	}

	k := max(int(wall(t, loc).Sub(wall(l.Start, loc))/l.Turn), 0)

	start := l.turnStart(k, loc)
	for k > 0 && start.After(t) {
		k--
		start = l.turnStart(k, loc)
	}

	end := l.turnStart(k+1, loc)
	for !end.After(t) {
		k++
		start, end = end, l.turnStart(k+1, loc)
	}

	return turn{member: l.Members[k%len(l.Members)], start: start, end: end}, true
}

// turnStart returns the start of turn k of l in loc.
func (l Layer) turnStart(k int, loc *time.Location) time.Time {
	if k == 0 {
		return l.Start
	}

	return inZone(wall(l.Start, loc).Add(time.Duration(k)*l.Turn), loc)
}

// inZone returns the instant of the wall clock w, given as a time in UTC,
// in loc. time.Date does not define the instant of a wall time that a
// change of the clock skips or repeats, so inZone resolves both itself:
//   - A wall time that a change to summer time skips resolves to the
//     instant of the change, the end of the skipped time. So a turn that
//     lies fully in the skipped time is empty.
//   - A wall time that a change to winter time repeats resolves to its
//     second occurrence. So the turn that ends there is longer, and the
//     turn that starts there is not.
func inZone(w time.Time, loc *time.Location) time.Time {
	// A zone changes its offset at most once in two days, so the offsets
	// of the two days around w hold every offset that w can have.
	const (
		span = 48 * time.Hour
		step = 24 * time.Hour
	)

	var best time.Time

	for d := -span; d <= span; d += step {
		_, off := w.Add(d).In(loc).Zone()

		c := w.Add(-time.Duration(off) * time.Second)
		if wall(c, loc).Equal(w) && c.After(best) {
			best = c
		}
	}

	if !best.IsZero() {
		return best
	}

	// No offset gives w, so the change of the clock skips it. The
	// candidate of the offset before the change lies after the change,
	// and its zone starts at the change.
	for d := -span; d <= span; d += step {
		_, off := w.Add(d).In(loc).Zone()

		c := w.Add(-time.Duration(off) * time.Second)
		if wall(c, loc).After(w) {
			if start, _ := c.In(loc).ZoneBounds(); !start.IsZero() && (best.IsZero() || start.Before(best)) {
				best = start
			}
		}
	}

	return best
}

// wall returns the wall clock of t in loc as a time in UTC, so that the
// difference of two wall clocks has no daylight saving change in it.
func wall(t time.Time, loc *time.Location) time.Time {
	lt := t.In(loc)

	return time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour(), lt.Minute(), lt.Second(), lt.Nanosecond(), time.UTC)
}

func clockAt(d calendar.Date, c calendar.Clock, loc *time.Location) time.Time {
	const minutesPerHour = 60

	return inZone(time.Date(d.Year, d.Month, d.Day, int(c)/minutesPerHour, int(c)%minutesPerHour, 0, 0, time.UTC), loc)
}
