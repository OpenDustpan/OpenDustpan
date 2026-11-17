package runnerpools

import (
	"maps"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagRunners is the tag of every operation of this adapter.
const tagRunners = "runners"

// operationLabels names the routes of this adapter that sit under
// apidesc.Prefix: the management API of pools, join tokens and runners. The
// key is the pattern of the route. The runner protocol is outside the prefix
// by design, so it has no entry. A route under the prefix with no entry stops
// the server at start.
func operationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, poolsPath): apidesc.Name("listRunnerPools", tagRunners).
			On(apidesc.KindNamespace).WithBodies(apidesc.NoBody, poolListSchema()),
		apidesc.Pattern(http.MethodPost, poolsPath): apidesc.Name("createRunnerPool", tagRunners).
			On(apidesc.KindNamespace).WithStatus(http.StatusCreated).WithBodies(poolCreateSchema(), poolSchema()),
		apidesc.Pattern(http.MethodGet, poolPath): apidesc.Name("getRunnerPool", tagRunners).
			On(apidesc.KindNamespace).WithBodies(apidesc.NoBody, poolSchema()),
		apidesc.Pattern(http.MethodPatch, poolPath): apidesc.Name("updateRunnerPool", tagRunners).
			On(apidesc.KindNamespace).WithBodies(poolPatchSchema(), poolSchema()),
		apidesc.Pattern(http.MethodDelete, poolPath): apidesc.Name("deleteRunnerPool", tagRunners).
			On(apidesc.KindNamespace).WithBodies(apidesc.NoBody, apidesc.NoBody),
		apidesc.Pattern(http.MethodGet, joinTokensPath): apidesc.Name("listRunnerJoinTokens", tagRunners).
			On(apidesc.KindNamespace).WithBodies(apidesc.NoBody, joinTokenListSchema()),
		apidesc.Pattern(http.MethodPost, joinTokensPath): apidesc.Name("createRunnerJoinToken", tagRunners).
			On(apidesc.KindNamespace).WithStatus(http.StatusCreated).
			WithBodies(joinTokenCreateSchema(), joinTokenCreatedSchema()),
		apidesc.Pattern(http.MethodPost, joinTokenPath): apidesc.Name("revokeRunnerJoinToken", tagRunners).
			On(apidesc.KindNamespace).WithBodies(apidesc.NoBody, apidesc.NoBody),
		apidesc.Pattern(http.MethodGet, poolRunnersPath): apidesc.Name("listPoolRunners", tagRunners).
			On(apidesc.KindNamespace).WithBodies(apidesc.NoBody, runnerListSchema()),
		apidesc.Pattern(http.MethodPost, drainPath): apidesc.Name("drainRunner", tagRunners).
			On(apidesc.KindNamespace).WithBodies(drainSchema(), apidesc.NoBody),
		apidesc.Pattern(http.MethodPost, retirePath): apidesc.Name("retireRunner", tagRunners).
			On(apidesc.KindNamespace).WithBodies(apidesc.NoBody, apidesc.NoBody),
	}
}

// The bodies of the management routes.

func poolSchema() apidesc.Body {
	return apidesc.Typed[poolJSON]("RunnerPool", apidesc.Named[gitCredentialsJSON]("RunnerGitCredentials"))
}

func poolListSchema() apidesc.Body {
	return apidesc.Typed[poolListJSON]("RunnerPoolList",
		apidesc.Named[poolJSON]("RunnerPool"),
		apidesc.Named[gitCredentialsJSON]("RunnerGitCredentials"))
}

func poolCreateSchema() apidesc.Body {
	return apidesc.Typed[poolRequest]("CreateRunnerPoolRequest",
		apidesc.Named[gitCredentialsJSON]("RunnerGitCredentials"))
}

func poolPatchSchema() apidesc.Body {
	return apidesc.Typed[poolPatch]("UpdateRunnerPoolRequest",
		apidesc.Named[gitCredentialsJSON]("RunnerGitCredentials"))
}

func joinTokenListSchema() apidesc.Body {
	return apidesc.Typed[joinTokenListJSON]("RunnerJoinTokenList", apidesc.Named[joinTokenJSON]("RunnerJoinToken"))
}

func joinTokenCreateSchema() apidesc.Body {
	return apidesc.Typed[createJoinTokenRequest]("CreateRunnerJoinTokenRequest")
}

func joinTokenCreatedSchema() apidesc.Body {
	return apidesc.Typed[createJoinTokenResponse]("CreatedRunnerJoinToken")
}

func runnerListSchema() apidesc.Body {
	return apidesc.Typed[runnerListJSON]("RunnerList", apidesc.Named[runnerJSON]("Runner"))
}

func drainSchema() apidesc.Body {
	return apidesc.Typed[drainRequest]("DrainRunnerRequest")
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix. No scope of
// this adapter is public: each scope needs a credential, so Public is false
// for every row.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		actions := make([]string, 0, len(route.Actions))
		for _, action := range route.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method:  route.Method,
			Path:    route.Path,
			Actions: actions,
			// Each action of a management route applies to the namespace
			// of the path.
			NamespaceActions: actions,
			Public:           false,
			IdempotencyKey:   false,
		})
	}

	labels := operationLabels()
	maps.Copy(labels, proxySettingsLabels())

	return apidesc.Collect(rows, labels)
}
