package accesstoken

import (
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Defaults of the policy (ADR 53).
const (
	// DefaultMaxLifetime is the longest lifetime of a token when an
	// administrator sets none. RFC 20 keeps the default open: this is the
	// lead proposal of one year.
	DefaultMaxLifetime = 365 * 24 * time.Hour
	// DefaultInactivity is the inactivity period after which the server
	// disables a token or a user account: PCI DSS v4.0.1 requirement 8.2.6
	// (decision 4).
	DefaultInactivity = 90 * 24 * time.Hour
	// MaxPolicyDuration bounds the maximum lifetime and the inactivity
	// period: 100 years, so a period in milliseconds never overflows.
	MaxPolicyDuration = 100 * 365 * 24 * time.Hour
	// MinInactivity is the shortest inactivity period that is not zero. It is
	// longer than touchEvery, so a token in use always has a last use inside
	// the period.
	MinInactivity = time.Hour
)

// ErrBreakGlassUnavailable reports a policy that turns on the disable of
// the break-glass credential. That credential has no stored record until
// ADR 57, so the switch would have no effect.
var ErrBreakGlassUnavailable = fmt.Errorf("%w: disableBreakGlass has no effect until ADR 57", ErrInvalid)

// Policy is the administrator setting of the token life cycle (ADR 53,
// decisions 2, 4, 5 and 6). It belongs to the installation.
//
// MaxLifetime bounds the lifetime that a create or a rotation can ask for.
// Inactivity is the period with no use after which the sweep disables a
// token or a user account. Zero turns the disable off: an administrator can
// set no limit. DisableTokens and DisableAccounts turn the disable on for
// access tokens and for user accounts (principals of the kind person).
// DisableBreakGlass turns it on for the break-glass credential of ADR 57,
// which is exempt by default (decision 5). UpdatedAt is the time of the last
// change, or zero for the seeded defaults.
type Policy struct {
	UpdatedAt         time.Time
	MaxLifetime       time.Duration
	Inactivity        time.Duration
	DisableTokens     bool
	DisableAccounts   bool
	DisableBreakGlass bool
}

// DefaultPolicy returns the policy of a new installation.
func DefaultPolicy() Policy {
	return Policy{
		UpdatedAt:         time.Time{},
		MaxLifetime:       DefaultMaxLifetime,
		Inactivity:        DefaultInactivity,
		DisableTokens:     true,
		DisableAccounts:   true,
		DisableBreakGlass: false,
	}
}

// Validate reports whether p holds usable values: a maximum lifetime of one
// second to MaxPolicyDuration, an inactivity period of zero or MinInactivity
// to MaxPolicyDuration, both whole milliseconds, and the break-glass disable
// off. It returns ErrBreakGlassUnavailable when DisableBreakGlass is set.
func (p Policy) Validate() error {
	switch {
	case p.MaxLifetime < time.Second || p.MaxLifetime > MaxPolicyDuration:
		return fmt.Errorf("%w: the maximum lifetime %s is not 1s to %s", ErrInvalid, p.MaxLifetime, MaxPolicyDuration)
	case p.Inactivity != 0 && (p.Inactivity < MinInactivity || p.Inactivity > MaxPolicyDuration):
		return fmt.Errorf("%w: the inactivity period %s is not 0 or %s to %s", ErrInvalid, p.Inactivity,
			MinInactivity, MaxPolicyDuration)
	case p.MaxLifetime%time.Millisecond != 0 || p.Inactivity%time.Millisecond != 0:
		return fmt.Errorf("%w: a period is not a whole count of milliseconds", ErrInvalid)
	case p.DisableBreakGlass:
		return ErrBreakGlassUnavailable
	default:
		return nil
	}
}

// DefaultLifetimeUnder returns the lifetime of a token when a request names
// no expiry: DefaultLifetime, or the maximum lifetime of p when that is
// shorter.
func (p Policy) DefaultLifetimeUnder() time.Duration {
	return min(DefaultLifetime, p.MaxLifetime)
}

// inactiveAt reports whether p disables the token t as inactive at now: the
// token disable is on, the period is not zero, and the last use of t, or
// its creation when no request used it, is at or before now minus the
// period. The sweep applies the same rule with the database clock.
func (p Policy) inactiveAt(t Token, now time.Time) bool {
	if p.Inactivity == 0 || !p.DisableTokens {
		return false
	}

	last := t.LastUsedAt
	if last.IsZero() {
		last = t.CreatedAt
	}

	return !last.After(now.Add(-p.Inactivity))
}

// accountInactiveAt reports whether p disables the user account o as
// inactive at now: the account disable is on, the period is not zero, o is
// a person, and its latest activity is at or before now minus the period.
// The sweep applies the same rule with the database clock.
func (p Policy) accountInactiveAt(o Owner, now time.Time) bool {
	if p.Inactivity == 0 || !p.DisableAccounts || o.Kind != principal.KindPerson {
		return false
	}

	return !o.ActiveAt.After(now.Add(-p.Inactivity))
}

// Sweep is one run of the inactivity disable as a store writes it. The
// store reads the time of the run from the database clock, not from the
// clock of the instance, so a wrong clock on one instance cannot disable
// every record. The cutoff is that time minus Inactivity.
//
// A token whose last use, or creation when no request used it, is at or
// before the cutoff is inactive. A user account (a principal of the kind
// person) is inactive when the latest of these is at or before the cutoff:
// its creation, its activity baseline (set by an enable and, for the
// records that existed before the policy, by its migration), and the
// creation and the last use of each of its tokens. Tokens and Accounts name
// what the run disables.
type Sweep struct {
	Inactivity time.Duration
	Tokens     bool
	Accounts   bool
}

// SweptToken names one token that a sweep disabled and its principal.
type SweptToken struct {
	ID          string
	PrincipalID string
}

// SweepResult is the time of one run by the database clock, and the tokens
// and the principals that it disabled.
type SweepResult struct {
	At         time.Time
	Tokens     []SweptToken
	Principals []string
}

// SweepSubject is the subject of the audit entries of a sweep that the
// server runs on its own schedule. It starts with '@', which no subject of
// a principal holds, so it never names a principal.
const SweepSubject = "@server"

// SweepEntry returns the audit entry of a sweep that the server runs on its
// own schedule: the subject SweepSubject and the action
// server:manage-token-policy. SweepInactive completes it for each record.
func SweepEntry() audit.Entry {
	return audit.Entry{
		At:         time.Time{},
		Namespace:  "",
		Subject:    SweepSubject,
		IDPSubject: "",
		TokenID:    "",
		Action:     string(identity.ActionServerManageTokenPolicy),
		Object:     "",
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		SourceIP:   "",
		UserAgent:  "",
		Channel:    audit.ChannelAPI,
		Kind:       "",
		Outcome:    "",
		RequestID:  "",
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
		Changes:    nil,
	}
}
