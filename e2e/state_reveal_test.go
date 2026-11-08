//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestStateDownloadKindFollowsThePrincipal proves, with each OpenTofu version
// of the matrix and on each store, that a state download through the CLI
// writes one entry of the kind reveal for a person with a personal token and
// one entry of the kind tool-operation for a service token, each with the
// kind of its principal, and that the download answers Cache-Control:
// no-store.
func TestStateDownloadKindFollowsThePrincipal(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	for _, version := range e2e.OpenTofu().Versions() {
		cli, installErr := fetcher.Install(installCtx, e2e.OpenTofu(), version)
		if installErr != nil {
			t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, version, installErr)
		}

		for _, db := range databases {
			t.Run(version+"/"+string(db), func(t *testing.T) {
				t.Parallel()

				if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
					t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
				}

				runStateDownloadKindScenario(t, cli, db)
			})
		}
	}
}

// runStateDownloadKindScenario runs the download scenario on one store.
func runStateDownloadKindScenario(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)
	env.CreateNamespace(ctx, t, refusalNamespace)

	person := issueRefusalPerson(ctx, t, env, "ann", "admin")
	service := issueRefusalService(ctx, t, env, "ci-reader", "admin")

	writeRefusalTofuProject(t, env)

	for _, p := range []*refusalPerson{person, service} {
		env.WriteFile(t, filepath.Join(env.Home(), "tofurc-refuse-"+p.name), fmt.Sprintf(
			"credentials %q {\n  token = %q\n}\n", env.Host(), p.token.Secret,
		))
	}

	for _, args := range [][]string{{"init", "-input=false"}, {"apply", "-input=false", "-auto-approve"}} {
		if res := runRefusalCLI(t, env, cli, person, args...); res.ExitCode != 0 {
			t.Fatalf("tofu %s as the person exits %d:\n%s", args[0], res.ExitCode, res.Stderr)
		}
	}

	unit := "opentofu:" + refusalNamespace + "/web"

	for _, tc := range []struct {
		who  *refusalPerson
		want downloadWant
	}{
		{person, downloadWant{subject: e2e.SignInSubject("ann@example.com"), kind: "reveal", principalKind: "person"}},
		{service, downloadWant{subject: "ci-reader", kind: "tool-operation", principalKind: "service"}},
	} {
		before := countKindDownloads(readAuditEntries(ctx, t, env, refusalNamespace), unit, tc.want)

		if res := runRefusalCLI(t, env, cli, tc.who, "state", "pull"); res.ExitCode != 0 {
			t.Fatalf("tofu state pull as %s exits %d:\n%s", tc.who.name, res.ExitCode, res.Stderr)
		}

		entries := readAuditEntries(ctx, t, env, refusalNamespace)

		if after := countKindDownloads(entries, unit, tc.want); after <= before {
			t.Fatalf("state pull as %s left %d entries of the kind %s, want more than the %d before",
				tc.who.name, after, tc.want.kind, before)
		}

		checkDownloadPrincipalKind(t, entries, unit, tc.want)
	}

	checkDownloadNoStore(ctx, t, env, person, unit)

	runStateDownloadHTTPScenario(t, env, cli, person, service)
}

// runStateDownloadHTTPScenario proves the same for a project with a backend
// "http" block: a state pull of a person writes a reveal entry and one of a
// service token writes a tool-operation entry, each with its principal kind,
// the read answers Cache-Control: no-store, a plan with a token that holds
// unit:read and unit:read-secrets runs, and a viewer, who holds only
// unit:read, gets 403.
func runStateDownloadHTTPScenario(t *testing.T, env *e2e.Env, cli e2e.Install, person, service *refusalPerson) {
	t.Helper()

	ctx := t.Context()

	dir := filepath.Join(env.Home(), "tofu-reveal-http")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}

	path := "/tofu/state/" + refusalNamespace + "/web-http"
	writeTofuHTTPConfig(t, dir, env.URL+path)

	httpEnv := func(p *refusalPerson) []string {
		return []string{"TF_HTTP_USERNAME=dustpan", "TF_HTTP_PASSWORD=" + p.token.Secret}
	}

	run := func(p *refusalPerson, args ...string) {
		t.Helper()

		res, err := env.RunAs(ctx, cli, dir, httpEnv(p), args...)
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("tofu %s as %s over the http backend: exit %d, %v:\n%s",
				args[0], p.name, res.ExitCode, err, res.Stderr)
		}
	}

	run(person, "init", "-input=false", "-no-color")
	run(person, "apply", "-input=false", "-auto-approve", "-no-color")
	run(person, "plan", "-input=false", "-no-color")

	unit := "opentofu:" + refusalNamespace + "/web-http"

	for _, tc := range []struct {
		who  *refusalPerson
		want downloadWant
	}{
		{person, downloadWant{subject: e2e.SignInSubject("ann@example.com"), kind: "reveal", principalKind: "person"}},
		{service, downloadWant{subject: "ci-reader", kind: "tool-operation", principalKind: "service"}},
	} {
		before := countKindDownloads(readAuditEntries(ctx, t, env, refusalNamespace), unit, tc.want)

		run(tc.who, "state", "pull")

		entries := readAuditEntries(ctx, t, env, refusalNamespace)
		if after := countKindDownloads(entries, unit, tc.want); after <= before {
			t.Fatalf("http state pull as %s left %d entries of the kind %s, want more than the %d before",
				tc.who.name, after, tc.want.kind, before)
		}

		checkDownloadPrincipalKind(t, entries, unit, tc.want)
	}

	read, err := env.TFEStatusHeaders(ctx, person.token.Secret, http.MethodGet, path, "")
	if err != nil || read.Status != http.StatusOK {
		t.Fatalf("http backend read answers %d, %v: %s", read.Status, err, read.Body)
	}

	if got := read.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("http backend read Cache-Control = %q, want no-store", got)
	}

	viewer := issueRefusalPerson(ctx, t, env, "vi", "viewer")

	denied, err := env.TFEStatusHeaders(ctx, viewer.token.Secret, http.MethodGet, path, "")
	if err != nil || denied.Status != http.StatusForbidden || strings.Contains(denied.Body, "greeting") {
		t.Fatalf("http backend read by a viewer answers %d, %v: %s; want 403 with no state",
			denied.Status, err, denied.Body)
	}
}

// issueRefusalService makes a service principal with a role on the namespace
// and one service token, and returns it as a person-shaped record so the CLI
// helpers can use its token.
func issueRefusalService(ctx context.Context, t *testing.T, env *e2e.Env, name, role string) *refusalPerson {
	t.Helper()

	p := new(refusalPerson)
	p.name, p.role = name, role

	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		fmt.Sprintf(`{"subject":%q,"kind":"service"}`, name),
		http.StatusCreated,
	}, &p.principal)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + refusalNamespace + "/grants",
		`{"subject":"principal:` + p.principal.ID + `","role":"` + role + `"}`, http.StatusCreated,
	}, nil)

	var tok e2eToken

	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals/" + p.principal.ID + "/tokens",
		`{"name":"reader"}`,
		http.StatusCreated,
	}, &tok)

	p.token = e2e.PersonalToken{ID: tok.ID, Secret: tok.Secret}

	return p
}

// downloadWant names the download entries that a subject must leave.
type downloadWant struct {
	subject       string
	kind          string
	principalKind string
}

// countKindDownloads counts the entries of the wanted kind with the action
// unit:read-secrets and an empty value path that the wanted subject made on
// unit.
func countKindDownloads(entries []auditListItem, unit string, want downloadWant) int {
	count := 0

	for i := range entries {
		if e := &entries[i]; e.Kind == want.kind && e.Action == "unit:read-secrets" &&
			e.Object == unit && e.ValuePath == "" && e.Subject == want.subject {
			count++
		}
	}

	return count
}

// checkDownloadPrincipalKind fails the test unless each download entry of the
// wanted subject names the wanted principal kind, and none has a job ID.
func checkDownloadPrincipalKind(t *testing.T, entries []auditListItem, unit string, want downloadWant) {
	t.Helper()

	for i := range entries {
		e := &entries[i]
		if e.Kind != want.kind || e.Action != "unit:read-secrets" || e.Object != unit || e.Subject != want.subject {
			continue
		}

		if e.PrincipalKind != want.principalKind || e.JobID != "" {
			t.Fatalf("download entry %d of %s holds principal kind %q and job ID %q, want %q and none",
				e.Sequence, want.subject, e.PrincipalKind, e.JobID, want.principalKind)
		}
	}
}

// stateVersionDocument reads the ID of a state version document.
type stateVersionDocument struct {
	Data stateVersionData `json:"data"`
}

// stateVersionData is the data object of a state version document.
type stateVersionData struct {
	ID string `json:"id"`
}

// checkDownloadNoStore proves that the download route of a state version
// answers Cache-Control: no-store.
func checkDownloadNoStore(ctx context.Context, t *testing.T, env *e2e.Env, person *refusalPerson, unit string) {
	t.Helper()

	status, body, err := env.TFEStatus(
		ctx, person.token.Secret, http.MethodGet,
		"/api/v2/organizations/"+refusalNamespace+"/workspaces/web", "",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("read the workspace of %s answers %d, %v: %s", unit, status, err, body)
	}

	var doc lockDocument
	if err = json.Unmarshal([]byte(body), &doc); err != nil || doc.Data.ID == "" {
		t.Fatalf("workspace document %q has no ID: %v", body, err)
	}

	current, err := env.TFEStatusHeaders(
		ctx, person.token.Secret, http.MethodGet, "/api/v2/workspaces/"+doc.Data.ID+"/current-state-version", "",
	)
	if err != nil || current.Status != http.StatusOK {
		t.Fatalf("read the current state version answers %d, %v: %s", current.Status, err, current.Body)
	}

	var sv stateVersionDocument
	if err = json.Unmarshal([]byte(current.Body), &sv); err != nil || sv.Data.ID == "" {
		t.Fatalf("state version document %q has no ID: %v", current.Body, err)
	}

	download, err := env.TFEStatusHeaders(
		ctx, person.token.Secret, http.MethodGet, "/api/v2/state-versions/"+sv.Data.ID+"/download", "",
	)
	if err != nil || download.Status != http.StatusOK {
		t.Fatalf("download answers %d, %v: %s", download.Status, err, download.Body)
	}

	if got := download.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("download Cache-Control = %q, want no-store", got)
	}
}
