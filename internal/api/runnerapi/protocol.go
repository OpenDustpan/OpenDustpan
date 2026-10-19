package runnerapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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
		FenceVersion: 0, Reason: "",
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

	return claimInfo{lost: false, canceled: op.CancelRequested}, nil
}

// poll long-polls for one phase or sweep of the pool of the runner. The
// server takes the kinds from the pool, never from the request.
func (h *Handler) poll(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	var req protocol.PollRequest
	if err := decodeBody(c.body, &req); err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	if c.runner.Draining {
		w.WriteHeader(http.StatusNoContent)

		return
	}

	pool, err := h.pools.Pool(ctx, c.runner.Pool)
	if err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	wait := min(max(req.Wait.Std(), 0), protocol.MaxPollWait)
	deadline := time.NewTimer(wait)

	defer deadline.Stop()

	for {
		work, found, claimErr := h.tryClaim(r, &c.runner, &pool)
		if claimErr != nil {
			h.writeError(ctx, w, ScopeRunnerKey, claimErr)

			return
		}

		if found {
			h.writeJSON(ctx, w, http.StatusOK, work)

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

// tryClaim asks the claimer of the purpose of pool for one phase or sweep.
func (h *Handler) tryClaim(r *http.Request, rn *runner.Runner, pool *runner.Pool) (protocol.Work, bool, error) {
	ctx := r.Context()

	var none protocol.Work

	kinds, err := runner.ClaimKinds(pool)
	if err != nil {
		return none, false, fmt.Errorf("claim kinds: %w", err)
	}

	lease := pool.EffectiveLease()

	switch pool.Purpose {
	case runner.PurposeJobs:
		if h.claimer == nil {
			return none, false, nil
		}

		a, err := h.claimer.Claim(ctx, job.ClaimRequest{Owner: rn.ID, Kinds: kinds, Lease: lease})
		if errors.Is(err, durable.ErrNoWork) {
			return none, false, nil
		}

		if err != nil {
			return none, false, fmt.Errorf("claim phase: %w", err)
		}

		limits := runner.LimitsFor(a.Job.Spec.Steps)
		claim := a.Claim

		return protocol.Work{
			Operation: string(claim.Claim.Operation), Token: claim.Claim.Token, ExpiresAt: claim.Claim.ExpiresAt.UTC(),
			Lease: protocol.FromStd(lease), Job: string(claim.Job), Phase: claim.Phase, Sweep: "", Image: "",
			ServiceAccount: "", Deadline: protocol.FromStd(limits.Deadline), Grace: protocol.FromStd(limits.Grace),
		}, true, nil
	case runner.PurposeSweeps:
		if h.sweeps == nil {
			return none, false, nil
		}

		s, err := h.sweeps.Claim(ctx, durable.ClaimRequest{Owner: rn.ID, Kinds: kinds, MaxVersions: nil, Lease: lease})
		if errors.Is(err, durable.ErrNoWork) {
			return none, false, nil
		}

		if err != nil {
			return none, false, fmt.Errorf("claim sweep: %w", err)
		}

		return protocol.Work{
			Operation: string(s.Claim.Operation), Token: s.Claim.Token, ExpiresAt: s.Claim.ExpiresAt.UTC(),
			Lease: protocol.FromStd(lease), Job: "", Phase: 0, Sweep: s.Work.Sweep, Image: s.Work.Image,
			ServiceAccount: "", Deadline: protocol.FromStd(s.Work.Deadline), Grace: protocol.FromStd(s.Work.Grace),
		}, true, nil
	default:
		return none, false, fmt.Errorf("%w: unknown purpose %q", runner.ErrInvalid, pool.Purpose)
	}
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

	renewed, err := h.handoffs.RunnerRenew(ctx, claim, pool.EffectiveLease(), h.now())

	switch {
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

	if err = h.operations.Release(ctx, claim); err != nil {
		h.writeError(ctx, w, ScopeRunnerKey, err)

		return
	}

	h.logger.InfoContext(ctx, "runner released a claim", slog.String("runner", c.runner.ID),
		slog.String("operation", string(claim.Operation)))
	w.WriteHeader(http.StatusNoContent)
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
