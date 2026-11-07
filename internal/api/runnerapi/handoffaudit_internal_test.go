package runnerapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/runner"
)

// NewHandoffAuditHandler returns a handler with only what the audit entry of a
// refused handoff token needs: the audit store, a logger, the clock, and the
// counter of failed appends. It has no namespace reader, so an entry lands in
// the installation scope.
func NewHandoffAuditHandler(
	store audit.Store, logger *slog.Logger, now func() time.Time, onFailure func(),
) *Handler {
	var h Handler

	h.audit, h.logger, h.now, h.onAuditErr = store, logger, now, onFailure
	h.limiter = denylimit.New(denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: now})

	return &h
}

// RecordUnverifiedHandoffRefusal calls the step that writes the entry of a
// refused exchange of a handoff token.
func RecordUnverifiedHandoffRefusal(ctx context.Context, h *Handler, r *http.Request, err error) {
	h.recordUnverifiedHandoffRefusal(ctx, r, err)
}

// RecordHandoffRefusal calls the step that writes the entry of ref, with no
// namespace reader.
func RecordHandoffRefusal(ctx context.Context, h *Handler, r *http.Request, ref *runner.HandoffRefusal) {
	h.recordHandoffRefusal(ctx, r, ref, "acme")
}

// RecordHandoffClaimRefusal calls the step that writes the entry of a refused
// request for the handoff token of a claim of rn.
func RecordHandoffClaimRefusal(ctx context.Context, h *Handler, r *http.Request, err error, rn *runner.Runner) {
	h.recordHandoffClaimRefusal(ctx, r, err, rn)
}

// LimitHandoffRefusals replaces the limiter of h and sets the function that
// runs for each dropped entry.
func LimitHandoffRefusals(h *Handler, l *denylimit.Limiter, onSuppressed func()) {
	h.limiter, h.onHandoffSuppressed = l, onSuppressed
}
