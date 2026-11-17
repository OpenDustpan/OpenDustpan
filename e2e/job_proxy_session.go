//go:build e2e

package e2e

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// contentTypeHeader is the header of a JSON body.
const contentTypeHeader = "Content-Type"

// ProxyHandoff asks for the proxy handoff token of the claim of operation.
func (r *FakeRunner) ProxyHandoff(ctx context.Context, tb testing.TB, operation string) string {
	tb.Helper()

	path := strings.NewReplacer("{operation}", operation).Replace(protocol.PathClaimProxyHandoff)

	status, body := r.call(ctx, tb, path, nil)
	if status != http.StatusOK {
		tb.Fatalf("proxy handoff = %d %s, want 200", status, body)
	}

	var resp protocol.HandoffResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		tb.Fatalf("decode the proxy handoff: %v", err)
	}

	return resp.HandoffToken
}

// FakeProxy is the job proxy of a claim: it holds the proxy session that it
// opened with the session client of the harness, and signs each call with the
// key of that session.
type FakeProxy struct {
	client *session.Client
	key    ed25519.PrivateKey
	ID     string
	// Assignment is what the server answered to the exchange.
	Assignment protocol.ProxyAssignment
}

// OpenProxySession exchanges a proxy handoff token for a proxy session, as
// the launcher exchanges its token for a job session. It returns the status
// and the body of the answer, and the proxy when the status is 200.
func (e *Env) OpenProxySession(ctx context.Context, tb testing.TB, handoff string) (*FakeProxy, int, []byte) {
	tb.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatalf("make the session key: %v", err)
	}

	client := e.protocolClient()
	challenge := runner.SessionChallenge(client.Server, handoff, runner.Key{Public: pub, CreatedAt: time.Time{}})

	raw, err := json.Marshal(protocol.SessionRequest{
		HandoffToken: handoff, PublicKey: pub, Release: client.Release, Proof: ed25519.Sign(priv, challenge),
	})
	if err != nil {
		tb.Fatalf("encode the proxy session exchange: %v", err)
	}

	tok := sha256.Sum256([]byte(handoff))

	resp, err := client.Signed(ctx, priv, hex.EncodeToString(tok[:]), &session.Request{
		Method: http.MethodPost, Path: protocol.PathProxySession, Body: raw,
		Header: map[string]string{contentTypeHeader: "application/json"},
	}, nil)
	if err != nil {
		tb.Fatalf("POST %s: %v", protocol.PathProxySession, err)
	}

	if resp.Status != http.StatusOK {
		return nil, resp.Status, resp.Body
	}

	var sr protocol.ProxySessionResponse
	if err = json.Unmarshal(resp.Body, &sr); err != nil || sr.Session == "" {
		tb.Fatalf("the proxy session answer %s is not a session: %v", resp.Body, err)
	}

	return &FakeProxy{client: client, key: priv, ID: sr.Session, Assignment: sr.Assignment}, resp.Status, resp.Body
}

// UpstreamCredential asks the upstream credential path with the proof of the
// proxy session key and returns the status and the body of the answer.
func (p *FakeProxy) UpstreamCredential(ctx context.Context, tb testing.TB) (int, []byte) {
	tb.Helper()

	resp, err := p.client.Signed(ctx, p.key, p.ID, &session.Request{
		Method: http.MethodPost, Path: protocol.PathProxyUpstreamCredential, Body: nil, Header: nil,
	}, nil)
	if err != nil {
		tb.Fatalf("POST %s: %v", protocol.PathProxyUpstreamCredential, err)
	}

	return resp.Status, resp.Body
}

// Post sends a signed POST of the proxy session to path with no body.
func (p *FakeProxy) Post(ctx context.Context, tb testing.TB, path string) (int, []byte) {
	tb.Helper()

	resp, err := p.client.Signed(ctx, p.key, p.ID, &session.Request{
		Method: http.MethodPost, Path: path, Body: nil, Header: nil,
	}, nil)
	if err != nil {
		tb.Fatalf("POST %s: %v", path, err)
	}

	return resp.Status, resp.Body
}
