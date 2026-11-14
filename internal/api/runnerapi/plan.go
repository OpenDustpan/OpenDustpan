package runnerapi

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
	"opendustpan.org/opendustpan/pkg/secret"
)

// planRoutes returns the routes that store the plan of a plan phase and read
// the plan of an apply phase. Each one is a request of a launcher, so the proof
// of the key of its job session authenticates it.
func (h *Handler) planRoutes() []Route {
	claim := []identity.Action{identity.ActionRunnerClaim}

	return []Route{
		{
			Method:  http.MethodPut,
			Path:    protocol.PathSessionPlanUpload,
			Scope:   ScopeSessionKey,
			Actions: claim,
			Serve:   h.putPlanPart,
		},
		{
			Method:  http.MethodPost,
			Path:    protocol.PathSessionPlanCommit,
			Scope:   ScopeSessionKey,
			Actions: claim,
			Serve:   h.commitPlan,
		},
		{
			Method:  http.MethodGet,
			Path:    protocol.PathSessionPlanPart,
			Scope:   ScopeSessionKey,
			Actions: claim,
			Serve:   h.getPlanPart,
		},
	}
}

// isUpload reports whether the route path is an upload: its proof signs the
// value of the header Dustpan-Content-SHA256 as the hash of the body, and the
// handler streams the body and compares the hash.
func isUpload(path string) bool {
	return path == protocol.PathSessionLog || path == protocol.PathSessionPlanUpload
}

// planKindOf returns the kind of the plan part that name stands for. Only the
// two plan outputs pass. The error never holds the name: the launcher chose it.
//
// Defense in depth: the recorder checks the kind too; this check stays so that
// a kind never reaches the recorder, a blob key or an error text unchecked.
func planKindOf(name string) (job.OutputKind, error) {
	switch job.OutputKind(name) {
	case job.OutputPlanDisplay:
		return job.OutputPlanDisplay, nil
	case job.OutputPlanApply:
		return job.OutputPlanApply, nil
	default:
		return "", fmt.Errorf(
			"%w: a plan part is %s or %s",
			runner.ErrInvalid,
			job.OutputPlanDisplay,
			job.OutputPlanApply,
		)
	}
}

// planPhase returns the phase of the session of c and its job, and checks that
// the phase is of the kind want. A server without the job reader answers
// not-found, and so does a claim of a sweep, which has no plan.
func (h *Handler) planPhase(r *http.Request, c *call, want job.PhaseKind) (sessionPhase, job.Job, error) {
	// Fail closed: a handler with no job reader checks nothing about the phase.
	//
	// Defense in depth: NewHandler refuses plan dependencies without a job
	// reader. This check stays so that a handler built another way never
	// stores or reads a plan without the reader.
	if h.jobs == nil {
		return sessionPhase{}, job.Job{}, fmt.Errorf("%w: this server stores no plans", runner.ErrNotFound)
	}

	sp, err := h.phaseOf(r, &c.session)
	if err != nil {
		return sessionPhase{}, job.Job{}, err
	}

	if sp.sweep {
		return sessionPhase{}, job.Job{}, fmt.Errorf("%w: the claim of a sweep has no plan", runner.ErrInvalid)
	}

	j, err := h.jobs.Get(r.Context(), sp.claim.Job)
	if err != nil {
		return sessionPhase{}, job.Job{}, fmt.Errorf("get job: %w", err)
	}

	if sp.claim.Phase < 0 || sp.claim.Phase >= len(j.Phases) || j.Phases[sp.claim.Phase].Kind != want {
		return sessionPhase{}, job.Job{}, fmt.Errorf(
			"%w: the phase of this session is not a %s phase",
			runner.ErrInvalid,
			want,
		)
	}

	return sp, j, nil
}

// putPlanPart stores one part of the plan of a plan phase. The proof signed
// the hash of the body, so the recorder compares it with the hash of the bytes
// that it seals, and it deletes the part when they differ. It never holds the
// part in memory: the body streams through the seal to the blob store.
func (h *Handler) putPlanPart(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	kind, err := planKindOf(r.PathValue("part"))
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	if h.plans == nil {
		h.writeError(ctx, w, ScopeSessionKey, fmt.Errorf("%w: this server stores no plans", runner.ErrNotFound))

		return
	}

	sp, _, err := h.planPhase(r, c, job.PhasePlan)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	// One byte above the limit shows an upload that is too long, as the
	// recorder reads it.
	body := io.LimitReader(r.Body, job.MaxPlanPart+1)

	err = h.plans.PutPlanPart(ctx, sp.claim, kind, body, hex.EncodeToString(c.contentSHA256))
	if errors.Is(err, job.ErrHashMismatch) {
		err = fmt.Errorf("%w: the body of plan part %s: %w", runner.ErrBadProof, kind, err)
	}

	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// commitPlan stores the plan of the phase with the parts that the launcher
// names. The recorder reads each staged part back and compares its size and
// hash with the named ones before it stores anything.
func (h *Handler) commitPlan(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	req := protocol.PlanCommit{Parts: nil}
	if err := decodeBody(c.body, &req); err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	parts, err := commitPartsOf(&req)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	if h.plans == nil {
		h.writeError(ctx, w, ScopeSessionKey, fmt.Errorf("%w: this server stores no plans", runner.ErrNotFound))

		return
	}

	sp, _, err := h.planPhase(r, c, job.PhasePlan)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	if _, err = h.plans.CommitPlan(ctx, sp.claim, parts); err != nil {
		if errors.Is(err, job.ErrHashMismatch) {
			// The server holds other content than the launcher says it sent.
			err = fmt.Errorf("%w: %w", runner.ErrConflict, err)
		}

		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// commitPartsOf checks the parts of a commit and returns them for the
// recorder. The recorder checks the same, and the hashes again.
func commitPartsOf(req *protocol.PlanCommit) ([]job.PlanCommitPart, error) {
	if len(req.Parts) == 0 || len(req.Parts) > protocol.MaxPlanParts {
		return nil, fmt.Errorf("%w: a commit names 1 to %d parts", runner.ErrInvalid, protocol.MaxPlanParts)
	}

	out := make([]job.PlanCommitPart, 0, len(req.Parts))

	for i := range req.Parts {
		p := &req.Parts[i]

		kind, err := planKindOf(p.Kind)
		if err != nil {
			return nil, err
		}

		if p.Size < 0 || p.Size > job.MaxPlanPart || !job.ValidHash(p.SHA256) {
			return nil, fmt.Errorf("%w: a plan part has a size from 0 to %d and a SHA-256 in hex", runner.ErrInvalid,
				job.MaxPlanPart)
		}

		out = append(out, job.PlanCommitPart{Kind: kind, Size: p.Size, SHA256: p.SHA256})
	}

	return out, nil
}

// getPlanPart streams the plaintext of one part of the plan that the apply
// phase of the session applies. The plan comes from the job of the session,
// never from the request, so a launcher reads no plan of another job. The
// headers hold the size and the hash that the server stored, so the launcher
// checks the bytes that it got.
func (h *Handler) getPlanPart(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	kind, err := planKindOf(r.PathValue("part"))
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	if h.artifacts == nil {
		h.writeError(ctx, w, ScopeSessionKey, fmt.Errorf("%w: this server reads no plans", runner.ErrNotFound))

		return
	}

	sp, j, err := h.planPhase(r, c, job.PhaseApply)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	plan := planToApply(&j, sp.claim.Phase)
	if plan == nil {
		h.writeError(ctx, w, ScopeSessionKey, fmt.Errorf("%w: job %s has no plan to apply", runner.ErrNotFound, j.ID))

		return
	}

	part := plan.Part(kind)
	if part == nil {
		h.writeError(ctx, w, ScopeSessionKey, fmt.Errorf("%w: the plan has no part %s", runner.ErrNotFound, kind))

		return
	}

	rc, err := h.artifacts.OpenPlanPart(ctx, sp.claim.Fence.Namespace, plan.ID, kind)
	if errors.Is(err, secret.ErrShredded) {
		// A redaction removed the key of the plan, so the plan is gone.
		err = fmt.Errorf("%w: the plan was redacted: %w", runner.ErrNotFound, err)
	}

	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, fmt.Errorf("open plan part: %w", err))

		return
	}

	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			h.logger.WarnContext(ctx, "close plan part failed", slog.String("error", closeErr.Error()))
		}
	}()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(part.Size, 10))
	w.Header().Set(protocol.HeaderContentSHA256, part.SHA256)

	if _, err = io.Copy(w, rc); err != nil {
		// The status went out with the first bytes. The answer is shorter than
		// its Content-Length, so the connection closes and the launcher fails.
		h.logger.WarnContext(ctx, "stream plan part failed", slog.String("session", c.session.ID),
			slog.String("operation", c.session.Operation), slog.String("error", err.Error()))
	}
}

// planToApply returns the plan that the phase at index phase applies: the plan
// of the nearest plan phase before it that stored one. It returns nil when
// there is none.
func planToApply(j *job.Job, phase int) *job.PlanRef {
	for i := min(phase, len(j.Phases)) - 1; i >= 0; i-- {
		if p := &j.Phases[i]; p.Kind == job.PhasePlan && p.Plan != nil {
			return p.Plan
		}
	}

	return nil
}
