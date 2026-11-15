package jobs

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// maxCancelBody bounds the body of a cancel. A reason of at most
// audit.MaxReasonLen characters is far less.
const maxCancelBody = 16 << 10

// cancelBody is the body of the request that cancels a job. A field that is not
// in this type fails the request.
type cancelBody struct {
	Reason string `json:"reason"`
}

// cancelPath is the path of the cancel of a job.
const cancelPath = jobPath + "/cancel"

// cancelJob answers POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/cancel
// with the job after the cancel. The route wrapper allowed job:read and
// job:cancel on the unit of the job. The job service checks both again, and
// checks the state of the job.
func (h *Handler) cancelJob(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	// The route takes no idempotency key. Refuse it with an error, so the
	// server never ignores a key in silence.
	if len(apidesc.HeaderValues(r, idempotencyHeader)) > 0 {
		h.writeError(ctx, w, fmt.Errorf("%w: this route takes no Idempotency-Key", idempotency.ErrInvalidKey))

		return
	}

	var body cancelBody

	err := textbody.Decode(w, r, maxCancelBody, &body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: the job service checks the reason again.
	err = audit.ValidateReason(body.Reason)
	if err != nil {
		h.writeError(ctx, w, fmt.Errorf("%w: reason: %w", errUnprocessable, err))

		return
	}

	// No client gives the agent or the authentication: the server sets them.
	actor := job.Actor{Principal: p, Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{}}

	j, err := h.starter.Cancel(ctx, actor, t.job.ID, body.Reason)
	if err != nil {
		h.recordCancelRefusal(r, p, t, err)

		// A change that is in git for good ended the job for a cancel: the
		// answer is the code job.finished.
		if errors.Is(err, job.ErrDelivered) {
			err = fmt.Errorf("%w: %w", job.ErrFinished, err)
		}

		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: the service cancels the job of the path. This check
	// stays so that a job of another namespace never answers under this one,
	// whatever a port of the service gives back.
	if j.ID != t.job.ID || j.Spec.Unit.Namespace() != t.ns.Name {
		h.writeError(ctx, w, errOtherNamespace)

		return
	}

	setETag(w, &j)
	h.answerStartedJob(ctx, w, http.StatusOK, &j)
}

// recordCancelRefusal writes one audit entry for a denial of job:cancel by the
// service. The entry names job:cancel on the unit that the route found,
// whatever the error names. A refusal of another kind writes none: the state of
// the job and a version conflict are no decision of security.
func (h *Handler) recordCancelRefusal(r *http.Request, p identity.Principal, t *target, err error) {
	if !errors.Is(err, identity.ErrDenied) {
		return
	}

	h.guard.Record(r.Context(), r, p, refusal.Denial{
		Action: identity.ActionJobCancel, Object: t.obj, Reason: refusal.ReasonPolicy, ReasonWithin: nil, Window: 0,
	})
}
