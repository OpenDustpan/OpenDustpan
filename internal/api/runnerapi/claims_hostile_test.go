package runnerapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// onlyAuthorizer allows one action on every object and denies the others.
type onlyAuthorizer struct {
	allowed identity.Action
}

func (a onlyAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == a.allowed {
		return nil
	}

	return &identity.DeniedError{Subject: p.Subject, Action: action, Object: obj}
}

func (onlyAuthorizer) ScopeExact() bool { return false }

func (onlyAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	var none identity.Scope

	return none, nil
}

// failingAuthorizer fails every call with a failure that is not a denial.
type failingAuthorizer struct{}

func (failingAuthorizer) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return errAuthorizerDown
}

func (failingAuthorizer) ScopeExact() bool { return false }

func (failingAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	var none identity.Scope

	return none, errAuthorizerDown
}

// claimCount returns the count of claims that the list shows, read with the
// access token of the administrator.
func (w *claimWorld) claimCount(t *testing.T) int {
	t.Helper()

	status, raw := w.call(t, http.MethodGet, claimPath(w.wsID), "token "+adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d %s, want 200", status, raw)
	}

	return len(decodeClaimList(t, raw).Claims)
}

// otherWorkspaceToken returns the bearer credential of a live job token of
// job-1 whose unit names another workspace than the one of the world.
func (w *claimWorld) otherWorkspaceToken(t *testing.T) string {
	t.Helper()

	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	other := w.unit.WithUnitID("ws-another")
	w.tokens.matches[jobtoken.DigestOf(secret)] = liveTokenMatch(
		time.UnixMilli(1_700_000_000_000).UTC(),
		secret,
		"job-1",
		other,
	)

	return "Bearer " + secret.Reveal()
}

// TestClaimHostileTokens sends hostile job tokens straight to the HTTP
// handler: a token with no job, and a token that names another workspace.
// Each is refused and stores no claim.
func TestClaimHostileTokens(t *testing.T) {
	t.Parallel()

	t.Run("job token with no job ID", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-1"),
			map[string]any{"base": w.base, "addresses": []string{"aws_instance.a"}})
		if status != http.StatusForbidden {
			t.Fatalf("register with a job token and no job = %d %s, want 403", status, raw)
		}

		if n := w.claimCount(t); n != 0 {
			t.Fatalf("the list holds %d claims, want none", n)
		}
	})
}

// TestClaimHostileTokensOfOtherWorkspaces sends the job token of another
// workspace to both routes: each answers as for a workspace that does not
// exist, and stores no claim.
func TestClaimHostileTokensOfOtherWorkspaces(t *testing.T) {
	t.Parallel()

	t.Run("job token of another workspace registers", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.otherWorkspaceToken(t),
			registerBody(w, "job-1", "aws_instance.a"))
		if status != http.StatusNotFound {
			t.Fatalf("register with the token of another workspace = %d %s, want 404", status, raw)
		}

		if n := w.claimCount(t); n != 0 {
			t.Fatalf("the list holds %d claims, want none", n)
		}
	})

	t.Run("job token of another workspace lists", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, raw := w.call(t, http.MethodGet, claimPath(w.wsID), w.otherWorkspaceToken(t), nil)
		if status != http.StatusNotFound {
			t.Fatalf("list with the token of another workspace = %d %s, want 404", status, raw)
		}
	})
}

// TestClaimHostileAccessAndActions sends an access token that names a job, and
// callers without the action, straight to the HTTP handler.
func TestClaimHostileAccessAndActions(t *testing.T) {
	t.Parallel()

	t.Run("access token that names a job", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), "token "+adminToken,
			registerBody(w, "job-1", "aws_instance.a"))
		if status != http.StatusForbidden {
			t.Fatalf("register with an access token and a job = %d %s, want 403", status, raw)
		}

		if n := w.claimCount(t); n != 0 {
			t.Fatalf("the list holds %d claims, want none", n)
		}
	})

	t.Run("access token that names a job, without the action", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, onlyAuthorizer{allowed: identity.ActionUnitRead})

		// The action comes before the job: the caller learns nothing about the
		// workspace from a refusal of its job.
		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), "token "+adminToken,
			registerBody(w, "job-1", "aws_instance.a"))
		if status != http.StatusNotFound {
			t.Fatalf("register with an access token, a job and no action = %d %s, want 404", status, raw)
		}
	})

	t.Run("list without unit:read", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, onlyAuthorizer{allowed: identity.ActionUnitClaimRegister})

		status, raw := w.call(t, http.MethodGet, claimPath(w.wsID), w.bearer("job-1"), nil)
		if status != http.StatusNotFound {
			t.Fatalf("list without unit:read = %d %s, want 404", status, raw)
		}
	})

	t.Run("register without unit:claim-register", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, onlyAuthorizer{allowed: identity.ActionUnitRead})

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-1"),
			registerBody(w, "job-1", "aws_instance.a"))
		if status != http.StatusNotFound {
			t.Fatalf("register without unit:claim-register = %d %s, want 404", status, raw)
		}
	})
}

// deleteTheWorkspace soft-deletes the workspace of the world: the workspace
// store no longer finds it, and the deleted store reports it.
func (w *claimWorld) deleteTheWorkspace() {
	w.deleted.deleted[workspace.ID(w.wsID)] = true
	delete(w.spaces.spaces, workspace.ID(w.wsID))
}

// TestClaimDeletedWorkspaceAnswersByTheRightOfTheCaller proves that 410 is
// only for a caller with the action: a caller without it gets 404 for a deleted
// workspace, as for a missing one, on register, list and renew.
func TestClaimDeletedWorkspaceAnswersByTheRightOfTheCaller(t *testing.T) {
	t.Parallel()

	calls := []struct {
		name         string
		method, path func(w *claimWorld) string
		body         func(w *claimWorld) any
	}{
		{
			"register", func(*claimWorld) string { return http.MethodPost },
			func(w *claimWorld) string { return claimPath(w.wsID) },
			func(w *claimWorld) any {
				return map[string]any{"base": w.base, "addresses": []string{"aws_instance.a"}}
			},
		},
		{
			"list", func(*claimWorld) string { return http.MethodGet },
			func(w *claimWorld) string { return claimPath(w.wsID) }, func(*claimWorld) any { return nil },
		},
		{
			"renew", func(*claimWorld) string { return http.MethodPost },
			func(w *claimWorld) string { return renewPath(w.wsID, "clm-x") }, func(*claimWorld) any { return nil },
		},
	}

	for _, c := range calls {
		for _, tc := range []struct {
			name  string
			authz identity.Authorizer
			want  int
		}{
			{"caller with the action", allowAllAuthorizer{}, http.StatusGone},
			{"caller without the action", denyAllAuthorizer{}, http.StatusNotFound},
		} {
			t.Run(c.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				w := newClaimWorld(t, tc.authz)
				w.deleteTheWorkspace()

				status, raw := w.call(t, c.method(w), c.path(w), "token "+adminToken, c.body(w))
				if status != tc.want {
					t.Fatalf("%s on a deleted workspace = %d %s, want %d", c.name, status, raw, tc.want)
				}
			})
		}
	}
}

// TestClaimAuthorizerFailureIsNotADenial proves that an authorizer that fails
// is not answered as a workspace that does not exist: the caller gets a server
// error on each route, and the server stores no claim.
func TestClaimAuthorizerFailureIsNotADenial(t *testing.T) {
	t.Parallel()

	w := newClaimWorld(t, failingAuthorizer{})

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, claimPath(w.wsID), registerBody(w, "job-1", "aws_instance.a")},
		{http.MethodGet, claimPath(w.wsID), nil},
		{http.MethodPost, renewPath(w.wsID, "clm-x"), nil},
	} {
		auth := w.bearer("job-1")
		if call.path != claimPath(w.wsID) {
			auth = "token " + adminToken
		}

		status, raw := w.call(t, call.method, call.path, auth, call.body)
		if status < http.StatusInternalServerError {
			t.Errorf(
				"%s %s with a failing authorizer = %d %s, want a server error",
				call.method,
				call.path,
				status,
				raw,
			)
		}
	}
}

// TestClaimUnauthenticatedLearnsNothing proves that a caller with no valid
// credential gets the same 401 for a workspace that exists, one that does not
// and one that is deleted: the credential is checked before the workspace is
// read.
func TestClaimUnauthenticatedLearnsNothing(t *testing.T) {
	t.Parallel()

	w := newClaimWorld(t, allowAllAuthorizer{})
	w.deleted.deleted["ws-deleted"] = true

	for _, ws := range []string{w.wsID, "ws-missing", "ws-deleted"} {
		for _, auth := range []string{"", "Bearer not-a-job-token", "token not-an-access-token", "Basic abc"} {
			for _, method := range []string{http.MethodPost, http.MethodGet} {
				var body any
				if method == http.MethodPost {
					body = registerBody(w, "job-1", "aws_instance.a")
				}

				status, raw := w.call(t, method, claimPath(ws), auth, body)
				if status != http.StatusUnauthorized {
					t.Errorf("%s %s with %q = %d %s, want 401", method, ws, auth, status, raw)
				}
			}
		}
	}
}
