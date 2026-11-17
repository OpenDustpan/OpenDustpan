package servertest_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// publicURL is the public URL of the servers of the round-trip flows. The
// challenges of the runner protocol sign it.
const publicURL = "https://dustpan.example.com"

// httpTransport sends the requests of the runner protocol to the server of the
// cluster of h.
type httpTransport struct {
	h *rtHarness
}

// Do implements session.Transport.
func (t httpTransport) Do(ctx context.Context, req *session.Request) (*session.Response, error) {
	hr, err := http.NewRequestWithContext(ctx, req.Method, t.h.cluster.URL(0)+req.Path, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	for k, v := range req.Header {
		hr.Header.Set(k, v)
	}

	resp, err := t.h.client.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	raw, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil || readErr != nil {
		return nil, fmt.Errorf("read the answer: %w %w", readErr, closeErr)
	}

	return &session.Response{Status: resp.StatusCode, Body: raw, RetryAfter: 0}, nil
}

// encodeJSON encodes v, or fails the test.
func encodeJSON(h *rtHarness, v any) []byte {
	h.t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("encode %T: %v", v, err)
	}

	return raw
}

// jobSession claims the phase of a new plan job on the workspace as a runner of
// the job pool and opens its job session. The first call joins the runner.
func (h *rtHarness) jobSession() *session.Session {
	h.t.Helper()

	client := &session.Client{Server: publicURL, Transport: httpTransport{h: h}, Release: "0.0.1", Now: nil}

	if h.runnerKey == nil {
		h.joinRunner(client)
	}

	h.jobID = h.startPlanJob()

	var work protocol.Work

	h.decode(h.signedPost(client, protocol.PathPoll, protocol.PollRequest{
		Wait: protocol.FromStd(2 * time.Second), AssignmentVersion: runner.AssignmentVersion,
	}), &work)

	path := strings.NewReplacer("{operation}", work.Operation).Replace(protocol.PathClaimHandoff)

	var handoff protocol.HandoffResponse

	h.decode(h.signedPost(client, path, nil), &handoff)

	sess, err := session.Open(h.t.Context(), client, handoff.HandoffToken)
	if err != nil {
		h.t.Fatalf("open the job session: %v", err)
	}

	return sess
}

// joinRunner makes a join token of the pool of the job flows and joins a
// runner with it.
func (h *rtHarness) joinRunner(client *session.Client) {
	h.t.Helper()

	var pool struct {
		ID string `json:"id"`
	}

	h.decode(h.call(http.MethodPost, "/api/dustpan/v1/namespaces/acme/runner-pools", `{"name":"ci","purpose":"jobs"}`,
		http.StatusCreated), &pool)

	h.jobPool = true

	var created struct {
		Token string `json:"token"`
	}

	h.decode(h.call(http.MethodPost, "/api/dustpan/v1/namespaces/acme/runner-pools/"+pool.ID+"/join-tokens", "",
		http.StatusCreated), &created)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		h.t.Fatalf("generate the key of the runner: %v", err)
	}

	body := encodeJSON(h, protocol.JoinRequest{
		Token: created.Token, Pool: pool.ID, Name: "builder-1", Kind: string(runner.KindMachine),
		Isolation: string(runner.IsolationGVisor), Labels: nil, PublicKey: pub,
		Proof: ed25519.Sign(priv, runner.JoinChallenge(publicURL, pool.ID, "builder-1", created.Token)),
	})

	resp, err := client.Send(h.t.Context(), &session.Request{
		Method: http.MethodPost, Path: protocol.PathJoin, Body: body, Header: nil,
	})
	if err != nil || resp.Status != http.StatusOK {
		h.t.Fatalf("join = %+v, %v; want 200", resp, err)
	}

	var joined protocol.JoinResponse

	h.decode(string(resp.Body), &joined)

	h.runnerKey, h.runnerID = priv, joined.Runner

	h.createStarter()
}

// createStarter stores the principal of the bootstrap admin, the starter of
// the jobs of the flows. The credentials path reads the record of the starter.
func (h *rtHarness) createStarter() {
	h.t.Helper()

	st := h.cluster.Store(h.t, 0)

	// The starter is a person. The inactivity disable of the server disables a
	// person whose records are older than the inactivity period, at the time of
	// the database clock. A fixed time in the past makes the sweep disable the
	// starter when its tick runs during the flow, and the credentials path then
	// answers grant-lost. A time at the database clock keeps the starter active.
	at, err := st.DatabaseNow(h.t.Context())
	if err != nil {
		h.t.Fatalf("read the clock of the database: %v", err)
	}

	at = at.UTC().Truncate(time.Millisecond)

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-admin", Subject: "admin",
		DisplayName: "admin", Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}

	err = st.CreatePrincipal(h.t.Context(), p, storetest.ServerChange(at, principal.Ref(p.ID)))
	if err != nil {
		h.t.Fatalf("create the starter: %v", err)
	}

	// The starter holds the role planner on the namespace: the token acts as
	// the starter, and the credentials path checks the grant of the starter.
	obj := identity.NamespaceObject("acme")
	fence, entry := storetest.GrantWrite(h.t, st, obj, at, "grant:roundtrips")

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: "grt-admin-planner", Role: access.RolePlanner,
		CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectPrincipal, ID: p.ID},
	}

	if err = st.CreateGrant(h.t.Context(), grant, fence, entry, nil); err != nil {
		h.t.Fatalf("create the grant of the starter: %v", err)
	}
}

// signedPost sends a POST of the runner and returns the answer body.
func (h *rtHarness) signedPost(client *session.Client, path string, body any) string {
	h.t.Helper()

	var raw []byte

	if body != nil {
		raw = encodeJSON(h, body)
	}

	resp, err := client.Signed(h.t.Context(), h.runnerKey, h.runnerID, &session.Request{
		Method: http.MethodPost, Path: path, Body: raw, Header: nil,
	}, nil)
	if err != nil || resp.Status != http.StatusOK {
		h.t.Fatalf("POST %s = %+v, %v; want 200", path, resp, err)
	}

	return string(resp.Body)
}

// credentials asks the credentials path of the job session for the token of the
// step plan.
func (h *rtHarness) credentials(sess *session.Session) {
	h.t.Helper()

	resp, err := sess.Post(h.t.Context(), protocol.PathSessionCredentials, protocol.CredentialsRequest{Step: "plan"})
	if err != nil || resp.Status != http.StatusOK {
		h.t.Fatalf("credentials = %+v, %v; want 200", resp, err)
	}
}
