package chatopstest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/chatops"
	"opendustpan.org/opendustpan/pkg/job"
)

// VerifierHarness is one adapter of a chat tool for one case.
type VerifierHarness struct {
	// Verifier is the adapter, with the signing secret of the
	// installation.
	Verifier chatops.Verifier
	// Platform is the chat tool of the adapter. Verify sets
	// Request.Platform to it, whatever the body says.
	Platform string
	// Encode returns the body that the chat tool sends for req. The
	// signed time is not in it. When the body of the tool can name a
	// platform, Encode puts req.Platform in it, so that the suite can
	// check that Verify ignores it.
	Encode func(req chatops.Request) []byte
	// Sign returns the headers that the chat tool sends with body, signed
	// at the time at with the signing secret of Verifier.
	Sign func(body []byte, at time.Time) map[string][]string
	// SignOther returns the headers that Sign returns, signed with another
	// secret.
	SignOther func(body []byte, at time.Time) map[string][]string
	// Retime returns a copy of headers from Sign with the timestamp set
	// to at and the signature kept, as an attacker who replays an old
	// request with a new timestamp does.
	Retime func(headers map[string][]string, at time.Time) map[string][]string
	// Unparsable is a body that the adapter cannot parse, or that has
	// none of the fields of a request.
	Unparsable []byte
}

// VerifierFactory returns a fresh harness for one case.
type VerifierFactory func(t *testing.T) VerifierHarness

// RunVerifier executes the conformance suite of chatops.Verifier against
// harnesses from newHarness.
func RunVerifier(t *testing.T, newHarness VerifierFactory) {
	t.Helper()

	req := verifierRequest()
	signed := now().Add(-time.Minute)

	cases := []struct {
		name    string
		headers func(h VerifierHarness) map[string][]string
		body    func(h VerifierHarness) []byte
		want    error
	}{
		{
			"a signed request",
			func(h VerifierHarness) map[string][]string { return h.Sign(h.Encode(req), signed) },
			nil, nil,
		},
		{
			"a request signed with another secret",
			func(h VerifierHarness) map[string][]string { return h.SignOther(h.Encode(req), signed) },
			nil, chatops.ErrSignature,
		},
		{
			"a body changed after the signature",
			func(h VerifierHarness) map[string][]string { return h.Sign(h.Encode(req), signed) },
			func(h VerifierHarness) []byte {
				other := req
				other.User = "U2"

				return h.Encode(other)
			},
			chatops.ErrSignature,
		},
		{"no headers", func(VerifierHarness) map[string][]string { return nil }, nil, chatops.ErrSignature},
		{
			"a timestamp older than the maximum age",
			func(h VerifierHarness) map[string][]string {
				return h.Sign(h.Encode(req), now().Add(-chatops.SignatureMaxAge-time.Second))
			},
			nil, chatops.ErrSignature,
		},
		{
			"a timestamp in the future",
			func(h VerifierHarness) map[string][]string {
				return h.Sign(h.Encode(req), now().Add(chatops.SignatureMaxSkew+time.Second))
			},
			nil, chatops.ErrSignature,
		},
		{
			"an old signature with a new timestamp",
			func(h VerifierHarness) map[string][]string {
				old := h.Sign(h.Encode(req), now().Add(-chatops.SignatureMaxAge-time.Minute))

				return h.Retime(old, signed)
			},
			nil, chatops.ErrSignature,
		},
		{
			"a signed body that does not parse",
			func(h VerifierHarness) map[string][]string { return h.Sign(h.Unparsable, signed) },
			func(h VerifierHarness) []byte { return h.Unparsable },
			chatops.ErrInvalid,
		},
		{
			"an unsigned body that does not parse",
			func(VerifierHarness) map[string][]string { return nil },
			func(h VerifierHarness) []byte { return h.Unparsable },
			chatops.ErrSignature,
		},
		{
			"a body that does not parse, signed with another secret",
			func(h VerifierHarness) map[string][]string { return h.SignOther(h.Unparsable, signed) },
			func(h VerifierHarness) []byte { return h.Unparsable },
			chatops.ErrSignature,
		},
		{
			"a signed body that names another chat tool",
			func(h VerifierHarness) map[string][]string { return h.Sign(h.Encode(otherPlatform(req)), signed) },
			func(h VerifierHarness) []byte { return h.Encode(otherPlatform(req)) },
			nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)

			body := h.Encode(req)
			if tc.body != nil {
				body = tc.body(h)
			}

			got, err := h.Verifier.Verify(t.Context(), tc.headers(h), body, now())
			if tc.want != nil {
				wantErr(t, "Verify", err, tc.want)

				return
			}

			want := req
			want.At, want.Platform = signed, h.Platform

			if err != nil || !got.At.Equal(signed) {
				t.Fatalf("Verify = %+v (%v), want the signed time %v", got, err, signed)
			}

			got.At = signed
			if got != want {
				t.Fatalf("Verify = %+v (%v), want %+v", got, err, want)
			}
		})
	}
}

// otherPlatform returns req as a body that names another chat tool than
// the adapter.
func otherPlatform(req chatops.Request) chatops.Request {
	req.Platform = "chat-other"

	return req
}

func verifierRequest() chatops.Request {
	return chatops.Request{
		Platform: "chat-a", Workspace: "T1", User: "U1", DeliveryID: "d-1", Action: chatops.ActionApprove,
		Target: chatops.Target{
			Kind: chatops.TargetJob, ID: "job-1", Phase: 1, Gate: job.GateApproval,
			Plan: job.PlanBinding{Plan: "plan-1", DisplaySHA256: "ab", ApplySHA256: "cd", StateVersion: "7"},
		},
		At: time.Time{},
	}
}
