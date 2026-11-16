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

// runnerPoolsDocsAgent is the User-Agent of the requests of the runner pools
// guide.
const runnerPoolsDocsAgent = "opendustpan-e2e-runner-pools-docs"

var errRunnerPoolsDocs = errors.New("runner pools guide")

// TestRunnerPoolsGuideCommands runs each command of docs/guides/runner-pools.md
// against a server of each store: make a pool, list the pools, make, list and
// revoke a join token, list the runners of a pool, drain a runner and retire it.
func TestRunnerPoolsGuideCommands(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			runnerPoolsGuideScenario(t.Context(), t, env)
		})
	}
}

func runnerPoolsGuideScenario(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	base := "/api/dustpan/v1/namespaces/" + e2e.Namespace
	pools := base + "/runner-pools"
	tokens := pools + "/jobs/join-tokens"

	step := func(name, method, path, body string, want int, check func(string) error) {
		env.APIStepA(ctx, t, e2e.APIStepSpec{
			UserAgent: runnerPoolsDocsAgent, Name: name, Method: method, Path: path, Body: body,
			Want: want, Scheme: "token", Check: check,
		})
	}

	var pool poolsGuideRunner

	step("make a pool", http.MethodPost, pools, `{"name":"jobs","purpose":"jobs"}`, http.StatusCreated,
		func(body string) error {
			if err := json.Unmarshal([]byte(body), &pool); err != nil {
				return fmt.Errorf("%w: pool answer %s: %w", errRunnerPoolsDocs, body, err)
			}

			if pool.ID == "" {
				return fmt.Errorf("%w: pool answer %s lacks the id", errRunnerPoolsDocs, body)
			}

			return poolsGuideBodyHas(`"name":"jobs"`)(body)
		})
	step("list the pools", http.MethodGet, pools, "", http.StatusOK, poolsGuideBodyHas(`"name":"jobs"`))

	var made poolsGuideToken

	step("make a join token", http.MethodPost, tokens, "", http.StatusCreated, made.check)
	step("list the join tokens", http.MethodGet, tokens, "", http.StatusOK, made.checkList)
	step("revoke a join token", http.MethodPost, tokens+"/"+made.ID+"/revoke", "", http.StatusNoContent, nil)

	// The guide drains and retires a runner that joined. The fake runner joins
	// the pool with its own token.
	env.JoinFakeRunner(ctx, t, e2e.Namespace, pool.ID, "docs-runner")

	var listed poolsGuideRunnerList

	step("list the runners of a pool", http.MethodGet, pools+"/jobs/runners", "", http.StatusOK, listed.check)
	step("drain a runner", http.MethodPost, base+"/runners/"+listed.first+"/drain", "", http.StatusNoContent, nil)
	step("retire a runner", http.MethodPost, base+"/runners/"+listed.first+"/retire", "", http.StatusNoContent, nil)
}

// poolsGuideRunner is the part of a runner that the guide steps read.
type poolsGuideRunner struct {
	ID string `json:"id"`
}

// poolsGuideRunnerList is the answer of the list of the runners of a pool.
type poolsGuideRunnerList struct {
	Runners []poolsGuideRunner `json:"runners"`
	first   string
}

func (l *poolsGuideRunnerList) check(body string) error {
	if err := json.Unmarshal([]byte(body), l); err != nil {
		return fmt.Errorf("%w: runner list %s: %w", errRunnerPoolsDocs, body, err)
	}

	if len(l.Runners) != 1 {
		return fmt.Errorf("%w: runner list %s, want 1 runner", errRunnerPoolsDocs, body)
	}

	l.first = l.Runners[0].ID

	for _, field := range []string{`"status"`, `"draining"`, `"keyIdentifier"`} {
		if !strings.Contains(body, field) {
			return fmt.Errorf("%w: runner list lacks %s: %s", errRunnerPoolsDocs, field, body)
		}
	}

	return nil
}

// poolsGuideToken is the part of a join token answer that the guide steps read.
type poolsGuideToken struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

func (m *poolsGuideToken) check(body string) error {
	if err := json.Unmarshal([]byte(body), m); err != nil {
		return fmt.Errorf("%w: token answer %s: %w", errRunnerPoolsDocs, body, err)
	}

	if m.ID == "" || m.Token == "" {
		return fmt.Errorf("%w: token answer %s lacks the id or the token", errRunnerPoolsDocs, body)
	}

	return poolsGuideBodyHas(`"command"`)(body)
}

func (m *poolsGuideToken) checkList(body string) error {
	if m.Token != "" && strings.Contains(body, m.Token) {
		return fmt.Errorf("%w: the token list shows a secret", errRunnerPoolsDocs)
	}

	return poolsGuideBodyHas(`"` + m.ID + `"`)(body)
}

// poolsGuideBodyHas answers a check that wants the text in the body.
func poolsGuideBodyHas(want string) func(string) error {
	return func(body string) error {
		if !strings.Contains(body, want) {
			return fmt.Errorf("%w: body %s, want %s", errRunnerPoolsDocs, body, want)
		}

		return nil
	}
}
