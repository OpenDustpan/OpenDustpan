package runnerapi

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// call is the checked request that a route handler gets. A route of a runner
// key sets the runner and the key of its proof. Every route sets the body,
// which is read once because the proof signs its hash.
type call struct {
	runner runner.Runner
	signer runner.Key
	proof  runner.Proof
	// session is the session of a route of ScopeSessionKey or
	// ScopeProxySessionKey, and handoff the checked request of a route of
	// ScopeHandoffToken or ScopeProxyHandoffToken.
	session runner.Session
	handoff protocol.SessionRequest
	// contentSHA256 is the signed hash of the body of an upload. The handler
	// compares it with the hash of the bytes that it got.
	contentSHA256 []byte
	body          []byte
}

// routeHandler serves a route after the registration wrapper has
// authenticated and authorized it.
type routeHandler func(w http.ResponseWriter, r *http.Request, c *call)

// enforce wraps one route table row: it authenticates, runs the checks of
// its scope, and only then runs the handler.
func (h *Handler) enforce(rt Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		c := new(call)

		// Defense in depth: the handler of a credential path sets no cache
		// header itself, and the error writer does not know the path. Set the
		// header here, before any check, so that the answer of the path and
		// each refusal of it forbid a cache, whichever layer answers.
		if answersCredential(rt.Path) {
			w.Header().Set("Cache-Control", "no-store")
		}

		var err error

		switch rt.Scope {
		case ScopeJoinToken, ScopeJobToken:
			// The handler of a job token route authenticates the bearer
			// job token or the access token itself, because the token,
			// not a runner or session key, carries the call. The wrapper
			// checks the release and reads the body only.
			err = h.checkProtocol(r, c)
		case ScopeRunnerKey:
			err = h.checkRunner(r, rt, c)
		case ScopeHandoffToken, ScopeProxyHandoffToken:
			err = h.checkHandoff(r, c)
		case ScopeSessionKey, ScopeProxySessionKey:
			err = h.checkSession(r, rt, c)
		default:
			err = fmt.Errorf("%w: route %s has no scope", errNotFound, rt.Path)
		}

		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		rt.Serve(w, r, c)
	}
}

// answersCredential reports whether the answer of the path carries a
// credential. A cache between the runner and the server must not keep it. An
// unknown path is not listed, and the protocol answers of the other paths keep
// no Cache-Control header, as runners expect.
func answersCredential(path string) bool {
	return path == protocol.PathProxyUpstreamCredential
}

// maxProtocolBody is the largest JSON body of a protocol request.
const maxProtocolBody = protocol.MaxBody

// checkProtocol checks the release of the sender and reads the body.
func (h *Handler) checkProtocol(r *http.Request, c *call) error {
	if err := h.checkRelease(r); err != nil {
		return err
	}

	var err error

	c.body, err = readBody(r, maxProtocolBody)

	return err
}

// checkRelease checks the release of the sender against the skew that this
// server accepts.
func (h *Handler) checkRelease(r *http.Request) error {
	raw := r.Header.Get(protocol.HeaderRelease)
	if raw == "" {
		return fmt.Errorf("%w: the request has no %s header", runner.ErrInvalid, protocol.HeaderRelease)
	}

	rel, err := runner.ParseRelease(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", protocol.HeaderRelease, err)
	}

	if err = runner.CheckSkew(h.release, rel); err != nil {
		return fmt.Errorf("check release: %w", err)
	}

	return nil
}

// checkRunner checks the release, reads the body, and checks the proof of
// the runner key. On the paths that are not safe to repeat, it keeps the
// nonce of the proof.
func (h *Handler) checkRunner(r *http.Request, rt Route, c *call) error {
	if err := h.checkProtocol(r, c); err != nil {
		return err
	}

	ctx := r.Context()
	now := h.now()

	proof, err := parseProof(r.Header.Get(protocol.HeaderProof))
	if err != nil {
		return err
	}

	rn, err := h.members.Runner(ctx, proof.Subject)
	if errors.Is(err, runner.ErrNotFound) {
		return errProofRefused
	}

	if err != nil {
		return fmt.Errorf("select runner: %w", err)
	}

	// The signature comes first, so a caller without the key learns nothing
	// about the runner: not its retire and not the age of its key.
	sum := sha256.Sum256(c.body)
	signed := runner.Signed{Method: r.Method, PathQuery: r.URL.RequestURI(), BodySHA256: sum[:]}

	signer, err := verifyAny(signingKeys(&rn, rt.Path, now), proof, signed, now)
	if err != nil {
		return err
	}

	switch {
	case !rn.RetiredAt.IsZero():
		return runner.ErrRetired
	case rn.Key.ExpiredAt(now):
		return runner.ErrKeyExpired
	}

	if protocol.NeedsNonce(rt.Path) {
		err = h.handoffs.UseNonce(ctx, proof.Subject, proof.Nonce, runner.NonceExpiry(proof), now)
		if err != nil {
			return fmt.Errorf("keep nonce: %w", err)
		}
	}

	c.runner, c.signer, c.proof = rn, signer, proof

	return nil
}

// verifyAny returns the first key of keys that verifies the proof, or the
// error of the last check.
func verifyAny(keys []runner.Key, proof runner.Proof, signed runner.Signed, now time.Time) (runner.Key, error) {
	for _, k := range keys {
		if runner.CheckProof(k, proof, signed, now) == nil {
			return k, nil
		}
	}

	return runner.Key{Public: nil, CreatedAt: time.Time{}}, errProofRefused
}

// errProofRefused is the one answer for a proof of an unknown runner and for
// a proof that no key of a known runner verifies, so the answer does not tell
// whether the runner exists.
var errProofRefused = fmt.Errorf("%w: the proof does not authenticate", runner.ErrBadProof)

// signingKeys returns the keys that may have signed a request of rn on the
// path at now, whatever the age of the key and whether the runner is
// retired: the current key, and on a rotation the previous key for
// runner.RotateGrace after the rotation.
func signingKeys(rn *runner.Runner, path string, now time.Time) []runner.Key {
	keys := []runner.Key{rn.Key}

	if path == protocol.PathRotate && len(rn.PreviousKey.Public) != 0 && !rn.RotatedAt.IsZero() &&
		now.Sub(rn.RotatedAt) <= runner.RotateGrace {
		keys = append(keys, rn.PreviousKey)
	}

	return keys
}

// readBody reads the body of r up to limit bytes. A longer body is an
// invalid request.
func readBody(r *http.Request, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: the body is not readable", runner.ErrInvalid)
	}

	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: the body has more than %d bytes", runner.ErrInvalid, limit)
	}

	return body, nil
}

// parseProof parses the Dustpan-Proof header: "v1 sub=<runner or session
// ID> t=<Unix ms> n=<nonce hex> sig=<signature base64url>".
func parseProof(header string) (runner.Proof, error) {
	bad := func(why string) (runner.Proof, error) {
		return runner.Proof{}, fmt.Errorf("%w: the %s header %s", runner.ErrBadProof, protocol.HeaderProof, why)
	}

	fields := strings.Split(header, " ")
	if len(fields) != proofFields || fields[0] != "v1" {
		return bad("is not in the form v1 sub= t= n= sig=")
	}

	values := make(map[string]string, len(fields)-1)

	for _, f := range fields[1:] {
		k, v, ok := strings.Cut(f, "=")
		if !ok || v == "" {
			return bad("has an empty field")
		}

		values[k] = v
	}

	ms, err := strconv.ParseInt(values["t"], 10, 64)
	if err != nil {
		return bad("has a time that is not a number")
	}

	sig, err := base64.RawURLEncoding.DecodeString(values["sig"])
	if err != nil {
		return bad("has a signature that is not base64url")
	}

	return runner.Proof{
		Subject: values["sub"], At: time.UnixMilli(ms).UTC(), Nonce: values["n"], Signature: sig,
	}, nil
}

// proofFields is the number of space-separated fields of the proof header.
const proofFields = 5
