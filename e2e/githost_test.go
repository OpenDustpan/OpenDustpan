//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// e2eHostConnection and e2eHostCheck decode the answers of the Git host routes.
type e2eHostConnection struct {
	ID         string `json:"id"`
	AppID      string `json:"appID"`
	APIAddress string `json:"apiAddress"`
	Default    bool   `json:"default"`
}

type e2eHostCheck struct {
	Account struct {
		ID     string   `json:"id"`
		Login  string   `json:"login"`
		Scopes []string `json:"scopes"`
	} `json:"account"`
	Missing map[string][]string `json:"missing"`
}

// TestGitHostConnections drives the Git host routes with the real server, on
// each store, against a fake GitHub API on a TLS test server. No real GitHub
// is called. The steps register the app as the operator, assign an installation
// to the namespace, make a connection, check it, and prove that a move of the
// installation by the operator stops the connection and that no answer holds a
// secret.
func TestGitHostConnections(t *testing.T) {
	t.Parallel()

	if e2e.UseBinaryServer() {
		t.Skip("the Git host steps run against the in-process server")
	}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runGitHostSteps(t, db)
		})
	}
}

func hostStep(ctx context.Context, t *testing.T, env *e2e.Env, method, path, body string, want int) string {
	t.Helper()

	status, got, err := env.ServerAPIStatus(ctx, method, path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	if status != want {
		t.Fatalf("%s %s = %d (%s), want %d", method, path, status, got, want)
	}

	return got
}

func runGitHostSteps(t *testing.T, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	fake := e2e.NewGitHostFake(t)
	env := e2e.NewEnvWithGitHost(ctx, t, store, fake)

	checkReady(ctx, t, env)

	const eventSecret = "e2e-event-secret-0123456789abcdef0123456789"

	app := "/api/dustpan/v1/git-host-apps/github"
	assign := "/api/dustpan/v1/git-host-installations/" + e2e.GitHostInstallationID
	conns := "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/git-host-connections"
	create := `{"kind":"github","auth":"app","installationID":"` + e2e.GitHostInstallationID + `","push":false}`

	keyJSON, err := json.Marshal(fake.PrivateKeyPEM())
	if err != nil {
		t.Fatalf("encode the key: %v", err)
	}

	appBody := `{"appID":"` + e2e.GitHostAppID + `","apiAddress":"` + fake.URL() + `","privateKey":` + string(keyJSON) +
		`,"eventSecret":"` + eventSecret + `"}`

	var answers []string

	keep := func(s string) string { answers = append(answers, s); return s }

	// Before the operator acts, a namespace administrator gets a conflict.
	hostStep(ctx, t, env, http.MethodPost, conns, create, http.StatusConflict)

	// The operator registers the app. A secret of 31 bytes and a bad address fail.
	hostStep(ctx, t, env, http.MethodPut, app, strings.Replace(appBody, eventSecret, "short", 1), http.StatusBadRequest)
	hostStep(ctx, t, env, http.MethodPut, app, strings.Replace(appBody, fake.URL(), "http://api.github.com", 1),
		http.StatusBadRequest)
	keep(hostStep(ctx, t, env, http.MethodPut, app, appBody, http.StatusCreated))

	// A namespace cannot make a connection to an installation that no one assigned.
	hostStep(ctx, t, env, http.MethodPost, conns, create, http.StatusBadRequest)
	keep(hostStep(ctx, t, env, http.MethodPut, assign, `{"namespace":"`+e2e.Namespace+`"}`, http.StatusCreated))

	// A request that holds a field that the operator owns is refused.
	hostStep(ctx, t, env, http.MethodPost, conns,
		`{"kind":"github","auth":"app","installationID":"`+e2e.GitHostInstallationID+`","apiAddress":"https://evil.example"}`,
		http.StatusBadRequest)

	var conn e2eHostConnection
	if err := json.Unmarshal([]byte(keep(hostStep(ctx, t, env, http.MethodPost, conns, create, http.StatusCreated))),
		&conn); err != nil {
		t.Fatalf("decode the connection: %v", err)
	}

	if !conn.Default || conn.AppID != e2e.GitHostAppID || conn.APIAddress != fake.URL() {
		t.Fatalf("connection = %+v", conn)
	}

	keep(hostStep(ctx, t, env, http.MethodGet, conns, "", http.StatusOK))
	keep(hostStep(ctx, t, env, http.MethodGet, conns+"/"+conn.ID, "", http.StatusOK))

	// Check calls the fake with a JWT that the fake verifies.
	var check e2eHostCheck
	if err := json.Unmarshal([]byte(keep(hostStep(ctx, t, env, http.MethodPost, conns+"/"+conn.ID+"/check", "",
		http.StatusOK))), &check); err != nil {
		t.Fatalf("decode the check: %v", err)
	}

	if check.Account.Login != "acme-org" || check.Account.ID != "99" || len(check.Account.Scopes) != 2 ||
		len(check.Missing) != 6 {
		t.Fatalf("check = %+v", check)
	}

	if fake.Calls() < 2 {
		t.Fatalf("the fake answered %d calls, want the read and the token", fake.Calls())
	}

	// The operator unassigns the installation: the connection stops at once.
	hostStep(ctx, t, env, http.MethodDelete, assign, "", http.StatusNoContent)
	hostStep(ctx, t, env, http.MethodPost, conns+"/"+conn.ID+"/check", "", http.StatusConflict)
	keep(hostStep(ctx, t, env, http.MethodPut, assign, `{"namespace":"`+e2e.Namespace+`"}`, http.StatusCreated))
	hostStep(ctx, t, env, http.MethodPost, conns+"/"+conn.ID+"/check", "", http.StatusOK)

	// Delete: the registration stays while an installation is assigned.
	hostStep(ctx, t, env, http.MethodDelete, conns+"/"+conn.ID, "", http.StatusNoContent)
	hostStep(ctx, t, env, http.MethodDelete, app, "", http.StatusConflict)
	hostStep(ctx, t, env, http.MethodDelete, assign, "", http.StatusNoContent)
	hostStep(ctx, t, env, http.MethodDelete, app, "", http.StatusNoContent)

	for _, a := range answers {
		if strings.Contains(a, eventSecret) || strings.Contains(a, "PRIVATE KEY") {
			t.Fatalf("an answer holds a secret: %s", a)
		}
	}
}
