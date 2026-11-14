package session_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

const (
	server  = "https://dustpan.example.com"
	handoff = "dph_0123456789abcdef"
	release = "0.0.1"
)

var errReset = errors.New("connection reset")

// answerFunc answers one request.
type answerFunc func(req *session.Request) (*session.Response, error)

// fakeServer is a Transport that checks each proof as the server does and
// answers with answer.
type fakeServer struct {
	t      *testing.T
	mu     sync.Mutex
	key    ed25519.PublicKey
	calls  []*session.Request
	answer answerFunc
}

func newFake(t *testing.T, answer answerFunc) *fakeServer {
	t.Helper()

	return &fakeServer{t: t, mu: sync.Mutex{}, key: nil, calls: nil, answer: answer}
}

// Do checks the release and the proof of req and returns the answer.
func (f *fakeServer) Do(_ context.Context, req *session.Request) (*session.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, req)

	if req.Header[protocol.HeaderRelease] != release {
		f.t.Errorf("%s: release header = %q", req.Path, req.Header[protocol.HeaderRelease])
	}

	if req.Path == protocol.PathSession {
		f.key = f.exchangeKey(req)
	}

	f.checkProof(req)

	return f.answer(req)
}

// exchangeKey checks the proof of the exchange and returns the new key.
func (f *fakeServer) exchangeKey(req *session.Request) ed25519.PublicKey {
	var sr protocol.SessionRequest
	if err := json.Unmarshal(req.Body, &sr); err != nil {
		f.t.Fatalf("decode the exchange: %v", err)
	}

	k := runner.Key{Public: sr.PublicKey, CreatedAt: time.Time{}}
	if !ed25519.Verify(sr.PublicKey, runner.SessionChallenge(server, handoff, k), sr.Proof) {
		f.t.Errorf("the exchange proof does not verify")
	}

	return sr.PublicKey
}

// checkProof checks the proof header of req with the session key.
func (f *fakeServer) checkProof(req *session.Request) {
	rest, ok := strings.CutPrefix(req.Header[protocol.HeaderProof], "v1 ")
	if !ok {
		f.t.Fatalf("%s: proof header %q has no version", req.Path, req.Header[protocol.HeaderProof])
	}

	fields := map[string]string{}

	for kv := range strings.FieldsSeq(rest) {
		k, v, _ := strings.Cut(kv, "=")
		fields[k] = v
	}

	ms, msErr := strconv.ParseInt(fields["t"], 10, 64)
	sig, sigErr := base64.RawURLEncoding.DecodeString(fields["sig"])

	if msErr != nil || sigErr != nil {
		f.t.Fatalf("%s: proof header fields: %v %v", req.Path, msErr, sigErr)
	}

	sum := sha256.Sum256(req.Body)

	h := req.Header[protocol.HeaderContentSHA256]
	if h != "" && h != hex.EncodeToString(sum[:]) {
		f.t.Errorf("%s: content hash header does not match the body", req.Path)
	}

	proof := runner.Proof{Subject: fields["sub"], At: time.UnixMilli(ms), Nonce: fields["n"], Signature: sig}
	signed := runner.Signed{Method: req.Method, PathQuery: req.Path, BodySHA256: sum[:]}

	key := runner.Key{Public: f.key, CreatedAt: time.Now()}
	if err := runner.CheckProof(key, proof, signed, time.Now()); err != nil {
		f.t.Errorf("%s: proof: %v", req.Path, err)
	}
}

// jsonAnswer returns an answer with the status and the JSON of v.
func jsonAnswer[T any](t *testing.T, status int, v *T) *session.Response {
	t.Helper()

	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	return &session.Response{Status: status, Body: body, RetryAfter: 0}
}

// noBody is the answer 204.
func noBody() *session.Response {
	return &session.Response{Status: 204, Body: nil, RetryAfter: 0}
}

// opened returns an answer function that opens the session "ses-1" and
// then answers rest.
func opened(t *testing.T, rest answerFunc) answerFunc {
	t.Helper()

	return func(req *session.Request) (*session.Response, error) {
		if req.Path != protocol.PathSession {
			return rest(req)
		}

		return jsonAnswer(t, 200, &protocol.SessionResponse{
			Session: "ses-1", ExpiresAt: time.Now(), Lease: protocol.FromStd(time.Minute), AssignmentVersion: 1,
			Assignment: protocol.Assignment{
				Namespace: "acme", Unit: "", Tool: "", Job: "job-1", Phase: "plan", Sweep: "", ServerURL: server,
				Source: nil, Steps: nil, Plan: nil, GitCredentials: "none", GitHelper: "", Profile: "",
			},
		}), nil
	}
}

// errorAnswer returns an answer with the protocol error code.
func errorAnswer(t *testing.T, status int, code string) *session.Response {
	t.Helper()

	return jsonAnswer(t, status, &protocol.Error{Code: code, Message: "refused", Supported: ""})
}

func client(f *fakeServer) *session.Client {
	return &session.Client{Server: server, Transport: f, Release: release, Now: nil}
}

func open(t *testing.T, f *fakeServer) *session.Session {
	t.Helper()

	sess, err := session.Open(t.Context(), client(f), handoff)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	return sess
}

// emptyEnd is the end of a step with no log.
func emptyEnd() protocol.StepEnd {
	now := time.Now()

	return protocol.StepEnd{ExitCode: 0, StartedAt: now, FinishedAt: now, LogChunks: 0, LogSHA256: ""}
}

func TestOpenAndCalls(t *testing.T) {
	t.Parallel()

	f := newFake(t, opened(t, func(req *session.Request) (*session.Response, error) {
		if req.Path == protocol.PathSessionRenew {
			return jsonAnswer(
					t,
					200,
					&protocol.RenewResponse{ExpiresAt: time.Now(), Canceled: true, SessionOpen: false},
				),
				nil
		}

		return noBody(), nil
	}))

	sess := open(t, f)
	if sess.ID != "ses-1" || sess.Assignment.Job != "job-1" || sess.Lease != time.Minute {
		t.Fatalf("session = %+v", sess)
	}

	ctx := t.Context()
	calls := []func() error{
		func() error { return sess.BeginStep(ctx, "init") },
		func() error { return sess.PutLog(ctx, "init", 0, []byte("line\n")) },
		func() error { return sess.EndStep(ctx, "init", emptyEnd()) },
		func() error {
			rr, err := sess.Renew(ctx)
			if err == nil && !rr.Canceled {
				t.Error("the renewal lost the cancel")
			}

			return err
		},
		func() error { return sess.Finish(ctx, protocol.Finish{Succeeded: true, Message: ""}) },
	}

	for i, call := range calls {
		if err := call(); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	want := []string{
		protocol.PathSession, "/runner/v1/session/steps/init/begin", "/runner/v1/session/steps/init/logs/0",
		"/runner/v1/session/steps/init/end", protocol.PathSessionRenew, protocol.PathSessionFinish,
	}

	checkCalls(t, f.calls, want)
}

// checkCalls checks the paths of the calls, and that no header holds the
// handoff token.
func checkCalls(t *testing.T, calls []*session.Request, want []string) {
	t.Helper()

	if len(calls) != len(want) {
		t.Fatalf("calls = %d, want %d", len(calls), len(want))
	}

	for i, c := range calls {
		if c.Path != want[i] {
			t.Errorf("call %d = %s, want %s", i, c.Path, want[i])
		}

		for k, v := range c.Header {
			if strings.Contains(v, handoff) {
				t.Errorf("%s: header %s holds the handoff token", c.Path, k)
			}
		}
	}
}

func TestErrorsMapToSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		code   string
		raw    string
		err    error
		want   error
	}{
		{"claim lost", 409, protocol.ErrorClaimLost, "", nil, session.ErrClaimLost},
		{"version unsupported", 426, protocol.ErrorVersionUnsupported, "", nil, session.ErrVersionUnsupported},
		{"version ahead", 426, protocol.ErrorVersionAhead, "", nil, session.ErrVersionAhead},
		{"unavailable", 503, protocol.ErrorUnavailable, "", nil, session.ErrUnavailable},
		{"rate limited", 429, protocol.ErrorRateLimited, "", nil, session.ErrUnavailable},
		{"invalid", 400, protocol.ErrorInvalid, "", nil, session.ErrRefused},
		{"no error body", 404, "", "404 page not found", nil, session.ErrRefused},
		{"gateway with no body", 502, "", "<html>bad gateway</html>", nil, session.ErrUnavailable},
		{"proxy rate limit with no body", 429, "", "slow down", nil, session.ErrUnavailable},
		{"server error with no body", 500, "", "internal error", nil, session.ErrUnavailable},
		{"no answer", 0, "", "", errReset, session.ErrUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answer := func(*session.Request) (*session.Response, error) {
				switch {
				case tc.err != nil:
					return nil, tc.err
				case tc.code == "":
					return &session.Response{Status: tc.status, Body: []byte(tc.raw), RetryAfter: 0}, nil
				default:
					return errorAnswer(t, tc.status, tc.code), nil
				}
			}

			sess := open(t, newFake(t, opened(t, answer)))

			failed := protocol.Finish{Succeeded: false, Message: ""}
			if err := sess.Finish(t.Context(), failed); !errors.Is(err, tc.want) {
				t.Fatalf("Finish = %v, want %v", err, tc.want)
			}

			// The exchange maps the same codes, and its error never holds
			// the token.
			_, err := session.Open(t.Context(), client(newFake(t, answer)), handoff)
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), handoff) {
				t.Fatalf("Open = %v, want %v with no token", err, tc.want)
			}
		})
	}
}

// TestHostileStepNamesNeverReachAPath checks that a step name that would
// change the path of a call is refused before any call.
func TestHostileStepNamesNeverReachAPath(t *testing.T) {
	t.Parallel()

	f := newFake(t, opened(t, func(*session.Request) (*session.Response, error) { return noBody(), nil }))
	sess := open(t, f)
	ctx := t.Context()

	for _, name := range []string{"../finish", "a/b", "", "UPPER", "a?x=1", strings.Repeat("a", 200)} {
		calls := []func() error{
			func() error { return sess.BeginStep(ctx, name) },
			func() error { return sess.EndStep(ctx, name, emptyEnd()) },
			func() error { return sess.PutLog(ctx, name, 0, []byte("x")) },
		}

		for i, call := range calls {
			if err := call(); !errors.Is(err, session.ErrInvalid) {
				t.Errorf("call %d with step %q = %v, want ErrInvalid", i, name, err)
			}
		}
	}

	if len(f.calls) != 1 {
		t.Fatalf("calls = %d, want only the exchange", len(f.calls))
	}
}
