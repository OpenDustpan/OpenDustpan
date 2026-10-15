package codec

import (
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// SessionPolicyRow is the column representation of the session policy.
type SessionPolicyRow struct {
	IdleMs      int64
	MaxLengthMs int64
	UpdatedAtMs int64
}

// FromSessionPolicyRow converts a row into the policy. An update time of 0
// is the zero time: the seeded defaults.
func FromSessionPolicyRow(row SessionPolicyRow) session.Policy {
	return session.Policy{
		UpdatedAt: FromOptionalMillis(row.UpdatedAtMs),
		Limits: session.Limits{
			Idle:      time.Duration(row.IdleMs) * time.Millisecond,
			MaxLength: time.Duration(row.MaxLengthMs) * time.Millisecond,
		},
	}
}

// ToSessionPolicyRow converts the policy into its columns.
func ToSessionPolicyRow(p session.Policy) SessionPolicyRow {
	return SessionPolicyRow{
		IdleMs:      p.Limits.Idle.Milliseconds(),
		MaxLengthMs: p.Limits.MaxLength.Milliseconds(),
		UpdatedAtMs: OptionalMillis(p.UpdatedAt),
	}
}
