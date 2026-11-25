package principal

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/setting"
)

// SettingBreakGlassLockSeconds is the key of the installation setting that
// sets how long a guarded change waits for the lock of the break-glass set.
// The settings resource shows the effective value and its source.
const SettingBreakGlassLockSeconds = "principal.breakGlassLockSeconds"

// Bounds and the default of the break-glass lock timeout.
const (
	// DefaultBreakGlassLockSeconds is the wait of a guarded change when no
	// administrator set another one.
	DefaultBreakGlassLockSeconds = 5
	// MinBreakGlassLockSeconds is the shortest wait that the setting accepts.
	MinBreakGlassLockSeconds = 1
	// MaxBreakGlassLockSeconds is the longest wait that the setting accepts.
	MaxBreakGlassLockSeconds = 60
)

// errBreakGlassLockSeconds is the reason of a value that the setting refuses.
// The settings resource shows the text, so it holds no value.
var errBreakGlassLockSeconds = errors.New(
	"the value is not a whole number of seconds from 1 to 60",
)

// ParseBreakGlassLockSeconds parses the value of
// SettingBreakGlassLockSeconds. It returns setting.ErrInvalidValue for a value
// that is not a whole number from MinBreakGlassLockSeconds to
// MaxBreakGlassLockSeconds.
func ParseBreakGlassLockSeconds(value string) (time.Duration, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < MinBreakGlassLockSeconds || n > MaxBreakGlassLockSeconds {
		return 0, fmt.Errorf("%w: %w", setting.ErrInvalidValue, errBreakGlassLockSeconds)
	}

	return time.Duration(n) * time.Second, nil
}

// BreakGlassLockTimeout reads the effective timeout of the lock of the
// break-glass set. An error or a value outside the bounds gives
// DefaultBreakGlassLockSeconds, never no wait.
type BreakGlassLockTimeout func(ctx context.Context) (time.Duration, error)

// DefaultBreakGlassLockTimeout is the reader of a server that keeps no
// settings.
func DefaultBreakGlassLockTimeout(context.Context) (time.Duration, error) {
	return DefaultBreakGlassLockSeconds * time.Second, nil
}

// LockTimeout returns the timeout of the break-glass lock that read answers
// now. A reader that fails, or that answers a value outside the bounds, gives
// DefaultBreakGlassLockSeconds.
func LockTimeout(ctx context.Context, read BreakGlassLockTimeout) time.Duration {
	if read == nil {
		return DefaultBreakGlassLockSeconds * time.Second
	}

	d, err := read(ctx)
	if err != nil || d < MinBreakGlassLockSeconds*time.Second || d > MaxBreakGlassLockSeconds*time.Second {
		return DefaultBreakGlassLockSeconds * time.Second
	}

	return d
}

// BreakGlassSettingDeclarations returns the declarations of the settings of
// the break-glass guard, for the settings resource of the installation.
func BreakGlassSettingDeclarations() []setting.Declaration {
	return []setting.Declaration{
		{
			Key: SettingBreakGlassLockSeconds, Level: setting.LevelInstallation, Kind: setting.KindInt,
			Default: strconv.Itoa(DefaultBreakGlassLockSeconds), Secret: false, Database: true,
			StaticCredentialHost: false, Apply: setting.ApplyLive,
			Check: func(value string) error {
				_, err := ParseBreakGlassLockSeconds(value)

				return err
			},
			// A change of the wait takes effect live and gives no more
			// access.
			ReducesSecurity: nil,
			Doc: fmt.Sprintf("How long a change of the break-glass accounts may wait for the lock of "+
				"the break-glass set, in whole seconds: %d to %d.",
				MinBreakGlassLockSeconds, MaxBreakGlassLockSeconds),
		},
	}
}
