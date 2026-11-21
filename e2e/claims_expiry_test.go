//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// expiryRetries is the count of calls that a step makes while it waits for
// the sweep to end a claim. With the short lease and sweep period of the
// environment, a claim ends well within this many calls.
const expiryRetries = 120

// TestClaimsLeaseExpires proves the lease of a claim of the local wrapper on a
// server of each store: the claim is registered with a lease, a renewal moves
// the lease and shows the time of the renewal, the claim blocks an overlapping
// claim while its lease lives, and after the lease passes the expiry sweep
// ends the claim and an overlapping claim is then accepted.
func TestClaimsLeaseExpires(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			claimsLeaseScenario(t, e2e.NewEnvWithClaimTiming(t.Context(), t, store))
		})
	}
}

// leaseRun holds what the steps of the lease scenario share.
type leaseRun struct {
	t       *testing.T
	env     *e2e.Env
	wsID    string
	base    *string
	claimID string
	headers map[string]string
}

// claimsLeaseScenario runs the steps of TestClaimsLeaseExpires on env.
func claimsLeaseScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	r := &leaseRun{
		t: t, env: env, wsID: "ws-missing", base: nil, claimID: "",
		headers: map[string]string{"Dustpan-Release": claimsRelease, "Content-Type": "application/json"},
	}

	r.createWorkspace()

	r.register("register the claim of the wrapper", []string{"aws_instance.a"}, http.StatusCreated, r.checkNew)
	r.renew("renew the claim", r.claimID, http.StatusOK, r.checkRenewed)
	r.renew("renew a claim that does not exist", "clm-unknown", http.StatusNotFound, nil)

	// The pause of the steps above is shorter than the lease, so the claim
	// still blocks an overlapping claim.
	overlap := []string{"aws_instance.a", "aws_instance.c"}

	r.register("refuse an overlapping claim while the lease lives", overlap, http.StatusConflict,
		func(body string) error { return leaseWant(body, "claim-overlap") })

	r.waitForEnd()
	r.register("accept an overlapping claim after the expiry", overlap, http.StatusCreated, nil)
	r.renew("refuse the renewal of an ended claim", r.claimID, http.StatusConflict,
		func(body string) error { return leaseWant(body, "claim-ended") })
}

// claimsPath returns the claims path of the workspace.
func (r *leaseRun) claimsPath() string { return "/runner/v1/workspaces/" + r.wsID + "/claims" }

// createWorkspace creates the workspace and its base state version.
func (r *leaseRun) createWorkspace() {
	const unit = "claims-lease"

	r.env.APIStepA(r.t.Context(), r.t, e2e.APIStepSpec{
		UserAgent: claimsAgent, Name: "lease: create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + unit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})

	r.env.APIStepA(r.t.Context(), r.t, e2e.APIStepSpec{
		UserAgent: claimsAgent, Name: "lease: read the workspace ID", Method: http.MethodGet,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces/" + unit,
		Body: "", Want: http.StatusOK, Scheme: "",
		Check: func(body string) error {
			var doc disjointWorkspaceDoc
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				return fmt.Errorf("%w: decode workspace: %w", errDisjointClaims, err)
			}

			r.wsID = doc.Data.ID

			return nil
		},
	})

	r.base = disjointBase(r.t.Context(), r.t, r.env, &r.wsID)
}

// register sends a register call of the local wrapper.
func (r *leaseRun) register(name string, addresses []string, want int, check func(string) error) {
	r.env.APIStepWithHeaders(r.t.Context(), r.t, e2e.APIStepSpec{
		UserAgent: claimsAgent, Name: "lease: " + name, Method: http.MethodPost, Path: r.claimsPath(),
		Body: `{"base":` + fmt.Sprintf("%q", *r.base) + `,"addresses":` + addressesJSON(addresses) + `}`,
		Want: want, Scheme: "token", Check: check,
	}, r.headers)
}

// renew sends a renew call of the local wrapper for the claim id.
func (r *leaseRun) renew(name, id string, want int, check func(string) error) {
	r.env.APIStepWithHeaders(r.t.Context(), r.t, e2e.APIStepSpec{
		UserAgent: claimsAgent, Name: "lease: " + name, Method: http.MethodPost,
		Path: r.claimsPath() + "/" + id + "/renew", Body: "", Want: want, Scheme: "token", Check: check,
	}, r.headers)
}

// waitForEnd reads the list until the sweep has ended the claim.
func (r *leaseRun) waitForEnd() {
	r.env.APIStepUntil(r.t.Context(), r.t, e2e.APIStepSpec{
		UserAgent: claimsAgent, Name: "lease: the sweep ends the claim after its lease", Method: http.MethodGet,
		Path: r.claimsPath(), Body: "", Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			var doc leaseListDoc
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				return fmt.Errorf("%w: decode claims: %w", errDisjointClaims, err)
			}

			for i := range doc.Claims {
				if c := &doc.Claims[i]; c.ID == r.claimID && c.State == "ended" && c.EndedAt != "" {
					return nil
				}
			}

			return fmt.Errorf("%w: claim %s has not ended: %s", errDisjointClaims, r.claimID, body)
		},
	}, r.headers, expiryRetries)
}

// checkNew checks the answer of the first register call: a claim with a lease
// and no renewal. It keeps the claim ID.
func (r *leaseRun) checkNew(body string) error {
	var doc leaseClaimDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: decode claim: %w", errDisjointClaims, err)
	}

	if doc.ID == "" || doc.LeaseEndsAt == "" || doc.RenewedAt != "" {
		return fmt.Errorf("%w: a new claim has a lease and no renewal: %s", errDisjointClaims, body)
	}

	r.claimID = doc.ID

	return nil
}

// checkRenewed checks the answer of a renewal: the same accepted claim, with
// its lease and the time of the renewal.
func (r *leaseRun) checkRenewed(body string) error {
	var doc leaseClaimDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: decode claim: %w", errDisjointClaims, err)
	}

	if doc.ID != r.claimID || doc.State != "accepted" || doc.RenewedAt == "" || doc.LeaseEndsAt == "" {
		return fmt.Errorf("%w: a renewed claim shows its renewal and its lease: %s", errDisjointClaims, body)
	}

	return nil
}

// leaseWant fails unless the refusal body names the code.
func leaseWant(body, code string) error {
	if !strings.Contains(body, code) {
		return fmt.Errorf("%w: the refusal names no %s code: %s", errDisjointClaims, code, body)
	}

	return nil
}

// leaseClaimDoc is the part of a claim document that the lease steps read.
type leaseClaimDoc struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	EndedAt     string `json:"endedAt"`
	LeaseEndsAt string `json:"leaseEndsAt"`
	RenewedAt   string `json:"renewedAt"`
}

// leaseListDoc is a list answer of the lease steps.
type leaseListDoc struct {
	Claims []leaseClaimDoc `json:"claims"`
}
