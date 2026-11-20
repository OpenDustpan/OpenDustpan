//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// agentBlockAgent is the User-Agent of the audit agent block requests. The
// route coverage check counts requests that do not carry the harness agent.
const agentBlockAgent = "opendustpan-e2e-audit-agent-block"

var (
	errAuditEntriesEmpty = errors.New("audit entries are empty")
	errAuditChainInvalid = errors.New("audit chain is not valid")
)

// agentBlockEntry is the part of an audit entry that the steps read.
type agentBlockEntry struct {
	Action    string         `json:"action"`
	Object    string         `json:"object"`
	Channel   string         `json:"channel"`
	Agent     map[string]any `json:"agent"`
	RequestID string         `json:"requestID"`
}

// TestAuditAgentBlock starts a plan job on a server of each store and reads
// its audit entry through GET /api/audit/entries with an access token. The
// start of a person carries no agent data, so the entry has no agent block;
// the step proves the route, the token and the audit read action of the
// namespace on the same path that a later agent start reads through.
func TestAuditAgentBlock(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	version := e2e.Pulumi().Single()
	if version == "" {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), version)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, version, err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()
			runAuditAgentBlockOnStore(t, cli, store)
		})
	}
}

// runAuditAgentBlockOnStore runs the agent block steps on one store.
func runAuditAgentBlockOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	const unit = "agent-block-net"

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: agentBlockAgent, Name: "create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + unit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: agentBlockAgent, Name: "create the runner pool", Method: http.MethodPost,
		Path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/runner-pools",
		Body: `{"name":"agent-block","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token", Check: nil,
	})

	headers := map[string]string{"Idempotency-Key": "audit-agent-block-key-1"}
	body := fmt.Sprintf(`{"unit":{"tool":"opentofu","name":%q},"operation":"plan","pool":"agent-block",`+
		`"location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"e2e"}`, unit)

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: agentBlockAgent, Name: "start a plan job", Method: http.MethodPost,
		Path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/jobs",
		Body: body, Want: http.StatusCreated, Scheme: "token", Check: nil,
	}, headers)

	entries := listAuditEntriesForAgentBlock(ctx, t, env)

	var start *agentBlockEntry

	for i := range entries {
		if entries[i].Action == "unit:run-plan" {
			start = &entries[i]

			break
		}
	}

	if start == nil {
		t.Fatalf("audit entries hold no unit:run-plan entry: %+v", entries)
	}

	if start.Agent != nil {
		t.Fatalf("audit entry of a person start holds an agent block: %+v", start.Agent)
	}

	checkAuditChainForAgentBlock(ctx, t, env)
}

// listAuditEntriesForAgentBlock reads the audit entries of the scenario
// namespace with the access token of the environment.
func listAuditEntriesForAgentBlock(ctx context.Context, t *testing.T, env *e2e.Env) []agentBlockEntry {
	t.Helper()

	var got struct {
		Entries []agentBlockEntry `json:"entries"`
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: agentBlockAgent, Name: "list the audit entries", Method: http.MethodGet,
		Path: "/api/audit/entries?organization=" + e2e.Namespace,
		Body: "", Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				return fmt.Errorf("decode audit entries: %w", err)
			}

			if len(got.Entries) == 0 {
				return errAuditEntriesEmpty
			}

			return nil
		},
	})

	return got.Entries
}

// checkAuditChainForAgentBlock proves the chain of the scenario namespace
// checks valid after the job start.
func checkAuditChainForAgentBlock(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var got struct {
		Valid bool `json:"valid"`
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: agentBlockAgent, Name: "check the audit chain", Method: http.MethodGet,
		Path: "/api/audit/check?organization=" + e2e.Namespace,
		Body: "", Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				return fmt.Errorf("decode audit check: %w", err)
			}

			if !got.Valid {
				return fmt.Errorf("%w: %s", errAuditChainInvalid, body)
			}

			return nil
		},
	})
}
