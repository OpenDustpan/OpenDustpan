package runnerapi_test

import (
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// renewPath returns the renew path of claim id on workspace ws.
func renewPath(ws, id string) string {
	return claimPath(ws) + "/" + id + "/renew"
}

// wrapperClaim registers a claim of the local wrapper and returns it.
func (w *claimWorld) wrapperClaim(t *testing.T, address string) claimDoc {
	t.Helper()

	status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), "token "+adminToken,
		map[string]any{"base": w.base, "addresses": []string{address}})
	if status != http.StatusCreated {
		t.Fatalf("register as local wrapper = %d %s, want 201", status, raw)
	}

	return decodeClaim(t, raw)
}

// listedClaim returns the claim id from the list.
func (w *claimWorld) listedClaim(t *testing.T, id string) claimDoc {
	t.Helper()

	status, raw := w.call(t, http.MethodGet, claimPath(w.wsID), "token "+adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d %s, want 200", status, raw)
	}

	listed := decodeClaimList(t, raw).Claims
	for i := range listed {
		if listed[i].ID == id {
			return listed[i]
		}
	}

	t.Errorf("the list misses claim %s", id)

	var none claimDoc

	return none
}

// TestClaimRenewExtendsTheLease proves that a renewal of the claim of the
// wrapper moves the end of the lease to one lease from the time of the
// renewal, records the time of the renewal, and shows both in the list.
func TestClaimRenewExtendsTheLease(t *testing.T) {
	t.Parallel()

	w := newClaimWorld(t, allowAllAuthorizer{})
	claim := w.wrapperClaim(t, "aws_instance.a")

	if claim.LeaseEndsAt == "" || claim.RenewedAt != "" {
		t.Fatalf("new claim lease = %q renewed = %q, want a lease and no renewal", claim.LeaseEndsAt, claim.RenewedAt)
	}

	w.clock.Add((workspace.ClaimLease / 2).Milliseconds())

	status, raw := w.call(t, http.MethodPost, renewPath(w.wsID, claim.ID), "token "+adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("renew = %d %s, want 200", status, raw)
	}

	renewed := decodeClaim(t, raw)
	listed := w.listedClaim(t, claim.ID)

	first, err := time.Parse(time.RFC3339Nano, claim.LeaseEndsAt)
	if err != nil {
		t.Fatalf("parse lease: %v", err)
	}

	next, err := time.Parse(time.RFC3339Nano, renewed.LeaseEndsAt)
	if err != nil {
		t.Fatalf("parse renewed lease: %v", err)
	}

	if got := next.Sub(first); got != workspace.ClaimLease/2 {
		t.Errorf("the lease moved by %s, want %s", got, workspace.ClaimLease/2)
	}

	if renewed.RenewedAt == "" || listed.RenewedAt != renewed.RenewedAt || listed.LeaseEndsAt != renewed.LeaseEndsAt {
		t.Errorf("renewed = %+v, listed = %+v, want the same lease and renewal time", renewed, listed)
	}
}

// renewEnv is a claim world with the test that drives it, for the cases of a
// hostile renew call.
type renewEnv struct {
	*claimWorld

	t *testing.T
}

// renewAs sends a renew call with the Authorization header auth.
func (e *renewEnv) renewAs(auth, ws, id string) (int, []byte) {
	return e.call(e.t, http.MethodPost, renewPath(ws, id), auth, nil)
}

// wrapperID registers a claim of the local wrapper and returns its ID.
func (e *renewEnv) wrapperID() string { return e.wrapperClaim(e.t, "aws_instance.a").ID }

// jobClaimID registers a claim of job-1 and returns its ID.
func (e *renewEnv) jobClaimID() string { return e.registerClaim(e.t, "job-1", "aws_instance.a").ID }

// renewCase is one hostile renew call: the authorizer of its world, the status
// that it gets, and the call.
type renewCase struct {
	name  string
	authz identity.Authorizer
	want  int
	send  func(e *renewEnv) (int, []byte)
}

// renewCases returns the hostile renew calls and the status of each.
func renewCases() []renewCase {
	const wrapper = "token " + adminToken

	allow := identity.Authorizer(allowAllAuthorizer{})

	return []renewCase{
		{"no credential", allow, http.StatusUnauthorized, func(e *renewEnv) (int, []byte) {
			return e.renewAs("", e.wsID, e.wrapperID())
		}},
		{"no credential on a missing workspace", allow, http.StatusUnauthorized, func(e *renewEnv) (int, []byte) {
			return e.renewAs("token wrong", "ws-missing", "clm-x")
		}},
		{"job token", allow, http.StatusNotFound, func(e *renewEnv) (int, []byte) {
			return e.renewAs(e.bearer("job-1"), e.wsID, e.jobClaimID())
		}},
		{"job token of another workspace", allow, http.StatusNotFound, func(e *renewEnv) (int, []byte) {
			return e.renewAs(e.otherWorkspaceToken(e.t), e.wsID, e.wrapperID())
		}},
		{"missing workspace", allow, http.StatusNotFound, func(e *renewEnv) (int, []byte) {
			return e.renewAs(wrapper, "ws-missing", "clm-x")
		}},
		{"deleted workspace", allow, http.StatusGone, func(e *renewEnv) (int, []byte) {
			id := e.wrapperID()
			e.deleted.deleted[workspace.ID(e.wsID)] = true
			delete(e.spaces.spaces, workspace.ID(e.wsID))

			return e.renewAs(wrapper, e.wsID, id)
		}},
		// A missing action answers as a workspace that does not exist: 404.
		{
			"without unit:claim-register",
			onlyAuthorizer{allowed: identity.ActionUnitRead},
			http.StatusNotFound,
			func(e *renewEnv) (int, []byte) { return e.renewAs(wrapper, e.wsID, "clm-unknown") },
		},
		{"unknown claim", allow, http.StatusNotFound, func(e *renewEnv) (int, []byte) {
			return e.renewAs(wrapper, e.wsID, "clm-unknown")
		}},
		// A claim of a job has no lease, even for the principal that registered it.
		{"claim of a job", allow, http.StatusUnprocessableEntity, func(e *renewEnv) (int, []byte) {
			return e.renewAs(wrapper, e.wsID, e.jobClaimID())
		}},
		{"lease that passed", allow, http.StatusConflict, func(e *renewEnv) (int, []byte) {
			id := e.wrapperID()
			e.clock.Add((workspace.ClaimLease + time.Second).Milliseconds())

			return e.renewAs(wrapper, e.wsID, id)
		}},
	}
}

// TestClaimRenewHostile sends hostile renew calls straight to the HTTP
// handler. Each one is refused with its status.
func TestClaimRenewHostile(t *testing.T) {
	t.Parallel()

	for _, tc := range renewCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, raw := tc.send(&renewEnv{t: t, claimWorld: newClaimWorld(t, tc.authz)})
			if status != tc.want {
				t.Fatalf("renew = %d %s, want %d", status, raw, tc.want)
			}
		})
	}
}

// TestClaimRenewLeavesTheLeaseOfOthers proves that a refused renewal does not
// move the lease: the claim of a job keeps its empty lease, and a lapsed lease
// stays as it was.
func TestClaimRenewLeavesTheLeaseOfOthers(t *testing.T) {
	t.Parallel()

	w := newClaimWorld(t, allowAllAuthorizer{})
	job := w.registerClaim(t, "job-1", "aws_instance.a")
	wrapper := w.wrapperClaim(t, "aws_instance.b")

	w.clock.Add((workspace.ClaimLease + time.Second).Milliseconds())

	for _, id := range []string{job.ID, wrapper.ID} {
		if status, raw := w.call(t, http.MethodPost, renewPath(w.wsID, id), "token "+adminToken, nil); status < 400 {
			t.Fatalf("renew of %s = %d %s, want a refusal", id, status, raw)
		}
	}

	if got := w.listedClaim(t, job.ID); got.LeaseEndsAt != "" || got.RenewedAt != "" {
		t.Errorf("job claim lease = %q renewed = %q, want none", got.LeaseEndsAt, got.RenewedAt)
	}

	if got := w.listedClaim(t, wrapper.ID); got.LeaseEndsAt != wrapper.LeaseEndsAt || got.RenewedAt != "" {
		t.Errorf(
			"wrapper claim lease = %q renewed = %q, want %q and none",
			got.LeaseEndsAt,
			got.RenewedAt,
			wrapper.LeaseEndsAt,
		)
	}
}
