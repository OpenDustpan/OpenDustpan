package drift

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

const (
	// maxStartBody bounds the body of a request to start a drift check. A
	// request has a pool, a location and a reason: far less.
	maxStartBody = 64 << 10
	// channelHeader declares where a request comes from.
	channelHeader = "Dustpan-Channel"
	// idempotencyHeader is the header of the key that this route refuses.
	idempotencyHeader = "Idempotency-Key"
)

// startBody is the body of the request that starts a drift check. The body is
// optional: with none, the server takes the only job pool of the namespace and
// the code location that it knows. The server fixes the operation, the purpose,
// the source kind, the executor and the steps. A client never sends them, and
// a field that is not in this type fails the request.
type startBody struct {
	Pool     *string          `dustpan:"optional" json:"pool"`
	Location *locationRequest `dustpan:"optional" json:"location"`
	Reason   *string          `dustpan:"optional" json:"reason"`
}

type locationRequest struct {
	Connection string `dustpan:"optional" json:"connection"`
	Repository string `dustpan:"optional" json:"repository"`
	Ref        string `dustpan:"optional" json:"ref"`
	Directory  string `dustpan:"optional" json:"directory"`
	Revision   string `dustpan:"optional" json:"revision"`
}

// checkResponse is the answer to a request that starts a drift check: the job
// that runs it. The result of the check is not in the answer. It appears in the
// drift read when the job ends.
//
// Contract: never remove or rename these fields.
type checkResponse struct {
	JobID string       `json:"jobId"` //nolint:tagliatelle // The description of the API demands camelCase: jobId.
	Unit  unitResponse `json:"unit"`
}

// startCheck answers POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift-checks
// with 202 and the job that runs the check. The route wrapper already allowed
// namespace:read, unit:read and unit:run-plan. The job service checks the start
// action again and applies the gates, the manual locks, the freezes and the
// limits of open jobs of the namespace. The source kind of the job is always
// api: no field of the body changes it.
func (h *Handler) startCheck(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	// The service gives two starts of one unit in one minute one job, so this
	// route takes no key. The server never ignores a key without an error.
	if len(apidesc.HeaderValues(r, idempotencyHeader)) > 0 {
		h.writeError(ctx, w, fmt.Errorf("%w: this route takes no %s header: two starts of one unit in one minute "+
			"give one job", errBody, idempotencyHeader))

		return
	}

	body, err := readStartBody(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	req := checkRequest(r, t, body)

	// Defense in depth: the drift service validates the request too. This
	// check stays so that a text that the server cannot keep is refused at the
	// boundary, before another layer holds it.
	if err = req.Validate(); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// A check starts a job, which inserts a durable operation that only a
	// worker runs.
	if err = admit.Start(ctx, h.workers); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	actor := job.Actor{Principal: p, Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{}}

	id, err := h.checker.Check(ctx, actor, req)
	if err != nil {
		// Every refusal of the start writes an audit entry, not only the
		// refusal of the Authorizer: a policy, an agent mode, a freeze, a
		// limit and a gate refuse the person too.
		if reason, ok := refusalReason(err); ok {
			h.guard.Record(ctx, r, p, refusal.Denial{
				Action: identity.ActionUnitRunPlan, Object: t.obj, Reason: reason, ReasonWithin: nil, Window: 0,
			})
		}

		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("Location", "/api/dustpan/v1/namespaces/"+url.PathEscape(t.ns.Name.String())+"/jobs/"+
		url.PathEscape(string(id)))

	h.writeJSON(ctx, w, http.StatusAccepted, checkResponse{JobID: string(id), Unit: toUnitResponse(t)})
}

// checkRequest builds the request to the checker from the body and the request.
func checkRequest(r *http.Request, t *target, b *startBody) drift.CheckRequest {
	req := drift.CheckRequest{
		Unit: t.obj, Location: job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""},
		Pool: "", Reason: "", Channel: sessionauth.AuditChannel(r, channelHeader), SourceIP: clientip.String(r),
		UserAgent: truncate(userAgent(r), audit.MaxUserAgentLen), RequestID: requestid.FromContext(r.Context()),
	}

	// The pool and the code location decide which hosts run which code. The
	// body may name them, and the job service then checks the extra actions:
	// namespace:manage-runners for a pool and unit:define-steps for a location.
	if b.Pool != nil {
		req.Pool = *b.Pool
	}

	if b.Reason != nil {
		req.Reason = *b.Reason
	}

	if l := b.Location; l != nil {
		req.Location = job.Location{
			Connection: l.Connection, Repository: l.Repository, Ref: l.Ref, Directory: l.Directory,
			Revision: l.Revision,
		}
	}

	return req
}

// Reasons of the audit entries of the refusals of a start. The entry never
// holds the text of the error.
const (
	reasonPolicyDenied = "policy denied the job"
	reasonAgentDenied  = "an agent cannot do this"
	reasonFrozen       = "a change freeze is active"
	reasonLimit        = "too many open jobs"
	reasonGate         = "an enforced gate covers the unit"
	reasonUnsupported  = "the server cannot start this job"
)

// refusalReason returns the reason for the audit entry of err, and false for
// an error that is not a refusal of the server: a request that is not valid
// and a failure of the server write no denial entry.
func refusalReason(err error) (string, bool) {
	var gates *job.GateError

	// A denial comes first, as in the answer: an error that also wraps a
	// specific refusal names the policy, and the entry never names the state of
	// an object the caller has no right on.
	switch {
	case errors.Is(err, identity.ErrDenied):
		return refusal.ReasonPolicy, true
	case errors.As(err, &gates):
		ids := make([]string, 0, len(gates.Gates))
		for _, g := range gates.Gates {
			ids = append(ids, g.ID)
		}

		return reasonGate + ": " + strings.Join(ids, ", "), true
	case errors.Is(err, job.ErrGateEnforced):
		return reasonGate, true
	case errors.Is(err, job.ErrPolicyDenied):
		return reasonPolicyDenied, true
	case errors.Is(err, job.ErrAgentDenied):
		return reasonAgentDenied, true
	case errors.Is(err, job.ErrFrozen):
		return reasonFrozen, true
	case errors.Is(err, job.ErrLimit):
		return reasonLimit, true
	case errors.Is(err, job.ErrUnsupported):
		return reasonUnsupported, true
	default:
		return "", false
	}
}

// readStartBody reads and decodes the optional body. An empty body is no body.
// It refuses a body above maxStartBody bytes, an unknown field, a value that
// is null, data after the document, and a repository that
// job.CheckRepository refuses.
func readStartBody(w http.ResponseWriter, r *http.Request) (*startBody, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxStartBody))
	if err != nil {
		return nil, fmt.Errorf("%w: send at most %d bytes of JSON", errBody, maxStartBody)
	}

	if len(bytes.TrimSpace(raw)) == 0 {
		return &startBody{Pool: nil, Location: nil, Reason: nil}, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var body startBody
	if err := dec.Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: send one JSON object with a known field set", errBody)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: send one JSON object only", errBody)
	}

	// Defense in depth: the planner of the job service checks the whole code
	// location. This check stays so that a repository with credentials is
	// refused at the boundary, before any other layer holds it.
	if body.Location != nil {
		if err := job.CheckRepository(body.Location.Repository); err != nil {
			return nil, fmt.Errorf("location: %w", err)
		}
	}

	return &body, nil
}

// truncate cuts s to at most n bytes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return strings.ToValidUTF8(s[:n], "")
}
