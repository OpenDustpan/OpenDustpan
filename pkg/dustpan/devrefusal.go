package dustpan

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/session"
)

const (
	// devRefusalWindow is how long one entry of a refused dev person covers:
	// the first refusal of a person writes an entry, and the others in the
	// hour are counted.
	devRefusalWindow = time.Hour
	// devRefusalSurface is the surface in the key of the shared limiter. The
	// key kind is "signin/server".
	devRefusalSurface = "signin"
	// devRefusalReason is the fixed reason of the entry. It holds no token,
	// no cookie and no request data.
	devRefusalReason = "sign-in by a dev person refused on a production installation"
)

// devRefusalAudit returns the function that writes the audit entry of a
// refused dev sign-in, for the guards of a server that is not of the kind dev.
// It uses the shared denial limiter of cfg, so a person gets at most one entry
// each hour and the others are counted. The entry goes in the installation
// scope, through the audit store of the namespace store. A store with no audit
// support writes nothing. The shutdown flush of the shared limiter writes the
// counts that are left.
func devRefusalAudit(cfg *settings) func(ctx context.Context, subject string) {
	var entries audit.Store
	if st, ok := cfg.namespaceStore.(audit.Store); ok {
		entries = st
	}

	rec := refusal.New(nil, entries, nil, cfg.logger, nil).
		WithLimiter(cfg.denialLimiter).
		WithSurface(devRefusalSurface)

	return func(ctx context.Context, subject string) {
		person := identity.Principal{
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
			Subject:                subject,
			DisplayName:            "",
			Admin:                  false,
			TokenID:                "",
			TokenExpiresAt:         time.Time{},
			Kind:                   identity.KindPerson,
			Credential:             "",
			JobID:                  "",
		}

		rec.Record(ctx, nil, person, refusal.Denial{
			Action: session.ActionSignIn,
			Object: identity.ServerObject(),
			Reason: devRefusalReason,
			Window: devRefusalWindow,
		})
	}
}
