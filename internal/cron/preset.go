package cron

import (
	"fmt"
	"hash/fnv"
	"slices"
)

// Preset is a frequency from the fixed list.
type Preset string

// The presets.
const (
	PresetHourly      Preset = "hourly"
	PresetEvery6Hours Preset = "every-6-hours"
	PresetDaily       Preset = "daily"
	PresetWeekly      Preset = "weekly"
)

// Presets returns the presets, from the most to the least frequent. Each
// call returns a new slice.
func Presets() []Preset {
	return []Preset{PresetHourly, PresetEvery6Hours, PresetDaily, PresetWeekly}
}

// Valid reports whether p is a preset of the list.
func (p Preset) Valid() bool {
	return slices.Contains(Presets(), p)
}

// Spread constants. A period is split into whole minutes.
const (
	minutesPerHour = 60
	hoursPerDay    = 24
	daysPerWeek    = 7
	sixHours       = 6
)

// Expression returns the expression of p for key. The key is the stable ID
// of the unit. The expression starts at an offset inside the period that a
// hash of the key fixes, so the units of one installation do not all start
// at the same minute. The same key always gives the same expression. It
// returns an error that wraps ErrInvalid for a preset that is not in the list
// and for an empty key. The result always passes Parse.
//
// The hash only spreads the load. It is not a secret and not a check of
// access.
func Expression(p Preset, key string) (string, error) {
	if !p.Valid() {
		return "", fmt.Errorf("%w: preset %s", ErrInvalid, echo(string(p)))
	}

	if key == "" {
		return "", fmt.Errorf("%w: a preset needs a key for its offset", ErrInvalid)
	}

	h := fnv.New64a()
	// A hash.Hash never returns an error from Write.
	_, _ = h.Write([]byte(key))
	sum := h.Sum64()

	minute := sum % minutesPerHour
	sum /= minutesPerHour

	switch p {
	case PresetHourly:
		return fmt.Sprintf("%d * * * *", minute), nil
	case PresetEvery6Hours:
		hour := sum % sixHours

		return fmt.Sprintf("%d %d,%d,%d,%d * * *", minute, hour, hour+sixHours, hour+2*sixHours, hour+3*sixHours), nil
	case PresetDaily:
		return fmt.Sprintf("%d %d * * *", minute, sum%hoursPerDay), nil
	case PresetWeekly:
		hour := sum % hoursPerDay
		sum /= hoursPerDay

		return fmt.Sprintf("%d %d * * %d", minute, hour, sum%daysPerWeek), nil
	}

	return "", fmt.Errorf("%w: preset %s", ErrInvalid, echo(string(p)))
}
