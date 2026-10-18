package freeze

import (
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/calendar"
)

// Occurrences returns the occurrences of s that overlap the range that
// starts at from and ends at to, ordered by start, with no two that overlap or touch. It does
// not cut an occurrence at from or to, so the start of an occurrence that
// began before from stays the same at each call. cal is the calendar that
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

// NoticeAt returns the time of the event that announces o.
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

	if w.Length <= 0 || w.Length > MaxWeeklyLength {
		return fmt.Errorf("%w: a weekly length is above 0 and at most %s", ErrInvalid, MaxWeeklyLength)
	}

	if gap := w.minGap(); w.Length+time.Hour > gap {
		return fmt.Errorf("%w: occurrences must not overlap: the length is at most %s for these days",
			ErrInvalid, gap-time.Hour)
	}

	if _, err := time.LoadLocation(w.Zone); err != nil || w.Zone == "" {
		return fmt.Errorf("%w: unknown zone %q", ErrInvalid, w.Zone)
	}

	return nil
}

// minGap returns the shortest time from one day of w.Days to the next day
// of the set, one week for a single day. The extra hour of validate allows
// for a daylight saving change.
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
// from up to to.
func (w Weekly) spans(from, to time.Time) ([]span, error) {
	loc, err := time.LoadLocation(w.Zone)
	if err != nil {
		return nil, fmt.Errorf("%w: unknown zone %q", ErrInvalid, w.Zone)
	}

	var out []span

	last := calendar.DateOf(to, loc)
	for d := calendar.DateOf(from.Add(-MaxWeeklyLength), loc); !last.Before(d); d = d.AddDays(1) {
		if !w.Days.Has(d.Weekday()) {
			continue
		}

		start := time.Date(d.Year, d.Month, d.Day, int(w.Start)/minutesPerHour, int(w.Start)%minutesPerHour, 0, 0, loc)
		out = append(out, span{start: start, end: start.Add(w.Length)})
	}

	return out, nil
}

const minutesPerHour = 60

// spans returns the periods of the rule on each day from the day before
// from to the day after to, in the zone of cal.
func (r CalendarRule) spans(cal calendar.Calendar, from, to time.Time) ([]span, error) {
	loc, err := cal.Location()
	if err != nil {
		return nil, fmt.Errorf("calendar rule: %w", err)
	}

	before := r.daysBeforeHolidays(cal, from, to, loc)

	var out []span

	last := calendar.DateOf(to, loc).AddDays(1)
	for d := r.firstDay(cal, calendar.DateOf(from, loc), before); !last.Before(d); d = d.AddDays(1) {
		dayStart := time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
		next := d.AddDays(1)
		dayEnd := time.Date(next.Year, next.Month, next.Day, 0, 0, 0, 0, loc)
		_, holiday := cal.IsHoliday(d)
		dayOff := !cal.WorkingDays.Has(d.Weekday())

		switch {
		case r.Holidays && holiday, r.DaysOff && dayOff, slices.Contains(before, d):
			out = append(out, span{start: dayStart, end: dayEnd})
		case r.OutsideHours && cal.IsBusinessDay(d):
			out = append(out,
				span{start: dayStart, end: clockAt(d, cal.DayStart, loc)},
				span{start: clockAt(d, cal.DayEnd, loc), end: dayEnd})
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
		_, holiday := cal.IsHoliday(d)
		dayOff := !cal.WorkingDays.Has(d.Weekday())

		whole := r.Holidays && holiday || r.DaysOff && dayOff || slices.Contains(before, d)
		if !whole {
			return d
		}

		d = d.AddDays(-1)
	}

	return d
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

func clockAt(d calendar.Date, c calendar.Clock, loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, int(c)/minutesPerHour, int(c)%minutesPerHour, 0, 0, loc)
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
