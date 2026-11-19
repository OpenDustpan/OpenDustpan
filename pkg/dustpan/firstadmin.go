package dustpan

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"time"

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
// namespace store. It returns mounted false, and a nil service, when the store
// keeps no first administrator port or no temporary administrator records, or
// when the server has no principal service or no audit log: the three routes
// are then not mounted, and the function logs the reason at Info level. The
// caller must not put the nil service in an interface.
func newFirstAdministrator(in *serverHandlerInputs) (*firstadminsvc.Service, bool, error) {
	store, ok := in.store.(firstadmin.Store)
	if !ok || in.principals == nil || in.extras.audit == nil {
		in.logger.Info("first administrator: the routes are not mounted: the store has no first administrator port, " +
			"or the server has no principal service or no audit log")

		return nil, false, nil
	}

	temps := tempReaderOf(in.store)
	if temps == nil {
		in.logger.Info(
			"first administrator: the routes are not mounted: the store has no temporary administrator records",
		)

		return nil, false, nil
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
		return nil, false, fmt.Errorf("first administrator: %w", err)
	}

	return svc, true, nil
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
