package codec

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// ProviderConfigRow is the column representation of a sign-in provider.
// Scopes is a JSON list.
type ProviderConfigRow struct {
	ProviderID   string
	Mode         string
	Issuer       string
	DisplayName  string
	ClientID     string
	Scopes       string
	ClientSecret []byte
	OAuth        session.OAuthSettings
	CreatedAtMs  int64
	UpdatedAtMs  int64
	Version      int64
	AutoCreate   bool
	Enabled      bool
}

// ToProviderConfigRow converts a provider into its columns.
func ToProviderConfigRow(p session.ProviderConfig) (ProviderConfigRow, error) {
	scopes := p.Scopes
	if scopes == nil {
		scopes = []string{}
	}

	data, err := json.Marshal(scopes)
	if err != nil {
		return ProviderConfigRow{}, fmt.Errorf("encode the scopes of sign-in provider %s: %w", p.ID, err)
	}

	return ProviderConfigRow{
		ProviderID: p.ID, Mode: string(p.Mode), Issuer: p.Issuer, DisplayName: p.DisplayName, ClientID: p.ClientID,
		Scopes: string(data), ClientSecret: p.SealedSecret, OAuth: p.OAuth, CreatedAtMs: Millis(p.CreatedAt),
		UpdatedAtMs: Millis(p.UpdatedAt), Version: p.Version, AutoCreate: p.AutoCreate, Enabled: p.Enabled,
	}, nil
}

// FromProviderConfigRow converts a row into the provider.
func FromProviderConfigRow(row ProviderConfigRow) (session.ProviderConfig, error) {
	scopes := []string{}
	if err := json.Unmarshal([]byte(row.Scopes), &scopes); err != nil {
		return session.ProviderConfig{}, fmt.Errorf("decode the scopes of sign-in provider %s: %w", row.ProviderID, err)
	}

	return session.ProviderConfig{
		CreatedAt: FromMillis(row.CreatedAtMs), UpdatedAt: FromMillis(row.UpdatedAtMs), ID: row.ProviderID,
		Mode: session.ProviderMode(row.Mode), Issuer: row.Issuer, DisplayName: row.DisplayName, ClientID: row.ClientID,
		SealedSecret: bytes.Clone(row.ClientSecret), Scopes: scopes, OAuth: row.OAuth, Version: row.Version,
		AutoCreate: row.AutoCreate, Enabled: row.Enabled,
	}, nil
}

// FromSessionAccountsRow converts the account columns of the access token
// policy that a session lookup joins into the account policy of a session.
func FromSessionAccountsRow(inactivityMs int64, disable bool) session.AccountPolicy {
	return session.AccountPolicy{
		Inactivity:      time.Duration(inactivityMs) * time.Millisecond,
		DisableAccounts: disable,
	}
}
