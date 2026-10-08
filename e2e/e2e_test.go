//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	stackName    = "dev"
	installLimit = 15 * time.Minute
)

func cacheDir(t *testing.T) string {
	t.Helper()

	if dir := os.Getenv("E2E_CACHE_DIR"); dir != "" {
		return dir
	}

	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("find user cache dir: %v", err)
	}

	return filepath.Join(base, "opendustpan-e2e")
}

// TestCLI runs the same scenario against every CLI version in the matrix.
func TestCLI(t *testing.T) {
	t.Parallel()

	reports := e2e.ReportDir()
	if err := e2e.ClearReports(reports); err != nil {
		t.Fatalf("clear old reports: %v", err)
	}

	// Runs after every parallel subtest has finished and written its report.
	t.Cleanup(func() {
		if err := e2e.WriteSummary(reports); err != nil {
			t.Errorf("write summary: %v", err)
		}
	})

	fetcher := &e2e.Fetcher{
		Client:       http.DefaultClient,
		CacheDir:     cacheDir(t),
		DownloadBase: e2e.DownloadBase,
		LatestURL:    e2e.LatestVersionURL,
	}

	for _, label := range e2e.Versions() {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
			defer cancel()

			cli, err := fetcher.Install(installCtx, label)
			if err != nil {
				t.Fatalf("install pulumi %s: %v", label, err)
			}

			t.Logf("pulumi %s (checksum %s) in %s", cli.Version, cli.ChecksumSource, cli.Dir)

			env := e2e.NewEnv(t.Context(), t, cli)

			t.Cleanup(func() {
				if err := e2e.WriteRun(reports, e2e.NewRun(env)); err != nil {
					t.Errorf("write report: %v", err)
				}
			})

			scenario(t, env)
		})
	}
}

var errUnexpected = errors.New("unexpected output")

func contains(want string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, want) {
			return fmt.Errorf("%w: stdout lacks %q", errUnexpected, want)
		}

		return nil
	}
}

func scenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	env.Step(ctx, t, e2e.StepSpec{Name: "login", Args: []string{"login"}, Env: nil, Check: contains(env.URL)})
	env.Step(ctx, t, e2e.StepSpec{Name: "whoami", Args: []string{"whoami"}, Env: nil, Check: func(r e2e.Result) error {
		if got := strings.TrimSpace(r.Stdout); got != e2e.Subject {
			return fmt.Errorf("%w: whoami printed %q, want %q", errUnexpected, got, e2e.Subject)
		}

		return nil
	}})
	env.Step(ctx, t, e2e.StepSpec{Name: "stack init", Args: []string{"stack", "init", qualified}, Env: nil, Check: nil})
	env.Step(
		ctx,
		t,
		e2e.StepSpec{
			Name: "stack ls",
			Args: []string{"stack", "ls", "--json"},
			Env:  nil,
			Check: func(r e2e.Result) error {
				var stacks []struct {
					Name string `json:"name"`
				}

				if err := json.Unmarshal([]byte(r.Stdout), &stacks); err != nil {
					return fmt.Errorf("%w: decode stack list: %w", errUnexpected, err)
				}

				for _, s := range stacks {
					if strings.HasSuffix(s.Name, stackName) {
						return nil
					}
				}

				return fmt.Errorf("%w: stack %q not listed: %s", errUnexpected, stackName, r.Stdout)
			},
		},
	)
	env.Step(
		ctx,
		t,
		e2e.StepSpec{Name: "stack select", Args: []string{"stack", "select", qualified}, Env: nil, Check: nil},
	)
	env.Step(ctx, t, e2e.StepSpec{
		Name: "stack show-name", Args: []string{"stack", "--show-name"}, Env: nil, Check: contains(stackName),
	})
	// Without an organization the CLI asks the backend for the user's default one.
	env.Step(
		ctx,
		t,
		e2e.StepSpec{Name: "stack init default org", Args: []string{"stack", "init", "other"}, Env: nil, Check: nil},
	)

	consoleURLSteps(t, env)
}

// consoleURLSteps records how the CLI derives console links from the backend
// URL. `pulumi login` prints "(<console URL of the user>)", empty when the CLI
// cannot derive one.
func consoleURLSteps(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	const (
		apiURL = "http://api.dustpan.test"
		appURL = "http://app.dustpan.test/" + e2e.Subject
	)

	overrideURL := "http://console.example.test:3000/" + e2e.Subject

	env.Step(ctx, t, e2e.StepSpec{
		Name: "console url from 127.0.0.1", Args: []string{"login"}, Env: nil,
		Check: contains("as " + e2e.Subject + " ()\n"),
	})
	env.Step(ctx, t, e2e.StepSpec{
		Name: "console url from api. host", Args: []string{"login"},
		// The fake host does not resolve, so the CLI sends its requests through the
		// test server acting as an HTTP proxy; the server routes on the request path.
		Env:   []string{"PULUMI_BACKEND_URL=" + apiURL, "HTTP_PROXY=" + env.URL},
		Check: contains("(" + appURL + ")"),
	})
	env.Step(ctx, t, e2e.StepSpec{
		Name: "console url from PULUMI_CONSOLE_DOMAIN", Args: []string{"login"},
		Env:   []string{"PULUMI_CONSOLE_DOMAIN=console.example.test:3000"},
		Check: contains("(" + overrideURL + ")"),
	})
}
