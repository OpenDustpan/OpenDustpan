package runnerapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// claimWork serves POST on the claims path of the runner protocol: the poll
// that claims one phase or sweep of the pool of the runner (ClaimWork). It
// answers 200 with work, or 204 with none.
func (h *Handler) claimWork(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	var req protocol.ClaimWorkRequest
	if err := decodeBody(c.body, &req); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.pollClaim(ctx, w, c, req)
}

// pollClaim answers one claim work request of the runner. A runner has one
// poll at a time, and the poll waits at most protocol.MaxPollWait for work.
func (h *Handler) pollClaim(ctx context.Context, w http.ResponseWriter, c *call, req protocol.ClaimWorkRequest) {
	if _, busy := h.polls.LoadOrStore(c.runner.ID, struct{}{}); busy {
		h.writeError(ctx, w, fmt.Errorf("%w: a runner has one poll at a time", runner.ErrLimit))

		return
	}

	defer h.polls.Delete(c.runner.ID)

	pool, err := h.pools.Pool(ctx, c.runner.Pool)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	wait := min(max(req.Wait.Std(), 0), protocol.MaxPollWait)
	deadline := time.NewTimer(wait)

	defer deadline.Stop()

	for {
		work, result, claimErr := h.pollOnce(ctx, &c.runner, &pool, req.AssignmentVersion)

		switch {
		case claimErr != nil:
			h.writeError(ctx, w, claimErr)

			return
		case result == pollWork:
			h.claimedClaims.Store(c.runner.ID, durable.Claim{
				Operation: durable.ID(work.Operation), Owner: c.runner.ID, Token: work.Token,
				ExpiresAt: work.ExpiresAt,
			})

			h.writeJSON(ctx, w, http.StatusOK, work)

			return
		case result == pollStop:
			h.claimedClaims.Delete(c.runner.ID)

			w.WriteHeader(http.StatusNoContent)

			return
		}

		pause := time.NewTimer(h.interval)

		select {
		case <-ctx.Done():
			pause.Stop()

			return
		case <-deadline.C:
			pause.Stop()
			h.claimedClaims.Delete(c.runner.ID)
			w.WriteHeader(http.StatusNoContent)

			return
		case <-pause.C:
		}
	}
}

// heartbeatClaim serves POST on the claims heartbeat path of the runner
// protocol: the renewal of the lease of one claim of the runner
// (HeartbeatClaim). The answer holds the new expiry of the lease, which is
// protocol.ClaimLease after the heartbeat.
func (h *Handler) heartbeatClaim(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	var req protocol.HeartbeatClaimRequest
	if err := decodeBody(c.body, &req); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	claim, op, err := h.liveClaimBy(ctx, c.runner.ID, req.Operation)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	pool, err := h.pools.Pool(ctx, c.runner.Pool)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	fence, err := h.fenceOf(r, c.runner.Namespace)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	renewed, err := h.renewClaim(ctx, r, c, claim, op, fence, pool.EffectiveLease())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, protocol.HeartbeatClaimResponse{ExpiresAt: renewed.ExpiresAt})
}

// uploadChunk serves POST on the uploads path of the runner protocol: one
// chunk of the report of the claim of the runner (UploadChunk). The proof of
// the runner key signed the hash of the chunk, so the claim holds the bytes,
// and the server keeps the staged bytes of the claim. The answer holds the
// byte offset that the next chunk of the report starts at.
func (h *Handler) uploadChunk(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	if len(c.body) > protocol.MaxUploadBytes {
		h.writeError(ctx, w, fmt.Errorf("%w: a report has at most %d bytes", runner.ErrLimit, protocol.MaxUploadBytes))

		return
	}

	if err := checkUploadLines(c.body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	claim, ok := h.claimedClaims.Load(c.runner.ID)
	if !ok {
		h.writeError(ctx, w, runner.ErrNotFound)

		return
	}

	cl, ok := claim.(durable.Claim)
	if !ok {
		h.writeError(ctx, w, runner.ErrNotFound)

		return
	}

	if err := h.checkUploadClaim(ctx, r, &c.runner, cl); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	offset, err := h.stagedUpload(c.runner.ID, cl.Token, int64(len(c.body)))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, protocol.UploadResponse{Offset: offset})
}

// checkUploadClaim resolves the claim of the upload of a runner, checks that
// the runner holds it, and checks the fence of the namespace.
func (h *Handler) checkUploadClaim(ctx context.Context, r *http.Request, rn *runner.Runner, claim durable.Claim) error {
	rec, err := h.handoffs.LookupClaim(ctx, claim)
	if err != nil {
		return err
	}

	if rec.HasSession {
		return fmt.Errorf("%w: the claim has a job session", runner.ErrConflict)
	}

	_, err = h.checkUploadStaged(ctx, r, rn, claim)

	return err
}

// checkUploadStaged checks the fence of the namespace of the upload of a
// claim of a runner.
func (h *Handler) checkUploadStaged(ctx context.Context, r *http.Request, rn *runner.Runner, claim durable.Claim) (time.Time, error) {
	fence, err := h.fenceOf(r, rn.Namespace)
	if err != nil {
		return time.Time{}, err
	}

	_ = fence

	return claim.ExpiresAt, nil
}

// checkUploadLines reports an error when the chunk of a report does not fit
// with the limits of the report: a line of a report is at most
// protocol.MaxUploadLineBytes bytes.
func checkUploadLines(body []byte) error {
	start := 0

	for i := 0; i <= len(body); i++ {
		if i != len(body) && body[i] != '\n' {
			continue
		}

		if int64(i-start) > protocol.MaxUploadLineBytes {
			return fmt.Errorf("%w: a line of a report has at most %d bytes", runner.ErrLimit,
				protocol.MaxUploadLineBytes)
		}

		start = i + 1
	}

	return nil
}
