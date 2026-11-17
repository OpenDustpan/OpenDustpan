package runnerapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// eventCredentialsRefused names the event of the entry of a credentials
// request that the server refused after the session checks.
const eventCredentialsRefused = "credentials-refused"

// Reason codes of the denial entries of the credentials path.
const (
	reasonClaimLost   = "claim-lost"
	reasonRetired     = "retired"
	reasonNoUnit      = "no-unit"
	reasonStarterGone = "starter-gone"
	reasonSweep       = "sweep"
	reasonNotLive     = "not-live"
	reasonExpired     = "expired"
	reasonRefused     = "refused"
)

// credentialsFailure is a refusal of the credentials path. Reason is the code
// of its denial entry, or empty when the failure is not a refusal (a store
// that does not answer) and writes no entry. Job is the job of the claim when
// it is known.
type credentialsFailure struct {
	err    error
	job    string
	reason string
}

// issuedToken is the answer of a successful issue.
type issuedToken struct {
	secret  jobtoken.Secret
	expires time.Time
}

// issueInput is what the issue of the token of one step reads.
type issueInput struct {
	sess    *runner.Session
	phase   *sessionPhase
	job     *job.Job
	starter *principal.Principal
	step    *job.Step
}

// credentials hands the job token of one step to the launcher of a job
// session. The session checks (key proof, live claim) ran before. The path
// checks the claim again, loads the job, and asks the token service, whose
// store checks the world in its own transaction. Each refusal after the step
// name check writes one denial entry that holds the job, the step and a
// reason code, and never a token.
func (h *Handler) credentials(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	req := protocol.CredentialsRequest{Step: ""}
	if err := decodeBody(c.body, &req); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: the session layer checks the proof, and the issuer
	// checks the job and its steps. This check stays so that no later error,
	// entry or log line holds a step name that was not checked.
	if !job.ValidStepName(req.Step) {
		h.writeError(ctx, w, fmt.Errorf("%w: the step name is 1 to %d lowercase letters, digits and '-'",
			runner.ErrInvalid, job.MaxStepName))

		return
	}

	// Fail closed: a server with no issuer, no job reader or no operations
	// has no credentials path.
	if h.tokens == nil || h.jobs == nil || h.starters == nil || h.operations == nil {
		h.writeError(ctx, w, fmt.Errorf("%w: this server issues no job tokens", runner.ErrNotFound))

		return
	}

	got, failure := h.issueToken(r, &c.session, req.Step)
	if failure != nil {
		if failure.reason != "" {
			h.recordCredentialsRefusal(ctx, r, &c.session, req.Step, failure)
		}

		h.writeError(ctx, w, failure.err)

		return
	}

	h.logger.InfoContext(ctx, "launcher got a job token", slog.String("session", c.session.ID),
		slog.String("step", req.Step))
	h.writeJSON(ctx, w, http.StatusOK, protocol.CredentialsResponse{
		Token: got.secret.Reveal(), ExpiresAt: got.expires.UTC(), Files: nil,
	})
}

// issueToken checks the phase of the session and issues the token of the step.
func (h *Handler) issueToken(
	r *http.Request, sess *runner.Session, step string,
) (issuedToken, *credentialsFailure) {
	var none issuedToken

	ctx := r.Context()

	sp, err := h.phaseOf(r, sess)
	if errors.Is(err, runner.ErrClaimLost) {
		return none, &credentialsFailure{err: err, job: h.claimJob(ctx, sess), reason: reasonClaimLost}
	}

	if err != nil {
		return none, &credentialsFailure{err: err, job: "", reason: ""}
	}

	if sp.sweep {
		err = fmt.Errorf("%w: the claim of a sweep gets no job token", runner.ErrInvalid)

		return none, &credentialsFailure{err: err, job: "", reason: reasonSweep}
	}

	jobID := string(sp.claim.Job)

	j, err := h.jobs.Get(ctx, sp.claim.Job)
	if err != nil {
		return none, &credentialsFailure{err: fmt.Errorf("get job: %w", err), job: jobID, reason: ""}
	}

	if sp.claim.Phase < 0 || sp.claim.Phase >= len(j.Phases) {
		err = fmt.Errorf("%w: job %s has no phase %d", runner.ErrInvalid, j.ID, sp.claim.Phase)

		return none, &credentialsFailure{err: err, job: jobID, reason: ""}
	}

	st, ok := stepOfPhase(&j, sp.claim.Phase, step)
	if !ok {
		err = fmt.Errorf("%w: the phase has no step of this name", runner.ErrInvalid)

		return none, &credentialsFailure{err: err, job: jobID, reason: ""}
	}

	// Defense in depth: the store checks the live unit row too. This check
	// stays so that a job with no stable unit ID never reaches the issuer.
	if j.Spec.Unit.UnitID() == "" {
		err = fmt.Errorf("%w: the job has no stable unit ID", runner.ErrInvalid)

		return none, &credentialsFailure{err: err, job: jobID, reason: reasonNoUnit}
	}

	starter, err := h.starters.GetPrincipalBySubject(ctx, j.StartedBy)
	if errors.Is(err, principal.ErrNotFound) {
		err = fmt.Errorf("%w: the starter of the job has no record", runner.ErrNotFound)

		return none, &credentialsFailure{err: err, job: jobID, reason: reasonStarterGone}
	}

	if err != nil {
		return none, &credentialsFailure{err: fmt.Errorf("get starter: %w", err), job: jobID, reason: ""}
	}

	return h.issue(r, &issueInput{sess: sess, phase: &sp, job: &j, starter: &starter, step: st})
}

// issue builds the request of the token and calls the service.
func (h *Handler) issue(r *http.Request, in *issueInput) (issuedToken, *credentialsFailure) {
	var none issuedToken

	now := h.now()
	jobID := string(in.job.ID)

	timeout := in.step.Timeout
	if timeout == 0 {
		timeout = job.DefaultStepTimeout
	}

	expires := jobtoken.Expiry(in.phase.claim.Claim.ExpiresAt, now, timeout, runner.GraceFor(in.step))
	if !expires.After(now) {
		err := fmt.Errorf("%w: the claim ended", runner.ErrClaimLost)

		return none, &credentialsFailure{err: err, job: jobID, reason: reasonExpired}
	}

	rule := runner.AuditRules()[runner.EventCredentialsIssued]
	e := h.entry(r, in.phase.claim.Fence.Namespace, in.sess.ID, rule, now)
	e.Object = in.job.Spec.Unit.String()
	e.JobID = jobID
	e.Changes = []audit.Change{
		{Key: changeEvent, Old: nil, New: string(runner.EventCredentialsIssued), Layer: "", TakesEffect: ""},
		{Key: "step", Old: nil, New: in.step.Name, Layer: "", TakesEffect: ""},
		{Key: "expiresAt", Old: nil, New: expires.UTC().Format(time.RFC3339), Layer: "", TakesEffect: ""},
	}

	secret, err := h.tokens.Issue(r.Context(), &jobtoken.IssueRequest{
		StarterID: in.starter.ID, StarterSubject: in.starter.Subject, Job: *in.job, Claim: in.phase.claim.Claim,
		ExpiresAt: expires, Fence: in.phase.claim.Fence, Entry: e,
	})
	if err == nil {
		return issuedToken{secret: secret, expires: expires}, nil
	}

	switch {
	case errors.Is(err, jobtoken.ErrNotLive), errors.Is(err, jobtoken.ErrNotFound):
		// The store found the claim, the job or the starter not live.
		err = fmt.Errorf("%w: the job token is not live: %w", runner.ErrClaimLost, err)

		return none, &credentialsFailure{err: err, job: jobID, reason: reasonNotLive}
	case errors.Is(err, jobtoken.ErrInvalid):
		err = fmt.Errorf("%w: %w", runner.ErrInvalid, err)

		return none, &credentialsFailure{err: err, job: jobID, reason: reasonRefused}
	default:
		// A store failure is not a refusal: no entry, and the answer is 5xx.
		return none, &credentialsFailure{err: err, job: jobID, reason: ""}
	}
}

// stepOfPhase returns the step of the phase with the name name.
func stepOfPhase(j *job.Job, phase int, name string) (*job.Step, bool) {
	kind := j.Phases[phase].Kind

	for i := range j.Spec.Steps {
		s := &j.Spec.Steps[i]
		if s.Name == name && s.Phase == kind {
			return s, true
		}
	}

	return nil, false
}

// claimJob returns the job of the claim of the session for the denial entry
// of a claim that ended, or "" when the records do not say. It is a best
// effort read: the refusal stands in each case.
func (h *Handler) claimJob(ctx context.Context, sess *runner.Session) string {
	op, err := h.operations.Get(ctx, durable.ID(sess.Operation))
	if err != nil {
		return ""
	}

	rec, err := h.handoffs.LookupClaim(ctx, op.Claim)
	if err != nil {
		return ""
	}

	return rec.Job
}

// sessionLogHash is the number of hex digits of the SHA-256 of a session ID
// that a log record holds in place of the ID.
const sessionLogHash = 16

// logCredentialsSessionRefusal writes one log record for a credentials request
// that the session layer refused before the proof check, because the claim of
// the session is lost or its runner is retired. No proof authenticated the
// request, so it writes no audit entry. The record holds the reason and a
// short hash of the session ID, never the ID, the proof or a token. Other
// errors write no record here.
func (h *Handler) logCredentialsSessionRefusal(ctx context.Context, sessionID string, err error) {
	var reason string

	switch {
	case errors.Is(err, runner.ErrClaimLost):
		reason = reasonClaimLost
	case errors.Is(err, runner.ErrRetired):
		reason = reasonRetired
	default:
		return
	}

	h.logger.WarnContext(ctx, "credentials request refused by the session layer",
		slog.String("session", sha256Hex(sessionID)[:sessionLogHash]), slog.String("reason", reason))
}

// recordCredentialsRefusal writes the denial entry of a refused credentials
// request, in the namespace of the runner of the session. It returns nothing:
// the refusal stays when the append fails (fail closed), and the failure is
// logged and counted. The entry holds the session, the job, the step and the
// reason code, never a token or the text of an error.
func (h *Handler) recordCredentialsRefusal(
	ctx context.Context, r *http.Request, sess *runner.Session, step string, failure *credentialsFailure,
) {
	if h.audit == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), handoffAuditTimeout)
	defer cancel()

	rn, err := h.members.Runner(ctx, sess.Runner)
	if err != nil {
		h.handoffAppendFailed(ctx, fmt.Errorf("select runner: %w", err))

		return
	}

	fence, err := h.fenceOf(r, rn.Namespace)
	if err != nil {
		h.handoffAppendFailed(ctx, err)

		return
	}

	rule := runner.AuditRule{Action: runner.AuditRules()[runner.EventCredentialsIssued].Action, Kind: audit.KindChange}
	e := h.entry(r, rn.Namespace, sess.ID, rule, h.now())
	e.Outcome = audit.OutcomeDenied
	e.Reason = failure.reason
	e.JobID = failure.job
	e.Changes = []audit.Change{
		{Key: changeEvent, Old: nil, New: eventCredentialsRefused, Layer: "", TakesEffect: ""},
		{Key: "step", Old: nil, New: step, Layer: "", TakesEffect: ""},
		{Key: "reason", Old: nil, New: failure.reason, Layer: "", TakesEffect: ""},
	}

	if _, err = h.audit.Append(ctx, fence, e); err != nil {
		h.handoffAppendFailed(ctx, fmt.Errorf("append entry: %w", err))
	}
}
