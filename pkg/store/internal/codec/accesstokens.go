package codec

import (
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/principal"
)

// AccessTokenRow is the column representation of an access token without
// its digest. A nullable time is 0 when the column is NULL, and an absent
// replaced_by is "".
type AccessTokenRow struct {
	ID           string
	PrincipalID  string
	Name         string
	Kind         string
	ReplacedBy   string
	CreatedBy    string
	CreatedAtMs  int64
	ExpiresAtMs  int64
	LastUsedAtMs int64
	RevokedAtMs  int64
	DisabledAtMs int64
}

// FromAccessTokenRow converts a row into a domain token.
func FromAccessTokenRow(row AccessTokenRow) accesstoken.Token {
	return accesstoken.Token{
		CreatedAt:   FromMillis(row.CreatedAtMs),
		ExpiresAt:   FromMillis(row.ExpiresAtMs),
		LastUsedAt:  FromOptionalMillis(row.LastUsedAtMs),
		RevokedAt:   FromOptionalMillis(row.RevokedAtMs),
		DisabledAt:  FromOptionalMillis(row.DisabledAtMs),
		ID:          row.ID,
		PrincipalID: row.PrincipalID,
		Name:        row.Name,
		Kind:        accesstoken.Kind(row.Kind),
		ReplacedBy:  row.ReplacedBy,
		CreatedBy:   row.CreatedBy,
	}
}

// AccessTokenOwnerRow is the column representation of the owner of a
// token.
type AccessTokenOwnerRow struct {
	ID           string
	Subject      string
	DisplayName  string
	Kind         string
	DisabledAtMs int64
	ActiveAtMs   int64
	BreakGlass   bool
}

// FromAccessTokenOwnerRow converts a row into the owner of a token.
func FromAccessTokenOwnerRow(row AccessTokenOwnerRow) accesstoken.Owner {
	return accesstoken.Owner{
		ActiveAt:    FromMillis(row.ActiveAtMs),
		DisabledAt:  FromOptionalMillis(row.DisabledAtMs),
		ID:          row.ID,
		Subject:     row.Subject,
		DisplayName: row.DisplayName,
		Kind:        principal.Kind(row.Kind),
		BreakGlass:  row.BreakGlass,
	}
}

// Status codes of a token list filter in the queries.
const (
	accessTokenFilterAll int64 = iota
	accessTokenFilterActive
	accessTokenFilterExpiring
	accessTokenFilterEnded
)

// AccessTokenListQuery is the column representation of an
// accesstoken.Query: Status is the code of the filter, AtMs its time and
// SoonMs the end of the expiring window. AfterMs and AfterID count only
// when HasAfter is set.
type AccessTokenListQuery struct {
	PrincipalID string
	AfterID     string
	Status      int64
	AtMs        int64
	SoonMs      int64
	AfterMs     int64
	HasAfter    bool
}

// ToAccessTokenListQuery converts q into its columns.
func ToAccessTokenListQuery(q accesstoken.Query) AccessTokenListQuery {
	status := map[accesstoken.Filter]int64{
		accesstoken.FilterAll:      accessTokenFilterAll,
		accesstoken.FilterActive:   accessTokenFilterActive,
		accesstoken.FilterExpiring: accessTokenFilterExpiring,
		accesstoken.FilterEnded:    accessTokenFilterEnded,
	}[q.Filter]

	return AccessTokenListQuery{
		PrincipalID: q.PrincipalID,
		AfterID:     q.AfterID,
		Status:      status,
		AtMs:        Millis(q.At),
		SoonMs:      Millis(q.At.Add(accesstoken.ExpiringWithin)),
		AfterMs:     Millis(q.AfterCreatedAt),
		HasAfter:    q.HasAfter,
	}
}

// AccessTokenPolicyRow is the column representation of the token policy.
// DisableBreakGlass stays for the database column, which this change does
// not drop. The server always reads and writes it as false: the setting is
// no longer supported.
type AccessTokenPolicyRow struct {
	MaxLifetimeMs     int64
	InactivityMs      int64
	UpdatedAtMs       int64
	DisableTokens     bool
	DisableAccounts   bool
	DisableBreakGlass bool
	// ExchangeLifetimeMs is the lifetime of an exchanged workload token.
	ExchangeLifetimeMs int64
}

// FromAccessTokenPolicyRow converts a row into the policy. An update time
// of 0 is the zero time: the seeded defaults. The break-glass disable is
// always false.
func FromAccessTokenPolicyRow(row AccessTokenPolicyRow) accesstoken.Policy {
	return accesstoken.Policy{
		UpdatedAt:         FromOptionalMillis(row.UpdatedAtMs),
		MaxLifetime:       time.Duration(row.MaxLifetimeMs) * time.Millisecond,
		Inactivity:        time.Duration(row.InactivityMs) * time.Millisecond,
		DisableTokens:     row.DisableTokens,
		DisableAccounts:   row.DisableAccounts,
		DisableBreakGlass: false,
		ExchangeLifetime:  time.Duration(row.ExchangeLifetimeMs) * time.Millisecond,
	}
}

// ToAccessTokenPolicyRow converts the policy into its columns. It always
// writes the break-glass disable as false.
func ToAccessTokenPolicyRow(p accesstoken.Policy) AccessTokenPolicyRow {
	return AccessTokenPolicyRow{
		MaxLifetimeMs:      p.MaxLifetime.Milliseconds(),
		InactivityMs:       p.Inactivity.Milliseconds(),
		UpdatedAtMs:        OptionalMillis(p.UpdatedAt),
		DisableTokens:      p.DisableTokens,
		DisableAccounts:    p.DisableAccounts,
		DisableBreakGlass:  false,
		ExchangeLifetimeMs: p.ExchangeLifetime.Milliseconds(),
	}
}
