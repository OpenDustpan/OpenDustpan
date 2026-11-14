//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
)

// gitHostAllowEntries covers the routes of the Git host connections. No CLI
// calls them: the console and the clients of the API do. The steps of
// TestGitHostConnections call each of them as the operator and as a namespace
// administrator.
func gitHostAllowEntries() []AllowEntry {
	const (
		app  = "/api/dustpan/v1/git-host-apps/{kind}"
		inst = "/api/dustpan/v1/git-host-installations"
		conn = "/api/dustpan/v1/namespaces/{namespace}/git-host-connections"
		test = "TestGitHostConnections"
		why  = "Console and client route: no CLI calls it. The steps of TestGitHostConnections call it."
	)

	return []AllowEntry{
		{Method: http.MethodGet, Path: app, CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: app, CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: app, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: inst, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: inst + "/{installation_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: inst + "/{installation_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: inst + "/{installation_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: conn, CoveringTest: test, Reason: why},
		{Method: http.MethodPost, Path: conn, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: conn + "/{id}", CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: conn + "/{id}", CoveringTest: test, Reason: why},
		{Method: http.MethodPost, Path: conn + "/{id}/make-default", CoveringTest: test, Reason: why},
		{Method: http.MethodPost, Path: conn + "/{id}/check", CoveringTest: test, Reason: why},
	}
}

// coverageGitHost returns a Git host service for the route table. Its parts
// are empty shells: they exist only to build a Handler.
func coverageGitHost(tb testing.TB) *forge.Service {
	tb.Helper()

	svc, err := forge.NewService(forge.Config{
		Store: struct{ forge.Store }{nil}, Namespaces: coverageNamespaceStore{},
		Secrets: struct{ forge.SecretSealer }{nil}, Audit: struct{ audit.Store }{nil},
		Openers:  []forge.Opener{coverageOpener{Opener: nil}},
		CheckURL: func(string) error { return nil }, OnAuditFailure: nil, OnRefused: nil, CheckLimits: nil,
		Logger: nil, Now: nil,
	})
	if err != nil {
		tb.Fatalf("Git host service: %v", err)
	}

	return svc
}

// coverageOpener is an Opener that only names its kind.
type coverageOpener struct{ forge.Opener }

func (coverageOpener) Kind() forge.Kind { return forge.KindGitHub }
