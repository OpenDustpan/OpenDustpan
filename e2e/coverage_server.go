//go:build e2e

package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// coverageMetrics writes nothing: the route table needs a metrics writer,
// and the coverage gate never serves a request.
type coverageMetrics struct{}

func (coverageMetrics) Write(_ io.Writer) error {
	return nil
}

// coverageSchema reports no applied migration: the route table needs a
// schema reporter, and the coverage gate never serves a request.
type coverageSchema struct{}

func (coverageSchema) AppliedMigrations(_ context.Context) ([]schema.AppliedMigration, error) {
	return nil, nil
}

// coverageServerRoutes returns the installation adapter's route table: the
// namespace route (ADR 39), the backup routes (ADR 30), the restore routes
// (ADR 38), the metrics route (ADR 32), the maintenance routes (ADR 38) and
// the check routes (ADR 39 decisions 3): backup check and upgrade check.
func coverageServerRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	handler, err := serverapi.NewHandler(&serverapi.Config{
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          coverageGitHost(tb),
		Idempotency:      nil,
		Instances:        nil,
		Workers:          nil,
		Namespaces:       coverageNamespaces(tb),
		Backups:          nil,
		Restores:         nil,
		Maintenance:      coverageMaintenance{},
		Metrics:          coverageMetrics{},
		TempAdmins:       nil,
		Version:          "test",
		Schema:           coverageSchema{},
		Principals:       nil,
		Access:           nil,
		Reach:            coverageReach(),
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		IPAllowlists:     coverageIPAllowlists(tb),
		Gates:            coverageGateConfig{},
		SessionPolicy:    coverageSessionPolicies(tb),
		SignIn:           nil,
		CheckpointEpochs: nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		StoreChecks:      coverageStoreChecks(tb),
		Cursors:          nil,
		Now:              nil,
		Authenticator:    coverageAuthenticator{},
		Authorizer:       coverageAuthorizer{},
		Logger:           slog.New(slog.DiscardHandler),
		Journal:          nil,
		Tokens:           nil,
		Audit:            coverageAuditStore{},
		OnAuditFailure:   nil,
		DenialLimiter:    nil,
	})
	if err != nil {
		tb.Fatalf("server handler: %v", err)
	}

	return append(serverRefs(handler.Routes()), coverageSignInRoutes(tb)...)
}

// coverageReach returns the reach config of the group routes of the route
// table. The coverage gate reads the route table only, and a zero reach config
// makes a group read fail closed.
func coverageReach() access.ReachConfig {
	return access.ReachConfig{
		Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
	}
}

// serverRefs formats the installation table as method and path pairs.
func serverRefs(routes []serverapi.Route) []RouteRef {
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// apiDescriptionAllowEntries covers the route of the description of the API.
// No CLI reads it: the generators of the SDKs and the clients do. The steps
// of TestAPIDescription read it with no credential, with a wrong credential
// and with the token.
func apiDescriptionAllowEntries() []AllowEntry {
	return []AllowEntry{{
		Method: http.MethodGet, Path: "/api/dustpan/v1/openapi.json", CoveringTest: "TestAPIDescription",
		Reason: "Client route: no CLI reads the description of the API. The harness steps of " +
			"TestAPIDescription read it with and without a credential and check one known path.",
	}}
}

// consoleAllowEntries returns the allowlist rows for console-only routes and
// routes the CLI never calls.
func consoleAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/{namespace}/units",
			CoveringTest: "TestListUnitsBothTools",
			Reason:       "Console-only: no CLI reads the cross-tool unit list.",
		},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/-/units/{unitID}",
			CoveringTest: "TestCLIs (checkInventoryUnitByID)",
			Reason: "Console-only: no CLI reads a unit by its ID. The harness " +
				"steps read a stack and a workspace by ID.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/workspaces/{id}",
			CoveringTest: "TestReadWorkspaceByID",
			Reason:       "Console-only: the CLI reads workspaces by name, never by ID.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/state-version-outputs/{output_id}",
			CoveringTest: "TestTofuSeededStateRead",
			Reason: "The CLI reads one output only for a sensitive value, so the " +
				"main scenario without one never reaches it. The seeded e2e run " +
				"proves it, but it writes no matrix report.",
		},
		{
			Method: http.MethodPost, Path: "/api/stacks/{namespace}/{project}/{stack}/batch-encrypt",
			CoveringTest: "TestSecretsBatchRoundTrip",
			Reason: "The CLI encrypts one value per call through the single " +
				"encrypt route and decrypts in batches, so no matrix version " +
				"calls batch encrypt.",
		},
		{
			Method: http.MethodPost, Path: "/api/stacks/{namespace}/{project}/{stack}/update/{update}/renew_lease",
			CoveringTest: "TestRenewLeaseEchoesToken",
			Reason:       "Fast e2e updates finish before the CLI renews its lease.",
		},
		{
			Method: http.MethodPost, Path: "/api/stacks/{namespace}/{project}/{stack}/restore/{version}",
			CoveringTest: "TestCLIs (pulumiRestoreScenario)",
			Reason: "Operator and console route (ADR 28): the CLI never restores. " +
				"The harness step restores version 1 and checks it with stack export.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/state-versions",
			CoveringTest: "TestCLIs (tofuStateVersionListScenario)",
			Reason: "Operator and console route (ADR 28): the CLI never lists " +
				"state versions. The harness step lists two versions after " +
				"two applies.",
		},
		{
			Method: http.MethodPost, Path: "/api/v2/state-versions/{state_version_id}/actions/restore",
			CoveringTest: "TestCLIs (tofuRestoreScenario)",
			Reason: "Operator and console route (ADR 28): the CLI never restores. " +
				"The harness step restores a version and checks it with state pull.",
		},
	}
}

// operatorAllowEntries returns the operator routes and probes that no CLI
// calls.
func operatorAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/server/metrics",
			CoveringTest: "TestBothToolsSameServer (checkServerMetrics)",
			Reason:       "Operator route (ADR 32): no CLI calls it.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/components",
			CoveringTest: "TestBothToolsSameServer (checkServerComponents)",
			Reason:       "Operator route: no CLI calls it.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/worker",
			CoveringTest: "TestBothToolsSameServer (checkServerComponents)",
			Reason:       "Operator route: no CLI calls it.",
		},
		{
			Method: http.MethodPost, Path: "/api/server/audit/checkpoint-epochs",
			CoveringTest: "TestNewCheckpointEpochIsAChange",
			Reason: "Operator route: no CLI calls it. An administrator starts a new " +
				"checkpoint epoch after a restore or a failed check of the signed checkpoints.",
		},
		{
			Method: http.MethodGet, Path: "/health/live",
			CoveringTest: "TestHealthLiveStillChecksNothing",
			Reason:       "Operator probe: no CLI calls it.",
		},
		{
			Method: http.MethodGet, Path: "/health/ready",
			CoveringTest: "TestHealthReadyOK",
			Reason:       "Operator probe: no CLI calls it.",
		},
	}
}
