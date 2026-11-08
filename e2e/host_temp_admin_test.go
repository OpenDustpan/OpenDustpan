//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// e2eHostPrincipal decodes one principal of the installation API with the
// banner flag of a working temporary administrator.
type e2eHostPrincipal struct {
	ID                     string  `json:"id"`
	Subject                string  `json:"subject"`
	TemporaryAdministrator bool    `json:"temporaryAdministrator"`
	TemporaryExpiresAt     *string `json:"temporaryExpiresAt"`
}

// e2eHostPrincipals decodes one page of the principal list.
type e2eHostPrincipals struct {
	Principals []e2eHostPrincipal `json:"principals"`
}

// e2eHostUser decodes GET /api/user with the banner flag and its expiry.
type e2eHostUser struct {
	GithubLogin            string  `json:"githubLogin"`
	TemporaryAdministrator bool    `json:"temporaryAdministrator"`
	ExpiresAt              *string `json:"expiresAt"`
}

// TestHostTempAdmin runs the host command against the database of the
// environment and signs in as the temporary administrator it makes. It
// proves the fixed expiry, the banner flag on both APIs, the
// site-administrator grant, and exactly one sign-in audit entry for each
// use. In the in-process mode it builds the server binary first, so the
// step runs under the default suite too.
func TestHostTempAdmin(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
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

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runHostTempAdminOnStore(t, cli, db)
		})
	}
}

// runHostTempAdminOnStore runs the host recovery step on one store.
func runHostTempAdminOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkReady(ctx, t, env)

	const person = "recovery-alice"

	if store == e2e.StoreSQLite && !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary: the host command runs only while the server is stopped")
	}

	if store == e2e.StoreSQLite {
		env.StopServer(ctx, t)
	}

	token, expires := runHostBinary(ctx, t, env, person)

	if store == e2e.StoreSQLite {
		env.StartServer(ctx, t)
	}

	session := hostSession{token: token, person: person, expires: expires}

	checkHostUser(ctx, t, env, session)
	checkHostPrincipal(ctx, t, env, session)
	checkHostSignIns(ctx, t, env, session)
}

// hostSession is one recovery: the token, the person and the fixed expiry.
type hostSession struct {
	token   string
	person  string
	expires time.Time
}

// runHostBinary runs the host command against the database of env and
// answers the token with its fixed expiry.
func runHostBinary(ctx context.Context, t *testing.T, env *e2e.Env, person string) (string, time.Time) {
	t.Helper()

	res := env.RunBinary(
		ctx, t, "host", "create-temp-admin", "--reason", "e2e recovery", "--database", env.DatabaseURL(t), person,
	)
	if res.ExitCode != 0 {
		t.Fatalf("host create-temp-admin exits %d (%s %s)", res.ExitCode, res.Stdout, res.Stderr)
	}

	token, expires := parseHostOutput(t, res.Stdout)

	if got := time.Until(expires); got <= 23*time.Hour || got > 25*time.Hour {
		t.Fatalf("host expiry is in %s, want about 24h", got)
	}

	return token, expires
}

// checkHostUser proves the token signs in: the user endpoint answers the
// banner flag with its fixed expiry. The user endpoint reads the console
// token scheme.
func checkHostUser(ctx context.Context, t *testing.T, env *e2e.Env, session hostSession) {
	t.Helper()

	user := callHostUser(ctx, t, env, session.token)
	if user.GithubLogin != session.person || !user.TemporaryAdministrator {
		t.Fatalf("user = %+v, want %s with the banner flag", user, session.person)
	}

	if user.ExpiresAt == nil {
		t.Fatal("user has no expiry, want the fixed expiry for the banner")
	}

	at, err := time.Parse(time.RFC3339Nano, *user.ExpiresAt)
	if err != nil || !at.Equal(session.expires) {
		t.Fatalf("user expiry = %v, %v, want the printed %s", user.ExpiresAt, err, session.expires)
	}
}

// checkHostPrincipal proves the principal list shows the flag and the
// fixed expiry. The grant makes the token a site administrator: it lists
// principals.
func checkHostPrincipal(ctx context.Context, t *testing.T, env *e2e.Env, session hostSession) {
	t.Helper()

	for _, p := range callHostPrincipals(ctx, t, env, session.token).Principals {
		if p.Subject != session.person {
			continue
		}

		if !p.TemporaryAdministrator || p.TemporaryExpiresAt == nil {
			t.Fatalf("principal = %+v, want the banner flag with its expiry", p)
		}

		at, err := time.Parse(time.RFC3339Nano, *p.TemporaryExpiresAt)
		if err != nil || !at.Equal(session.expires) {
			t.Fatalf(
				"principal expiry = %v, %v, want the printed %s",
				p.TemporaryExpiresAt,
				err,
				session.expires,
			)
		}

		return
	}

	t.Fatalf("no principal names %q", session.person)
}

// checkHostSignIns proves each use wrote exactly one sign-in entry tracing
// to the named person, with the client address and the request ID of its
// request: the user call, the principal list, and this audit read.
func checkHostSignIns(ctx context.Context, t *testing.T, env *e2e.Env, session hostSession) {
	t.Helper()

	if n := countHostSignIns(ctx, t, env, session.token, session.person); n != hostUses {
		t.Fatalf("sign-in entries = %d, want exactly %d, one for each use", n, hostUses)
	}
}

// hostUses is the count of uses before the audit read returns: the user
// call, the principal list, and the audit read itself.
const hostUses = 3

// parseHostOutput reads the token and the expiry of host output.
func parseHostOutput(t *testing.T, out string) (string, time.Time) {
	t.Helper()

	var token string

	var expires time.Time

	for line := range strings.Lines(out) {
		name, value, _ := strings.Cut(line, ":")

		switch strings.TrimSpace(name) {
		case "token":
			token = strings.TrimSpace(value)
		case "expires":
			at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
			if err != nil {
				t.Fatalf("parse host expiry %q: %v", value, err)
			}

			expires = at
		default:
			continue
		}
	}

	if token == "" || expires.IsZero() {
		t.Fatalf("host output = %q, want token and expires lines", out)
	}

	return token, expires
}

// callHostUser signs in with token at GET /api/user.
func callHostUser(ctx context.Context, t *testing.T, env *e2e.Env, token string) e2eHostUser {
	t.Helper()

	got, err := env.StatusWithHeader(ctx, "token "+token, http.MethodGet, "/api/user", "")
	if err != nil || got.Status != http.StatusOK {
		t.Fatalf("GET /api/user = %d, %v (%s); want 200", got.Status, err, got.Body)
	}

	var user e2eHostUser

	if err := json.Unmarshal([]byte(got.Body), &user); err != nil {
		t.Fatalf("decode user: %v (%s)", err, got.Body)
	}

	return user
}

// callHostPrincipals signs in with token at the principal list.
func callHostPrincipals(ctx context.Context, t *testing.T, env *e2e.Env, token string) e2eHostPrincipals {
	t.Helper()

	status, body, err := env.ServerAPIStatusAs(ctx, token, http.MethodGet, "/api/server/principals", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET /api/server/principals = %d, %v (%s); want 200", status, err, body)
	}

	var list e2eHostPrincipals

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode principals: %v (%s)", err, body)
	}

	return list
}

// e2eHostEntry decodes one audit entry with its action, subject, client
// address and request ID.
type e2eHostEntry struct {
	Subject   string `json:"subject"`
	Action    string `json:"action"`
	Object    string `json:"object"`
	SourceIP  string `json:"sourceIP"`
	RequestID string `json:"requestID"`
}

// e2eHostEntries decodes one page of the audit list.
type e2eHostEntries struct {
	Entries []e2eHostEntry `json:"entries"`
}

// countHostSignIns counts the sign-in entries of the life cycle action for
// person: the host create, each use, and no expiry yet. Each entry names
// the client address and the request ID of its request. The audit API reads
// the console token scheme.
func countHostSignIns(ctx context.Context, t *testing.T, env *e2e.Env, token, person string) int {
	t.Helper()

	got, err := env.StatusWithHeader(ctx, "token "+token, http.MethodGet, "/api/audit/entries?organization=@server", "")
	if err != nil || got.Status != http.StatusOK {
		t.Fatalf("GET audit = %d, %v (%s); want 200", got.Status, err, got.Body)
	}

	var list e2eHostEntries

	if err := json.Unmarshal([]byte(got.Body), &list); err != nil {
		t.Fatalf("decode audit entries: %v (%s)", err, got.Body)
	}

	n := 0

	for _, e := range list.Entries {
		if e.Action == "server:manage-temporary-administrators" && e.Subject == person {
			if e.SourceIP == "" || e.RequestID == "" {
				t.Fatalf("sign-in entry = %+v, want the client address and the request ID", e)
			}

			n++
		}
	}

	return n
}
