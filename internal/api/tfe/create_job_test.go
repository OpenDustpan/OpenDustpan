package tfe_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// jobCreateWorkspaces records the job that each Create gets in its context.
type jobCreateWorkspaces struct {
	stubWorkspaces

	mu   *sync.Mutex
	jobs *[]string
}

// Create keeps the job of ctx, then creates the workspace as the stub does.
func (w jobCreateWorkspaces) Create(
	ctx context.Context,
	ref workspace.Ref,
	p workspace.Placement,
) (workspace.Workspace, error) {
	w.mu.Lock()
	*w.jobs = append(*w.jobs, workspace.JobFrom(ctx))
	w.mu.Unlock()

	return w.stubWorkspaces.Create(ctx, ref, p)
}

// jobIDOnAccessToken authenticates the test token as an access token whose
// principal names a job. That is no job token, so it passes no job.
type jobIDOnAccessToken struct{}

func (jobIDOnAccessToken) AuthenticateToken(ctx context.Context, raw string) (identity.Principal, error) {
	p, err := tokenAuthenticator{}.AuthenticateToken(ctx, raw)
	if err != nil {
		return identity.Principal{}, err
	}

	p.JobID = "job-1"

	return p, nil
}

// TestCreateWorkspacePassesTheJobOfAJobToken proves that the TFE create of a
// workspace hands the job of a job token to the workspace service, as the
// OpenTofu HTTP backend does, so a manual lock of the namespace applies to the
// create with the same job rule on both APIs. An access token passes no job,
// also when its principal names one.
func TestCreateWorkspacePassesTheJobOfAJobToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		auth identity.Authenticator
		want string
	}{
		{name: "job token", auth: jobTokenAuthenticator{}, want: "job-1"},
		{name: "access token that names a job", auth: jobIDOnAccessToken{}, want: ""},
		{name: "access token", auth: tokenAuthenticator{}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var jobs []string

			authz := selectiveAuthorizer{deny: map[identity.Action]bool{}}
			workspaces := jobCreateWorkspaces{stubWorkspaces: stubWorkspaces{}, mu: &sync.Mutex{}, jobs: &jobs}

			handler, err := tfe.NewHandler(tfe.Config{
				Registry:       nil,
				Authenticator:  tt.auth,
				Authorizer:     authz,
				AddressGate:    nil,
				Logger:         slog.New(slog.DiscardHandler),
				Namespaces:     newTestNamespaces(t),
				Workspaces:     workspaces,
				OutputChecker:  newTestOutputChecker(t, authz, workspaces),
				BaseURL:        baseurl.Resolver{},
				Audit:          newTestAudit(),
				Gate:           nil,
				MaxStateBytes:  0,
				OnAuditFailure: nil,
				DenialLimiter:  nil,
				Sessions:       nil,
				SessionPolicy:  nil,
				StateGuard:     stategate.Off("the test keeps no gates"),
				Placer:         testPlacer(),
			})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, workspaceRequest(t, http.MethodPost, "/api/v2/organizations/acme/workspaces",
				createBody("fresh")))

			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
			}

			if len(jobs) != 1 || jobs[0] != tt.want {
				t.Fatalf("jobs of the create = %q, want [%q]", jobs, tt.want)
			}
		})
	}
}
