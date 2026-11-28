//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/internal/agent"
)

// agentSecretsDoc is the part of a secret item answer that the steps read.
type agentSecretsDoc struct {
	Ref       string `json:"ref"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

// agentSecretsListDoc is the part of the secret item list that the steps read.
type agentSecretsListDoc struct {
	Secrets []agentSecretsDoc `json:"secrets"`
}

// TestAgentSecrets proves the routes of the model connection on a server of
// each store. No CLI calls them, so the steps call the routes with the token
// of an administrator, as the console does. They store a secret item, read
// the list without values, see 409 while a stored document refers to the
// item (the steps put the document into the store through the store port,
// because the write route is a later change), disconnect and delete the item.
// They also read the formats and the egress allowlist.
func TestAgentSecrets(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			agentSecretsScenario(t, env)
		})
	}
}

// agentCaller sends the requests of the steps with the token of the
// administrator.
type agentCaller struct {
	t   *testing.T
	env *e2e.Env
}

// call sends one request and returns its status and body.
func (c agentCaller) call(step, method, path, body string) (int, string) {
	c.t.Helper()

	status, answer, err := c.env.ServerAPIStatus(c.t.Context(), method, path, body)
	if err != nil {
		c.t.Fatalf("%s: %s %s: %v", step, method, path, err)
	}

	return status, answer
}

// want sends one request and returns its body. It fails the test when the
// status is not status.
func (c agentCaller) want(step, method, path, body string, status int) string {
	c.t.Helper()

	got, answer := c.call(step, method, path, body)
	if got != status {
		c.t.Fatalf("%s: %s %s answers %d (%s), want %d", step, method, path, got, answer, status)
	}

	return answer
}

// wantHas fails the test when body holds no fragment.
func (c agentCaller) wantHas(step, body, fragment string) {
	c.t.Helper()

	if !strings.Contains(body, fragment) {
		c.t.Fatalf("%s answers %s, want it to hold %s", step, body, fragment)
	}
}

// wantAbsent fails the test when body holds fragment.
func (c agentCaller) wantAbsent(step, body, fragment string) {
	c.t.Helper()

	if strings.Contains(body, fragment) {
		c.t.Fatalf("%s answers %s, want no %s", step, body, fragment)
	}
}

// agentSecretsScenario runs the steps of TestAgentSecrets.
func agentSecretsScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	caller := agentCaller{t: t, env: env}

	agentCheckFreshConnection(caller)
	ref := agentStoreAndList(caller)
	agentRefuseDeleteWhilePending(t, env, caller, ref)
	agentCheckConnectedDocument(caller, ref)
	agentDisconnectAndDelete(caller, ref)
	agentReplaceAllowlist(caller)
}

// agentCheckFreshConnection reads the connection and the formats of a new
// installation.
func agentCheckFreshConnection(caller agentCaller) {
	caller.t.Helper()

	body := caller.want(
		"read the connection of a new installation",
		http.MethodGet, "/api/agent/connection", "",
		http.StatusNotFound,
	)
	caller.wantHas(
		"read the connection of a new installation",
		body, "agent-connection.not-connected",
	)

	body = caller.want(
		"read the formats", http.MethodGet, "/api/agent/formats", "", http.StatusOK,
	)
	caller.wantHas("read the formats", body, `{"formats":`)
	caller.wantHas("read the formats", body, `"chat-completions"`)
	caller.wantHas("read the formats", body, `"messages"`)
}

// agentStoreAndList stores one secret item and reads the list back. It
// returns the reference of the item.
func agentStoreAndList(caller agentCaller) string {
	caller.t.Helper()

	body := caller.want(
		"store a secret item",
		http.MethodPost, "/api/agent/secrets",
		`{"name":"e2e-key","value":"e2e-agent-secret-value"}`,
		http.StatusCreated,
	)

	var stored agentSecretsDoc

	if err := json.Unmarshal([]byte(body), &stored); err != nil {
		caller.t.Fatalf("decode the stored secret item: %v: %s", err, body)
	}

	if !strings.HasPrefix(stored.Ref, "itm-") || stored.Name != "e2e-key" || stored.CreatedAt == "" {
		caller.t.Fatalf("stored secret item = %+v, want a reference, the name and a time", stored)
	}

	caller.wantAbsent("store a secret item", body, "e2e-agent-secret-value")

	body = caller.want(
		"read the secret items", http.MethodGet, "/api/agent/secrets", "", http.StatusOK,
	)

	var list agentSecretsListDoc

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		caller.t.Fatalf("decode the secret item list: %v: %s", err, body)
	}

	if len(list.Secrets) != 1 || list.Secrets[0].Ref != stored.Ref {
		caller.t.Fatalf("secret item list = %+v, want the one stored item", list)
	}

	caller.wantAbsent("read the secret items", body, "e2e-agent-secret-value")

	return stored.Ref
}

// agentSeedDocument is the pending document that the steps store through the
// store port. It names the secret item ref in the credential.
func agentSeedDocument(ref string) string {
	return fmt.Sprintf(`{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": %q },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "on" }
}`, ref)
}

// agentRefuseDeleteWhilePending stores the pending document through the store
// port and proves that the delete of the item it refers to answers 409.
func agentRefuseDeleteWhilePending(
	t *testing.T, env *e2e.Env, caller agentCaller, ref string,
) {
	t.Helper()

	spec, err := agent.ParseConnectionSpec([]byte(agentSeedDocument(ref)))
	if err != nil {
		t.Fatalf("parse the seed document: %v", err)
	}

	env.SeedAgentPendingConnection(t.Context(), t, spec)

	body := caller.want(
		"delete a secret item that a pending document refers to",
		http.MethodDelete, "/api/agent/secrets/"+ref, "",
		http.StatusConflict,
	)
	caller.wantHas(
		"delete a secret item that a pending document refers to",
		body, "agent-connection.secret-in-use",
	)
}

// agentCheckConnectedDocument promotes the pending document to the current
// document through the store port, proves that the delete still answers 409,
// and reads the connected document back.
func agentCheckConnectedDocument(caller agentCaller, ref string) {
	caller.t.Helper()

	caller.env.PromoteAgentPendingConnection(caller.t.Context(), caller.t)

	body := caller.want(
		"delete a secret item that the current document refers to",
		http.MethodDelete, "/api/agent/secrets/"+ref, "",
		http.StatusConflict,
	)
	caller.wantHas(
		"delete a secret item that the current document refers to",
		body, "agent-connection.secret-in-use",
	)

	body = caller.want(
		"read the connected document",
		http.MethodGet, "/api/agent/connection", "",
		http.StatusOK,
	)
	caller.wantHas("read the connected document", body, `"version":1`)
	caller.wantHas("read the connected document", body, ref)
}

// agentDisconnectAndDelete disconnects, proves that the connection is gone,
// and deletes the secret item.
func agentDisconnectAndDelete(caller agentCaller, ref string) {
	caller.t.Helper()

	caller.want(
		"disconnect", http.MethodDelete, "/api/agent/connection", "", http.StatusNoContent,
	)

	caller.want(
		"read the connection after the disconnect",
		http.MethodGet, "/api/agent/connection", "",
		http.StatusNotFound,
	)

	caller.want(
		"delete the secret item after the disconnect",
		http.MethodDelete, "/api/agent/secrets/"+ref, "",
		http.StatusNoContent,
	)

	body := caller.want(
		"read the secret items after the delete",
		http.MethodGet, "/api/agent/secrets", "",
		http.StatusOK,
	)
	if strings.TrimSpace(body) != `{"secrets":[]}` {
		caller.t.Fatalf("GET /api/agent/secrets answers %s, want an empty list", body)
	}
}

// agentReplaceAllowlist reads the egress allowlist of a new installation and
// replaces it.
func agentReplaceAllowlist(caller agentCaller) {
	caller.t.Helper()

	body := caller.want(
		"read the egress allowlist of a new installation",
		http.MethodGet, "/api/server/egress-allowlist", "",
		http.StatusOK,
	)
	if strings.TrimSpace(body) != `{"hosts":[],"cidrs":[]}` {
		caller.t.Fatalf("GET /api/server/egress-allowlist answers %s, want an empty list", body)
	}

	body = caller.want(
		"replace the egress allowlist",
		http.MethodPut, "/api/server/egress-allowlist",
		`{"hosts":["models.example.com"],"cidrs":[{"cidr":"10.0.0.0/8","port":443,"http":false}]}`,
		http.StatusOK,
	)
	caller.wantHas("replace the egress allowlist", body, `"models.example.com"`)
	caller.wantHas("replace the egress allowlist", body, `"10.0.0.0/8"`)
}
