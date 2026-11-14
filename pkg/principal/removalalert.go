package principal

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/setting"
)

// Keys of the installation settings of the removal alert. The settings
// resource shows the effective value and the source of each one.
const (
	// SettingRemovalAlertCount is the number of members that one source may
	// remove in the window before the server alerts the administrators.
	SettingRemovalAlertCount = "principal.removalAlertCount"
	// SettingRemovalAlertPercent is the percent of the members of one group
	// that one source may remove in the window before the server alerts the
	// administrators.
	SettingRemovalAlertPercent = "principal.removalAlertPercent"
	// SettingRemovalAlertWindow is the rolling window of the removal alert,
	// as a Go duration, for example "15m".
	SettingRemovalAlertWindow = "principal.removalAlertWindow"
)

// Bounds and defaults of the removal alert limits.
const (
	// DefaultRemovalAlertCount is the count of members that one source may
	// remove in the window when no administrator set another one.
	DefaultRemovalAlertCount = 10
	// MinRemovalAlertCount is the smallest count that the setting accepts.
	MinRemovalAlertCount = 1
	// MaxRemovalAlertCount is the largest count that the setting accepts.
	MaxRemovalAlertCount = 10000
	// DefaultRemovalAlertPercent is the percent of the members of one group
	// that one source may remove in the window when no administrator set
	// another one.
	DefaultRemovalAlertPercent = 20
	// MinRemovalAlertPercent is the smallest percent that the setting
	// accepts.
	MinRemovalAlertPercent = 1
	// MaxRemovalAlertPercent is the largest percent that the setting
	// accepts.
	MaxRemovalAlertPercent = 100
	// DefaultRemovalAlertWindow is the rolling window of the removal alert
	// when no administrator set another one.
	DefaultRemovalAlertWindow = 15 * time.Minute
	// MinRemovalAlertWindow is the shortest window that the setting accepts.
	MinRemovalAlertWindow = time.Minute
	// MaxRemovalAlertWindow is the longest window that the setting accepts.
	MaxRemovalAlertWindow = MaxRemovalWindow
)

// The reasons of a value that the removal alert settings refuse. The
// settings resource shows the text, so each one holds no value.
var (
	errRemovalAlertCount   = errors.New("the value is not a whole number of 1 to 10000")
	errRemovalAlertPercent = errors.New("the value is not a whole number of 1 to 100")
	errRemovalAlertWindow  = errors.New("the value is not a duration of 1 minute to 24 hours in whole seconds")
)

// ParseRemovalAlertCount parses the value of SettingRemovalAlertCount. It
// returns ErrInvalid for a value that is not a whole number of
// MinRemovalAlertCount to MaxRemovalAlertCount.
func ParseRemovalAlertCount(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < MinRemovalAlertCount || n > MaxRemovalAlertCount {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, errRemovalAlertCount)
	}

	return n, nil
}

// ParseRemovalAlertPercent parses the value of SettingRemovalAlertPercent.
// It returns ErrInvalid for a value that is not a whole number of
// MinRemovalAlertPercent to MaxRemovalAlertPercent.
func ParseRemovalAlertPercent(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < MinRemovalAlertPercent || n > MaxRemovalAlertPercent {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, errRemovalAlertPercent)
	}

	return n, nil
}

// ParseRemovalAlertWindow parses the value of SettingRemovalAlertWindow. It
// returns ErrInvalid for a value that is not a Go duration of
// MinRemovalAlertWindow to MaxRemovalAlertWindow in whole seconds.
func ParseRemovalAlertWindow(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d < MinRemovalAlertWindow || d > MaxRemovalAlertWindow || d%time.Second != 0 {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, errRemovalAlertWindow)
	}

	return d, nil
}

// RemovalAlertLimits holds the limits of the removal alert: the count of
// members that one source may remove in the window, the percent of the
// members of one group that it may remove in the window, and the window.
type RemovalAlertLimits struct {
	Count   int
	Percent int
	Window  time.Duration
}

// DefaultRemovalAlertLimits returns the limits of an installation where no
// administrator set another value.
func DefaultRemovalAlertLimits() RemovalAlertLimits {
	return RemovalAlertLimits{
		Count: DefaultRemovalAlertCount, Percent: DefaultRemovalAlertPercent, Window: DefaultRemovalAlertWindow,
	}
}

// RemovalAlertSettingDeclarations returns the declarations of the settings
// of the removal alert, for the settings resource of the installation. The
// database layer holds each one, and the static layer can lock each one.
func RemovalAlertSettingDeclarations() []setting.Declaration {
	return []setting.Declaration{
		{
			Key: SettingRemovalAlertCount, Level: setting.LevelInstallation, Kind: setting.KindInt,
			Default: strconv.Itoa(DefaultRemovalAlertCount), Secret: false, Database: true,
			StaticCredentialHost: false, Apply: setting.ApplyLive,
			Check: func(value string) error {
				_, err := ParseRemovalAlertCount(value)

				return err
			},
			// A larger count gives one source more room before the alert.
			ReducesSecurity: func(old, next string) bool {
				o, oErr := ParseRemovalAlertCount(old)
				n, nErr := ParseRemovalAlertCount(next)

				return oErr != nil || nErr != nil || n > o
			},
			Doc: fmt.Sprintf("The number of members that one source may remove in the removal window before the "+
				"server alerts the administrators: %d to %d.", MinRemovalAlertCount, MaxRemovalAlertCount),
		},
		{
			Key: SettingRemovalAlertPercent, Level: setting.LevelInstallation, Kind: setting.KindInt,
			Default: strconv.Itoa(DefaultRemovalAlertPercent), Secret: false, Database: true,
			StaticCredentialHost: false, Apply: setting.ApplyLive,
			Check: func(value string) error {
				_, err := ParseRemovalAlertPercent(value)

				return err
			},
			// A larger percent gives one source more room before the alert.
			ReducesSecurity: func(old, next string) bool {
				o, oErr := ParseRemovalAlertPercent(old)
				n, nErr := ParseRemovalAlertPercent(next)

				return oErr != nil || nErr != nil || n > o
			},
			Doc: fmt.Sprintf("The percent of the members of one group that one source may remove in the removal "+
				"window before the server alerts the administrators: %d to %d.",
				MinRemovalAlertPercent, MaxRemovalAlertPercent),
		},
		{
			Key: SettingRemovalAlertWindow, Level: setting.LevelInstallation, Kind: setting.KindString,
			Default: DefaultRemovalAlertWindow.String(), Secret: false, Database: true,
			StaticCredentialHost: false, Apply: setting.ApplyLive,
			Check: func(value string) error {
				_, err := ParseRemovalAlertWindow(value)

				return err
			},
			// A shorter window gives one source more room before the alert.
			ReducesSecurity: func(old, next string) bool {
				o, oErr := ParseRemovalAlertWindow(old)
				n, nErr := ParseRemovalAlertWindow(next)

				return oErr != nil || nErr != nil || n < o
			},
			Doc: fmt.Sprintf("The rolling window of the removal alert, as a duration: %s to %s. The server "+
				"counts the members that one source removed in this window.", MinRemovalAlertWindow, MaxRemovalAlertWindow),
		},
	}
}
