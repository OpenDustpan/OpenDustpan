package runnerapi

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// maxFinishMessage bounds the reason that a launcher gives for a failed
// phase. The job record keeps it, and the console shows it.
const maxFinishMessage = 2048

// maxSessionID bounds the subject of a proof of a job session, so that a
// forged header cannot make a long lookup.
const maxSessionID = 128

// checkHandoff authenticates the session exchange. The proof header is
// signed by the new session key, with the hex SHA-256 of the handoff token as
// its subject, and the body holds the signature of the session challenge by
// the same key. The server keeps the nonce of the proof, so a replayed
// exchange fails.
//
// Defense in depth: OpenSession checks the token, its expiry and its claim in
// its own transaction. These checks stay so that a request without the new
// key never reaches the store, and so that a request that has the token but
// not the key never uses it.
func (h *Handler) checkHandoff(r *http.Request, c *call) error {
	if err := h.checkProtocol(r, c); err != nil {
		return err
	}

	req := protocol.SessionRequest{HandoffToken: "", PublicKey: nil, Proof: nil, Release: ""}
	if err := decodeBody(c.body, &req); err != nil {
		return err
	}

	now := h.now()

	key, err := h.checkSessionRequest(&req, now)
	if err != nil {
		return err
	}

	if !ed25519.Verify(key.Public, runner.SessionChallenge(h.serverURL, req.HandoffToken, key), req.Proof) {
		return fmt.Errorf("%w: the signature of the session challenge does not match the key", runner.ErrBadProof)
	}

	proof, err := parseProof(r.Header.Get(protocol.HeaderProof))
	if err != nil {
		return err
	}

	subject := sha256Hex(req.HandoffToken)
	sum := sha256.Sum256(c.body)
	signed := runner.Signed{Method: r.Method, PathQuery: r.URL.RequestURI(), BodySHA256: sum[:]}

	if proof.Subject != subject || runner.CheckProof(key, proof, signed, now) != nil {
		return errProofRefused
	}

	if err = h.handoffs.UseNonce(r.Context(), subject, proof.Nonce, runner.NonceExpiry(proof), now); err != nil {
		return fmt.Errorf("keep nonce: %w", err)
	}

	c.handoff = req

	return nil
}

// checkSessionRequest checks the fields of a session exchange and returns the
// new session key: the handoff token has the form of one, the key is an
// Ed25519 key, and the release of the launcher is inside the skew.
func (h *Handler) checkSessionRequest(req *protocol.SessionRequest, now time.Time) (runner.Key, error) {
	var none runner.Key

	if !strings.HasPrefix(req.HandoffToken, runner.HandoffTokenPrefix) || len(req.HandoffToken) > maxSessionID {
		return none, fmt.Errorf("%w: the handoff token is not a handoff token", runner.ErrInvalid)
	}

	key := runner.Key{Public: req.PublicKey, CreatedAt: now}
	if err := key.Validate(); err != nil {
		return none, err
	}

	rel, err := runner.ParseRelease(req.Release)
	if err != nil {
		return none, fmt.Errorf("parse launcher release: %w", err)
	}

	if err = runner.CheckSkew(h.release, rel); err != nil {
		return none, fmt.Errorf("check launcher release: %w", err)
	}

	return key, nil
}

// checkSession checks the release, reads the body, and checks the proof of
// the key of a job session. A log chunk is the one upload: its proof signs the
// value of the header Dustpan-Content-SHA256 as the hash of the body, and the
// handler compares it with the hash of the bytes that it got.
//
// The store answers an unknown session with the same refusal as a proof that
// does not verify. A session whose claim is lost or whose runner is retired
// has no key to check against, so it gets its own answer: a session ID is a
// random value that no one else holds, and the launcher must learn that it
// stops.
func (h *Handler) checkSession(r *http.Request, rt Route, c *call) error {
	sum, err := h.signedHash(r, rt, c)
	if err != nil {
		return err
	}

	proof, err := parseProof(r.Header.Get(protocol.HeaderProof))
	if err != nil {
		return err
	}

	if proof.Subject == "" || len(proof.Subject) > maxSessionID {
		return errProofRefused
	}

	sess, err := h.handoffs.Session(r.Context(), proof.Subject)
	if errors.Is(err, runner.ErrNotFound) {
		return errProofRefused
	}

	if err != nil {
		return fmt.Errorf("select job session: %w", err)
	}

	signed := runner.Signed{Method: r.Method, PathQuery: r.URL.RequestURI(), BodySHA256: sum}
	if err = runner.CheckProof(sess.Key, proof, signed, h.now()); err != nil {
		return errProofRefused
	}

	c.session, c.proof, c.contentSHA256 = sess, proof, sum

	return nil
}

// signedHash checks the release and returns the hash that the proof of a
// session request signs: the hash of the JSON body, which it reads, or for an
// upload the value of the header, which leaves the body to the handler.
func (h *Handler) signedHash(r *http.Request, rt Route, c *call) ([]byte, error) {
	if isUpload(rt.Path) {
		if err := h.checkRelease(r); err != nil {
			return nil, err
		}

		return contentHash(r)
	}

	if err := h.checkProtocol(r, c); err != nil {
		return nil, err
	}

	digest := sha256.Sum256(c.body)

	return digest[:], nil
}

// contentHash returns the SHA-256 of the body of an upload, from its header.
func contentHash(r *http.Request) ([]byte, error) {
	sum, err := hex.DecodeString(r.Header.Get(protocol.HeaderContentSHA256))
	if err != nil || len(sum) != sha256.Size {
		return nil, fmt.Errorf("%w: the %s header is not a SHA-256 in hex", runner.ErrInvalid,
			protocol.HeaderContentSHA256)
	}

	return sum, nil
}

// sessionPhase is the phase that a job session works on, with what a write
// needs: the claim with the fence of its namespace, the operation and the
// pool of the runner.
type sessionPhase struct {
	claim job.PhaseClaim
	op    durable.Operation
	pool  runner.Pool
	// sweep is true for the claim of a sweep, which has no job.
	sweep bool
}

// phaseOf returns the phase of the session of c. It returns
// runner.ErrClaimLost when the operation is not running with the owner and
// the token of the session. The store of the write checks the claim and the
// fence again in its own transaction.
func (h *Handler) phaseOf(r *http.Request, sess *runner.Session) (sessionPhase, error) {
	var none sessionPhase

	if h.operations == nil {
		return none, fmt.Errorf("%w: this server has no durable operations", runner.ErrNotFound)
	}

	ctx := r.Context()

	op, err := h.operations.Get(ctx, durable.ID(sess.Operation))
	if errors.Is(err, durable.ErrNotFound) {
		return none, runner.ErrClaimLost
	}

	if err != nil {
		return none, fmt.Errorf("get operation: %w", err)
	}

	if op.State != durable.StateRunning || op.Claim.Owner != sess.Runner || op.Claim.Token != sess.Token {
		return none, runner.ErrClaimLost
	}

	rn, err := h.members.Runner(ctx, sess.Runner)
	if err != nil {
		return none, fmt.Errorf("select runner: %w", err)
	}

	pool, err := h.pools.Pool(ctx, rn.Pool)
	if err != nil {
		return none, fmt.Errorf("select pool: %w", err)
	}

	rec, err := h.handoffs.LookupClaim(ctx, op.Claim)
	if err != nil {
		return none, fmt.Errorf("look up claim: %w", err)
	}

	fence, err := h.fenceOf(r, rn.Namespace)
	if err != nil {
		return none, err
	}

	return sessionPhase{
		claim: job.PhaseClaim{Job: job.ID(rec.Job), Phase: rec.Phase, Claim: op.Claim, Fence: fence},
		op:    op, pool: pool, sweep: rec.Job == "",
	}, nil
}

// jobPhaseOf is phaseOf for a route that writes the record of a job phase. A
// session of a sweep writes no step, log or finish.
func (h *Handler) jobPhaseOf(r *http.Request, c *call) (sessionPhase, error) {
	// Fail closed: a handler with no recorder or no job reader records nothing
	// and checks nothing, so it answers before it reads the claim.
	//
	// Defense in depth: NewHandler refuses a recorder without a job reader.
	// This check stays so that a handler built another way never records a
	// step without the reader.
	if h.recorder == nil || h.jobs == nil {
		return sessionPhase{}, fmt.Errorf("%w: this server records no steps", runner.ErrNotFound)
	}

	sp, err := h.phaseOf(r, &c.session)
	if err != nil {
		return sessionPhase{}, err
	}

	if sp.sweep {
		return sessionPhase{}, fmt.Errorf("%w: the claim of a sweep has no steps, logs or finish", runner.ErrInvalid)
	}

	return sp, nil
}

// openSession exchanges the handoff token for a job session and answers the
// assignment. The checks of the token, the claim and the session wait limit
// are those of OpenSession, in its transaction.
func (h *Handler) openSession(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	// Nothing is used until the server can answer the assignment.
	if h.operations == nil || h.jobs == nil {
		h.writeError(ctx, w, ScopeHandoffToken,
			fmt.Errorf("%w: this server runs no job phases", runner.ErrNotFound))

		return
	}

	now := h.now()
	key := runner.Key{Public: c.handoff.PublicKey, CreatedAt: now}

	sess, err := h.handoffs.OpenSession(ctx, sha256Hex(c.handoff.HandoffToken), key, now)
	if err != nil {
		// The request names no runner before the session opens, so the entry
		// holds no claimed runner ID and the installation scope.
		h.recordUnverifiedHandoffRefusal(ctx, r, err)
		h.writeError(ctx, w, ScopeHandoffToken, err)

		return
	}

	resp, err := h.sessionResponse(r, &sess)
	if err != nil {
		h.writeError(ctx, w, ScopeHandoffToken, err)

		return
	}

	h.logger.InfoContext(ctx, "launcher opened a job session", slog.String("session", sess.ID),
		slog.String("operation", sess.Operation), slog.String("runner", sess.Runner))
	h.writeJSON(ctx, w, http.StatusOK, resp)
}

// sessionResponse returns the answer to the exchange: the session, its lease
// and the assignment of the phase.
func (h *Handler) sessionResponse(r *http.Request, sess *runner.Session) (protocol.SessionResponse, error) {
	var none protocol.SessionResponse

	ctx := r.Context()

	sp, err := h.phaseOf(r, sess)
	if err != nil {
		return none, err
	}

	if sp.sweep {
		return none, fmt.Errorf("%w: this server serves no session for a sweep", runner.ErrInvalid)
	}

	j, err := h.jobs.Get(ctx, sp.claim.Job)
	if err != nil {
		return none, fmt.Errorf("get job: %w", err)
	}

	if sp.claim.Phase < 0 || sp.claim.Phase >= len(j.Phases) {
		return none, fmt.Errorf("%w: job %s has no phase %d", runner.ErrInvalid, j.ID, sp.claim.Phase)
	}

	kind := j.Phases[sp.claim.Phase].Kind
	unit := j.Spec.Unit

	return protocol.SessionResponse{
		Session: sess.ID, ExpiresAt: sp.claim.Claim.ExpiresAt.UTC(), Lease: protocol.FromStd(sp.pool.EffectiveLease()),
		AssignmentVersion: runner.AssignmentVersion,
		Assignment: protocol.Assignment{
			Namespace: unit.Namespace().String(), Unit: unit.String(), Tool: unit.Tool(), Job: string(j.ID),
			Phase: string(kind), Sweep: "", ServerURL: h.serverURL, Source: nil,
			Steps: stepsOf(j.Spec.Steps, kind, j.Phases, sp.claim.Phase),
			Plan:  nil, GitCredentials: gitModeOf(sp.pool.GitCredentials), GitHelper: sp.pool.GitCredentials.Helper,
			Profile: j.Spec.Profile,
		},
	}, nil
}

// gitModeOf returns the git credential mode of g with its default made
// visible: an unset mode is none.
func gitModeOf(g runner.GitCredentials) string {
	if g.Mode == "" {
		return string(runner.GitCredentialsNone)
	}

	return string(g.Mode)
}

// stepsOf returns the steps of the phase kind, in the wire form of the
// assignment. The assignment holds no secret: a step has an argument list and
// variables that are not secret (job.Step.Env).
func stepsOf(steps []job.Step, kind job.PhaseKind, phases []job.Phase, current int) []protocol.Step {
	out := make([]protocol.Step, 0, len(steps))

	for i := range steps {
		s := &steps[i]
		if s.Phase != kind {
			continue
		}

		outputs := make([]protocol.Output, 0, len(s.Outputs))
		for _, o := range s.Outputs {
			outputs = append(outputs, protocol.Output{
				Kind: string(o.Kind), Path: o.Path, Needed: outputNeeded(o.Kind, phases, current),
			})
		}

		out = append(out, protocol.Step{
			Name: s.Name, Role: string(s.Role), Args: s.Args, Env: s.Env, Dir: s.Dir,
			Timeout: protocol.FromStd(s.Timeout), AllowFailure: s.AllowFailure, ChangesResources: s.ChangesResources,
			Outputs: outputs,
		})
	}

	return out
}

// outputNeeded reports whether a later phase of the job reads an output of
// kind k: a plan-apply output is read by a later apply phase, and no other
// output has a reader.
func outputNeeded(k job.OutputKind, phases []job.Phase, current int) bool {
	if k != job.OutputPlanApply {
		return false
	}

	for i := current + 1; i < len(phases); i++ {
		if phases[i].Kind == job.PhaseApply {
			return true
		}
	}

	return false
}

// sessionRenew renews the claim of the session by the lease of the pool, also
// during a cancel (runner.SessionStore.SessionRenew): the launcher stops its
// step with the grace period, and its last log chunks and its finish need the
// claim. A renewal during a cancel answers canceled.
func (h *Handler) sessionRenew(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	sp, err := h.phaseOf(r, &c.session)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	renewed, err := h.handoffs.SessionRenew(ctx, c.session.ID, sp.claim.Fence, sp.pool.EffectiveLease())
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, protocol.RenewResponse{
		ExpiresAt: renewed.ExpiresAt.UTC(), Canceled: sp.op.CancelRequested, SessionOpen: false,
	})
}

// stepOf returns the step name of the path. The error never holds the name:
// the launcher chose it. Only a name that passed this check reaches the
// recorder, the store and an error text, so no later error holds an unchecked
// name.
//
// Defense in depth: the recorder checks the name too; this check stays so
// that a name never reaches the store or a log line unchecked.
func stepOf(r *http.Request) (string, error) {
	name := r.PathValue("step")
	if !job.ValidStepName(name) {
		return "", fmt.Errorf("%w: the step name is 1 to %d lowercase letters, digits and '-'", runner.ErrInvalid,
			job.MaxStepName)
	}

	return name, nil
}

// stepBegin records the begin of the step in the path. For a step that
// changes resources, the launcher starts the process only after the answer.
func (h *Handler) stepBegin(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	sp, err := h.jobPhaseOf(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	name, err := stepOf(r)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	if err = h.recorder.BeginStep(ctx, sp.claim, name); err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// stepEnd records the end of the step in the path, and checks that the log
// that the server holds is the log that the launcher says it sent.
func (h *Handler) stepEnd(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	req := protocol.StepEnd{
		ExitCode: 0, StartedAt: time.Time{}, FinishedAt: time.Time{}, LogChunks: 0, LogSHA256: "",
	}
	if err := decodeBody(c.body, &req); err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	if err := checkStepEnd(&req); err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	sp, err := h.jobPhaseOf(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	name, err := stepOf(r)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	rec := job.StepRecord{
		Name:       name,
		Token:      sp.claim.Claim.Token,
		ExitCode:   req.ExitCode,
		StartedAt:  time.Time{},
		FinishedAt: time.Time{},
		Log:        nil,
	}
	// The recorder compares the log with the count and the hash before it
	// writes, so a launcher that sends a wrong end changes nothing.
	want := job.LogExpectation{Chunks: req.LogChunks, SHA256: req.LogSHA256}
	if err = h.recorder.EndStepChecked(ctx, sp.claim, rec, want); err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// checkStepEnd checks the fields of the end of a step that the server does not
// take from its own records.
func checkStepEnd(req *protocol.StepEnd) error {
	switch {
	case req.ExitCode < -1 || req.ExitCode > job.MaxExitCode:
		return fmt.Errorf("%w: the exit code is -1 to %d", runner.ErrInvalid, job.MaxExitCode)
	case req.LogChunks < 0 || req.LogChunks > protocol.MaxLogChunksPerStep:
		return fmt.Errorf("%w: a step has 0 to %d log chunks", runner.ErrInvalid, protocol.MaxLogChunksPerStep)
	case req.LogChunks > 0 && !job.ValidHash(req.LogSHA256):
		return fmt.Errorf("%w: the hash of the log is not a SHA-256 in hex", runner.ErrInvalid)
	default:
		return nil
	}
}

// putLog stores one chunk of the log of a step. The proof signed the hash of
// the body, so the handler compares it with the bytes that it got before the
// recorder stores any of them.
func (h *Handler) putLog(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq < 0 || seq >= protocol.MaxLogChunksPerStep {
		h.writeError(ctx, w, ScopeSessionKey,
			fmt.Errorf("%w: the number of a log chunk is 0 to %d", runner.ErrInvalid, protocol.MaxLogChunksPerStep-1))

		return
	}

	chunk, err := io.ReadAll(io.LimitReader(r.Body, job.MaxLogChunk+1))
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, fmt.Errorf("%w: the body is not readable", runner.ErrInvalid))

		return
	}

	if len(chunk) > job.MaxLogChunk {
		h.writeError(ctx, w, ScopeSessionKey,
			fmt.Errorf("%w: a log chunk has at most %d bytes", runner.ErrInvalid, job.MaxLogChunk))

		return
	}

	got := sha256.Sum256(chunk)
	if err = runner.CheckContentHash(c.contentSHA256, got[:]); err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	sp, err := h.jobPhaseOf(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	name, err := stepOf(r)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	if err = h.recorder.AppendLog(ctx, sp.claim, name, seq, bytes.NewReader(chunk)); err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// finish ends the phase with the outcome of the launcher. A second finish
// finds the claim ended and answers claim-lost.
func (h *Handler) finish(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	req := protocol.Finish{Succeeded: false, Message: ""}
	if err := decodeBody(c.body, &req); err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	if err := checkFinishMessage(req.Message); err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	sp, err := h.phaseOf(r, &c.session)
	if err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	if sp.sweep {
		h.writeError(ctx, w, ScopeSessionKey, fmt.Errorf("%w: a sweep has no finish", runner.ErrInvalid))

		return
	}

	if h.claimer == nil {
		h.writeError(ctx, w, ScopeSessionKey, fmt.Errorf("%w: this server cannot finish a phase", runner.ErrNotFound))

		return
	}

	if err = h.claimer.Finish(ctx, sp.claim, job.Outcome{Succeeded: req.Succeeded, Message: req.Message}); err != nil {
		h.writeError(ctx, w, ScopeSessionKey, err)

		return
	}

	// The message is the launcher's text and may quote the output of a tool,
	// so the log holds the outcome and never the message.
	h.logger.InfoContext(ctx, "launcher finished a phase", slog.String("session", c.session.ID),
		slog.String("operation", c.session.Operation), slog.Bool("succeeded", req.Succeeded))
	w.WriteHeader(http.StatusNoContent)
}

// checkFinishMessage checks the reason of a failed phase: valid text of at
// most maxFinishMessage bytes with no control character but a line feed or a
// tab. The server never writes the reason to its log or to a header; the line
// feed stays because a reason can have more than one line.
func checkFinishMessage(m string) error {
	if len(m) > maxFinishMessage || !utf8.ValidString(m) {
		return fmt.Errorf("%w: the reason is at most %d bytes of text", runner.ErrInvalid, maxFinishMessage)
	}

	if strings.ContainsFunc(m, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return fmt.Errorf("%w: the reason has a control character", runner.ErrInvalid)
	}

	return nil
}
