package servertest_test

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestJobTokenWiring checks that a job token that the credentials path of a job
// session issues works on the state route of the unit of the job, and on no
// other unit. It runs the whole chain on one server: the claim of a phase, the
// job session, the credentials of a step, and the authenticator chain.
func TestJobTokenWiring(t *testing.T) {
	t.Parallel()

	h := newRoundTripHarness(t, traceOff, dustpan.WithPublicURL(publicURL))

	h.workspace = h.createWorkspace("web")
	h.createWorkspace("other")

	sess := h.jobSession()

	// The starter of the flows holds the role planner, which cannot read state.
	// The token acts as the starter, so the starter also gets the role writer.
	grantStarterWriter(h)

	resp, err := sess.Post(t.Context(), protocol.PathSessionCredentials, protocol.CredentialsRequest{Step: "plan"})
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("credentials = %+v, %v; want 200", resp, err)
	}

	var issued protocol.CredentialsResponse

	h.decode(string(resp.Body), &issued)

	if !strings.HasPrefix(issued.Token, "odj_") {
		t.Fatal("token has no odj_ prefix")
	}

	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("job:"+issued.Token))
	base := h.cluster.URL(0)

	// The unit of the job: the token passes the authenticator and the
	// authorizer. The workspace has no state yet, so the answer is 404.
	status, answer := h.send(http.MethodGet, base+"/tofu/state/acme/web", basic, "")
	if status != http.StatusNotFound {
		t.Fatalf("state of the unit of the job = %d %s, want 404", status, answer)
	}

	// One state version of the unit of the job: the token reads it, and the
	// read writes one audit entry that names the job of the token.
	h.readStateWithJobToken(base, basic)

	// Another unit: the token is valid, and the authorizer refuses it.
	status, answer = h.send(http.MethodGet, base+"/tofu/state/acme/other", basic, "")
	if status != http.StatusForbidden {
		t.Fatalf("state of another unit = %d %s, want 403", status, answer)
	}

	if strings.Contains(answer, issued.Token) {
		t.Fatal("the refusal holds the token")
	}

	// A token that the server did not issue gets 401.
	bad := "Basic " + base64.StdEncoding.EncodeToString([]byte("job:odj_not-issued"))

	status, _ = h.send(http.MethodGet, base+"/tofu/state/acme/web", bad, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("state with a token that was not issued = %d, want 401", status)
	}
}

// grantStarterWriter gives the starter of the flows the role writer on the
// namespace acme.
func grantStarterWriter(h *rtHarness) {
	h.t.Helper()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	obj := identity.NamespaceObject("acme")
	st := h.cluster.Store(h.t, 0)
	fence, entry := storetest.GrantWrite(h.t, st, obj, at, "grant:job-token-wiring")

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: "grt-admin-writer", Role: access.RoleWriter,
		CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-admin"},
	}

	if err := st.CreateGrant(h.t.Context(), grant, fence, entry, nil); err != nil {
		h.t.Fatalf("create the writer grant of the starter: %v", err)
	}
}

// auditTrail is the part of the answer of the audit list that the test reads.
type auditTrail struct {
	Entries []auditTrailEntry `json:"entries"`
}

// auditTrailEntry is one entry of the audit list.
type auditTrailEntry struct {
	Action string `json:"action"`
	Object string `json:"object"`
	JobID  string `json:"jobID"`
}

// readStateWithJobToken writes one state version for the workspace web, reads
// it with the job token, and checks the state and the audit entry of the read.
func (h *rtHarness) readStateWithJobToken(base, basic string) {
	h.t.Helper()

	h.lock()
	h.upload()
	h.unlock()

	status, answer := h.send(http.MethodGet, base+"/tofu/state/acme/web", basic, "")
	if status != http.StatusOK {
		h.t.Fatalf("state of the unit of the job after an upload = %d %s, want 200", status, answer)
	}

	if !strings.Contains(answer, `"lineage":"round-trips"`) || !strings.Contains(answer, `"serial":1`) {
		h.t.Fatalf("state of the unit of the job = %s, want the uploaded state version", answer)
	}

	// The server sets the job ID of an audit entry for the credential kind job
	// token only, and the entry must name the job of this session, so the entry
	// of the read shows both the kind and the job.
	var trail auditTrail

	h.decode(h.call(http.MethodGet, "/api/audit/entries?organization=acme", "", http.StatusOK), &trail)

	if h.jobID == "" {
		h.t.Fatal("the harness holds no job ID")
	}

	jobEntries := 0

	for _, e := range trail.Entries {
		if e.Action == "unit:read-secrets" && e.Object == "opentofu:acme/web" && e.JobID == h.jobID {
			jobEntries++
		}
	}

	if jobEntries != 1 {
		h.t.Fatalf("audit entries of the read with the job ID %q = %d, want 1: %+v", h.jobID, jobEntries, trail.Entries)
	}
}
