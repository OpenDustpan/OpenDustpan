// Package session is the client side of the runner protocol for the
// launcher: it opens a job session with a handoff token and a key that it
// makes in memory, and signs each call of the session with that key. It
// never imports a transport: the caller gives a Transport, so the package
// links no HTTP client, SQL driver or forge.
package session

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// Errors of a call. Each error of a call wraps one of them, and an *Error
// when the server answered.
var (
	// ErrRefused reports an answer of 400 or above that no other sentinel
	// names.
	ErrRefused = errors.New("the server refused the call")
	// ErrClaimLost reports the code claim-lost: the claim ended. The
	// launcher stops its work and reports nothing more.
	ErrClaimLost = errors.New("the claim of the session is lost")
	// ErrVersionUnsupported reports the code version-unsupported.
	ErrVersionUnsupported = errors.New("the server does not accept the release of the launcher")
	// ErrVersionAhead reports the code version-ahead: the server is older
	// than the launcher. The launcher retries with backoff.
	ErrVersionAhead = errors.New("the release of the launcher is newer than the server")
	// ErrUnavailable reports a call that got no answer, or an answer of 429
	// or 503. A retry can succeed.
	ErrUnavailable = errors.New("the server is not available")
	// ErrInvalid reports an input that the client refuses before it sends
	// a call.
	ErrInvalid = errors.New("the session call is not valid")
)

// Request is one request of the runner protocol, before the transport
// sends it.
type Request struct {
	Method string
	// Path is the path of the request with its query, from the root of
	// the server.
	Path   string
	Body   []byte
	Header map[string]string
}

// Response is the answer to a Request.
type Response struct {
	Status int
	Body   []byte
	// RetryAfter is the value of the header Retry-After, or 0.
	RetryAfter time.Duration
}

// Transport sends one request to the server and returns its answer. It
// returns an error only when it got no answer. The launcher binary gives an
// HTTPS transport.
type Transport interface {
	Do(ctx context.Context, req *Request) (*Response, error)
}

// Error is the error body of an answer, with its status.
type Error struct {
	Status int
	Body   protocol.Error
	// RetryAfter is the value of the header Retry-After of the answer, or 0.
	RetryAfter time.Duration
}

// Error returns the code and the message of the answer.
func (e *Error) Error() string {
	return fmt.Sprintf("the server answered %d %s: %s", e.Status, e.Body.Code, e.Body.Message)
}

// Is matches the sentinel of the code of e.
func (e *Error) Is(target error) bool {
	switch e.Body.Code {
	case protocol.ErrorClaimLost:
		return target == ErrClaimLost
	case protocol.ErrorVersionUnsupported:
		return target == ErrVersionUnsupported
	case protocol.ErrorVersionAhead:
		return target == ErrVersionAhead
	case protocol.ErrorRateLimited, protocol.ErrorUnavailable:
		return target == ErrUnavailable
	default:
		return target == ErrRefused
	}
}

// Client is the base of the signed calls: the server, the transport and
// the release of the launcher.
type Client struct {
	// Server is the URL of the server, as the server knows its own URL.
	// The challenge of the exchange signs it.
	Server    string
	Transport Transport
	// Release is the release that the launcher sends in every request.
	Release string
	// Now returns the time of a proof. Nil takes time.Now.
	Now func() time.Time
}

// nonceBytes is the length of the nonce of a proof, in bytes.
const nonceBytes = 16

// Sign returns the value of the proof header of a request: subject signs
// the method, the path and bodySHA256 at the time now with key.
func Sign(key ed25519.PrivateKey, subject string, signed runner.Signed, now time.Time) (string, error) {
	nonce := make([]byte, nonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("make the nonce of a proof: %w", err)
	}

	proof := runner.Proof{Subject: subject, At: now, Nonce: hex.EncodeToString(nonce), Signature: nil}
	proof.Signature = ed25519.Sign(key, runner.ProofInput(proof, signed))

	return fmt.Sprintf("v1 sub=%s t=%d n=%s sig=%s", subject, proof.At.UnixMilli(), proof.Nonce,
		base64.RawURLEncoding.EncodeToString(proof.Signature)), nil
}

// Signed sends a request with the proof of subject and key. A nil
// contentSHA256 signs the hash of the body. A non-nil one is the hash of a
// streamed upload: the request carries it in protocol.HeaderContentSHA256,
// and the proof signs it. It returns the answer of any status; the caller
// reads it.
func (c *Client) Signed(
	ctx context.Context, key ed25519.PrivateKey, subject string, req *Request, contentSHA256 []byte,
) (*Response, error) {
	sum := contentSHA256
	if sum == nil {
		h := sha256.Sum256(req.Body)
		sum = h[:]
	}

	proof, err := Sign(key, subject, runner.Signed{Method: req.Method, PathQuery: req.Path, BodySHA256: sum},
		c.now())
	if err != nil {
		return nil, err
	}

	header := maps.Clone(req.Header)
	if header == nil {
		header = map[string]string{}
	}

	header[protocol.HeaderProof] = proof

	if contentSHA256 != nil {
		header[protocol.HeaderContentSHA256] = hex.EncodeToString(contentSHA256)
	}

	return c.Send(ctx, &Request{Method: req.Method, Path: req.Path, Body: req.Body, Header: header})
}

// Send sends req with the release header and no proof. It returns an error
// that wraps ErrUnavailable when the transport got no answer.
func (c *Client) Send(ctx context.Context, req *Request) (*Response, error) {
	header := maps.Clone(req.Header)
	if header == nil {
		header = map[string]string{}
	}

	header[protocol.HeaderRelease] = c.Release

	resp, err := c.Transport.Do(ctx, &Request{Method: req.Method, Path: req.Path, Body: req.Body, Header: header})
	if err != nil {
		return nil, fmt.Errorf("%w: %s %s: %w", ErrUnavailable, req.Method, req.Path, err)
	}

	return resp, nil
}

// now returns the time of a proof.
func (c *Client) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}

	return c.Now()
}

// Session is an open job session: its ID, its key and its assignment. The
// key stays in memory.
type Session struct {
	client *Client
	key    ed25519.PrivateKey
	// ID is the ID of the session.
	ID string
	// ExpiresAt and Lease are the end of the claim and its lease.
	ExpiresAt time.Time
	Lease     time.Duration
	// AssignmentVersion is the version of Assignment.
	AssignmentVersion int
	Assignment        protocol.Assignment
}

// Open makes a session key in memory and exchanges the handoff token for a
// job session. It returns an error that wraps ErrVersionUnsupported or
// ErrVersionAhead for a release that the server refuses, ErrUnavailable
// when the server did not answer, and ErrRefused for any other refusal.
// The error never holds the token.
func Open(ctx context.Context, c *Client, handoff string) (*Session, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("make the session key: %w", err)
	}

	challenge := runner.SessionChallenge(c.Server, handoff, runner.Key{Public: pub, CreatedAt: time.Time{}})

	body, err := json.Marshal(protocol.SessionRequest{
		HandoffToken: handoff, PublicKey: pub, Release: c.Release, Proof: ed25519.Sign(priv, challenge),
	})
	if err != nil {
		return nil, fmt.Errorf("encode the session exchange: %w", err)
	}

	// The subject of the proof of the exchange is the hash of the token,
	// so the server can keep its nonce before a session exists.
	tok := sha256.Sum256([]byte(handoff))

	resp, err := c.Signed(ctx, priv, hex.EncodeToString(tok[:]),
		&Request{Method: methodPost, Path: protocol.PathSession, Body: body, Header: jsonHeader()}, nil)
	if err != nil {
		return nil, err
	}

	if err = check(resp, statusOK); err != nil {
		return nil, fmt.Errorf("open the job session: %w", err)
	}

	var sr protocol.SessionResponse
	if err = json.Unmarshal(resp.Body, &sr); err != nil || sr.Session == "" {
		return nil, fmt.Errorf("%w: the answer to the session exchange is not a session", ErrRefused)
	}

	return &Session{
		client: c, key: priv, ID: sr.Session, ExpiresAt: sr.ExpiresAt, Lease: sr.Lease.Std(),
		AssignmentVersion: sr.AssignmentVersion, Assignment: sr.Assignment,
	}, nil
}

// HTTP methods and statuses that the protocol uses. The package names them
// so that it links no HTTP package.
const (
	methodGet       = "GET"
	methodPost      = "POST"
	methodPut       = "PUT"
	statusOK        = 200
	statusNoContent = 204
	statusError     = 400
	// statusTooManyRequests and statusServerError (and above) are the
	// statuses of a proxy or a load balancer that a retry can pass.
	statusTooManyRequests = 429
	statusServerError     = 500
)

// jsonHeader returns the content type of a JSON body.
func jsonHeader() map[string]string {
	return map[string]string{"Content-Type": "application/json"}
}

// check returns nil when resp has the status want, else an error that
// wraps the sentinel of its code.
func check(resp *Response, want int) error {
	if resp.Status == want {
		return nil
	}

	var body protocol.Error
	if resp.Status >= statusError && json.Unmarshal(resp.Body, &body) == nil && body.Code != "" {
		return &Error{Status: resp.Status, Body: body, RetryAfter: resp.RetryAfter}
	}

	// A proxy or a load balancer in front of the server answers 429 and
	// 5xx with no protocol body. A retry can succeed; the caller bounds the
	// retries.
	code := ""
	if resp.Status == statusTooManyRequests || resp.Status >= statusServerError {
		code = protocol.ErrorUnavailable
	}

	return &Error{
		Status: resp.Status, Body: protocol.Error{
			Code: code, Message: "the answer has the status " + strconv.Itoa(resp.Status), Supported: "",
		},
		RetryAfter: resp.RetryAfter,
	}
}

// Post sends a signed POST of the session with a JSON body (nil sends
// none) and returns the answer of any status.
func (s *Session) Post(ctx context.Context, path string, body any) (*Response, error) {
	var raw []byte

	if body != nil {
		var err error

		if raw, err = json.Marshal(body); err != nil {
			return nil, fmt.Errorf("encode the body of %s: %w", path, err)
		}
	}

	return s.client.Signed(ctx, s.key, s.ID, &Request{Method: methodPost, Path: path, Body: raw, Header: jsonHeader()},
		nil)
}

// Get sends a signed GET of the session, for example the read of a plan part,
// and returns the answer of any status. The proof signs the hash of the empty
// body.
func (s *Session) Get(ctx context.Context, path string) (*Response, error) {
	return s.client.Signed(ctx, s.key, s.ID, &Request{Method: methodGet, Path: path, Body: nil, Header: nil}, nil)
}

// PutLogChunk uploads one chunk of a log and returns the answer of any
// status. The header and the proof carry the hash of the chunk.
func (s *Session) PutLogChunk(ctx context.Context, path string, chunk []byte) (*Response, error) {
	sum := sha256.Sum256(chunk)

	return s.client.Signed(ctx, s.key, s.ID, &Request{
		Method: methodPut,
		Path:   path,
		Body:   chunk,
		Header: map[string]string{"Content-Type": "application/octet-stream"},
	}, sum[:])
}

// Renew renews the claim of the session.
func (s *Session) Renew(ctx context.Context) (protocol.RenewResponse, error) {
	var none protocol.RenewResponse

	resp, err := s.Post(ctx, protocol.PathSessionRenew, nil)
	if err != nil {
		return none, err
	}

	if err = check(resp, statusOK); err != nil {
		return none, fmt.Errorf("renew the claim: %w", err)
	}

	var rr protocol.RenewResponse
	if err = json.Unmarshal(resp.Body, &rr); err != nil {
		return none, fmt.Errorf("%w: the answer to the renewal is not valid", ErrRefused)
	}

	return rr, nil
}

// checkStep refuses a step name that is not valid, so that a name never
// changes the path of a call.
//
// Defense in depth: the server checks the name of the path too; this check
// stays so that a hostile name in an assignment never reaches a path.
func checkStep(step string) error {
	if !job.ValidStepName(step) {
		return fmt.Errorf("%w: the step name is not valid", ErrInvalid)
	}

	return nil
}

// BeginStep records the begin of the step.
func (s *Session) BeginStep(ctx context.Context, step string) error {
	if err := checkStep(step); err != nil {
		return err
	}

	return s.noContent(ctx, "begin the step", StepPath(protocol.PathSessionStepBegin, step, 0), nil)
}

// EndStep records the end of the step.
func (s *Session) EndStep(ctx context.Context, step string, end protocol.StepEnd) error {
	if err := checkStep(step); err != nil {
		return err
	}

	return s.noContent(ctx, "end the step", StepPath(protocol.PathSessionStepEnd, step, 0), end)
}

// PutLog stores the chunk seq of the log of the step.
func (s *Session) PutLog(ctx context.Context, step string, seq int, chunk []byte) error {
	if err := checkStep(step); err != nil {
		return err
	}

	resp, err := s.PutLogChunk(ctx, StepPath(protocol.PathSessionLog, step, seq), chunk)
	if err != nil {
		return err
	}

	if err = check(resp, statusNoContent); err != nil {
		return fmt.Errorf("store log chunk %d: %w", seq, err)
	}

	return nil
}

// Finish ends the phase with its outcome.
func (s *Session) Finish(ctx context.Context, f protocol.Finish) error {
	return s.noContent(ctx, "finish the phase", protocol.PathSessionFinish, f)
}

// noContent sends a signed POST and wants 204.
func (s *Session) noContent(ctx context.Context, what, path string, body any) error {
	resp, err := s.Post(ctx, path, body)
	if err != nil {
		return err
	}

	if err = check(resp, statusNoContent); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	return nil
}

// StepPath returns the path of a step route with the step and the chunk
// number filled in.
func StepPath(pattern, step string, seq int) string {
	return strings.NewReplacer("{step}", step, "{seq}", strconv.Itoa(seq)).Replace(pattern)
}
