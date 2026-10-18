package freeze

import (
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/calendar"
)

// Occurrences returns the occurrences of s that overlap the range that
// starts at from and ends at to, ordered by start, with no two that overlap
// or touch. It does not cut an occurrence at from or to: for a calendar
// rule it reads back and forward over each day that the rule freezes
// whole, for at most calendar.MaxSearchDays days in each direction. So
// the start and the end of an occurrence stay the same at each call
// while the calendar does not change. cal is the calendar that
// applies to the namespace. Only KindCalendar reads it. It returns
// ErrInvalid for a schedule that fails Validate, and the errors of the
// calendar for a calendar rule.
func (s Schedule) Occurrences(cal calendar.Calendar, from, to time.Time) ([]Occurrence, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}

	if !s.Enabled || !to.After(from) {
		return nil, nil
	}

	var (
		spans []span
		err   error
	)

	switch s.Kind {
	case KindWindow:
		spans = []span{{start: s.Window.Start, end: s.Window.End}}
	case KindWeekly:
		spans, err = s.Weekly.spans(from, to)
	case KindCalendar:
		spans, err = s.Calendar.spans(cal, from, to)
	default:
		// Validate refuses every other kind.
	}

	if err != nil {
		return nil, err
	}

	return s.keep(merge(spans), from, to), nil
}

// Plan returns the occurrences of s to write at now: those that overlap
// the range from now to now plus Horizon. It returns the errors of
// Occurrences.
func (s Schedule) Plan(cal calendar.Calendar, now time.Time) ([]Occurrence, error) {
	return s.Occurrences(cal, now, now.Add(Horizon))
}

// NoticeAt returns the time of the notification that announces o.
func (s Schedule) NoticeAt(o Occurrence) time.Time {
	n := s.Notice
	if n == 0 {
		n = DefaultNotice
	}

	return o.Start.Add(-n)
}

// span is a time range, start included and end excluded.
type span struct {
	start time.Time
	end   time.Time
}

func (s Schedule) validateRule() error {
	switch s.Kind {
	case KindWindow:
		if s.Window.Start.IsZero() || !s.Window.End.After(s.Window.Start) {
			return fmt.Errorf("%w: a window needs a start and an end after it", ErrInvalid)
		}
	case KindWeekly:
		return s.Weekly.validate()
	case KindCalendar:
		return s.Calendar.validate()
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalid, s.Kind)
	}

	return nil
}

// keep returns the spans that overlap the range that starts at from and
// ends at to, and inside the range of
// the schedule, as occurrences.
func (s Schedule) keep(spans []span, from, to time.Time) []Occurrence {
	var out []Occurrence

	for _, sp := range spans {
		if !sp.end.After(from) || !sp.start.Before(to) {
			continue
		}

		if sp.end.Before(s.From) || sp.end.Equal(s.From) || (!s.Until.IsZero() && !sp.start.Before(s.Until)) {
			continue
		}

		out = append(out, Occurrence{Schedule: s.ID, Start: sp.start.UTC(), End: sp.end.UTC()})
	}

	return out
}

func (w Weekly) validate() error {
	if w.Days.Empty() {
		return fmt.Errorf("%w: a weekly rule needs at least one day", ErrInvalid)
	}

	if w.Start < 0 || w.Start >= calendar.MinutesPerDay {
		return fmt.Errorf("%w: a weekly start is a time of day", ErrInvalid)
	}

	if w.Length <= 0 || w.Length > MaxWeeklyLength || w.Length%time.Minute != 0 {
		return fmt.Errorf("%w: a weekly length is whole minutes, above 0 and at most %s",
			ErrInvalid, MaxWeeklyLength)
	}

	if gap := w.minGap(); w.Length+time.Hour > gap {
		return fmt.Errorf("%w: occurrences must not overlap: the length is at most %s for these days",
			ErrInvalid, gap-time.Hour)
	}

	if _, err := w.location(); err != nil {
		return err
	}

	return nil
}

// location loads w.Zone with calendar.LoadZone. It refuses the empty zone
// and each name that follows the zone of the host, for example "Local" or
// "localtime".
func (w Weekly) location() (*time.Location, error) {
	loc, err := calendar.LoadZone(w.Zone)
	if err != nil {
		return nil, fmt.Errorf("%w: a weekly rule needs an IANA zone: %w", ErrInvalid, err)
	}

	return loc, nil
}

// minGap returns the shortest time from one day of w.Days to the next day
// of the set, one week for a single day. The extra hour of validate keeps
// one hour between two occurrences on the local clock, so that the choice
// of an instant for a local time never makes two occurrences overlap.
func (w Weekly) minGap() time.Duration {
	const day = 24 * time.Hour

	gap := daysPerWeek * day

	for d := range daysPerWeek {
		if !w.Days.Has(time.Weekday(d)) {
			continue
		}

		for n := 1; n <= daysPerWeek; n++ {
			if w.Days.Has(time.Weekday((d + n) % daysPerWeek)) {
				gap = min(gap, time.Duration(n)*day)

				break
			}
		}
	}

	return gap
}

const daysPerWeek = 7

// spans returns the weekly occurrences whose start is from one week before
// from up to to. Both the start and the end are local times in the zone.
func (w Weekly) spans(from, to time.Time) ([]span, error) {
	loc, err := w.location()
	if err != nil {
		return nil, err
	}

	var out []span

	length := int(w.Length / time.Minute)
	last := calendar.DateOf(to, loc)

	for d := calendar.DateOf(from.Add(-MaxWeeklyLength-day), loc); !last.Before(d); d = d.AddDays(1) {
		if !w.Days.Has(d.Weekday()) {
			continue
		}

		out = append(out, span{
			start: localTime(d, int(w.Start), loc),
			end:   localTime(d, int(w.Start)+length, loc),
		})
	}

	return out, nil
}

const day = 24 * time.Hour

// localTime returns the instant of the local time minutes after the start
// of the date d in loc. minutes can be more than one day: the local clock
// goes on over the next dates. A local time that occurs two times is the
// first of the two instants. A local time that a daylight saving change
// skips moves forward by the length of the gap.
func localTime(d calendar.Date, minutes int, loc *time.Location) time.Time {
	wall := time.Date(d.Year, d.Month, d.Day, 0, minutes, 0, 0, time.UTC)

	var (
		first time.Time
		found bool
	)

	// The offsets in effect one day before and after the local time
	// include both offsets of each change near it.
	for _, probe := range [...]time.Duration{-day, 0, day} {
		_, offset := wall.Add(probe).In(loc).Zone()

		t := wall.Add(-time.Duration(offset) * time.Second)
		if sameWall(t.In(loc), wall) && (!found || t.Before(first)) {
			first, found = t, true
		}
	}

	if found {
		return first
	}

	// The change skips the local time. The offset before the change gives
	// the instant that is the local time moved forward by the gap.
	_, offset := wall.Add(-day).In(loc).Zone()

	return wall.Add(-time.Duration(offset) * time.Second)
}

// sameWall reports whether t shows the date, the hour and the minute of
// wall.
func sameWall(t, wall time.Time) bool {
	y, m, d := t.Date()
	wy, wm, wd := wall.Date()

	return y == wy && m == wm && d == wd && t.Hour() == wall.Hour() && t.Minute() == wall.Minute()
}

// spans returns the periods of the rule on each day from firstDay to
// lastDay, in the zone of cal.
func (r CalendarRule) spans(cal calendar.Calendar, from, to time.Time) ([]span, error) {
	loc, err := cal.Location()
	if err != nil {
		return nil, fmt.Errorf("calendar rule: %w", err)
	}

	before := r.daysBeforeHolidays(cal, from, to, loc)

	var out []span

	last := r.lastDay(cal, calendar.DateOf(to, loc), before)
	for d := r.firstDay(cal, calendar.DateOf(from, loc), before); !last.Before(d); d = d.AddDays(1) {
		dayStart := localTime(d, 0, loc)
		dayEnd := localTime(d.AddDays(1), 0, loc)

		switch {
		case r.whole(cal, d, before):
			out = append(out, span{start: dayStart, end: dayEnd})
		case r.OutsideHours && cal.IsBusinessDay(d):
			out = append(out,
				span{start: dayStart, end: localTime(d, int(cal.DayStart), loc)},
				span{start: localTime(d, int(cal.DayEnd), loc), end: dayEnd})
		default:
			// The rule does not freeze this day.
		}
	}

	return out, nil
}

// firstDay returns the first day to read for a range that starts on the
// date from. It goes back over each day that the rule freezes whole, and
// one day more, so that an occurrence that began before from keeps its
// start at each call.
func (r CalendarRule) firstDay(cal calendar.Calendar, from calendar.Date, before []calendar.Date) calendar.Date {
	d := from.AddDays(-1)

	for range calendar.MaxSearchDays {
		if !r.whole(cal, d, before) {
			return d
		}

		d = d.AddDays(-1)
	}

	return d
}

// lastDay returns the last day to read for a range that ends on the date
// to. It goes forward over each day that the rule freezes whole, and one
// day more, so that an occurrence that goes on after to keeps its end, for
// example the morning before the working hours after a long holiday.
func (r CalendarRule) lastDay(cal calendar.Calendar, to calendar.Date, before []calendar.Date) calendar.Date {
	d := to.AddDays(1)

	for range calendar.MaxSearchDays {
		if !r.whole(cal, d, before) {
			return d
		}

		d = d.AddDays(1)
	}

	return d
}

// whole reports whether the rule freezes the whole day d.
func (r CalendarRule) whole(cal calendar.Calendar, d calendar.Date, before []calendar.Date) bool {
	_, holiday := cal.IsHoliday(d)
	dayOff := !cal.WorkingDays.Has(d.Weekday())

	return r.Holidays && holiday || r.DaysOff && dayOff || slices.Contains(before, d)
}

// daysBeforeHolidays returns the last r.DaysBefore business days before
// each holiday near the range.
func (r CalendarRule) daysBeforeHolidays(
	cal calendar.Calendar, from, to time.Time, loc *time.Location,
) []calendar.Date {
	if r.DaysBefore == 0 {
		return nil
	}

	first := calendar.DateOf(from, loc).AddDays(-calendar.MaxSearchDays)
	last := calendar.DateOf(to, loc).AddDays(calendar.MaxSearchDays)

	var out []calendar.Date

	for _, h := range cal.Holidays {
		if h.Date.Before(first) || last.Before(h.Date) {
			continue
		}

		d, n := h.Date.AddDays(-1), 0
		for i := 0; n < r.DaysBefore && i < calendar.MaxSearchDays; i++ {
			if cal.IsBusinessDay(d) {
				out = append(out, d)
				n++
			}

			d = d.AddDays(-1)
		}
	}

	return out
}

// merge sorts spans by start and joins the spans that overlap or touch.
func merge(spans []span) []span {
	slices.SortFunc(spans, func(a, b span) int { return a.start.Compare(b.start) })

	out := make([]span, 0, len(spans))

	for _, sp := range spans {
		if !sp.end.After(sp.start) {
			continue
		}

		if n := len(out); n > 0 && !sp.start.After(out[n-1].end) {
			if sp.end.After(out[n-1].end) {
				out[n-1].end = sp.end
			}

			continue
		}

		out = append(out, sp)
	}

	return out
}

func (r CalendarRule) validate() error {
	if !r.DaysOff && !r.Holidays && !r.OutsideHours && r.DaysBefore == 0 {
		return fmt.Errorf("%w: a calendar rule needs at least one period", ErrInvalid)
	}

	if r.DaysBefore < 0 || r.DaysBefore > MaxDaysBefore {
		return fmt.Errorf("%w: days before a holiday are 0 to %d", ErrInvalid, MaxDaysBefore)
	}

	return nil
}
