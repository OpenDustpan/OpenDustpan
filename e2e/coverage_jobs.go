//go:build e2e

package e2e

import (
	"log/slog"
	"net/http"
	"testing"

	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// coverageJobRoutes returns the jobs adapter's route table.
func coverageJobRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := jobsapi.NewHandler(jobsapi.Config{
		Namespaces:     namespaces,
		Workers:        nil,
		Jobs:           struct{ job.Store }{nil},
		Units:          nil,
		Artifacts:      struct{ job.ArtifactReader }{nil},
		Logs:           nil,
		Cursors:        nil,
		Starter:        struct{ job.Starter }{nil},
		Pools:          nil,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("jobs handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for i := range routes {
		out = append(out, RouteRef{Method: routes[i].Method, Path: routes[i].Path})
	}

	return out
}

// jobAllowEntries covers the routes that start, list and read a job, and read
// its log. No CLI calls
// them: the console and the clients of the API do. The harness steps of
// TestJobsAPI start a plan job, start it again with the same key, refuse
// starts that are not valid, and read a job, a job that the caller may not
// read and a job that does not exist through the API.
func jobAllowEntries() []AllowEntry {
	const (
		test = "TestJobsAPI"
		why  = "Console and client route: no CLI calls it. The harness steps of TestJobsAPI call it through the API."
	)

	return []AllowEntry{
		{Method: http.MethodPost, Path: "/api/dustpan/v1/namespaces/{namespace}/jobs", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/{namespace}/jobs", CoveringTest: test, Reason: why},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/logs/{phase}",
			CoveringTest: test, Reason: why,
		},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}",
			CoveringTest: test, Reason: why,
		},
	}
}
