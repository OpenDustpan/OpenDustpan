package gate

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// RetentionKey is the key of the installation setting that holds the
// retention period of the decision records, in whole hours.
const RetentionKey = "decisionRecords.retentionHours"

const (
	// MinRetentionHours is the shortest retention period. The floor stops a typo.
	MinRetentionHours = 24
	// MaxRetentionHours is the longest retention period: 10 years.
	MaxRetentionHours = 87600
	// maxRetentionDigits is the length of the text of MaxRetentionHours.
	maxRetentionDigits = 5
)

// ErrInvalidRetention means that a value of the retention setting is not "0"
// and not a canonical whole number of hours from MinRetentionHours to
// MaxRetentionHours.
var ErrInvalidRetention = errors.New("gate: invalid decision record retention")

// RetentionCheck returns nil for "0" (keep forever) and for a canonical whole
// number of hours from 24 to 87600. Any other value gives an error that wraps
// ErrInvalidRetention and names the key, the value and the range.
func RetentionCheck(value string) error {
	_, _, err := RetentionPeriod(value)

	return err
}

// RetentionPeriod parses the stored value once. "0" gives keepForever true. A
// valid number gives the period in hours. A refused text gives an error that
// wraps ErrInvalidRetention, no period and keepForever false: the caller must
// not read a refused value as keep-forever or as a period.
func RetentionPeriod(value string) (time.Duration, bool, error) {
	if value == "0" {
		return 0, true, nil
	}

	hours, ok := parseRetentionHours(value)
	if !ok {
		return 0, false, fmt.Errorf("%w: %s is %q, give 0 (keep forever) or a whole number of hours from %d to %d",
			ErrInvalidRetention, RetentionKey, value, MinRetentionHours, MaxRetentionHours)
	}

	return time.Duration(hours) * time.Hour, false, nil
}

// parseRetentionHours parses a canonical whole number of hours in range. It
// accepts ASCII digits only, with no sign, no space and no leading zero.
func parseRetentionHours(value string) (int64, bool) {
	// Defense in depth: the range check below also refuses a long number, and
	// strconv.ParseInt refuses an overflow. This length check stays so that no
	// later change of those layers lets an unbounded text reach the parse.
	if value == "" || len(value) > maxRetentionDigits || value[0] == '0' {
		return 0, false
	}

	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}

	hours, err := strconv.ParseInt(value, 10, 64)
	if err != nil || hours < MinRetentionHours || hours > MaxRetentionHours {
		return 0, false
	}

	return hours, true
}

// RetentionReducesSecurity reports whether a change from old to next ends
// records sooner: next is not "0" and old is "0" or longer than next. Fail
// closed: a value that does not parse counts as a reduction.
func RetentionReducesSecurity(old, next string) bool {
	oldPeriod, oldForever, err := RetentionPeriod(old)
	if err != nil {
		return true
	}

	nextPeriod, nextForever, err := RetentionPeriod(next)
	if err != nil {
		return true
	}

	if nextForever {
		return false
	}

	return oldForever || nextPeriod < oldPeriod
}
