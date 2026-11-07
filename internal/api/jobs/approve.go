package jobs

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

const (
	// maxApproveBody bounds the body of an approval. A verdict, a binding of
	// four short values and a comment of at most audit.MaxReasonLen characters
	// are far less.
	maxApproveBody = 16 << 10
	// maxBindingField bounds each text of the plan binding, in bytes.
	maxBindingField = 128
)

// errUnprocessable reports a body that is valid JSON with known fields, but
// with a value that the API refuses: a verdict that is not one of the two, a
// comment that audit.ValidateReason refuses, or a text of the plan binding that
// is too long or holds a hidden character.
var errUnprocessable = errors.New("request body holds a value that is not valid")

// approveBody is the body of the request that records the verdict of an
// approver. A field that is not in this type fails the request.
//
// The plan binding is what the approver saw: the plan, the SHA-256 of both of
// its parts, and the state version that it read. They are the id, the two
// sha256 values and the stateVersion of the plan in the job document. A
// rejection needs no binding.
type approveBody struct {
	Verdict     string       `json:"verdict"`
	PlanBinding *bindingBody `json:"planBinding"`
	Comment     string       `json:"comment"`
}

// bindingBody is the wire form of job.PlanBinding.
type bindingBody struct {
	Plan          string `json:"plan"`
	DisplaySHA256 string `json:"displaySha256"`
	ApplySHA256   string `json:"applySha256"`
	StateVersion  string `json:"stateVersion"`
}

// Reasons of the audit entries of the refusals of an approval. The entry never
// holds the text of the error.
const (
	reasonSelfApproval    = "the person who started the job cannot approve it"
	reasonAuthStale       = "the approval of a destroy needs a fresh MFA step"
	reasonAlreadyApproved = "the approver approved this phase already"
	reasonPlanChanged     = "the plan of the approval is not the stored plan"
	reasonPlanStale       = "the stored plan is too old or was redacted"
)

// classifyApproval maps the errors of the body and of the approval of a job.
// An error that no case knows goes to package problem.
func classifyApproval(err error) (problem.Problem, bool) {
	if tooLarge, ok := errors.AsType[*textbody.TooLargeError](err); ok {
		return problem.New(http.StatusRequestEntityTooLarge, errcode.RequestBodyTooLarge, tooLarge.Error(),
			errcode.Quantity("maxBytes", tooLarge.Limit)), true
	}

	switch {
	case errors.Is(err, textbody.ErrEmpty), errors.Is(err, textbody.ErrText), errors.Is(err, textbody.ErrJSON),
		errors.Is(err, textbody.ErrUnreadable):
		return problem.New(http.StatusBadRequest, errcode.RequestBodyInvalid,
			"send one JSON object with a known field set"), true
	case errors.Is(err, errUnprocessable):
		return problem.Refused(err, http.StatusUnprocessableEntity, errcode.JobInvalid), true
	case errors.Is(err, job.ErrSelfApproval):
		return problem.Refused(err, http.StatusForbidden, errcode.JobSelfApproval), true
	case errors.Is(err, job.ErrAuthStale):
		return problem.Refused(err, http.StatusForbidden, errcode.JobAuthStale), true
	case errors.Is(err, job.ErrAlreadyApproved):
		return problem.Refused(err, http.StatusConflict, errcode.JobAlreadyApproved), true
	case errors.Is(err, job.ErrFinished):
		return problem.Refused(err, http.StatusConflict, errcode.JobFinished), true
	case errors.Is(err, job.ErrTransition):
		return problem.Refused(err, http.StatusConflict, errcode.JobNotAwaitingApproval), true
	case errors.Is(err, job.ErrPlanChanged):
		return problem.Refused(err, http.StatusConflict, errcode.JobPlanChanged), true
	case errors.Is(err, job.ErrPlanStale):
		return problem.Refused(err, http.StatusConflict, errcode.JobPlanStale), true
	case errors.Is(err, job.ErrConflict):
		return problem.Refused(err, http.StatusConflict, errcode.JobConflict), true
	}

	// Not a refusal of an approval: package problem answers the error.
	return problem.Pass()
}

// approveJob answers POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/approvals
// with the job after the verdict. The route wrapper allowed job:read and
// job:approve on the unit of the job. The job service checks both again, and
// checks the state of the job, the approver, the plan and the MFA step of a
// destroy (defense in depth: this handler holds no copy of those rules).
//
// This handler does not set Actor.ConfirmAuth or Actor.AuthAt: no part of this
// API knows the MFA step of the session yet. So the approval of a destroy fails
// closed with job.auth-stale until the server sets them from the sign-in.
func (h *Handler) approveJob(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	// The route takes no idempotency key. Refuse it with an error, so the
	// server never ignores a key in silence.
	if len(apidesc.HeaderValues(r, idempotencyHeader)) > 0 {
		h.writeError(ctx, w, fmt.Errorf("%w: this route takes no Idempotency-Key", idempotency.ErrInvalidKey))

		return
	}

	var body approveBody

	err := textbody.Decode(w, r, maxApproveBody, &body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	verdict, bound, err := checkApproveBody(&body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	actor := job.Actor{Principal: p, Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{}}

	j, err := h.starter.Approve(ctx, actor, t.job.ID, verdict, bound, body.Comment)
	if err != nil {
		h.recordApprovalRefusal(r, p, t, err)
		h.writeError(ctx, w, err)

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

// checkApproveBody is the check of the body at the boundary. It uses an
// allowlist for the verdict, the rules of the audit reason for the comment,
// and a bound for each text of the plan binding. It refuses a value and never
// changes it.
//
// Defense in depth: the job service checks the verdict and the comment again.
func checkApproveBody(b *approveBody) (job.Verdict, job.PlanBinding, error) {
	v := job.Verdict(b.Verdict)
	if v != job.VerdictApprove && v != job.VerdictReject {
		return "", job.PlanBinding{}, fmt.Errorf("%w: the verdict is %q, send approve or reject", errUnprocessable,
			refusal.Clean(b.Verdict, maxOperationEcho))
	}

	err := audit.ValidateReason(b.Comment)
	if err != nil {
		return "", job.PlanBinding{}, fmt.Errorf("%w: comment: %w", errUnprocessable, err)
	}

	var bound job.PlanBinding

	if pb := b.PlanBinding; pb != nil {
		for _, field := range []string{pb.Plan, pb.DisplaySHA256, pb.ApplySHA256, pb.StateVersion} {
			if len(field) > maxBindingField || !utf8.ValidString(field) ||
				strings.ContainsFunc(field, audit.IsHiddenRune) {
				return "", job.PlanBinding{}, fmt.Errorf(
					"%w: a text of planBinding is too long or holds a hidden character",
					errUnprocessable,
				)
			}
		}

		bound = job.PlanBinding{
			Plan: pb.Plan, DisplaySHA256: pb.DisplaySHA256, ApplySHA256: pb.ApplySHA256, StateVersion: pb.StateVersion,
		}
	}

	return v, bound, nil
}

// recordApprovalRefusal writes one audit entry for a refusal of an approval
// that is a decision of security: a denial of job:approve, an agent, the
// starter, a stale MFA step, and a plan that is not the stored one. A request
// that is not valid, a job that does not wait, and a version conflict write
// none. The entry names job:approve on the unit that the route found, whatever
// the error names.
func (h *Handler) recordApprovalRefusal(r *http.Request, p identity.Principal, t *target, err error) {
	reason, ok := approvalRefusalReason(err)
	if !ok {
		return
	}

	h.guard.Record(r.Context(), r, p, refusal.Denial{
		Action: identity.ActionJobApprove, Object: t.obj, Reason: reason, Window: 0,
	})
}

// approvalRefusalReason returns the reason for the audit entry of err, and
// false for an error that is not a refusal of security.
func approvalRefusalReason(err error) (string, bool) {
	switch {
	case errors.Is(err, identity.ErrDenied):
		return refusal.ReasonPolicy, true
	case errors.Is(err, job.ErrAgentDenied):
		return reasonAgentDenied, true
	case errors.Is(err, job.ErrSelfApproval):
		return reasonSelfApproval, true
	case errors.Is(err, job.ErrAuthStale):
		return reasonAuthStale, true
	case errors.Is(err, job.ErrAlreadyApproved):
		return reasonAlreadyApproved, true
	case errors.Is(err, job.ErrPlanChanged):
		return reasonPlanChanged, true
	case errors.Is(err, job.ErrPlanStale):
		return reasonPlanStale, true
	default:
		return "", false
	}
}
