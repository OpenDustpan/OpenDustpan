package dustpan

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/pkg/backup"
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

// devRefusalState holds the one recorder of the refused dev sign-ins. The
// guards are built before the namespace service and the metrics exist, so bind
// builds the recorder later, before the server takes requests.
type devRefusalState struct {
	rec *refusal.Recorder
}

// bind builds the recorder, from the audit store of cfg, the namespace reader
// and the failure counter. It must run before the server takes requests. The
// recorder needs the reader because the shared limiter also holds the counts
// of keys of other principals, and the shutdown flush of such a count is an
// entry in the namespace of that key. It answers ErrMissingOption when cfg has
// no audit store.
func (d *devRefusalState) bind(cfg *settings, namespaces refusal.Namespaces, onFailure func()) error {
	entries, err := auditStoreOf(cfg)
	if err != nil {
		return err
	}

	rec, err := refusal.New(nil, entries, namespaces, cfg.logger, onFailure)
	if err != nil {
		return fmt.Errorf("dev sign-in refusal recorder: %w", err)
	}

	d.rec = rec.WithLimiter(cfg.denialLimiter).WithSurface(devRefusalSurface)

	return nil
}

// recorder answers the one recorder of cfg, or nil before bind ran.
func (d *devRefusalState) recorder() *refusal.Recorder {
	return d.rec
}

// devRefusalAudit returns the function that writes the audit entry of a
// refused dev sign-in, for the guards of a server that is not of the kind dev.
// It uses the shared denial limiter of cfg, so a person gets at most one entry
// each hour and the others are counted. When the table of the limiter is full,
// a refusal uses the overflow slot, which has a window of one minute, so the
// bound of one entry each hour does not hold then. The entry goes in the
// installation scope, through the audit store of the namespace store. The
// shutdown flush of the shared limiter writes the counts that are left. All guards of cfg share one
// recorder.
func devRefusalAudit(cfg *settings) func(ctx context.Context, subject string) {
	state := cfg.devRefusal

	return func(ctx context.Context, subject string) {
		// The subject comes from a stored person and can hold any bytes. The
		// backup report cleans it with the same function.
		clean, _ := backup.CleanSubject(subject)

		person := identity.Principal{
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
			Subject:                clean,
			DisplayName:            "",
			Admin:                  false,
			TokenID:                "",
			TokenExpiresAt:         time.Time{},
			Kind:                   identity.KindPerson,
			Credential:             "",
			JobID:                  "",
		}

		// Defense in depth: the recorder detaches its write from the context
		// and bounds it with a timeout too. This detach stays so that a change
		// of the recorder cannot let a client that cancels its request drop the
		// record of the refusal.
		ctx = context.WithoutCancel(ctx)

		rec := state.recorder()
		if rec == nil {
			// bind runs before the server takes requests, so this is a bug. The
			// refusal still stands; the log keeps the loss visible.
			cfg.logger.ErrorContext(ctx, "dev sign-in refusal has no audit recorder")

			return
		}

		rec.Record(ctx, nil, person, refusal.Denial{
			Action:       session.ActionSignIn,
			Object:       identity.ServerObject(),
			Reason:       devRefusalReason,
			ReasonWithin: nil,
			Window:       devRefusalWindow,
		})
	}
}
