package tempadmin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/setting"
)

// SettingSessionLimit is the key of the installation setting that holds the
// session limit of a new temporary administrator.
const SettingSessionLimit = "tempadmin.session-limit"

// The bounds and the default of the session limit.
const (
	// DefaultSessionLimit is the session limit when no value is stored.
	DefaultSessionLimit = time.Hour
	// MinSessionLimit is the shortest session limit.
	MinSessionLimit = time.Millisecond
	// MaxSessionLimit is the longest session limit. It is the longest record
	// lifetime, Lifetime.
	MaxSessionLimit = Lifetime
)

// ErrSessionLimit reports a session limit that is not a duration from
// MinSessionLimit to MaxSessionLimit, in whole milliseconds.
var ErrSessionLimit = errors.New("invalid temporary administrator session limit")

// ParseTempAdminLimit returns the session limit that value holds. The value
// is a duration with a unit, for example "1h" or "30m". It returns
// ErrSessionLimit for a value that is not a duration, that is not a whole
// count of milliseconds, or that is outside MinSessionLimit to
// MaxSessionLimit.
func ParseTempAdminLimit(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%w: the value is not a duration with a unit, for example 1h", ErrSessionLimit)
	}

	if d < MinSessionLimit || d > MaxSessionLimit || d%time.Millisecond != 0 {
		return 0, fmt.Errorf("%w: the value is not %s to %s in whole milliseconds",
			ErrSessionLimit, MinSessionLimit, MaxSessionLimit)
	}

	return d, nil
}

// SessionLimitSettingDeclarations returns the declaration of the session limit
// for the settings resource of the installation. The database layer holds it.
// A longer limit is weaker, so a change to a longer limit needs a fresh
// authentication.
func SessionLimitSettingDeclarations() []setting.Declaration {
	return []setting.Declaration{
		{
			Key: SettingSessionLimit, Level: setting.LevelInstallation, Kind: setting.KindString,
			Default: DefaultSessionLimit.String(), Secret: false, Database: true,
			StaticCredentialHost: false, Apply: setting.ApplyLive,
			Check: func(value string) error {
				_, err := ParseTempAdminLimit(value)

				return err
			},
			ReducesSecurity: func(old, next string) bool {
				o, oErr := ParseTempAdminLimit(old)
				n, nErr := ParseTempAdminLimit(next)

				return oErr != nil || nErr != nil || n > o
			},
			Doc: fmt.Sprintf("The time that a new temporary administrator works, as a duration: %s to %s. "+
				"A running record keeps its end time.", MinSessionLimit, MaxSessionLimit),
		},
	}
}

// RecordEnd returns the end of a record that starts at at: the session limit
// after at, or the maximum lifetime of the token policy after at when that is
// shorter. No record ends later than Lifetime after at.
func RecordEnd(at time.Time, limit, policyMax time.Duration) time.Time {
	return at.Add(min(limit, policyMax, Lifetime))
}

// LimitChange returns the audit change that names the session limit in force,
// in seconds, for the entry of a create.
func LimitChange(limit time.Duration) audit.Change {
	return audit.Change{
		Key: "session-limit-seconds", Old: nil, New: strconv.FormatInt(int64(limit/time.Second), 10),
		Layer: "", TakesEffect: "",
	}
}

// SettingLoader reads the stored settings of a scope. setting.Store
// implements it.
type SettingLoader interface {
	Load(ctx context.Context, scope setting.Scope) (setting.Stored, error)
}

// SessionLimitSource names what ResolveSessionLimit needs.
type SessionLimitSource struct {
	// Settings reads the stored value. Required.
	Settings SettingLoader
	// Audit takes the entry of a refusal. Required.
	Audit audit.ServerStore
	// Entry is the audit entry of the caller. ResolveSessionLimit sets the
	// time, the scope, the action, the object and the outcome.
	Entry audit.Entry
	// At is the time of the entry.
	At time.Time
}

// ResolveSessionLimit returns the session limit in force: the stored value,
// or DefaultSessionLimit when none is stored. A stored value that
// ParseTempAdminLimit refuses fails closed: the call writes a failure entry
// and returns ErrSessionLimit with the key and the reset option, so no record
// is created with a limit that nobody checked.
func ResolveSessionLimit(ctx context.Context, src SessionLimitSource) (time.Duration, error) {
	if src.Settings == nil || src.Audit == nil {
		return 0, fmt.Errorf("%w: session limit source", ErrMissingDependency)
	}

	stored, err := src.Settings.Load(ctx, setting.InstallationScope())
	if err != nil {
		return 0, fmt.Errorf("read the setting %s: %w", SettingSessionLimit, err)
	}

	value, has := stored.Values[SettingSessionLimit]
	if !has {
		return DefaultSessionLimit, nil
	}

	limit, parseErr := ParseTempAdminLimit(value)
	if parseErr == nil {
		return limit, nil
	}

	e := src.Entry
	e.At = src.At.UTC().Truncate(time.Millisecond)
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeFailure
	e.Action = Action
	e.Object = "setting:" + SettingSessionLimit

	if _, appendErr := src.Audit.AppendServer(ctx, e); appendErr != nil {
		return 0, fmt.Errorf("write the refusal entry: %w", appendErr)
	}

	return 0, fmt.Errorf("%w: the stored setting %s is outside the range: "+
		"opendustpan host create-temp-admin --reset-session-limit --reason \"<text>\"",
		ErrSessionLimit, SettingSessionLimit)
}
