package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/setting"
)

// SettingBreakGlassMaxLength is the key of the installation setting that
// sets the longest time a session of a break-glass account lasts. The limit
// is absolute: it counts from the sign-in and no use moves it. The settings
// resource shows the effective value and its source.
const SettingBreakGlassMaxLength = "session.breakGlassMaxLength"

// Bounds of the break-glass session limit.
const (
	// DefaultBreakGlassMaxLength is the limit of a break-glass session when no
	// administrator set another one.
	DefaultBreakGlassMaxLength = time.Hour
	// MinBreakGlassMaxLength is the shortest limit that the setting accepts.
	MinBreakGlassMaxLength = MinIdle
	// MaxBreakGlassMaxLength is the longest limit that the setting accepts.
	MaxBreakGlassMaxLength = 12 * time.Hour
)

// errBreakGlassLimit is the reason of a value that the setting refuses. The
// settings resource shows the text, so it holds no value.
var errBreakGlassLimit = errors.New("the value is not a duration of 5 minutes to 12 hours in whole seconds")

// ParseBreakGlassMaxLength parses the value of SettingBreakGlassMaxLength. It
// returns ErrInvalid for a value that is not a Go duration of
// MinBreakGlassMaxLength to MaxBreakGlassMaxLength in whole seconds.
func ParseBreakGlassMaxLength(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d < MinBreakGlassMaxLength || d > MaxBreakGlassMaxLength || d%time.Second != 0 {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, errBreakGlassLimit)
	}

	return d, nil
}

// SettingDeclarations returns the declarations of the settings of the console
// sessions, for the settings resource of the installation. The database layer
// holds each one, and the static layer can lock each one.
func SettingDeclarations() []setting.Declaration {
	return []setting.Declaration{
		{
			Key: SettingBreakGlassMaxLength, Level: setting.LevelInstallation, Kind: setting.KindString,
			Default: DefaultBreakGlassMaxLength.String(), Secret: false, Database: true, StaticCredentialHost: false,
			Apply: setting.ApplyLive,
			Check: func(value string) error {
				_, err := ParseBreakGlassMaxLength(value)

				return err
			},
			// A longer limit gives the elevated session more time.
			ReducesSecurity: func(old, next string) bool {
				o, oErr := ParseBreakGlassMaxLength(old)
				n, nErr := ParseBreakGlassMaxLength(next)

				return oErr != nil || nErr != nil || n > o
			},
			Doc: fmt.Sprintf("The longest time a session of a break-glass account lasts, as a duration: %s to %s. "+
				"The time counts from the sign-in, and no use of the session moves it.",
				MinBreakGlassMaxLength, MaxBreakGlassMaxLength),
		},
	}
}

// BreakGlassLimit reads the effective limit of a break-glass session. An error
// or a value that is not valid makes the service use DefaultBreakGlassMaxLength.
type BreakGlassLimit func(ctx context.Context) (time.Duration, error)

// SetBreakGlassLimit sets the reader of the limit of a break-glass session.
// Without a reader, the limit is DefaultBreakGlassMaxLength.
func (s *Sessions) SetBreakGlassLimit(read BreakGlassLimit) {
	s.breakGlass = read
}

// BreakGlassMaxLength returns the limit of a break-glass session now. It
// fails toward the default: a reader that fails, or that answers a value
// outside the bounds, gives DefaultBreakGlassMaxLength, never no limit.
func (s *Sessions) BreakGlassMaxLength(ctx context.Context) time.Duration {
	if s.breakGlass == nil {
		return DefaultBreakGlassMaxLength
	}

	d, err := s.breakGlass(ctx)
	if err != nil || d < MinBreakGlassMaxLength || d > MaxBreakGlassMaxLength {
		return DefaultBreakGlassMaxLength
	}

	return d
}

// limitsFor returns the limits that apply to a session of the owner o. A
// break-glass account has a maximum length of at most the break-glass limit,
// and an idle limit within it.
func (s *Sessions) limitsFor(ctx context.Context, o *Owner, l Limits) Limits {
	if !o.BreakGlass {
		return l
	}

	l.MaxLength = min(l.MaxLength, s.BreakGlassMaxLength(ctx))
	l.Idle = min(l.Idle, l.MaxLength)

	return l
}
