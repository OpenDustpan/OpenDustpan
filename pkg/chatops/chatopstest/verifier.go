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
	// Encode returns the body that the chat tool sends for req. The
	// signed time is not in it.
	Encode func(req chatops.Request) []byte
	// Sign returns the headers that the chat tool sends with body, signed
	// at the time at with the signing secret of Verifier.
	Sign func(body []byte, at time.Time) map[string][]string
	// SignOther returns the headers that Sign returns, signed with another
	// secret.
	SignOther func(body []byte, at time.Time) map[string][]string
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
			"a signed body that does not parse",
			func(h VerifierHarness) map[string][]string { return h.Sign(h.Unparsable, signed) },
			func(h VerifierHarness) []byte { return h.Unparsable },
			chatops.ErrInvalid,
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
			want.At = signed

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
