package runnerapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// changeEvent is the key of the change that names the event of an audit entry.
const changeEvent = "event"

// join registers a runner key with a join token. The join token and the
// signature of the new key authenticate it.
func (h *Handler) join(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	parsed, err := h.parseJoin(r, c.body)
	if err != nil {
		h.writeError(ctx, w, ScopeJoinToken, err)

		return
	}

	req, joining, hash := parsed.request, parsed.joining, parsed.hash

	tok, pool, err := h.members.LookupJoin(ctx, hash)
	if err != nil {
		h.writeError(ctx, w, ScopeJoinToken, err)

		return
	}

	if req.Pool != pool.ID {
		h.writeError(ctx, w, ScopeJoinToken, fmt.Errorf("%w: the join token is for another pool", runner.ErrInvalid))

		return
	}

	challenge := runner.JoinChallenge(h.serverURL, pool.ID, req.Name, req.Token)
	if !ed25519.Verify(req.PublicKey, challenge, req.Proof) {
		h.writeError(
			ctx,
			w,
			ScopeJoinToken,
			fmt.Errorf("%w: the signature of the join challenge does not match the key",
				runner.ErrBadProof),
		)

		return
	}

	ns, err := h.namespaces.WritableHere(ctx, pool.Namespace)
	if err != nil {
		h.writeError(ctx, w, ScopeJoinToken, err)

		return
	}

	now := h.now()
	joined, err := h.members.Join(ctx, runner.Joining{TokenSHA256: hash, Request: joining, At: now}, ns.Fence(),
		func(j *runner.Joined) []audit.Entry { return []audit.Entry{h.joinEntry(r, &tok, &pool, j, now)} })
	if err != nil {
		h.writeError(ctx, w, ScopeJoinToken, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, protocol.JoinResponse{
		Runner: joined.Runner.ID, Pool: pool.ID, Namespace: pool.Namespace.String(),
		KeyExpires:  joined.Runner.Key.CreatedAt.Add(runner.MaxKeyAge),
		Lease:       protocol.FromStd(pool.EffectiveLease()),
		Concurrency: pool.EffectiveConcurrency(),
	})
}

// parsedJoin is a checked join: the request, the request as the domain
// checks it, and the hash of the join token.
type parsedJoin struct {
	request protocol.JoinRequest
	joining runner.JoinRequest
	hash    string
}

// parseJoin decodes and checks the body of a join.
func (h *Handler) parseJoin(r *http.Request, body []byte) (parsedJoin, error) {
	var none parsedJoin

	req := protocol.JoinRequest{
		Token: "", Pool: "", Name: "", Kind: "", Isolation: "", Labels: nil, PublicKey: nil, Proof: nil,
	}

	if err := decodeBody(body, &req); err != nil {
		return none, err
	}

	rel, err := runner.ParseRelease(r.Header.Get(protocol.HeaderRelease))
	if err != nil {
		return none, fmt.Errorf("parse release: %w", err)
	}

	joining := runner.JoinRequest{
		Token: req.Token, Name: req.Name, Kind: runner.Kind(req.Kind), Isolation: runner.Isolation(req.Isolation),
		Labels: req.Labels, Release: rel, Key: runner.Key{Public: req.PublicKey, CreatedAt: time.Time{}},
		Proof: req.Proof,
	}
	if err = joining.Validate(); err != nil {
		return none, fmt.Errorf("check join request: %w", err)
	}

	return parsedJoin{request: req, joining: joining, hash: sha256Hex(req.Token)}, nil
}

// joinEntry returns the audit entry of a join. It holds IDs, names, the
// release and the ID of the key, never a token or a key.
func (h *Handler) joinEntry(
	r *http.Request, tok *runner.JoinToken, pool *runner.Pool, j *runner.Joined, now time.Time,
) audit.Entry {
	event := runner.JoinEvent(j)
	rule := runner.AuditRules()[event]

	e := h.entry(r, pool.Namespace, tok.ID, rule, now)
	e.ObjectID = pool.ID
	e.Changes = []audit.Change{
		{Key: changeEvent, Old: nil, New: string(event), Layer: "", TakesEffect: ""},
		{Key: "runner", Old: nil, New: j.Runner.ID, Layer: "", TakesEffect: ""},
		{Key: "name", Old: nil, New: j.Runner.Name, Layer: "", TakesEffect: ""},
		{Key: "release", Old: nil, New: j.Runner.Release.String(), Layer: "", TakesEffect: ""},
		{Key: "isolation", Old: nil, New: string(j.Runner.Isolation), Layer: "", TakesEffect: ""},
		{Key: "keyID", Old: nil, New: j.Runner.Key.ID(), Layer: "", TakesEffect: ""},
	}

	if j.Rejoined {
		e.Changes[len(e.Changes)-1].Old = &j.PreviousKeyID
		e.Changes = append(e.Changes,
			audit.Change{
				Key: "endedClaims", Old: nil, New: strings.Join(j.EndedClaims, ","), Layer: "", TakesEffect: "",
			})
	}

	return e
}

// entry returns the audit entry of a change in ns by subject.
func (h *Handler) entry(
	r *http.Request, ns namespace.Name, subject string, rule runner.AuditRule, at time.Time,
) audit.Entry {
	return audit.Entry{
		At: at, Namespace: ns, Subject: subject, IDPSubject: "", TokenID: "", Action: string(rule.Action),
		Object: identity.NamespaceObject(ns).String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: clientip.String(r), UserAgent: truncateUserAgent(r.UserAgent()),
		Channel: sessionauth.AuditChannel(r, channelHeader), Kind: rule.Kind, Outcome: audit.OutcomeSuccess,
		RequestID: requestid.FromContext(r.Context()), ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

// fenceOf returns the fence of the namespace of the runner.
func (h *Handler) fenceOf(r *http.Request, ns namespace.Name) (namespace.Fence, error) {
	n, err := h.namespaces.WritableHere(r.Context(), ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("namespace of the runner: %w", err)
	}

	return n.Fence(), nil
}

// rotate replaces the key of the runner. The proof header holds the
// signature of the current key (or of the previous key, for a retry), and
// the body holds the new key and its signature of the rotation challenge.
func (h *Handler) rotate(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	var req protocol.RotateRequest
	if err := decodeBody(c.body, &req); err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	now := h.now()
	next := runner.Key{Public: req.PublicKey, CreatedAt: now}

	if err := next.Validate(); err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	if !ed25519.Verify(next.Public, runner.RotateChallenge(c.runner.ID, next), req.NextProof) {
		h.writeError(
			ctx,
			w,
			ScopeRunnerKey,
			fmt.Errorf("%w: the signature of the rotation challenge does not match the new key",
				runner.ErrBadProof),
		)

		return
	}

	fence, err := h.fenceOf(r, c.runner.Namespace)
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	rule := runner.AuditRules()[runner.EventKeyRotated]
	e := h.entry(r, c.runner.Namespace, c.runner.ID, rule, now)
	e.ObjectID = c.runner.Pool
	e.Changes = []audit.Change{
		{Key: changeEvent, Old: nil, New: string(runner.EventKeyRotated), Layer: "", TakesEffect: ""},
		{Key: "keyID", Old: new(c.runner.Key.ID()), New: next.ID(), Layer: "", TakesEffect: ""},
	}

	_, err = h.members.RotateKey(ctx, runner.Rotation{Runner: c.runner.ID, Signer: c.signer.ID(), Next: next}, fence,
		[]audit.Entry{e})
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// heartbeat records that the runner lives and tells it what changed: the
// claims that a cancel stops, the claims that are no longer live, the due
// rotation of its key and its drain.
func (h *Handler) heartbeat(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	var req protocol.Heartbeat
	if err := decodeBody(c.body, &req); err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	if len(req.Claims) > runner.MaxConcurrency {
		h.writeError(ctx, w, ScopeRunnerKey,
			fmt.Errorf("%w: a runner holds at most %d claims", runner.ErrInvalid, runner.MaxConcurrency))

		return
	}

	rel, err := runner.ParseRelease(r.Header.Get(protocol.HeaderRelease))
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	now := h.now()

	rn, err := h.members.Heartbeat(ctx, c.runner.ID, rel, now)
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	resp := protocol.HeartbeatResponse{
		Cancel: nil, Lost: nil, RotateKey: rn.Key.RotationDue(now), Draining: rn.Draining,
	}

	for _, op := range req.Claims {
		info, err := h.claimState(r, rn.ID, op)
		if err != nil {
			h.writeError(ctx, w, ScopeRunnerKey, err)

			return
		}

		if info.lost {
			resp.Lost = append(resp.Lost, op)
		}

		if info.canceled {
			resp.Cancel = append(resp.Cancel, op)
		}
	}

	h.writeJSON(ctx, w, http.StatusOK, resp)
}

// claimInfo is what the store tells about a claim of a runner: whether the
// claim is lost, and whether a cancel stops its work.
type claimInfo struct {
	lost     bool
	canceled bool
}

// claimState returns the state of the claim of the runner on the operation.
// Without a store of operations, every claim is live.
func (h *Handler) claimState(r *http.Request, runnerID, operation string) (claimInfo, error) {
	if h.operations == nil {
		return claimInfo{lost: false, canceled: false}, nil
	}

	op, err := h.operations.Get(r.Context(), durable.ID(operation))
	if errors.Is(err, durable.ErrNotFound) {
		return claimInfo{lost: true, canceled: false}, nil
	}

	if err != nil {
		return claimInfo{lost: false, canceled: false}, fmt.Errorf("get operation: %w", err)
	}

	if op.State != durable.StateRunning || op.Claim.Owner != runnerID {
		return claimInfo{lost: true, canceled: false}, nil
	}

	_, err = h.handoffs.LookupClaim(r.Context(), op.Claim)

	switch {
	case errors.Is(err, runner.ErrClaimLost):
		return claimInfo{lost: true, canceled: false}, nil
	case err != nil && !errors.Is(err, runner.ErrNotFound):
		return claimInfo{lost: false, canceled: false}, fmt.Errorf("look up claim: %w", err)
	}

	return claimInfo{lost: false, canceled: op.CancelRequested}, nil
}

// poll long-polls for one phase or sweep of the pool of the runner. The
// server takes the kinds from the pool, never from the request. A runner has
// one poll at a time.
func (h *Handler) poll(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	var req protocol.PollRequest
	if err := decodeBody(c.body, &req); err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	if _, busy := h.polls.LoadOrStore(c.runner.ID, struct{}{}); busy {
		h.writeError(ctx, w, ScopeRunnerKey, fmt.Errorf("%w: a runner has one poll at a time", runner.ErrLimit))

		return
	}

	defer h.polls.Delete(c.runner.ID)

	pool, err := h.pools.Pool(ctx, c.runner.Pool)
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	wait := min(max(req.Wait.Std(), 0), protocol.MaxPollWait)
	deadline := time.NewTimer(wait)

	defer deadline.Stop()

	for {
		work, result, claimErr := h.pollOnce(r, &c.runner, &pool, req.AssignmentVersion)

		switch {
		case claimErr != nil:
			h.writeError(ctx, w, ScopeRunnerKey, claimErr)

			return
		case result == pollWork:
			h.writeJSON(ctx, w, http.StatusOK, work)

			return
		case result == pollStop:
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
			w.WriteHeader(http.StatusNoContent)

			return
		case <-pause.C:
		}
	}
}

// pollResult is what one try of a poll found.
type pollResult int

const (
	// pollIdle means no work now: the poll goes on until its deadline.
	pollIdle pollResult = iota
	// pollWork means the poll claimed work.
	pollWork
	// pollStop means the runner is draining or retired: the poll ends.
	pollStop
)

// pollOnce tries one claim for the runner. A runner that is draining or
// retired now claims nothing: the server reads the runner again, because a
// drain or a retire may start during a long poll.
func (h *Handler) pollOnce(
	r *http.Request, caller *runner.Runner, pool *runner.Pool, assignmentVersion int,
) (protocol.Work, pollResult, error) {
	var none protocol.Work

	rn, err := h.members.Runner(r.Context(), caller.ID)
	if err != nil {
		return none, pollIdle, fmt.Errorf("select runner: %w", err)
	}

	if rn.Draining || !rn.RetiredAt.IsZero() {
		return none, pollStop, nil
	}

	work, found, err := h.tryClaim(r, &rn, pool, assignmentVersion)
	if !found {
		return work, pollIdle, err
	}

	// A drain or a retire that committed during the claim gets the claim
	// back. The claim then comes before the drain, or the runner gets no work.
	after, err := h.members.Runner(r.Context(), caller.ID)
	if err == nil && !after.Draining && after.RetiredAt.IsZero() {
		return work, pollWork, nil
	}

	claim := durable.Claim{
		Operation: durable.ID(work.Operation), Owner: rn.ID, Token: work.Token, ExpiresAt: work.ExpiresAt,
	}
	if giveErr := h.giveBack(r, &rn, claim); giveErr != nil && !errors.Is(giveErr, runner.ErrClaimLost) {
		h.logger.WarnContext(r.Context(), "release of a claim after a drain", slog.String("runner", rn.ID),
			slog.String("operation", work.Operation), slog.Any("error", giveErr))
	}

	if err != nil {
		return none, pollIdle, fmt.Errorf("select runner: %w", err)
	}

	return none, pollStop, nil
}

// tryClaim asks the claimer of the purpose of pool for one phase or sweep.
func (h *Handler) tryClaim(
	r *http.Request, rn *runner.Runner, pool *runner.Pool, assignmentVersion int,
) (protocol.Work, bool, error) {
	kinds, err := runner.ClaimKinds(pool)
	if err != nil {
		return protocol.Work{}, false, fmt.Errorf("claim kinds: %w", err)
	}

	// The claim never takes an operation above the assignment version that
	// the launcher reads.
	maxVersions := make(map[durable.Kind]int, len(kinds))
	for _, k := range kinds {
		maxVersions[k] = assignmentVersion
	}

	switch pool.Purpose {
	case runner.PurposeJobs:
		return h.claimPhase(r, rn, pool, kinds, maxVersions)
	case runner.PurposeSweeps:
		return h.claimSweep(r, rn, pool, kinds, maxVersions)
	default:
		return protocol.Work{}, false, fmt.Errorf("%w: unknown purpose %q", runner.ErrInvalid, pool.Purpose)
	}
}

// claimPhase takes one phase of the kinds for the runner and records the
// claim.
func (h *Handler) claimPhase(
	r *http.Request, rn *runner.Runner, pool *runner.Pool, kinds []durable.Kind, maxVersions map[durable.Kind]int,
) (protocol.Work, bool, error) {
	ctx := r.Context()

	var none protocol.Work

	if h.claimer == nil {
		return none, false, nil
	}

	lease := pool.EffectiveLease()

	a, err := h.claimer.Claim(ctx, job.ClaimRequest{
		Owner: rn.ID, Kinds: kinds, MaxVersions: maxVersions, Lease: lease,
	})
	if errors.Is(err, durable.ErrNoWork) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("claim phase: %w", err)
	}

	limits := runner.LimitsFor(a.Job.Spec.Steps)
	claim := a.Claim

	if err = h.handoffs.RecordClaim(ctx, claim.Claim, string(claim.Job), claim.Phase); err != nil {
		// Nothing else knows the claim, so release it now. Its lease would
		// block the operation until the lease ends.
		phase := job.PhaseClaim{Job: claim.Job, Phase: claim.Phase, Claim: claim.Claim, Fence: claim.Fence}
		if relErr := h.claimer.Release(context.WithoutCancel(ctx), phase); relErr != nil {
			h.logger.WarnContext(ctx, "release of a claim that failed to record", slog.String("runner", rn.ID),
				slog.String("operation", string(claim.Claim.Operation)), slog.Any("error", relErr))
		}

		return none, false, fmt.Errorf("record claim: %w", err)
	}

	return protocol.Work{
		Operation: string(claim.Claim.Operation), Token: claim.Claim.Token, ExpiresAt: claim.Claim.ExpiresAt.UTC(),
		Lease: protocol.FromStd(lease), Job: string(claim.Job), Phase: claim.Phase, Sweep: "", Image: "",
		ServiceAccount: "", Deadline: protocol.FromStd(limits.Deadline), Grace: protocol.FromStd(limits.Grace),
	}, true, nil
}

// claimSweep takes one sweep of the kinds for the runner and records the
// claim.
func (h *Handler) claimSweep(
	r *http.Request, rn *runner.Runner, pool *runner.Pool, kinds []durable.Kind, maxVersions map[durable.Kind]int,
) (protocol.Work, bool, error) {
	ctx := r.Context()

	var none protocol.Work

	if h.sweeps == nil {
		return none, false, nil
	}

	lease := pool.EffectiveLease()

	s, err := h.sweeps.Claim(ctx, durable.ClaimRequest{
		Owner: rn.ID, Kinds: kinds, MaxVersions: maxVersions, Lease: lease,
	})
	if errors.Is(err, durable.ErrNoWork) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("claim sweep: %w", err)
	}

	if err = h.handoffs.RecordClaim(ctx, s.Claim, "", 0); err != nil {
		if relErr := h.sweeps.Release(context.WithoutCancel(ctx), s.Claim, s.Fence); relErr != nil {
			h.logger.WarnContext(ctx, "release of a claim that failed to record", slog.String("runner", rn.ID),
				slog.String("operation", string(s.Claim.Operation)), slog.Any("error", relErr))
		}

		return none, false, fmt.Errorf("record claim: %w", err)
	}

	return protocol.Work{
		Operation: string(s.Claim.Operation), Token: s.Claim.Token, ExpiresAt: s.Claim.ExpiresAt.UTC(),
		Lease: protocol.FromStd(lease), Job: "", Phase: 0, Sweep: s.Work.Sweep, Image: s.Work.Image,
		ServiceAccount: "", Deadline: protocol.FromStd(s.Work.Deadline), Grace: protocol.FromStd(s.Work.Grace),
	}, true, nil
}

// liveClaim returns the live claim of the runner on the operation of the
// path, or runner.ErrClaimLost.
func (h *Handler) liveClaim(r *http.Request, rn *runner.Runner) (durable.Claim, durable.Operation, error) {
	if h.operations == nil {
		return durable.Claim{}, durable.Operation{}, fmt.Errorf(
			"%w: this server has no durable operations",
			runner.ErrNotFound,
		)
	}

	op, err := h.operations.Get(r.Context(), durable.ID(r.PathValue("operation")))
	if errors.Is(err, durable.ErrNotFound) {
		return durable.Claim{}, durable.Operation{}, runner.ErrNotFound
	}

	if err != nil {
		return durable.Claim{}, durable.Operation{}, fmt.Errorf("get operation: %w", err)
	}

	if op.State != durable.StateRunning || op.Claim.Owner != rn.ID {
		return durable.Claim{}, durable.Operation{}, runner.ErrClaimLost
	}

	return op.Claim, op, nil
}

// handoff issues the handoff token of a claim of the runner.
func (h *Handler) handoff(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	claim, _, err := h.liveClaim(r, &c.runner)
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	secret, err := newSecret(runner.HandoffTokenPrefix)
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	now := h.now()
	sum := sha256.Sum256([]byte(secret))
	tok := runner.HandoffToken{
		SHA256: hex.EncodeToString(sum[:]), Operation: string(claim.Operation), Token: claim.Token,
		Runner: c.runner.ID, IssuedAt: now, ExpiresAt: now.Add(runner.HandoffTokenTTL), UsedAt: time.Time{},
	}

	if err = h.handoffs.IssueHandoffToken(ctx, tok, claim); err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, protocol.HandoffResponse{HandoffToken: secret, ExpiresAt: tok.ExpiresAt})
}

// renew renews a claim of the runner that has no job session yet. After the
// session opens, the answer says so and renews nothing.
func (h *Handler) renew(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	claim, op, err := h.liveClaim(r, &c.runner)
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	pool, err := h.pools.Pool(ctx, c.runner.Pool)
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	fence, err := h.fenceOf(r, c.runner.Namespace)
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	renewed, err := h.handoffs.RunnerRenew(ctx, claim, fence, pool.EffectiveLease(), h.now())

	switch {
	case errors.Is(err, runner.ErrSessionWaitPassed):
		// The store changed nothing. The claimer releases the claim with
		// its own rules, then the runner gets the claim-lost answer.
		if giveErr := h.giveBack(r, &c.runner, claim); giveErr != nil {
			h.writeError(ctx, w, ScopeRunnerKey, giveErr)

			return
		}

		h.writeError(ctx, w, ScopeRunnerKey, err)
	case errors.Is(err, runner.ErrSessionOpen):
		h.writeJSON(ctx, w, http.StatusOK, protocol.RenewResponse{
			ExpiresAt: claim.ExpiresAt.UTC(), Canceled: op.CancelRequested, SessionOpen: true,
		})
	case err != nil:
		h.writeError(ctx, w, ScopeRunnerKey, err)
	default:
		h.writeJSON(ctx, w, http.StatusOK, protocol.RenewResponse{
			ExpiresAt: renewed.ExpiresAt.UTC(), Canceled: op.CancelRequested, SessionOpen: false,
		})
	}
}

// releaseClaim gives back a claim of the runner whose launcher never opened a
// session. The operation then waits for the next claim.
func (h *Handler) releaseClaim(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	var req protocol.ReleaseRequest
	if len(c.body) > 0 {
		if err := decodeBody(c.body, &req); err != nil {
			h.writeError(ctx, w, ScopeRunnerKey, err)

			return
		}
	}

	claim, _, err := h.liveClaim(r, &c.runner)
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	if err = h.giveBack(r, &c.runner, claim); err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	h.logger.InfoContext(ctx, "runner released a claim", slog.String("runner", c.runner.ID),
		slog.String("operation", string(claim.Operation)))
	w.WriteHeader(http.StatusNoContent)
}

// giveBack releases the claim of the runner through the claimer of the
// purpose of its pool, so the rules of the claimer decide what the release
// does to the operation: a phase after a started change fails, and a cancel
// ends the job. A claim with a job session is not released.
func (h *Handler) giveBack(r *http.Request, rn *runner.Runner, claim durable.Claim) error {
	ctx := r.Context()

	rec, err := h.handoffs.LookupClaim(ctx, claim)
	if err != nil {
		return fmt.Errorf("look up claim: %w", err)
	}

	if rec.HasSession {
		return fmt.Errorf("%w: the claim has a job session", runner.ErrConflict)
	}

	pool, err := h.pools.Pool(ctx, rn.Pool)
	if err != nil {
		return fmt.Errorf("select pool: %w", err)
	}

	fence, fenceErr := h.fenceOf(r, rn.Namespace)
	if fenceErr != nil {
		return fenceErr
	}

	switch pool.Purpose {
	case runner.PurposeJobs:
		if h.claimer == nil {
			return runner.ErrNotFound
		}

		phase := job.PhaseClaim{Job: job.ID(rec.Job), Phase: rec.Phase, Claim: claim, Fence: fence}
		if err = h.claimer.Release(ctx, phase); err != nil {
			return fmt.Errorf("release phase: %w", err)
		}
	case runner.PurposeSweeps:
		if h.sweeps == nil {
			return runner.ErrNotFound
		}

		if err = h.sweeps.Release(ctx, claim, fence); err != nil {
			return fmt.Errorf("release sweep: %w", err)
		}
	default:
		return fmt.Errorf("%w: unknown purpose %q", runner.ErrInvalid, pool.Purpose)
	}

	return nil
}

// newSecret returns a secret that starts with prefix and holds 256 random
// bits as hex.
func newSecret(prefix string) (string, error) {
	var b [32]byte

	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return prefix + hex.EncodeToString(b[:]), nil
}

// truncateUserAgent caps s at audit.MaxUserAgentLen bytes, at a rune
// boundary.
func truncateUserAgent(s string) string {
	if len(s) <= audit.MaxUserAgentLen {
		return s
	}

	cut := audit.MaxUserAgentLen
	for cut > 0 && !runeStart(s[cut]) {
		cut--
	}

	return s[:cut]
}

func runeStart(b byte) bool {
	const continuationMask, continuationBits = 0xC0, 0x80

	return b&continuationMask != continuationBits
}

// sha256Hex returns the hex SHA-256 of s.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}
