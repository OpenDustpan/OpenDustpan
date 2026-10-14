package codec

import (
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
	CreatedAtMs  int64
	ExpiresAtMs  int64
	LastUsedAtMs int64
	RevokedAtMs  int64
}

// FromAccessTokenRow converts a row into a domain token.
func FromAccessTokenRow(row AccessTokenRow) accesstoken.Token {
	return accesstoken.Token{
		CreatedAt:   FromMillis(row.CreatedAtMs),
		ExpiresAt:   FromMillis(row.ExpiresAtMs),
		LastUsedAt:  FromOptionalMillis(row.LastUsedAtMs),
		RevokedAt:   FromOptionalMillis(row.RevokedAtMs),
		ID:          row.ID,
		PrincipalID: row.PrincipalID,
		Name:        row.Name,
		Kind:        accesstoken.Kind(row.Kind),
		ReplacedBy:  row.ReplacedBy,
	}
}

// AccessTokenOwnerRow is the column representation of the owner of a
// token.
type AccessTokenOwnerRow struct {
	ID          string
	Subject     string
	DisplayName string
	Kind        string
}

// FromAccessTokenOwnerRow converts a row into the owner of a token.
func FromAccessTokenOwnerRow(row AccessTokenOwnerRow) accesstoken.Owner {
	return accesstoken.Owner{
		ID:          row.ID,
		Subject:     row.Subject,
		DisplayName: row.DisplayName,
		Kind:        principal.Kind(row.Kind),
	}
}

// AccessTokenListQuery is the column representation of an
// accesstoken.Query: an ActiveAtMs of 0 keeps every token, and AfterMs and
// AfterID count only when HasAfter is set.
type AccessTokenListQuery struct {
	PrincipalID string
	AfterID     string
	ActiveAtMs  int64
	AfterMs     int64
	HasAfter    bool
}

// ToAccessTokenListQuery converts q into its columns.
func ToAccessTokenListQuery(q accesstoken.Query) AccessTokenListQuery {
	return AccessTokenListQuery{
		PrincipalID: q.PrincipalID,
		AfterID:     q.AfterID,
		ActiveAtMs:  OptionalMillis(q.ActiveAt),
		AfterMs:     Millis(q.AfterCreatedAt),
		HasAfter:    q.HasAfter,
	}
}
