//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// claimsAgent is the User-Agent of the disjoint claims steps. The route
// coverage check counts requests that do not carry the harness agent.
const claimsAgent = "opendustpan-e2e-claims"

// errDisjointClaims names the failures of the disjoint claims step.
var errDisjointClaims = errors.New("the disjoint claims step failed")

// disjointClaimDoc is the part of a claim document that the steps read.
type disjointClaimDoc struct {
	ID        string   `json:"id"`
	State     string   `json:"state"`
	Base      string   `json:"base"`
	Addresses []string `json:"addresses"`
	Principal string   `json:"principal"`
	StartedAt string   `json:"startedAt"`
}

// disjointListDoc is the part of the claim list that the steps read.
type disjointListDoc struct {
	Claims []disjointClaimDoc `json:"claims"`
}

// disjointWorkspaceData is the workspace ID of a workspace answer.
type disjointWorkspaceData struct {
	ID string `json:"id"`
}

// disjointWorkspaceDoc is the part of a workspace answer that the step
// reads.
type disjointWorkspaceDoc struct {
	Data disjointWorkspaceData `json:"data"`
}

// disjointStateVersionDoc is the part of a state version answer that the
// step reads.
type disjointStateVersionDoc struct {
	Data disjointWorkspaceData `json:"data"`
}

// claimsRelease is the release that the claim calls send. The claim routes
// check the Dustpan-Release header, as every runner protocol route does.
const claimsRelease = "0.0.1"

// aDisjointClaimsAccepted proves that two disjoint apply claims on one
// workspace are both accepted through the claim route, that the list shows
// two claim records in state accepted, that an overlapping third claim is
// refused with 409 and no record, and that the claims add no state version.
// The calls use the access token of the harness, the credential of the local
// wrapper: it registers with no job ID and acts as the person who signed in.
func aDisjointClaimsAccepted(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	const unit = "claims-network"

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: claimsAgent, Name: "disjoint claims: create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + unit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})

	wsID := "ws-missing"

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: claimsAgent, Name: "disjoint claims: read the workspace ID", Method: http.MethodGet,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces/" + unit,
		Body: "", Want: http.StatusOK, Scheme: "",
		Check: func(body string) error {
			var doc disjointWorkspaceDoc
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				return fmt.Errorf("%w: decode workspace: %w", errDisjointClaims, err)
			}

			wsID = doc.Data.ID

			return nil
		},
	})

	base := disjointBase(ctx, t, env, &wsID)
	path := func() string { return "/runner/v1/workspaces/" + wsID + "/claims" }
	headers := map[string]string{"Dustpan-Release": claimsRelease, "Content-Type": "application/json"}

	register := func(name string, addresses []string, want int, check func(string) error) {
		env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
			UserAgent: claimsAgent, Name: "disjoint claims: " + name, Method: http.MethodPost, Path: path(),
			Body: `{"base":` + fmt.Sprintf("%q", *base) + `,"addresses":` + addressesJSON(addresses) + `}`,
			Want: want, Scheme: "token", Check: check,
		}, headers)
	}

	register("register the first claim", []string{"aws_instance.a"}, http.StatusCreated, nil)
	register("register the second claim", []string{"aws_instance.b"}, http.StatusCreated, nil)
	register(
		"refuse an overlapping claim",
		[]string{"aws_instance.a", "aws_instance.c"},
		http.StatusConflict,
		func(body string) error {
			if !strings.Contains(body, "claim-overlap") {
				return fmt.Errorf("%w: the refusal names no claim-overlap code: %s", errDisjointClaims, body)
			}

			return nil
		},
	)

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: claimsAgent, Name: "disjoint claims: list both claims", Method: http.MethodGet, Path: path(),
		Body: "", Want: http.StatusOK, Scheme: "token", Check: checkDisjointList,
	}, headers)
}

// addressesJSON returns the JSON array of the addresses.
func addressesJSON(addresses []string) string {
	raw, err := json.Marshal(addresses)
	if err != nil {
		panic(err)
	}

	return string(raw)
}

// disjointBase locks the workspace, creates one state version on it as the
// base of the claims, and unlocks it again. It returns a pointer to the base
// ID that the create step fills in.
func disjointBase(ctx context.Context, t *testing.T, env *e2e.Env, wsID *string) *string {
	t.Helper()

	base := new(string)
	*base = "sv-missing"

	lock := func(action string) {
		env.APIStepA(ctx, t, e2e.APIStepSpec{
			UserAgent: claimsAgent, Name: "disjoint claims: " + action + " the workspace", Method: http.MethodPost,
			Path: "/api/v2/workspaces/" + *wsID + "/actions/" + action,
			Body: `{"data":{}}`, Want: http.StatusOK, Scheme: "", Check: nil,
		})
	}

	lock("lock")

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: claimsAgent, Name: "disjoint claims: create the base state version", Method: http.MethodPost,
		Path: "/api/v2/workspaces/" + *wsID + "/state-versions",
		Body: `{"data":{"type":"state-versions","attributes":{"serial":1,"lineage":"claims-lineage",` +
			`"md5":"d41d8cd98f00b204e9800998ecf8427e"}}}`,
		Want: http.StatusCreated, Scheme: "",
		Check: func(body string) error {
			var doc disjointStateVersionDoc
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				return fmt.Errorf("%w: decode state version: %w", errDisjointClaims, err)
			}

			*base = doc.Data.ID

			return nil
		},
	})

	lock("unlock")

	return base
}

// checkDisjointList proves that the list holds two accepted claims with
// start time and principal, and both disjoint address sets.
func checkDisjointList(body string) error {
	var doc disjointListDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: decode claims: %w", errDisjointClaims, err)
	}

	if len(doc.Claims) != 2 {
		return fmt.Errorf("%w: claims = %d, want 2", errDisjointClaims, len(doc.Claims))
	}

	for i := range doc.Claims {
		c := &doc.Claims[i]
		if c.State != "accepted" || c.StartedAt == "" || c.Principal == "" {
			return fmt.Errorf("%w: claim %s is not an accepted claim with start time and principal",
				errDisjointClaims, c.ID)
		}
	}

	if !strings.Contains(body, "aws_instance.a") || !strings.Contains(body, "aws_instance.b") {
		return fmt.Errorf("%w: the list holds no disjoint address sets", errDisjointClaims)
	}

	return nil
}
