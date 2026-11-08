package servertest_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// issueJobToken makes a live plan job of unit with a starter that holds
// writer on the unit, and returns the secret of its job token.
//
// The server of the harness runs on the wall clock, so the times of the
// records come from the wall clock too: the server checks the expiry of the
// token against its own clock.
func (h *rtHarness) issueJobToken(unit identity.Object) string {
	h.t.Helper()

	ctx := h.t.Context()
	s := h.cluster.Store(h.t, 0)

	ns, err := s.GetNamespace(ctx, unit.Namespace())
	if err != nil {
		h.t.Fatalf("get namespace %s: %v", unit.Namespace(), err)
	}

	at := time.Now().UTC()
	starterID := ident.NewID("prn-")
	starter := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: starterID, Subject: starterID,
		DisplayName: starterID, Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	err = s.CreatePrincipal(ctx, starter, storetest.ServerChange(at, principal.Ref(starterID)))
	if err != nil {
		h.t.Fatalf("create starter %s: %v", starterID, err)
	}

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: unit, ID: ident.NewID(access.GrantIDPrefix),
		Role: access.RoleWriter, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: starterID},
	}

	err = s.CreateGrant(ctx, grant, ns.Fence(), namespaceChange(at, unit.Namespace(), "unit:"+unit.String()), nil)
	if err != nil {
		h.t.Fatalf("grant writer to %s: %v", starterID, err)
	}

	kinds, err := job.DefaultPhases(job.ExecutorRunner, job.OperationPlan)
	if err != nil {
		h.t.Fatalf("phases of %s: %v", job.OperationPlan, err)
	}

	phases := make([]job.Phase, len(kinds))
	for i := range kinds {
		phases[i].Kind = kinds[i]
	}

	pool := "rt-" + starterID
	id := job.ID(ident.NewID(job.IDPrefix))
	j := job.Job{
		ID: id,
		Spec: job.Spec{
			Unit:      unit,
			Operation: job.OperationPlan,
			Purpose:   "",
			Executor:  job.ExecutorRunner,
			Location: job.Location{
				Connection: "",
				Repository: "",
				Ref:        "",
				Directory:  "",
				Revision:   "",
			},
			Steps:     nil,
			Pool:      pool,
			Profile:   "",
			Edits:     nil,
			Delivery:  "",
			Defaulted: nil,
		},
		State: job.StateQueued, Wait: job.WaitNone, Reason: "", Phases: phases, Current: 0,
		StartedBy: starterID, Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil,
		Trigger: nil, Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "",
		RequestHash: "", Version: 0, CreatedAt: time.Time{}, UpdatedAt: time.Time{}, StateChangedAt: time.Time{},
	}

	dispatch := &durable.StartRequest{
		Key: job.PhaseKey(id, 0), Kind: job.RunnerKind(pool), Version: job.PhaseVersion, Input: nil,
	}

	stored, _, err := s.Jobs().Create(ctx, j, dispatch, ns.Fence(), nil)
	if err != nil {
		h.t.Fatalf("create job %s: %v", id, err)
	}

	claim, err := h.cluster.Operations(h.t, 0).Claim(ctx, durable.ClaimRequest{
		Owner: "round-trips", Kinds: []durable.Kind{dispatch.Kind}, MaxVersions: nil,
		Lease: time.Hour, TakeCanceled: false,
	})
	if err != nil {
		h.t.Fatalf("claim the operation of job %s: %v", id, err)
	}

	secret, err := jobtoken.NewSecret()
	if err != nil {
		h.t.Fatalf("new job token: %v", err)
	}

	tok := jobtoken.Token{
		Digest: jobtoken.DigestOf(secret), StarterID: starterID, JobID: stored.ID,
		Namespace: unit.Namespace(), Unit: unit, ClaimOperation: claim.ID, ClaimToken: claim.Claim.Token,
		ExpiresAt: at.Add(time.Hour), CreatedAt: at,
	}

	entry := namespaceChange(at, unit.Namespace(), "job-token:"+string(id))

	err = s.JobTokens().Issue(ctx, tok, ns.Fence(), entry)
	if err != nil {
		h.t.Fatalf("issue job token of %s: %v", id, err)
	}

	return secret.Reveal()
}

// namespaceChange returns a valid audit entry of the kind change in the
// namespace ns, for the record object at the time at.
func namespaceChange(at time.Time, ns namespace.Name, object string) audit.Entry {
	entry := storetest.ServerChange(at, object)
	entry.Namespace = ns

	return entry
}

// stackUnit returns the unit of the stack of the harness.
func (h *rtHarness) stackUnit() identity.Object {
	h.t.Helper()

	store := h.cluster.Store(h.t, 0)
	ref, err := stack.ParseRef("acme", "web", h.stack)
	if err != nil {
		h.t.Fatalf("parse ref: %v", err)
	}

	st, err := store.GetStack(h.t.Context(), ref)
	if err != nil {
		h.t.Fatalf("get stack %s: %v", ref, err)
	}

	ns, err := namespace.ParseName("acme")
	if err != nil {
		h.t.Fatalf("namespace: %v", err)
	}

	unit, err := identity.UnitObject("pulumi", ns, ref.Project.String()+"/"+ref.Stack.String())
	if err != nil {
		h.t.Fatalf("stack unit: %v", err)
	}

	return unit.WithUnitID(st.ID.String())
}

// callWithToken sends a request with auth and fails the test when the status
// is not want.
func (h *rtHarness) callWithToken(method, path, body, auth string, want int) {
	h.t.Helper()

	status, answer := h.send(method, h.cluster.URL(0)+path, auth, body)
	if status != want {
		h.t.Fatalf("%s %s = %d %s, want %d", method, path, status, answer, want)
	}
}

// encrypt returns the base64 ciphertext of one value under the stack of the
// harness.
func (h *rtHarness) encrypt() string {
	h.t.Helper()

	answer := h.call(
		http.MethodPost,
		"/api/stacks/acme/web/"+h.stack+"/encrypt",
		`{"plaintext":"`+base64.StdEncoding.EncodeToString([]byte("round trips"))+`"}`,
		http.StatusOK,
	)

	var doc map[string]string
	if err := json.Unmarshal([]byte(answer), &doc); err != nil || doc["ciphertext"] == "" {
		h.t.Fatalf("encrypt answers %q: %v", answer, err)
	}

	return doc["ciphertext"]
}
