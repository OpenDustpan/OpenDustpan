package principal

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
)

// DevGuard is an identity.Authenticator that refuses a caller whose subject
// starts with DevSubjectPrefix.
type DevGuard struct {
	next      identity.Authenticator
	onRefused func(ctx context.Context, subject string)
}

var _ identity.Authenticator = DevGuard{next: nil, onRefused: nil}

// RefuseDevSubjects returns an authenticator that asks next and then refuses
// each caller whose subject starts with DevSubjectPrefix, with
// identity.ErrUnauthenticated. A server that is not of the kind dev wraps its
// authenticator with it, so a token of a dev person that a restored dev
// database holds authenticates nothing.
func RefuseDevSubjects(next identity.Authenticator) DevGuard {
	return DevGuard{next: next, onRefused: nil}
}

// OnRefused returns a copy of g that calls f with the context and the subject
// of each caller that g refuses. The call comes before g returns the error. f
// returns within a bounded time, and it must not put the token or any request data in a
// record.
func (g DevGuard) OnRefused(f func(ctx context.Context, subject string)) DevGuard {
	g.onRefused = f

	return g
}

// AuthenticateToken implements identity.Authenticator.
func (g DevGuard) AuthenticateToken(ctx context.Context, token string) (identity.Principal, error) {
	p, err := g.next.AuthenticateToken(ctx, token)
	if err != nil {
		return identity.Principal{}, err
	}

	if IsDevSubject(p.Subject) {
		if g.onRefused != nil {
			g.onRefused(ctx, p.Subject)
		}

		return identity.Principal{}, fmt.Errorf("%w: a token of a dev person is not valid on this installation",
			identity.ErrUnauthenticated)
	}

	return p, nil
}
