package chatopstest_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/chatops"
	"opendustpan.org/opendustpan/pkg/chatops/chatopstest"
	"opendustpan.org/opendustpan/pkg/job"
)

// TestFakeVerifierConformance runs the suite of the Verifier port against
// a fake chat tool that signs with HMAC-SHA256, so that the cases of the
// suite run before the first adapter of a real chat tool.
func TestFakeVerifierConformance(t *testing.T) {
	t.Parallel()

	chatopstest.RunVerifier(t, func(*testing.T) chatopstest.VerifierHarness {
		secret, other := []byte("secret-of-the-installation"), []byte("secret-of-another-installation")
		v := fakeVerifier{platform: "chat-a", secret: secret}

		return chatopstest.VerifierHarness{
			Verifier:   v,
			Platform:   "chat-a",
			Encode:     encode,
			Sign:       func(body []byte, at time.Time) map[string][]string { return sign(secret, body, at) },
			SignOther:  func(body []byte, at time.Time) map[string][]string { return sign(other, body, at) },
			Retime:     retime,
			Unparsable: []byte("{not a request"),
		}
	})
}

const (
	headerTimestamp = "X-Fake-Timestamp"
	headerSignature = "X-Fake-Signature"
)

// fakeVerifier is an adapter of a fake chat tool. The tool signs
// "v0:<unix seconds>:<body>" with HMAC-SHA256 and sends the timestamp and
// the signature in two headers, as many chat tools do. The body names a
// platform, which the adapter ignores.
type fakeVerifier struct {
	platform string
	secret   []byte
}

func (f fakeVerifier) Verify(_ context.Context, headers map[string][]string, body []byte, now time.Time) (
	chatops.Request, error,
) {
	ts, sig := first(headers, headerTimestamp), first(headers, headerSignature)
	if ts == "" || sig == "" {
		return chatops.Request{}, fmt.Errorf("verify: %w: a header is missing", chatops.ErrSignature)
	}

	got, err := hex.DecodeString(sig)
	if err != nil || !hmac.Equal(got, mac(f.secret, ts, body)) {
		return chatops.Request{}, fmt.Errorf("verify: %w", chatops.ErrSignature)
	}

	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return chatops.Request{}, fmt.Errorf("verify: %w: the timestamp is not a number", chatops.ErrSignature)
	}

	at := time.Unix(sec, 0).UTC()
	if err := chatops.CheckTimestamp(at, now); err != nil {
		return chatops.Request{}, fmt.Errorf("verify: %w", err)
	}

	var w wire
	if err := json.Unmarshal(body, &w); err != nil {
		return chatops.Request{}, fmt.Errorf("verify: %w: %w", chatops.ErrInvalid, err)
	}

	req := w.request(at)
	req.Platform = f.platform

	if err := req.Validate(); err != nil {
		return chatops.Request{}, fmt.Errorf("verify: %w", err)
	}

	return req, nil
}

func first(headers map[string][]string, name string) string {
	if v := headers[name]; len(v) == 1 {
		return v[0]
	}

	return ""
}

func mac(secret []byte, ts string, body []byte) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte("v0:" + ts + ":"))
	h.Write(body)

	return h.Sum(nil)
}

// wire is the body that the fake chat tool sends.
type wire struct {
	Platform   string `json:"platform"`
	Workspace  string `json:"workspace"`
	User       string `json:"user"`
	DeliveryID string `json:"deliveryID"`
	Action     string `json:"action"`
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Phase      int    `json:"phase"`
	Gate       string `json:"gate"`
	Plan       string `json:"plan"`
	Display    string `json:"display"`
	Apply      string `json:"apply"`
	State      string `json:"state"`
}

func (w wire) request(at time.Time) chatops.Request {
	return chatops.Request{
		Platform: w.Platform, Workspace: w.Workspace, User: w.User, DeliveryID: w.DeliveryID,
		Action: chatops.Action(w.Action),
		Target: chatops.Target{
			Kind: chatops.TargetKind(w.Kind), ID: w.ID, Phase: w.Phase, Gate: job.GateType(w.Gate),
			Plan: job.PlanBinding{Plan: w.Plan, DisplaySHA256: w.Display, ApplySHA256: w.Apply, StateVersion: w.State},
		},
		At: at,
	}
}

func encode(req chatops.Request) []byte {
	t := req.Target

	b, err := json.Marshal(wire{
		Platform: req.Platform, Workspace: req.Workspace, User: req.User, DeliveryID: req.DeliveryID,
		Action: string(req.Action), Kind: string(t.Kind), ID: t.ID, Phase: t.Phase, Gate: string(t.Gate),
		Plan: t.Plan.Plan, Display: t.Plan.DisplaySHA256, Apply: t.Plan.ApplySHA256, State: t.Plan.StateVersion,
	})
	if err != nil {
		panic(err)
	}

	return b
}

func sign(secret, body []byte, at time.Time) map[string][]string {
	ts := strconv.FormatInt(at.Unix(), 10)

	return map[string][]string{
		headerTimestamp: {ts},
		headerSignature: {hex.EncodeToString(mac(secret, ts, body))},
	}
}

func retime(headers map[string][]string, at time.Time) map[string][]string {
	out := maps.Clone(headers)

	out[headerTimestamp] = []string{strconv.FormatInt(at.Unix(), 10)}

	return out
}
