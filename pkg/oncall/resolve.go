package oncall

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/calendar"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const day = 24 * time.Hour

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

	loc, err := time.LoadLocation(s.Zone)
	if err != nil {
		return nil, fmt.Errorf("%w: unknown zone %q", ErrInvalid, s.Zone)
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
	for _, o := range slices.Backward(s.Overrides) {
		if !t.Before(o.Start) && t.Before(o.End) {
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

	for _, o := range s.Overrides {
		add(o.Start)
		add(o.End)
	}

	for _, c := range s.Copied {
		add(c.Start)
		add(c.End)
	}

	for _, l := range s.Layers {
		add(l.Start)

		for tn, ok := l.turnAt(from, loc); ok && tn.end.Before(to); tn, ok = l.turnAt(tn.end, loc) {
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
// of the layer.
func (l Layer) turnAt(t time.Time, loc *time.Location) (turn, bool) {
	if t.Before(l.Start) {
		return turn{member: "", start: time.Time{}, end: time.Time{}}, false
	}

	var (
		k          int
		start, end time.Time
	)

	if l.Turn%day == 0 {
		days := int(l.Turn / day)
		s0 := l.Start.In(loc)
		d0 := calendar.DateOf(s0, loc)
		at := func(d calendar.Date) time.Time {
			return time.Date(d.Year, d.Month, d.Day, s0.Hour(), s0.Minute(), s0.Second(), 0, loc)
		}

		dt := calendar.DateOf(t, loc)

		n := daysBetween(d0, dt)
		if t.Before(at(dt)) {
			n--
		}

		k = n / days
		start, end = at(d0.AddDays(k*days)), at(d0.AddDays((k+1)*days))
	} else {
		k = int(t.Sub(l.Start) / l.Turn)
		start = l.Start.Add(time.Duration(k) * l.Turn)
		end = start.Add(l.Turn)
	}

	return turn{member: l.Members[k%len(l.Members)], start: start, end: end}, true
}

func daysBetween(a, b calendar.Date) int {
	ta := time.Date(a.Year, a.Month, a.Day, 0, 0, 0, 0, time.UTC)
	tb := time.Date(b.Year, b.Month, b.Day, 0, 0, 0, 0, time.UTC)

	return int(tb.Sub(ta) / day)
}

func clockAt(d calendar.Date, c calendar.Clock, loc *time.Location) time.Time {
	const minutesPerHour = 60

	return time.Date(d.Year, d.Month, d.Day, int(c)/minutesPerHour, int(c)%minutesPerHour, 0, 0, loc)
}

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
