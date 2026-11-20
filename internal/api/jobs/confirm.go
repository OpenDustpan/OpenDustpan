package jobs

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// maxConfirmBody bounds the body of a confirmation: a verdict and a binding of
// four short values.
const maxConfirmBody = 16 << 10

// confirmationsPath is the path of the confirmations of a job.
const confirmationsPath = jobPath + "/confirmations"

// reasonNotRequester is the reason of the audit entry of a confirmation by a
// person who did not start the job.
const reasonNotRequester = "only the person who started the job can confirm it"

// confirmBody is the body of the request that confirms a job. A field that is
// not in this type fails the request. The binding is what the person saw, as
// in an approval. A rejection needs no binding.
type confirmBody struct {
	Verdict     string       `json:"verdict"`
	PlanBinding *bindingBody `json:"planBinding"`
}

// confirmJob answers POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/confirmations
// with the job after the verdict. The route wrapper allowed job:read. The job
// service checks it again, the start action of the job, the state, the
// requester, the fresh authentication and the plan.
//
// This handler does not set Actor.ConfirmAuth or Actor.AuthAt: no part of this
// API knows the fresh step of the session yet. So the confirmation of a job
// fails closed until the server sets them from the sign-in. A rejection needs
// no step.
func (h *Handler) confirmJob(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	if h.refuseDamaged(ctx, w, t) {
		return
	}

	if len(apidesc.HeaderValues(r, idempotencyHeader)) > 0 {
		h.writeError(ctx, w, fmt.Errorf("%w: this route takes no Idempotency-Key", idempotency.ErrInvalidKey))

		return
	}

	var body confirmBody

	err := textbody.Decode(w, r, maxConfirmBody, &body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: the job service checks the verdict again.
	verdict, bound, err := checkApproveBody(
		&approveBody{Verdict: body.Verdict, PlanBinding: body.PlanBinding, Comment: ""},
	)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	actor := job.Actor{Principal: p, Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{}}

	j, err := h.starter.Confirm(ctx, actor, t.job.ID, verdict, bound)
	if err != nil {
		h.recordConfirmRefusal(r, p, t, err)
		h.writeError(ctx, w, wrongState(err))

		return
	}

	// Defense in depth: the service records the verdict on the job of the
	// path. This check stays so that a job of another namespace never answers
	// under this one, whatever a port of the service gives back.
	if j.ID != t.job.ID || j.Spec.Unit.Namespace() != t.ns.Name {
		h.writeError(ctx, w, errOtherNamespace)

		return
	}

	setETag(w, &j)
	h.answerStartedJob(ctx, w, http.StatusCreated, &j)
}

// recordConfirmRefusal writes one audit entry for a refusal of a confirmation
// that is a decision of security. The entry names the start action of the job
// on the unit that the route found, whatever the error names.
func (h *Handler) recordConfirmRefusal(r *http.Request, p identity.Principal, t *target, err error) {
	reason, ok := approvalRefusalReason(err)
	if errors.Is(err, job.ErrNotRequester) {
		reason, ok = reasonNotRequester, true
	}

	if !ok {
		return
	}

	action, actionErr := job.ConfirmAction(&t.job)
	if actionErr != nil {
		return
	}

	h.guard.Record(r.Context(), r, p, refusal.Denial{
		Action: action, Object: t.obj, Reason: reason, ReasonWithin: nil, Window: 0,
	})
}
