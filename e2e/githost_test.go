//go:build e2e

package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// e2eHostConnection decodes a connection.
type e2eHostConnection struct {
	ID         string `json:"id"`
	AppID      string `json:"appID"`
	APIAddress string `json:"apiAddress"`
	Default    bool   `json:"default"`
}

// e2eHostAccount and e2eHostCheck decode a check.
type e2eHostAccount struct {
	ID     string   `json:"id"`
	Login  string   `json:"login"`
	Scopes []string `json:"scopes"`
}

type e2eHostCheck struct {
	Account e2eHostAccount      `json:"account"`
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

const e2eEventSecret = "e2e-event-secret-0123456789abcdef0123456789"

// hostScenario holds what the steps share: the server, the fake GitHub, the
// paths, and every answer, for the check that none holds a secret.
type hostScenario struct {
	env     *e2e.Env
	fake    *e2e.GitHostFake
	answers []string
	app     string
	assign  string
	conns   string
	create  string
}

// do sends one request as the bootstrap administrator and checks the status. A
// write of the registration or of an installation needs a console session with
// a recent sign-in, so it goes with the session of a site administrator.
func (s *hostScenario) do(t *testing.T, method, path, body string, want int) string {
	t.Helper()

	status, got := freshServerCall(t.Context(), t, s.env, principalStep{method, path, body, want})

	if status != want {
		t.Fatalf("%s %s = %d (%s), want %d", method, path, status, got, want)
	}

	s.answers = append(s.answers, got)

	return got
}

func runGitHostSteps(t *testing.T, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	fake := e2e.NewGitHostFake(t)
	env := e2e.NewEnvWithGitHost(ctx, t, store, fake)

	checkReady(ctx, t, env)

	s := &hostScenario{
		env: env, fake: fake, answers: nil, app: "/api/dustpan/v1/git-host-apps/github",
		assign: "/api/dustpan/v1/git-host-installations/" + e2e.GitHostInstallationID,
		conns:  "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/git-host-connections",
		create: `{"kind":"github","auth":"app","installationID":"` + e2e.GitHostInstallationID + `","push":false}`,
	}

	s.operatorRegisters(t)

	conn := s.makeConnection(t)

	s.check(t, conn.ID)
	s.operatorMoves(t, conn.ID)
	s.cleanUp(t, conn.ID)

	for _, a := range s.answers {
		if strings.Contains(a, e2eEventSecret) || strings.Contains(a, "PRIVATE KEY") {
			t.Fatalf("an answer holds a secret: %s", a)
		}
	}
}

// operatorRegisters registers the app. A namespace administrator gets a
// conflict before it. A short event secret and an address with the http scheme
// fail.
func (s *hostScenario) operatorRegisters(t *testing.T) {
	t.Helper()

	key, err := json.Marshal(s.fake.PrivateKeyPEM())
	if err != nil {
		t.Fatalf("encode the key: %v", err)
	}

	body := `{"appID":"` + e2e.GitHostAppID + `","apiAddress":"` + s.fake.URL() + `","privateKey":` + string(key) +
		`,"eventSecret":"` + e2eEventSecret + `"}`

	s.do(t, http.MethodPost, s.conns, s.create, http.StatusConflict)
	s.do(t, http.MethodPut, s.app, strings.Replace(body, e2eEventSecret, "short", 1), http.StatusBadRequest)
	s.do(t, http.MethodPut, s.app, strings.Replace(body, s.fake.URL(), "http://api.github.com", 1),
		http.StatusBadRequest)
	s.do(t, http.MethodPut, s.app, body, http.StatusCreated)
}

// makeConnection assigns the installation and makes the connection. A request
// that holds a field of the operator is refused.
func (s *hostScenario) makeConnection(t *testing.T) e2eHostConnection {
	t.Helper()

	s.do(t, http.MethodPost, s.conns, s.create, http.StatusConflict)
	s.do(t, http.MethodPut, s.assign, `{"namespace":"`+e2e.Namespace+`"}`, http.StatusCreated)
	s.do(t, http.MethodPost, s.conns,
		`{"kind":"github","auth":"app","installationID":"`+e2e.GitHostInstallationID+
			`","apiAddress":"https://evil.example"}`, http.StatusBadRequest)

	var conn e2eHostConnection

	created := s.do(t, http.MethodPost, s.conns, s.create, http.StatusCreated)
	if err := json.Unmarshal([]byte(created), &conn); err != nil {
		t.Fatalf("decode the connection: %v", err)
	}

	if !conn.Default || conn.AppID != e2e.GitHostAppID || conn.APIAddress != s.fake.URL() {
		t.Fatalf("connection = %+v", conn)
	}

	s.do(t, http.MethodGet, s.conns, "", http.StatusOK)
	s.do(t, http.MethodGet, s.conns+"/"+conn.ID, "", http.StatusOK)

	return conn
}

// check calls the fake with a JWT that the fake verifies.
func (s *hostScenario) check(t *testing.T, id string) {
	t.Helper()

	var check e2eHostCheck

	answer := s.do(t, http.MethodPost, s.conns+"/"+id+"/check", "", http.StatusOK)
	if err := json.Unmarshal([]byte(answer), &check); err != nil {
		t.Fatalf("decode the check: %v", err)
	}

	const consumers = 6

	if check.Account.Login != "acme-org" || check.Account.ID != "99" || len(check.Account.Scopes) != 2 ||
		len(check.Missing) != consumers {
		t.Fatalf("check = %+v", check)
	}

	// The connection reads files, so the Catalog misses nothing.
	if catalog, found := check.Missing["catalog"]; !found || len(catalog) != 0 {
		t.Fatalf("missing for the catalog = %v, %v; want an empty list", catalog, found)
	}

	if s.fake.Calls() < 2 {
		t.Fatalf("the fake answered %d calls, want the read and the token", s.fake.Calls())
	}
}

// operatorMoves ends the assignment: the connection stops at once, and it works
// again when the operator assigns the installation again.
func (s *hostScenario) operatorMoves(t *testing.T, id string) {
	t.Helper()

	s.do(t, http.MethodDelete, s.assign, "", http.StatusNoContent)
	s.do(t, http.MethodPost, s.conns+"/"+id+"/check", "", http.StatusConflict)
	s.do(t, http.MethodPut, s.assign, `{"namespace":"`+e2e.Namespace+`"}`, http.StatusCreated)
	s.do(t, http.MethodPost, s.conns+"/"+id+"/check", "", http.StatusOK)
}

// cleanUp deletes the connection. The registration stays while an installation
// is assigned.
func (s *hostScenario) cleanUp(t *testing.T, id string) {
	t.Helper()

	s.do(t, http.MethodDelete, s.conns+"/"+id, "", http.StatusNoContent)
	s.do(t, http.MethodDelete, s.app, "", http.StatusConflict)
	s.do(t, http.MethodDelete, s.assign, "", http.StatusNoContent)
	s.do(t, http.MethodDelete, s.app, "", http.StatusNoContent)
}
