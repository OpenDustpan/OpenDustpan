package codec

import (
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// SessionPolicyRow is the column representation of the session policy.
// TokenFreshnessSet is false for a NULL token_freshness_ms: the policy then
// has session.DefaultTokenFreshness.
type SessionPolicyRow struct {
	IdleMs            int64
	MaxLengthMs       int64
	UpdatedAtMs       int64
	TokenFreshnessMs  int64
	TokenFreshnessSet bool
}

// FromSessionPolicyRow converts a row into the policy. An update time of 0
// is the zero time: the seeded defaults.
func FromSessionPolicyRow(row SessionPolicyRow) session.Policy {
	p := session.Policy{
		UpdatedAt: FromOptionalMillis(row.UpdatedAtMs),
		Limits: session.Limits{
			Idle:      time.Duration(row.IdleMs) * time.Millisecond,
			MaxLength: time.Duration(row.MaxLengthMs) * time.Millisecond,
		},
		TokenFreshness:        session.DefaultTokenFreshness,
		TokenFreshnessDefault: !row.TokenFreshnessSet,
	}

	if row.TokenFreshnessSet {
		p.TokenFreshness = time.Duration(row.TokenFreshnessMs) * time.Millisecond
	}

	return p
}

// ToSessionPolicyRow converts the policy into its columns.
func ToSessionPolicyRow(p session.Policy) SessionPolicyRow {
	return SessionPolicyRow{
		IdleMs:            p.Limits.Idle.Milliseconds(),
		MaxLengthMs:       p.Limits.MaxLength.Milliseconds(),
		UpdatedAtMs:       OptionalMillis(p.UpdatedAt),
		TokenFreshnessMs:  p.TokenFreshness.Milliseconds(),
		TokenFreshnessSet: !p.TokenFreshnessDefault,
	}
}
