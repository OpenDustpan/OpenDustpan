//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// ipAllowlistsDocsPage is the guide that this step runs.
const ipAllowlistsDocsPage = "docs/guides/ip-allowlists.md"

// TestE2EIPAllowlistsDocsPage proves that the commands of the IP allowlist
// guide run as written. It reads the guide, checks the settings resource, turns
// on the installation list with a merge patch, and sends the units request
// from an address inside the list and from the default address outside it.
func TestE2EIPAllowlistsDocsPage(t *testing.T) {
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

			ipAllowlistsDocsScenario(t.Context(), t, env)
		})
	}
}

// ipAllowlistsDocsScenario runs the page's commands against env.
func ipAllowlistsDocsScenario(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ipAllowlistsDocsPageText(t)

	checkReady(ctx, t, env)

	// The page changes the list with a fresh sign-in. Make that person before
	// the read, so that a restart of the server binary cannot move the revision.
	env.PrepareFreshAdmin(ctx, t)

	resp, doc := settingsRead(ctx, t, env, settingsRoute)
	etag := resp.Header.Get("ETag")

	enabled := settingFor(t, doc, "ipAllowlist.enabled")
	if value, ok := enabled.Value.(bool); !ok || value {
		t.Fatalf("ipAllowlist.enabled = %+v, want the false default", enabled)
	}

	installation := settingFor(t, doc, "ipAllowlist.installation")
	if value, ok := installation.Value.(string); !ok || value != "" {
		t.Fatalf("ipAllowlist.installation = %+v, want the empty default", installation)
	}

	// The page's merge patch: both keys in one change.
	// A change that widens the installation list needs a fresh sign-in.
	status, answer := env.FreshAdminMergePatch(ctx, t, settingsRoute, etag,
		`{"ipAllowlist.enabled":true,"ipAllowlist.installation":"`+ipAllowlistSource+`/32"}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH the installation list answers %d (%s), want 200", status, answer)
	}

	// The default address is outside the list now, so decode the answer of the
	// patch instead of a new read.
	var changed settingsDoc
	if err := json.Unmarshal([]byte(answer), &changed); err != nil {
		t.Fatalf("decode the patch answer: %v", err)
	}

	if value, ok := settingFor(t, changed, "ipAllowlist.enabled").Value.(bool); !ok || !value {
		t.Fatalf("ipAllowlist.enabled after the patch = %+v, want true", settingFor(t, changed, "ipAllowlist.enabled"))
	}

	checkEffectiveInstallationList(ctx, t, env)

	// The units route answers a client inside the list. The default address
	// is outside the list: the same request is refused with the page's code
	// and scope.
	checkUnitsBehindInstallationList(ctx, t, env)
}

// checkEffectiveInstallationList reads the settings from a client inside the
// list, as the page does, and checks the effective value of the list.
func checkEffectiveInstallationList(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.ServerAPIStatusFrom(ctx, ipAllowlistSource, env.TokenA(),
		http.MethodGet, settingsRoute, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET the settings from %s answers %d, %v: %s", ipAllowlistSource, status, err, body)
	}

	var effective settingsDoc
	if decodeErr := json.Unmarshal([]byte(body), &effective); decodeErr != nil {
		t.Fatalf("decode the settings from %s: %v: %s", ipAllowlistSource, decodeErr, body)
	}

	if value, ok := settingFor(t, effective, "ipAllowlist.installation").Value.(string); !ok ||
		value != ipAllowlistSource+"/32" {
		t.Fatalf("ipAllowlist.installation = %+v, want %s/32",
			settingFor(t, effective, "ipAllowlist.installation"), ipAllowlistSource)
	}
}

// ipAllowlistsDocsPageText fails unless the page holds the keys, the codes and
// the cap that the commands use.
func ipAllowlistsDocsPageText(t *testing.T) {
	t.Helper()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	data, readErr := root.ReadFile(ipAllowlistsDocsPage)
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read %s: %v %v", ipAllowlistsDocsPage, readErr, closeErr)
	}

	page := string(data)

	for _, want := range []string{
		"ipAllowlist.enabled",
		"ipAllowlist.installation",
		"access.ip-not-allowed",
		"ip-allowlist.invalid-cidr",
		"at most 64 distinct entries",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("%s does not hold %q", ipAllowlistsDocsPage, want)
		}
	}
}
