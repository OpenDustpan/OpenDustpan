package localuser

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/setting"
)

// Keys of the installation settings of the password sign-in. The settings
// resource shows the effective value and the source of each one.
const (
	// SettingEnabled turns the password sign-in of local users on or off.
	// Empty means that no one set it: the password sign-in is then on when
	// the installation has no identity provider, and off when it has one.
	SettingEnabled = "signIn.localPassword.enabled"
	// SettingMinLength is the minimum length of a new password, in code
	// points.
	SettingMinLength = "password.minLength"
	// SettingRequireLettersAndDigits makes each new password hold a letter
	// and a digit.
	SettingRequireLettersAndDigits = "password.requireLettersAndDigits"
	// SettingResetLifetime is the lifetime of a reset code or a set-up code,
	// as a Go duration, for example "1h".
	SettingResetLifetime = "password.resetLifetime"
)

// The text forms of a boolean setting.
const (
	textTrue  = "true"
	textFalse = "false"
)

// errSettingValue is the reason of a value that a check of these settings
// refuses. The settings resource shows the text, so it holds no value.
var errSettingValue = errors.New("the value is outside the allowed values")

// SettingDeclarations returns the declarations of the settings of the
// password sign-in, for the settings resource of the installation. The
// database layer holds each one, and the static layer can lock each one.
func SettingDeclarations() []setting.Declaration {
	return []setting.Declaration{
		{
			Key: SettingEnabled, Level: setting.LevelInstallation, Kind: setting.KindString, Default: "",
			Secret: false, Database: true, StaticCredentialHost: false, Apply: setting.ApplyLive, Check: checkEnabled,
			// A change to "true" or back to the configuration can turn the
			// password sign-in on next to an identity provider. Each such
			// change needs a fresh authentication.
			ReducesSecurity: func(old, next string) bool { return old != next && next != textFalse },
			Doc: `Password sign-in of local users: "true", "false", or empty. Empty means on when the ` +
				"installation has no identity provider, and off when it has one.",
		},
		{
			Key: SettingMinLength, Level: setting.LevelInstallation, Kind: setting.KindInt,
			Default: strconv.Itoa(DefaultMinLength), Secret: false, Database: true, StaticCredentialHost: false,
			Apply: setting.ApplyLive,
			Check: checkMinLength, ReducesSecurity: func(old, next string) bool { return atoi(next) < atoi(old) },
			Doc: fmt.Sprintf("The minimum length of a new password, in characters: %d to %d.",
				MinLengthFloor, MinLengthCeiling),
		},
		{
			Key: SettingRequireLettersAndDigits, Level: setting.LevelInstallation, Kind: setting.KindBool,
			Default: textFalse, Secret: false, Database: true, StaticCredentialHost: false,
			Apply: setting.ApplyLive, Check: nil,
			ReducesSecurity: func(old, next string) bool { return old == textTrue && next != textTrue },
			Doc:             "Each new password holds at least one letter and one digit.",
		},
		{
			Key: SettingResetLifetime, Level: setting.LevelInstallation, Kind: setting.KindString,
			Default: DefaultResetLifetime.String(), Secret: false, Database: true, StaticCredentialHost: false,
			Apply: setting.ApplyLive, Check: checkResetLifetime,
			ReducesSecurity: func(old, next string) bool {
				o, oErr := time.ParseDuration(old)
				n, nErr := time.ParseDuration(next)

				return oErr != nil || nErr != nil || n > o
			},
			Doc: fmt.Sprintf("The lifetime of a reset code or a set-up code, as a duration: %s to %s.",
				MinResetLifetime, MaxResetLifetime),
		},
	}
}

// checkEnabled accepts "true", "false" and the empty value.
func checkEnabled(value string) error {
	switch value {
	case "", textTrue, textFalse:
		return nil
	default:
		return errSettingValue
	}
}

// checkMinLength accepts a whole number from MinLengthFloor to
// MinLengthCeiling.
func checkMinLength(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil || n < MinLengthFloor || n > MinLengthCeiling {
		return errSettingValue
	}

	return nil
}

// checkResetLifetime accepts a Go duration from MinResetLifetime to
// MaxResetLifetime.
func checkResetLifetime(value string) error {
	_, err := ParseResetLifetime(value)

	return err
}

// ParseResetLifetime parses the value of SettingResetLifetime. It returns
// ErrInvalid for a value that is not a Go duration from MinResetLifetime to
// MaxResetLifetime.
func ParseResetLifetime(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d < MinResetLifetime || d > MaxResetLifetime {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, errSettingValue)
	}

	return d, nil
}

// ParseMinLength parses the value of SettingMinLength. It returns ErrInvalid
// for a value out of the bounds.
func ParseMinLength(value string) (int, error) {
	if err := checkMinLength(value); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return atoi(value), nil
}

// atoi returns the whole number of value, or 0 for a value that is not one.
// The settings resource checks the kind before it calls ReducesSecurity.
func atoi(value string) int {
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}

	return n
}
