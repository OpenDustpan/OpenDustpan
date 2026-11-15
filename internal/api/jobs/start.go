package jobs

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
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/driftsvc"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

const (
	// maxStartBody bounds the body of a request to start a job. A start has a
	// unit, an operation, a pool, a location and a reason: far less.
	maxStartBody = 64 << 10
	// idempotencyHeader and replayedHeader are the headers of the key. The
	// job store holds the key, so the answer of a retry is the job.
	idempotencyHeader = "Idempotency-Key"
	replayedHeader    = "Idempotent-Replayed"
	// channelHeader declares where a request comes from.
	channelHeader = "Dustpan-Channel"
)

var (
	// errBody reports a request body that is not valid.
	errBody = errors.New("request body is not valid")
	// errOtherNamespace reports a job of another namespace than the request.
	errOtherNamespace = errors.New("the job is in another namespace than the request")
)

// maxOperationEcho bounds the text of an operation that a refusal repeats.
const maxOperationEcho = 32

// startBody is the body of the request that starts a job. The server fixes
// the source kind, the executor, the steps and the profile. A client never
// sends them, and a field that is not in this type fails the request.
type startBody struct {
	Unit      *unitRequest     `json:"unit"`
	Operation string           `json:"operation"`
	Pool      string           `json:"pool"`
	Location  *locationRequest `json:"location"`
	Reason    string           `json:"reason"`
}

type unitRequest struct {
	Tool string `json:"tool"`
	Name string `json:"name"`
}

type locationRequest struct {
	Connection string `json:"connection"`
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
	Directory  string `json:"directory"`
	Revision   string `json:"revision"`
}

// startJob answers POST /api/dustpan/v1/namespaces/{namespace}/jobs with the
// job that the request starts. The route wrapper allowed namespace:read on the
// namespace. The job service checks the start action on the unit of the body,
// fails closed, and applies the gates, the approval defaults, the freezes and
// the manual locks of the namespace. The source kind of the job is always
// api: no field of the body changes it.
func (h *Handler) startJob(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	key, err := idempotencyKey(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	body, err := readStartBody(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	req, err := h.startRequest(r, t, body, key)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// A start inserts a durable operation, and only a worker runs it.
	if err = admit.Start(ctx, h.workers); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	actor := job.Actor{Principal: p, Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{}}

	j, created, err := h.starter.StartOnce(ctx, actor, req)
	if err != nil {
		// Every refusal of the start writes an audit entry, not only the
		// refusal of the Authorizer: a policy, an agent mode, a freeze, a
		// limit and a gate refuse the person too.
		if reason, ok := refusalReason(err); ok {
			action, object := deniedTarget(err, req.Unit)
			h.guard.Record(ctx, r, p, refusal.Denial{
				Action: action, Object: object, Reason: reason, ReasonWithin: nil, Window: 0,
			})
		}

		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: the service starts the job on the unit of the request.
	// This check stays so that a job of another namespace never answers under
	// this one, whatever a port of the service gives back.
	if j.Spec.Unit.Namespace() != t.ns.Name {
		h.writeError(ctx, w, errOtherNamespace)

		return
	}

	setETag(w, &j)
	w.Header().Set("Location", "/api/dustpan/v1/namespaces/"+url.PathEscape(t.ns.Name.String())+"/jobs/"+
		url.PathEscape(string(j.ID)))

	if !created && key != "" {
		w.Header().Set(replayedHeader, "true")
	}

	h.answerStartedJob(ctx, w, http.StatusCreated, &j)
}

// deniedTarget returns the action and the object of the audit entry of the
// refusal err of a start on unit: the action and the object that the
// authorizer refused, for a *job.DeniedError, the start action of the planned
// job on its unit, for a *job.StartRefusalError, and else the start of a plan
// on unit, the action of the route.
func deniedTarget(err error, unit identity.Object) (identity.Action, identity.Object) {
	action, object := refusedTarget(err)
	if action == "" {
		return identity.ActionUnitRunPlan, unit
	}

	// Defense in depth: the service checks the actions on the unit of the
	// request and on its namespace only, and plans the job on that unit. This
	// check stays so that a refusal that names an object of another namespace
	// never writes its entry into the audit log of that namespace.
	if object.Namespace() != unit.Namespace() {
		return identity.ActionUnitRunPlan, unit
	}

	return action, object
}

// refusedTarget returns the action and the object that the typed refusal in
// err names, and an empty action when err holds none.
func refusedTarget(err error) (identity.Action, identity.Object) {
	if denied, ok := errors.AsType[*job.DeniedError](err); ok {
		return denied.Action, denied.Object
	}

	if refused, ok := errors.AsType[*job.StartRefusalError](err); ok {
		return refused.Action, refused.Object
	}

	return "", identity.Object{}
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
// an error that is not a refusal of the server: a request that is not valid,
// a failure of the server and a replay conflict write no denial entry.
func refusalReason(err error) (string, bool) {
	var gates *job.GateError

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

// idempotencyKey returns the key of the request, or "" for a request with no
// key. A key that is not valid, or a second key, is an error.
func idempotencyKey(r *http.Request) (string, error) {
	values := apidesc.HeaderValues(r, idempotencyHeader)

	switch len(values) {
	case 0:
		return "", nil
	case 1:
		k, err := idempotency.ParseKey(values[0])
		if err != nil {
			return "", fmt.Errorf("%w: send 1 to %d printable ASCII characters", err, idempotency.MaxKeyLength)
		}

		// The keys of drift checks share the key space of the jobs. The server
		// makes them, so a caller key with the prefix is refused.
		if driftsvc.ReservedKey(string(k)) {
			return "", fmt.Errorf(
				"%w: the prefix %q is reserved for the server",
				idempotency.ErrInvalidKey,
				driftsvc.CheckKeyPrefix,
			)
		}

		return string(k), nil
	default:
		return "", fmt.Errorf("%w: send one Idempotency-Key header", idempotency.ErrInvalidKey)
	}
}

// readStartBody reads and decodes the body. It refuses a body above
// maxStartBody bytes, an unknown field, and data after the document.
func readStartBody(w http.ResponseWriter, r *http.Request) (*startBody, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxStartBody))
	if err != nil {
		return nil, fmt.Errorf("%w: send at most %d bytes of JSON", errBody, maxStartBody)
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

	return &body, nil
}

// startRequest builds the request to the job service from the body. It checks
// each field against an allowlist and refuses a value without changing it.
func (h *Handler) startRequest(r *http.Request, t *target, b *startBody, key string) (job.StartRequest, error) {
	if b.Unit == nil || b.Unit.Tool == "" || b.Unit.Name == "" {
		return job.StartRequest{}, fmt.Errorf("%w: name the unit with its tool and its name", errBody)
	}

	// The wire form of the tool is its lowercase code, as in the document of
	// a job. The label of the glossary reads as well: OpenTofu.
	unit, err := identity.UnitObject(strings.ToLower(b.Unit.Tool), t.ns.Name, b.Unit.Name)
	if err != nil || strings.ContainsFunc(b.Unit.Name, audit.IsHiddenRune) || !utf8.ValidString(b.Unit.Name) {
		return job.StartRequest{}, fmt.Errorf("%w: %s", job.ErrInvalid, "the tool or the name of the unit is not valid")
	}

	op := job.Operation(b.Operation)
	if !op.Valid() {
		return job.StartRequest{}, fmt.Errorf("%w: the operation %q is not one of plan, apply, destroy, refresh",
			job.ErrInvalid, refusal.Clean(b.Operation, maxOperationEcho))
	}

	// Defense in depth: the planner of the job service checks the whole code
	// location. This check stays so that a repository with credentials is
	// refused at the boundary, before any other layer holds it.
	if l := b.Location; l != nil {
		if err := job.CheckRepository(l.Repository); err != nil {
			return job.StartRequest{}, fmt.Errorf("location: %w", err)
		}
	}

	// The pool and the code location decide which hosts run which code. The
	// body may name them, and the service then checks the extra actions:
	// namespace:manage-runners for a pool and unit:define-steps for a
	// location. Without them the server takes the only job pool of the
	// namespace, and refuses a unit with no code location.
	var loc job.Location
	if l := b.Location; l != nil {
		loc = job.Location{
			Connection: l.Connection, Repository: l.Repository, Ref: l.Ref, Directory: l.Directory,
			Revision: l.Revision,
		}
	}

	return job.StartRequest{
		Unit: unit, Operation: op, Purpose: "", Executor: "", Location: loc, Pool: b.Pool, Edits: nil, Delivery: "",
		Source: job.SourceAPI, IdempotencyKey: key, Reason: b.Reason, BreakGlass: nil, Trigger: nil,
		Channel:  sessionauth.AuditChannel(r, channelHeader),
		SourceIP: clientip.String(r), UserAgent: truncate(userAgent(r), audit.MaxUserAgentLen),
		RequestID: requestid.FromContext(r.Context()),
	}, nil
}

// truncate cuts s to at most n bytes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return strings.ToValidUTF8(s[:n], "")
}
