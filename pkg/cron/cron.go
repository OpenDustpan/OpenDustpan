// Package cron parses the custom cron expression of a schedule and finds the
// time of its next run.
//
// The grammar is small and fixed. An expression has five fields in this
// order: minute (0-59), hour (0-23), day of month (1-31), month (1-12) and
// day of week (0-6, where 0 and 7 are Sunday). A field is a list of terms,
// separated by a comma. A term is "*", "*/n", a number, "a-b" or "a-b/n". A
// number has one or two digits. One space separates the fields. The package
// has no names (JAN, MON), no macros (@daily) and no "?", "L", "W" or "#".
//
// An expression runs in UTC. A day matches when the month matches and the day
// of month and the day of week agree. When both fields start with something
// other than "*", the day matches if either field matches (the rule of the
// classic Vixie cron). When one field starts with "*", both must match.
//
// The shortest time between two runs is MinInterval. Parse refuses a more
// frequent expression, and an expression that never runs, for example
// "0 0 30 2 *".
//
// The package reads text from outside the process. It checks the text against
// an allowlist, keeps a fixed bound on the work for any input, and returns the
// zero Schedule with an error for each input that it does not accept. It
// decides nothing about access: the caller checks who may set a schedule.
// Only the standard library is used.
package cron

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Bounds of an expression.
const (
	// MaxBytes is the longest expression that Parse reads.
	MaxBytes = 256
	// MinInterval is the shortest time between two runs of an accepted
	// expression.
	MinInterval = time.Hour
)

const (
	fieldCount = 5
	// maxEchoBytes bounds the part of the input that an error text repeats.
	maxEchoBytes = 32
	// cycleYears is the length of the cycle of the calendar. From 1901 to
	// 2099 the days of the month and the days of the week repeat every 28
	// years, so an expression that never matches in one cycle never matches.
	cycleYears = 28
	// cycleStartYear is the first year of the cycle that Parse scans.
	cycleStartYear = 2001
	// maxScanDays bounds the days that Next scans, and so its work.
	maxScanDays   = cycleYears*366 + 1
	minutesPerDay = hoursPerDay * minutesPerHour
	decimalBase   = 10
	// sundayAlias is the number 7 in the day of week field. It means Sunday,
	// as 0 does.
	sundayAlias = 7
)

// The highest number of each field.
const (
	maxMinute  = minutesPerHour - 1
	maxHour    = hoursPerDay - 1
	maxDay     = 31
	maxMonth   = 12
	maxWeekday = daysPerWeek - 1
)

// Sentinel errors of the package.
var (
	// ErrInvalid reports an expression that is not valid. The error text
	// names the field and the term, and it never repeats more than a short
	// part of the input.
	ErrInvalid = errors.New("invalid cron expression")
	// ErrTooFrequent reports an expression whose runs can be closer than
	// MinInterval. It wraps ErrInvalid.
	ErrTooFrequent = fmt.Errorf("%w: two runs can be closer than %v", ErrInvalid, MinInterval)
)

// field is the description of one field of an expression.
type field struct {
	name   string
	lo, hi int
	// max is the highest number that the input may hold. It is hi, except
	// for the day of week, where 7 means Sunday.
	max int
}

// fieldSpecs returns the five fields in the order of an expression.
func fieldSpecs() [fieldCount]field {
	return [fieldCount]field{
		{name: "minute", lo: 0, hi: maxMinute, max: maxMinute},
		{name: "hour", lo: 0, hi: maxHour, max: maxHour},
		{name: "day of month", lo: 1, hi: maxDay, max: maxDay},
		{name: "month", lo: 1, hi: maxMonth, max: maxMonth},
		{name: "day of week", lo: 0, hi: maxWeekday, max: sundayAlias},
	}
}

// Schedule is a parsed expression. The zero value never runs. A Schedule is
// a value: it is safe to copy and to use from many goroutines.
type Schedule struct {
	expr string
	// The sets are bit masks. Bit n is set when the number n is in the set.
	minutes, hours, days, months, weekdays uint64
	// domStar and dowStar tell that the day of month or the day of week
	// starts with "*".
	domStar, dowStar bool
}

// Parse reads expr. It returns an error that wraps ErrInvalid for text that
// is not an expression of the grammar of this package, and ErrTooFrequent for
// an expression whose runs can be closer than MinInterval. On error, it
// returns the zero Schedule.
func Parse(expr string) (Schedule, error) {
	var zero Schedule

	if err := checkText(expr); err != nil {
		return zero, err
	}

	parts := strings.Split(expr, " ")
	if len(parts) != fieldCount {
		return zero, fmt.Errorf("%w: %d fields, want %d separated by one space", ErrInvalid, len(parts), fieldCount)
	}

	var masks [fieldCount]uint64

	specs := fieldSpecs()

	for i, part := range parts {
		mask, err := parseField(specs[i], part)
		if err != nil {
			return zero, err
		}

		masks[i] = mask
	}

	// 7 is Sunday, and the set holds 0 only.
	if masks[4]&(1<<sundayAlias) != 0 {
		masks[4] = masks[4]&^(1<<sundayAlias) | 1
	}

	s := Schedule{
		expr:     expr,
		minutes:  masks[0],
		hours:    masks[1],
		days:     masks[2],
		months:   masks[3],
		weekdays: masks[4],
		domStar:  strings.HasPrefix(parts[2], "*"),
		dowStar:  strings.HasPrefix(parts[4], "*"),
	}

	if err := s.checkRuns(); err != nil {
		return zero, err
	}

	return s, nil
}

// String returns the expression that Parse read, or an empty string for the
// zero Schedule.
func (s Schedule) String() string {
	return s.expr
}

// Next returns the first run after the given time, in UTC. The result is on a
// whole minute. It returns false, and the zero time, for the zero Schedule.
// Its work has a fixed bound.
func (s Schedule) Next(after time.Time) (time.Time, bool) {
	if s.minutes == 0 {
		return time.Time{}, false
	}

	start := after.UTC().Truncate(time.Minute).Add(time.Minute)
	floor := start.Hour()*minutesPerHour + start.Minute()
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)

	for range maxScanDays {
		if s.matchesDay(day) {
			if tod, ok := s.firstTimeOfDay(floor); ok {
				return day.Add(time.Duration(tod) * time.Minute), true
			}
		}

		floor = 0
		day = day.AddDate(0, 0, 1)
	}

	return time.Time{}, false
}

// checkText is the check at the boundary: a length bound and an allowlist of
// bytes. It does not change the text to make it valid.
func checkText(expr string) error {
	if expr == "" {
		return fmt.Errorf("%w: the expression is empty", ErrInvalid)
	}

	if len(expr) > MaxBytes {
		return fmt.Errorf("%w: %d bytes, the most is %d", ErrInvalid, len(expr), MaxBytes)
	}

	for i := range len(expr) {
		c := expr[i]
		if (c < '0' || c > '9') && !strings.ContainsRune("*/,- ", rune(c)) {
			return fmt.Errorf("%w: byte %d is not a digit, one of \"*/,-\" or a space", ErrInvalid, i)
		}
	}

	return nil
}

// parseField reads one field and returns the set of its numbers.
func parseField(f field, text string) (uint64, error) {
	if text == "" {
		return 0, fmt.Errorf("%w: the %s field is empty", ErrInvalid, f.name)
	}

	var mask uint64

	for term := range strings.SplitSeq(text, ",") {
		bits, err := parseTerm(f, term)
		if err != nil {
			return 0, err
		}

		mask |= bits
	}

	return mask, nil
}

// parseTerm reads one term of a field: "*", "*/n", "a", "a-b" or "a-b/n".
func parseTerm(f field, term string) (uint64, error) {
	rangePart, stepPart, hasStep := strings.Cut(term, "/")
	lo, hi := f.lo, f.hi

	switch {
	case rangePart == "*":
	case strings.Contains(rangePart, "-"):
		a, b, _ := strings.Cut(rangePart, "-")

		var err error
		if lo, err = parseNumber(f, term, a); err != nil {
			return 0, err
		}

		if hi, err = parseNumber(f, term, b); err != nil {
			return 0, err
		}

		if lo > hi {
			return 0, fmt.Errorf("%w: the %s term %s runs backwards", ErrInvalid, f.name, echo(term))
		}
	case hasStep:
		// "a/n" has no clear meaning, so it is not accepted.
		return 0, fmt.Errorf("%w: the %s term %s has a step and no range", ErrInvalid, f.name, echo(term))
	default:
		n, err := parseNumber(f, term, rangePart)
		if err != nil {
			return 0, err
		}

		lo, hi = n, n
	}

	step := 1

	if hasStep {
		n, err := parseStep(f, term, stepPart, hi-lo+1)
		if err != nil {
			return 0, err
		}

		step = n
	}

	var mask uint64
	for n := lo; n <= hi; n += step {
		mask |= 1 << uint(n)
	}

	return mask, nil
}

// parseNumber reads a number of one or two digits that is in the range of
// the field.
func parseNumber(f field, term, text string) (int, error) {
	n, ok := digits(text)
	if !ok {
		return 0, fmt.Errorf("%w: the %s term %s needs a number of one or two digits", ErrInvalid, f.name, echo(term))
	}

	if n < f.lo || n > f.max {
		return 0, fmt.Errorf("%w: the %s term %s is outside %d-%d", ErrInvalid, f.name, echo(term), f.lo, f.max)
	}

	return n, nil
}

// parseStep reads a step from 1 to size.
func parseStep(f field, term, text string, size int) (int, error) {
	n, ok := digits(text)
	if !ok || n < 1 || n > size {
		return 0, fmt.Errorf("%w: the %s term %s needs a step from 1 to %d", ErrInvalid, f.name, echo(term), size)
	}

	return n, nil
}

// digits reads one or two ASCII digits. The check is on bytes, so a digit of
// another script is not accepted.
func digits(text string) (int, bool) {
	if len(text) < 1 || len(text) > 2 {
		return 0, false
	}

	n := 0

	for i := range len(text) {
		c := text[i]
		if c < '0' || c > '9' {
			return 0, false
		}

		n = n*decimalBase + int(c-'0')
	}

	return n, true
}

// echo quotes a part of the input for an error text. It cuts the input and
// escapes each control character, so an error text is one short line.
func echo(text string) string {
	if len(text) > maxEchoBytes {
		text = text[:maxEchoBytes]
	}

	return fmt.Sprintf("%q", text)
}

// checkRuns refuses an expression that never runs and an expression that can
// run twice within MinInterval.
func (s Schedule) checkRuns() error {
	day := time.Date(cycleStartYear, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := day.AddDate(cycleYears, 0, 0)

	matches := false

	for ; day.Before(end); day = day.AddDate(0, 0, 1) {
		if s.matchesDay(day) {
			matches = true

			break
		}
	}

	if !matches {
		return fmt.Errorf("%w: the expression never runs", ErrInvalid)
	}

	// The runs of one day repeat on each matching day, so the gaps inside one
	// day decide. A gap across midnight needs no check: the hours and the
	// minutes are the product of two sets, so such a gap is 60 minutes less
	// the spread of the minutes, and a spread above 0 gives a gap inside one
	// day that is shorter still.
	prev := -1

	for tod := range minutesPerDay {
		if s.hours&(1<<uint(tod/minutesPerHour)) == 0 || s.minutes&(1<<uint(tod%minutesPerHour)) == 0 {
			continue
		}

		if prev >= 0 && time.Duration(tod-prev)*time.Minute < MinInterval {
			return ErrTooFrequent
		}

		prev = tod
	}

	return nil
}

// firstTimeOfDay returns the first minute of the day, from floor on, that is
// in the hour and minute sets.
func (s Schedule) firstTimeOfDay(floor int) (int, bool) {
	for tod := floor; tod < minutesPerDay; tod++ {
		if s.hours&(1<<uint(tod/minutesPerHour)) != 0 && s.minutes&(1<<uint(tod%minutesPerHour)) != 0 {
			return tod, true
		}
	}

	return 0, false
}

// matchesDay reports whether the day, a time at 00:00 UTC, is in the month,
// day of month and day of week sets.
func (s Schedule) matchesDay(day time.Time) bool {
	if s.months&(1<<uint(day.Month())) == 0 {
		return false
	}

	dom := s.days&(1<<uint(day.Day())) != 0
	dow := s.weekdays&(1<<uint(day.Weekday())) != 0

	if s.domStar || s.dowStar {
		return dom && dow
	}

	return dom || dow
}
