//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// readingStateDocsPage is the guide that this step runs.
const readingStateDocsPage = "docs/guides/reading-state-contents.md"

// TestReadingStateGuideCommands proves that the commands of the reading state
// contents guide run as written: an OpenTofu apply stores a sensitive value,
// then each read of the guide runs with an access token, and each error code
// that the guide names is the code of the answer.
func TestReadingStateGuideCommands(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), e2e.OpenTofu().Single())
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, e2e.OpenTofu().Single(), err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			env := e2e.NewEnvWithSignIn(t.Context(), t, tofuCLI, db)

			readingStateDocsScenario(t.Context(), t, env)
		})
	}
}

// readingStateDocsScenario runs the commands of the guide against env.
func readingStateDocsScenario(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	raw, err := os.ReadFile("../" + readingStateDocsPage)
	if err != nil {
		t.Fatalf("read the guide: %v", err)
	}

	page := string(raw)

	for _, want := range []string{
		"/api/dustpan/v1/units/",
		"%2F",
		"?version=",
		"?cursor=",
		"state.bad-path",
		"state-version.not-found",
		"unit.forbidden",
		"state.read-timeout",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the guide lacks %q", want)
		}
	}

	dir := t.TempDir()
	writeImportConfig(t, dir, maskedCloudConfig(env.Host(), readingStateWorkspace))

	env.StepIn(ctx, t, dir, step("reading docs init", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, dir, e2e.StepSpec{
		Check:  nil,
		Name:   "reading docs apply",
		Args:   []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		Env:    maskedApplyEnv(maskedSecretV1),
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	ref := "opentofu:" + e2e.Namespace + "/" + readingStateWorkspace
	versionID := maskedCurrentVersion(ctx, t, env, readingStateWorkspace)

	for _, read := range []readingRead{
		{
			name: "guide: read the current version", path: readingStatePath(ref, ""),
			want: http.StatusOK, check: checkReadingPage(ref, maskedSecretV1),
		},
		{
			name: "guide: read one version",
			path: readingStatePath(ref, apidesc.UnitsStateVersion+"="+url.QueryEscape(versionID)),
			want: http.StatusOK, check: checkReadingVersion(ref, versionID),
		},
		{
			name: "guide: malformed version", path: readingStatePath(ref, apidesc.UnitsStateVersion+"=..%2Fetc"),
			want: http.StatusBadRequest, check: readingBodyHas(`"state.bad-path"`),
		},
		{
			name: "guide: unknown version", path: readingStatePath(ref, apidesc.UnitsStateVersion+"=sv-doesnotexist"),
			want: http.StatusNotFound, check: readingBodyHas(`"state-version.not-found"`),
		},
		{
			name: "guide: malformed cursor", path: readingStatePath(ref, apidesc.UnitsStateCursor+"=not-a-cursor"),
			want: http.StatusBadRequest, check: readingBodyHas(`"state.bad-path"`),
		},
	} {
		readingStateStep(ctx, t, env, read)
	}
}

// readingBodyHas returns a check that the body holds want.
func readingBodyHas(want string) func(string) error {
	return func(body string) error {
		if !strings.Contains(body, want) {
			return &readingDocsError{want: want}
		}

		return nil
	}
}

// readingDocsError means that an answer lacks the text of the guide.
type readingDocsError struct{ want string }

func (e *readingDocsError) Error() string {
	return "the answer does not hold " + e.want
}
