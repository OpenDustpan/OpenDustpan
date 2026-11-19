package dustpan

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"time"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/firstadminsvc"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/firstadmin"
	"opendustpan.org/opendustpan/pkg/session"
)

// firstAdministratorPolicyTimeout is the time that the read of the session
// policy may take when the service asks for the freshness window. A read that
// ends in time gives a window of 0, which refuses each sign-in.
const firstAdministratorPolicyTimeout = 5 * time.Second

// newFirstAdministrator builds the service of the first administrator on the
// namespace store. It returns nil, with no error, when the store keeps no
// first administrator port, no temporary administrator records or no server
// audit log, or when the server has no principal service: the three routes
// are then not mounted. It never returns a typed nil.
//
//nolint:ireturn // The server API reads the service through its own port.
func newFirstAdministrator(in *serverHandlerInputs) (serverapi.FirstAdministratorService, error) {
	store, ok := in.store.(firstadmin.Store)
	if !ok || in.principals == nil || in.extras.audit == nil {
		return nil, nil //nolint:nilnil // No store port: the routes are not mounted.
	}

	temps := tempReaderOf(in.store)
	if temps == nil {
		return nil, nil //nolint:nilnil // No temporary administrator records: the routes are not mounted.
	}

	now := in.now
	if now == nil {
		now = time.Now
	}

	svc, err := firstadminsvc.New(firstadminsvc.Config{
		Store: store, Principals: in.principals, TempAdmins: temps, Audit: in.extras.audit, Now: now,
		Random:  rand.Reader,
		GrantID: func() string { return ident.NewID(access.GrantIDPrefix) },
		Window:  func(ctx context.Context) time.Duration { return freshnessWindowOf(ctx, in.policies, in.logger) },
		Logger:  in.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("first administrator: %w", err)
	}

	return svc, nil
}

// freshnessWindowOf returns the token freshness window of the session policy.
// With no policy service it is the default window, as the console API reads
// it. A read that fails gives 0: the window of 0 refuses each sign-in, so the
// redeem fails closed.
func freshnessWindowOf(ctx context.Context, policies *session.Policies, logger *slog.Logger) time.Duration {
	if policies == nil {
		return session.DefaultTokenFreshness
	}

	ctx, cancel := context.WithTimeout(ctx, firstAdministratorPolicyTimeout)
	defer cancel()

	policy, err := policies.Policy(ctx)
	if err != nil {
		logger.WarnContext(ctx, "first administrator: the session policy cannot be read, so no sign-in is fresh",
			slog.String("error", err.Error()))

		return 0
	}

	return policy.TokenFreshness
}
