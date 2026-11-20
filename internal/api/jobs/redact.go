package jobs

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// maxRedactBody bounds the body of a redaction: at most job.MaxSubstitutions
// matches of job.MaxSubstitutionBytes bytes, with the room of JSON escapes.
const maxRedactBody = 1 << 20

// maxVersionID bounds the ID of a log version of a request. A longer value is
// no ID.
const maxVersionID = 64

// redactPath is the path of the redaction of the logs of a job.
const redactPath = jobPath + "/log-redactions"

// redactBody is the body of the request that redacts a log version. A field
// that is not in this type fails the request.
type redactBody struct {
	Version       string             `json:"version"`
	Substitutions []substitutionBody `json:"substitutions"`
	Reason        string             `json:"reason"`
}

// substitutionBody is one text that the server replaces with the marker. The
// server never keeps it.
type substitutionBody struct {
	Match string `json:"match"`
}

// redactResponse is the answer to a redaction: the new log version and the
// number of matches that the server replaced.
type redactResponse struct {
	Log             logRef `json:"log"`
	RedactedVersion string `json:"redactedVersion"`
	Matches         int    `json:"matches"`
}

// redactLog answers POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/log-redactions
// with the new log version. The route wrapper allowed job:read and unit:redact
// on the unit of the job. The job service checks both again, and the log
// version.
func (h *Handler) redactLog(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	if h.refuseDamaged(ctx, w, t) {
		return
	}

	if len(apidesc.HeaderValues(r, idempotencyHeader)) > 0 {
		h.writeError(ctx, w, fmt.Errorf("%w: this route takes no Idempotency-Key", idempotency.ErrInvalidKey))

		return
	}

	var body redactBody

	err := textbody.Decode(w, r, maxRedactBody, &body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	subs, err := checkRedactBody(&body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	actor := job.Actor{Principal: p, Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{}}

	req := job.RedactRequest{Version: body.Version, Substitutions: subs, Reason: body.Reason}

	next, err := h.starter.RedactLog(ctx, actor, t.job.ID, req)
	if err != nil {
		h.recordRedactRefusal(r, p, t, err)
		h.writeError(ctx, w, wrongState(err))

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, redactResponse{
		Log: logRef{
			ID: next.ID, Version: next.Version, Size: next.Size, Closed: !next.ClosedAt.IsZero(),
			Redacted: !next.RedactedAt.IsZero(),
		},
		RedactedVersion: body.Version,
		Matches:         next.Matches,
	})
}

// recordRedactRefusal writes one audit entry for a denial of unit:redact by the
// service. The entry names unit:redact on the unit that the route found. A
// refusal of another kind writes none: the state of a log is no decision of
// security.
func (h *Handler) recordRedactRefusal(r *http.Request, p identity.Principal, t *target, err error) {
	if !errors.Is(err, identity.ErrDenied) {
		return
	}

	h.guard.Record(r.Context(), r, p, refusal.Denial{
		Action: identity.ActionUnitRedact, Object: t.obj, Reason: refusal.ReasonPolicy, ReasonWithin: nil,
		Window: 0,
	})
}

// checkRedactBody is the check of the body at the boundary: an allowlist for
// the sizes, and the rules of history.ValidateReason for the reason. It refuses
// a value and never changes it. An empty match is a refusal.
//
// Defense in depth: the job service checks the same values again.
func checkRedactBody(b *redactBody) ([]job.Substitution, error) {
	err := history.ValidateReason(b.Reason)
	if err != nil {
		return nil, fmt.Errorf("%w: reason: %w", job.ErrInvalid, err)
	}

	if b.Version == "" || len(b.Version) > maxVersionID {
		return nil, fmt.Errorf("%w: version must hold 1 to %d bytes", job.ErrInvalid, maxVersionID)
	}

	if len(b.Substitutions) == 0 || len(b.Substitutions) > job.MaxSubstitutions {
		return nil, fmt.Errorf("%w: send 1 to %d substitutions", job.ErrInvalid, job.MaxSubstitutions)
	}

	subs := make([]job.Substitution, 0, len(b.Substitutions))

	for i := range b.Substitutions {
		m := b.Substitutions[i].Match
		if m == "" || len(m) > job.MaxSubstitutionBytes {
			return nil, fmt.Errorf("%w: the match of substitution %d must hold 1 to %d bytes", job.ErrInvalid, i,
				job.MaxSubstitutionBytes)
		}

		subs = append(subs, job.Substitution{Match: []byte(m)})
	}

	return subs, nil
}
