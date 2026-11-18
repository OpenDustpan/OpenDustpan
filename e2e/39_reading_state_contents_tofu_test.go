//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// readingStateWorkspace is the workspace of the OpenTofu reading scenario.
const readingStateWorkspace = "reading"

// readingStatePath returns the path of the masked read of the state of the
// unit ref, with the query. The slash of the reference is escaped, so that it
// fills one path segment.
func readingStatePath(ref, query string) string {
	path := "/api/dustpan/v1/units/" + url.PathEscape(ref) + "/state"
	if query != "" {
		path += "?" + query
	}

	return path
}

// checkReadingPage returns a check of the body of one page of the masked read.
// It proves that the page names the unit, holds at least one marker, keeps the
// markers and the value paths in the same order, marks each value as secret,
// and holds none of the secret values.
func checkReadingPage(ref string, secrets ...string) func(string) error {
	return func(body string) error {
		var page apidesc.UnitStatePage

		if err := json.Unmarshal([]byte(body), &page); err != nil {
			return fmt.Errorf("%w: decode the page: %w", errUnexpected, err)
		}

		if page.Unit != ref {
			return fmt.Errorf("%w: the page names the unit %q, want %q", errUnexpected, page.Unit, ref)
		}

		if err := checkReadingMarkers(page); err != nil {
			return err
		}

		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				return fmt.Errorf("%w: the page holds a secret value", errUnexpected)
			}
		}

		return nil
	}
}

// checkReadingMarkers checks the version, the markers and the value paths of
// one page.
func checkReadingMarkers(page apidesc.UnitStatePage) error {
	if page.Version == "" || len(page.Markers) == 0 {
		return fmt.Errorf("%w: the page holds version %q and %d markers",
			errUnexpected, page.Version, len(page.Markers))
	}

	if len(page.ValuePaths) != len(page.Markers) {
		return fmt.Errorf("%w: %d value paths for %d markers",
			errUnexpected, len(page.ValuePaths), len(page.Markers))
	}

	for i, marker := range page.Markers {
		if !marker.Secret || marker.Path != page.ValuePaths[i] {
			return fmt.Errorf("%w: marker %d is %+v, value path %q", errUnexpected, i, marker, page.ValuePaths[i])
		}
	}

	return nil
}

// tofuReadingStateScenario proves the masked read of the state of a unit with
// the real OpenTofu CLI: an apply that stores a sensitive output and a
// sensitive variable, then the tool-neutral read with an access token, for
// the current version and for the version by ID. The page names the secret
// values by path and never holds one. A malformed version and an unknown
// version are refused.
func tofuReadingStateScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	dir := t.TempDir()
	writeImportConfig(t, dir, maskedCloudConfig(env.Host(), readingStateWorkspace))

	env.StepIn(ctx, t, dir, step("reading init", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, dir, e2e.StepSpec{
		Check:  nil,
		Name:   "reading apply",
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
			name: "reading state current version", path: readingStatePath(ref, ""),
			want: http.StatusOK, check: checkReadingPage(ref, maskedSecretV1),
		},
		{
			name: "reading state version by ID",
			path: readingStatePath(ref, apidesc.UnitsStateVersion+"="+url.QueryEscape(versionID)),
			want: http.StatusOK, check: checkReadingVersion(ref, versionID),
		},
		{
			name: "reading state malformed version",
			path: readingStatePath(ref, apidesc.UnitsStateVersion+"="+url.QueryEscape("../etc")),
			want: http.StatusBadRequest, check: nil,
		},
		{
			name: "reading state unknown version",
			path: readingStatePath(ref, apidesc.UnitsStateVersion+"=sv-doesnotexist"),
			want: http.StatusNotFound, check: nil,
		},
		{
			name: "reading state malformed cursor",
			path: readingStatePath(ref, apidesc.UnitsStateCursor+"=not-a-cursor"),
			want: http.StatusBadRequest, check: nil,
		},
	} {
		readingStateStep(ctx, t, env, read)
	}
}

// checkReadingVersion returns a check that the page names the version.
func checkReadingVersion(ref, versionID string) func(string) error {
	return func(body string) error {
		if !strings.Contains(body, `"version":"`+versionID+`"`) {
			return fmt.Errorf("%w: the page does not name the version %s", errUnexpected, versionID)
		}

		return checkReadingPage(ref, maskedSecretV1)(body)
	}
}

// readingRead is one read of the tool-neutral masked route.
type readingRead struct {
	check func(string) error
	name  string
	path  string
	want  int
}

// readingStateStep runs one read of the tool-neutral masked route as principal
// A with an access token.
func readingStateStep(ctx context.Context, t *testing.T, env *e2e.Env, read readingRead) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:     read.check,
		Name:      read.name,
		Method:    http.MethodGet,
		Path:      read.path,
		Body:      "",
		Want:      read.want,
		UserAgent: "",
		Scheme:    "token",
	})
}
