package calendar

import (
	"fmt"
	"time"
)

// InBusinessHours reports whether t is on a business day, at or after the
// start of the working hours and before their end, in the zone of the
// calendar. It returns ErrUnknownZone for a zone that the server cannot
// load.
func (c Calendar) InBusinessHours(t time.Time) (bool, error) {
	loc, err := c.Location()
	if err != nil {
		return false, err
	}

	d := DateOf(t, loc)
	if !c.IsBusinessDay(d) {
		return false, nil
	}

	return !t.Before(c.DayStart.at(d, loc)) && t.Before(c.DayEnd.at(d, loc)), nil
}

// NextBusinessDay returns the first business day after the date of t in
// the zone of the calendar. The date of t itself never counts, also when
// it is a business day: a break-glass at 10:00 on a Tuesday is due on the
// Wednesday. It returns ErrNoBusinessDay when no business day comes in
// MaxSearchDays, and ErrUnknownZone for a zone that the server cannot load.
func (c Calendar) NextBusinessDay(t time.Time) (Date, error) {
	loc, err := c.Location()
	if err != nil {
		return Date{}, err
	}

	return c.businessDayFrom(DateOf(t, loc).AddDays(1))
}

// EndOfNextBusinessDay returns the end of the working hours of
// NextBusinessDay(t). This is the deadline "by the next business day". It
// returns the errors of NextBusinessDay.
func (c Calendar) EndOfNextBusinessDay(t time.Time) (time.Time, error) {
	d, err := c.NextBusinessDay(t)
	if err != nil {
		return time.Time{}, err
	}

	loc, err := c.Location()
	if err != nil {
		return time.Time{}, err
	}

	return c.DayEnd.at(d, loc), nil
}

// NextBusinessMoment returns t when t is in business hours, and otherwise
// the start of the next working hours. A notice that waits for business
// hours goes out at this time. It returns the errors of NextBusinessDay.
func (c Calendar) NextBusinessMoment(t time.Time) (time.Time, error) {
	loc, err := c.Location()
	if err != nil {
		return time.Time{}, err
	}

	d := DateOf(t, loc)
	if c.IsBusinessDay(d) {
		start, end := c.DayStart.at(d, loc), c.DayEnd.at(d, loc)
		if t.Before(start) {
			return start, nil
		}

		if t.Before(end) {
			return t, nil
		}
	}

	next, err := c.businessDayFrom(d.AddDays(1))
	if err != nil {
		return time.Time{}, err
	}

	return c.DayStart.at(next, loc), nil
}

// AddBusinessTime returns the time that is d of business hours after t.
// Time outside business hours does not count. So with working hours from
// 09:00 to 17:00, 2 hours after Friday 16:00 is Monday 10:00 when Monday is
// a business day. A d of 0 gives NextBusinessMoment(t). A negative d
// returns ErrInvalid. It returns the errors of NextBusinessDay.
func (c Calendar) AddBusinessTime(t time.Time, d time.Duration) (time.Time, error) {
	if d < 0 {
		return time.Time{}, fmt.Errorf("%w: a business duration cannot be negative", ErrInvalid)
	}

	loc, err := c.Location()
	if err != nil {
		return time.Time{}, err
	}

	at, err := c.NextBusinessMoment(t)
	if err != nil {
		return time.Time{}, err
	}

	for range MaxSearchDays {
		end := c.DayEnd.at(DateOf(at, loc), loc)
		if !end.After(at) {
			// A zone change can skip the start of the working hours and
			// leave no business time on this date: go to the next day.
			next, nerr := c.businessDayFrom(DateOf(at, loc).AddDays(1))
			if nerr != nil {
				return time.Time{}, nerr
			}

			at = c.DayStart.at(next, loc)

			continue
		}

		left := end.Sub(at)
		if d <= left {
			return at.Add(d), nil
		}

		d -= left

		at, err = c.NextBusinessMoment(end)
		if err != nil {
			return time.Time{}, err
		}
	}

	return time.Time{}, fmt.Errorf("%w: %s of business time", ErrNoBusinessDay, d)
}

// businessDayFrom returns the first business day at or after d.
func (c Calendar) businessDayFrom(start Date) (Date, error) {
	d := start
	for range MaxSearchDays {
		if c.IsBusinessDay(d) {
			return d, nil
		}

		d = d.AddDays(1)
	}

	return Date{}, fmt.Errorf("%w: none in %d days from %s", ErrNoBusinessDay, MaxSearchDays, start)
}

// WorkStart returns the start of the working hours on the date d, in the
// zone of the calendar, also when d is not a business day. It returns
// ErrUnknownZone for a zone that the server cannot load.
func (c Calendar) WorkStart(d Date) (time.Time, error) {
	loc, err := c.Location()
	if err != nil {
		return time.Time{}, err
	}

	return c.DayStart.at(d, loc), nil
}
