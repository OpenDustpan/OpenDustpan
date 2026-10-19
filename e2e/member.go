package e2e

import (
	"context"
	"crypto/subtle"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// MemberSubject is the subject of the member token of an in-process
// server (Env.MemberToken): a principal that is not an administrator, so a
// step can drive a CLI with the access of its grants only. A step makes the
// principal with this subject before it grants it anything.
const MemberSubject = "e2e-member"

// memberTokens authenticates member as MemberSubject, and every other
// token with admins.
type memberTokens struct {
	admins identity.Authenticator
	member string
}

// AuthenticateToken implements identity.Authenticator.
func (m memberTokens) AuthenticateToken(ctx context.Context, token string) (identity.Principal, error) {
	if m.member != "" && subtle.ConstantTimeCompare([]byte(token), []byte(m.member)) == 1 {
		return identity.Principal{
			Subject:                MemberSubject,
			DisplayName:            MemberSubject,
			Admin:                  false,
			TokenID:                "",
			TokenExpiresAt:         time.Time{},
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
		}, nil
	}

	p, err := m.admins.AuthenticateToken(ctx, token)
	if err != nil {
		return identity.Principal{}, fmt.Errorf("authenticate: %w", err)
	}

	return p, nil
}

// AdminSubjects returns the subjects of the bootstrap administrators of
// admins, so the access-review export shows them as the server does
// without the member token.
func (m memberTokens) AdminSubjects() []string {
	if a, ok := m.admins.(interface{ AdminSubjects() []string }); ok {
		return a.AdminSubjects()
	}

	return nil
}
