package cron_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cron"
)

func utc(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
}

func TestParseAccepts(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"0 * * * *",
		"59 * * * *",
		"0 */6 * * *",
		"30 2 * * *",
		"0 0 1 1 *",
		"0 9 * * 1",
		"0 9 * * 1-5",
		"0 0 * * 7",
		"0 0 * * 0-7",
		"0 0,23 * * *",
		"1 0,23 * * *",
		"0 0-23/1 * * *",
		"15 1,13 1,15 */3 *",
		"05 09 * * *",
		"0 0 29 2 *",
		"0 0 */2 * 1",
		"*/60 * * * *",
	} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			s, err := cron.Parse(expr)
			if err != nil {
				t.Fatalf("Parse(%q) = %v", expr, err)
			}

			if s.String() != expr {
				t.Fatalf("String() = %q, want %q", s.String(), expr)
			}
		})
	}
}

func TestParseRejectsHostileInput(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("1,", cron.MaxBytes) + "1 * * * *"

	for name, expr := range map[string]string{
		"empty":                  "",
		"too long":               long,
		"four fields":            "0 * * *",
		"six fields":             "0 * * * * *",
		"leading space":          " 0 * * * *",
		"trailing space":         "0 * * * * ",
		"double space":           "0  * * * *",
		"tab":                    "0\t* * * *",
		"newline":                "0 * * * *\n",
		"newline between":        "0\n* * * *",
		"carriage return":        "0 * * * *\r",
		"NUL":                    "0 * * * \x00",
		"escape":                 "0 * * * \x1b[2J",
		"arabic digit":           "٣ * * * *",
		"fullwidth digit":        "３ * * * *",
		"no-break space":         "0 * * * *",
		"month name":             "0 0 1 JAN *",
		"day name":               "0 0 * * MON",
		"macro":                  "@hourly",
		"question mark":          "0 0 ? * *",
		"last day":               "0 0 L * *",
		"hash":                   "0 0 * * 1#2",
		"empty field term":       "0 0 1,,2 * *",
		"trailing comma":         "0 0 1, * *",
		"leading comma":          "0 0 ,1 * *",
		"lone slash":             "0 / * * *",
		"empty step":             "*/ * * * *",
		"zero step":              "*/0 * * * *",
		"step above the range":   "0 */25 * * *",
		"step above a range":     "0 0-5/7 * * *",
		"step with no range":     "5/10 * * * *",
		"step twice":             "*/2/3 * * * *",
		"negative":               "-1 * * * *",
		"range with no end":      "1- * * * *",
		"range with no start":    "-5 * * * *",
		"range backwards":        "30-10 * * * *",
		"range of ranges":        "1-2-3 * * * *",
		"star then number":       "*5 * * * *",
		"three digits":           "000 * * * *",
		"huge number":            "99999999999999999999 * * * *",
		"minute 60":              "60 * * * *",
		"hour 24":                "0 24 * * *",
		"day of month 0":         "0 0 0 * *",
		"day of month 32":        "0 0 32 * *",
		"month 0":                "0 0 1 0 *",
		"month 13":               "0 0 1 13 *",
		"day of week 8":          "0 0 * * 8",
		"seven outside weekdays": "0 0 7-8 * 8",
		"never runs, 30 Feb":     "0 0 30 2 *",
		"never runs, 31 Apr":     "0 0 31 4 *",
		"never runs, 31 Feb":     "0 0 31 2 *",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, err := cron.Parse(expr)
			if !errors.Is(err, cron.ErrInvalid) {
				t.Fatalf("Parse(%q) error = %v, want ErrInvalid", expr, err)
			}

			if s != (cron.Schedule{}) {
				t.Fatalf("Parse(%q) returned a schedule with an error", expr)
			}
		})
	}
}

// TestParseErrorTextIsOneShortLine checks that a hostile input never reaches
// a log line as a control character, and never as a long text.
func TestParseErrorTextIsOneShortLine(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"0 * * * *\n2026-10-20 level=ERROR forged",
		"0\r\n* * * *",
		"0 0 1,\x1b[31m * *",
		strings.Repeat("9", cron.MaxBytes) + " * * * *",
		strings.Repeat("1", cron.MaxBytes+1),
		"99" + strings.Repeat("-", 200) + " * * * *",
	} {
		_, err := cron.Parse(expr)
		if err == nil {
			t.Fatalf("Parse(%q) accepted the input", expr)
		}

		text := err.Error()
		if strings.ContainsAny(text, "\r\n\x00\x1b") {
			t.Errorf("error text holds a control character: %q", text)
		}

		if len(text) > 200 {
			t.Errorf("error text of %d bytes repeats the input: %q", len(text), text)
		}
	}
}

func TestParseMinInterval(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"0 * * * *",
		"59 * * * *",
		"0 0,23 * * *",
		"0 0,23 * * 1",
		"0 0,23 * * 1,2",
		"0 */1 * * *",
		"1 0,1 1 1 *",
		"0 0-23 29 2 *",
		"0 0-23 * * 1-5",
		"0 0 * * 1",
		"59 23 1,2,3 * *",
	} {
		if _, err := cron.Parse(expr); err != nil {
			t.Errorf("Parse(%q) = %v, want a schedule", expr, err)
		}
	}

	for _, expr := range []string{
		"* * * * *",
		"*/30 * * * *",
		"*/59 * * * *",
		"0,30 * * * *",
		"0,59 * * * *",
		"0,59 23 * * *",
		"10,50 0,23 * * *",
		"0-1 5 * * *",
		"0,1 0 1 1 *",
		"0-30/30 0 * * 1,3",
		"0,58 23 1,2,3 * *",
		"0-59/59 1 1,2,3 * *",
	} {
		if _, err := cron.Parse(expr); !errors.Is(err, cron.ErrTooFrequent) {
			t.Errorf("Parse(%q) = %v, want ErrTooFrequent", expr, err)
		}
	}
}

func TestErrTooFrequentIsInvalid(t *testing.T) {
	t.Parallel()

	_, err := cron.Parse("* * * * *")
	if !errors.Is(err, cron.ErrTooFrequent) || !errors.Is(err, cron.ErrInvalid) {
		t.Fatalf("Parse error = %v, want ErrTooFrequent that wraps ErrInvalid", err)
	}
}

func TestNext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		expr  string
		after time.Time
		want  time.Time
	}{
		{"hourly, mid hour", "0 * * * *", utc(2026, 10, 20, 10, 30), utc(2026, 10, 20, 11, 0)},
		{"strictly after a run", "0 * * * *", utc(2026, 10, 20, 11, 0), utc(2026, 10, 20, 12, 0)},
		{
			"seconds do not count",
			"0 * * * *",
			time.Date(2026, 10, 20, 11, 0, 30, 0, time.UTC),
			utc(2026, 10, 20, 12, 0),
		},
		{"just before a run", "0 * * * *", time.Date(2026, 10, 20, 10, 59, 59, 0, time.UTC), utc(2026, 10, 20, 11, 0)},
		{"next day", "30 2 * * *", utc(2026, 10, 20, 3, 0), utc(2026, 10, 21, 2, 30)},
		{"same day later", "30 2 * * *", utc(2026, 10, 20, 1, 0), utc(2026, 10, 20, 2, 30)},
		{"next year", "0 0 1 1 *", utc(2026, 10, 20, 0, 0), utc(2027, 1, 1, 0, 0)},
		{"end of year", "0 23 31 12 *", utc(2026, 12, 31, 23, 0), utc(2027, 12, 31, 23, 0)},
		{"every 6 hours", "0 */6 * * *", utc(2026, 10, 20, 0, 0), utc(2026, 10, 20, 6, 0)},
		{"every 6 hours, last of day", "0 */6 * * *", utc(2026, 10, 20, 18, 0), utc(2026, 10, 21, 0, 0)},
		{"monday", "0 9 * * 1", utc(2026, 10, 20, 12, 0), utc(2026, 10, 26, 9, 0)},
		{"sunday as 7", "0 0 * * 7", utc(2026, 10, 20, 12, 0), utc(2026, 10, 25, 0, 0)},
		{"sunday as 0", "0 0 * * 0", utc(2026, 10, 20, 12, 0), utc(2026, 10, 25, 0, 0)},
		{"weekdays", "0 8 * * 1-5", utc(2026, 10, 23, 9, 0), utc(2026, 10, 26, 8, 0)},
		{"day of month or day of week", "0 0 13 * 5", utc(2026, 10, 20, 12, 0), utc(2026, 10, 23, 0, 0)},
		{"day of month with a star in the week", "0 0 13 * *", utc(2026, 10, 20, 12, 0), utc(2026, 11, 13, 0, 0)},
		{"step day of month and week", "0 0 */2 * 1", utc(2026, 10, 20, 12, 0), utc(2026, 11, 9, 0, 0)},
		{"leap day", "0 0 29 2 *", utc(2026, 10, 20, 0, 0), utc(2028, 2, 29, 0, 0)},
		{"leap day after a leap day", "0 0 29 2 *", utc(2028, 2, 29, 0, 0), utc(2032, 2, 29, 0, 0)},
		{"month list", "0 0 1 3,9 *", utc(2026, 10, 20, 0, 0), utc(2027, 3, 1, 0, 0)},
		{"range with step", "0 0-12/6 * * *", utc(2026, 10, 20, 7, 0), utc(2026, 10, 20, 12, 0)},
		{"zero time", "0 0 1 1 *", time.Time{}, utc(2, 1, 1, 0, 0)},
		{
			"a zone is turned into UTC",
			"0 12 * * *",
			time.Date(2026, 10, 20, 12, 30, 0, 0, time.FixedZone("x", 3600)),
			utc(2026, 10, 20, 12, 0),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s, err := cron.Parse(tc.expr)
			if err != nil {
				t.Fatalf("Parse(%q) = %v", tc.expr, err)
			}

			got, ok := s.Next(tc.after)
			if !ok || !got.Equal(tc.want) || got.Location() != time.UTC {
				t.Fatalf("Next(%v) = %v, %v; want %v", tc.after, got, ok, tc.want)
			}
		})
	}
}

// TestNextOfTheZeroScheduleFailsClosed checks that a zero Schedule never
// runs, so a schedule that failed to load starts nothing.
func TestNextOfTheZeroScheduleFailsClosed(t *testing.T) {
	t.Parallel()

	var s cron.Schedule

	if got, ok := s.Next(utc(2026, 10, 20, 0, 0)); ok || !got.IsZero() {
		t.Fatalf("Next of the zero schedule = %v, %v", got, ok)
	}

	if s.String() != "" {
		t.Fatalf("String of the zero schedule = %q", s.String())
	}
}

// TestNextWalksEveryRun checks the order of the runs of one expression over
// a full year: each run is after the one before, and one hour or more apart.
func TestNextWalksEveryRun(t *testing.T) {
	t.Parallel()

	s, err := cron.Parse("15 1,13 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}

	at := utc(2026, 1, 1, 0, 0)
	end := utc(2027, 1, 1, 0, 0)
	runs := 0

	for {
		next, ok := s.Next(at)
		if !ok || !next.After(at) || next.Weekday() == time.Saturday || next.Weekday() == time.Sunday {
			t.Fatalf("Next(%v) = %v, %v", at, next, ok)
		}

		if next.Sub(at) > 0 && runs > 0 && next.Sub(at) < cron.MinInterval {
			t.Fatalf("runs %v and %v are closer than %v", at, next, cron.MinInterval)
		}

		if !next.Before(end) {
			break
		}

		at = next
		runs++
	}

	// 261 weekdays in 2026, two runs on each.
	if runs != 261*2 {
		t.Fatalf("runs in 2026 = %d, want %d", runs, 261*2)
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"0 * * * *", "*/60 * * * *", "0 0 29 2 *", "0 0 * * 7", "1-5/2 1,2 * * *",
		"", "* * * * *", "99999999999999999999 * * * *", "0 0 30 2 *", "\x00",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, expr string) {
		s, err := cron.Parse(expr)
		if err != nil {
			if !errors.Is(err, cron.ErrInvalid) || s != (cron.Schedule{}) {
				t.Fatalf("Parse(%q) = %v, %v", expr, s, err)
			}

			return
		}

		at := utc(2026, 10, 20, 12, 34)

		first, ok := s.Next(at)
		if !ok || !first.After(at) {
			t.Fatalf("Next of %q = %v, %v", expr, first, ok)
		}

		second, ok := s.Next(first)
		if !ok || second.Sub(first) < cron.MinInterval {
			t.Fatalf("runs of %q: %v then %v", expr, first, second)
		}
	})
}

func TestPresets(t *testing.T) {
	t.Parallel()

	want := []cron.Preset{"hourly", "every-6-hours", "daily", "weekly"}
	got := cron.Presets()

	if len(got) != len(want) {
		t.Fatalf("Presets() = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] || !got[i].Valid() {
			t.Fatalf("Presets() = %v, want %v", got, want)
		}
	}

	got[0] = "changed"

	if cron.Presets()[0] != "hourly" {
		t.Fatal("Presets() shares its slice between calls")
	}

	for _, bad := range []cron.Preset{"", "Hourly", "monthly", "hourly ", "0 * * * *"} {
		if bad.Valid() {
			t.Errorf("Preset(%q).Valid() = true", bad)
		}
	}
}

func TestExpression(t *testing.T) {
	t.Parallel()

	// Each preset has a fixed period. The first run of a key is inside the
	// period that follows the start, and two runs are one period apart.
	periods := map[cron.Preset]time.Duration{
		cron.PresetHourly:      time.Hour,
		cron.PresetEvery6Hours: 6 * time.Hour,
		cron.PresetDaily:       24 * time.Hour,
		cron.PresetWeekly:      7 * 24 * time.Hour,
	}

	for preset, period := range periods {
		offsets := map[time.Duration]bool{}

		for i := range 500 {
			key := "unit-" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('0'+i%10))
			offsets[checkPreset(t, preset, key, period)] = true
		}

		// The hash spreads the keys: 500 keys do not start at one offset.
		if len(offsets) < 20 {
			t.Errorf("%s: 500 keys gave only %d offsets", preset, len(offsets))
		}
	}
}

// checkPreset checks the expression of preset for key and returns the offset
// of its first run from the start of the period.
func checkPreset(t *testing.T, preset cron.Preset, key string, period time.Duration) time.Duration {
	t.Helper()

	expr, err := cron.Expression(preset, key)
	if err != nil {
		t.Fatalf("Expression(%s, %q) = %v", preset, key, err)
	}

	again, err := cron.Expression(preset, key)
	if err != nil || again != expr {
		t.Fatalf("Expression(%s, %q) = %q then %q, %v", preset, key, expr, again, err)
	}

	s, err := cron.Parse(expr)
	if err != nil {
		t.Fatalf("Expression(%s, %q) = %q, which Parse refuses: %v", preset, key, expr, err)
	}

	start := utc(2026, 10, 19, 0, 0) // a Monday
	first, _ := s.Next(start.Add(-time.Minute))
	second, _ := s.Next(first)

	if first.Sub(start) >= period {
		t.Fatalf("%s %q: first run %v is not inside the first period", preset, key, first.Sub(start))
	}

	if preset != cron.PresetEvery6Hours && second.Sub(first) != period {
		t.Fatalf("%s %q: runs %v apart, want %v", preset, key, second.Sub(first), period)
	}

	return first.Sub(start)
}

func TestExpressionRefusesBadInput(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		preset cron.Preset
		key    string
	}{
		"empty preset":   {"", "unit-1"},
		"unknown preset": {"monthly", "unit-1"},
		"cron as preset": {"0 * * * *", "unit-1"},
		"empty key":      {cron.PresetDaily, ""},
		"newline preset": {"daily\n", "unit-1"},
	} {
		expr, err := cron.Expression(tc.preset, tc.key)
		if !errors.Is(err, cron.ErrInvalid) || expr != "" {
			t.Errorf("%s: Expression = %q, %v; want ErrInvalid", name, expr, err)
		}

		if err != nil && strings.ContainsAny(err.Error(), "\r\n") {
			t.Errorf("%s: error text holds a line break: %q", name, err)
		}
	}
}
