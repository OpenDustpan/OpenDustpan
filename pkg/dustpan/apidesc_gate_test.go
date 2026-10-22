package dustpan_test

import (
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// lintConfig is the configuration of golangci-lint, from this package.
const lintConfig = "../../.golangci.yml"

// modulePath is the path of the module, the start of each package path.
const modulePath = "opendustpan.org/opendustpan/"

// TestDescriptionGateNamesTheRoute proves the drift check of the description
// on the routes of the server with every module on: a fake route that the
// description lacks, an operation that no route serves and a query parameter
// that only the route declares each fail, with a problem that names the route
// and the one command that fixes the description.
func TestDescriptionGateNamesTheRoute(t *testing.T) {
	t.Parallel()

	described, err := apidesc.Read(apidesc.Document())
	if err != nil {
		t.Fatalf("read the checked-in description: %v", err)
	}

	served := newEveryModuleServer(t).Operations()

	const fakePath = "/api/dustpan/v1/namespaces/{namespace}/fakes/{fake_id}"

	fake := hostileOperation(func(o *apidesc.Operation) {
		o.ID, o.Path, o.Tags, o.Actions = "getFake", fakePath, []string{"fakes"}, []string{"fake:read"}
		o.ActionObjects = map[string]string{"fake:read": apidesc.KindNamespace}
	})

	listUnits := slices.IndexFunc(served, func(o apidesc.Operation) bool { return o.ID == "listUnits" })
	if listUnits < 0 {
		t.Fatal("the server has no operation listUnits")
	}

	withExtraQuery := slices.Clone(served)
	withExtraQuery[listUnits].Query = append(
		slices.Clone(withExtraQuery[listUnits].Query), apidesc.StringParam("fakeFilter", "A fake filter."),
	)

	tests := []struct {
		name   string
		routes []apidesc.Operation
		want   string
	}{
		{"a served route that the description lacks", append(slices.Clone(served), fake), "GET " + fakePath},
		{
			"an operation that no route serves",
			slices.Delete(slices.Clone(served), listUnits, listUnits+1),
			"GET /api/dustpan/v1/namespaces/{namespace}/units is in the description, and no route serves it",
		},
		{
			"a query parameter that only the route declares",
			withExtraQuery,
			"GET /api/dustpan/v1/namespaces/{namespace}/units differs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			drift, err := apidesc.Diff(tt.routes, described)
			if err != nil {
				t.Fatalf("compare: %v", err)
			}

			problems := drift.Problems()
			if len(problems) != 1 {
				t.Fatalf("problems = %q, want one", problems)
			}

			if !strings.Contains(problems[0], tt.want) || !strings.Contains(problems[0], apidesc.FixCommand) {
				t.Fatalf("problem = %q, want it to hold %q and %q", problems[0], tt.want, apidesc.FixCommand)
			}
		})
	}
}

// TestEveryDescribedAdapterReadsThroughApidesc proves that the linter bans
// the direct reads of the query and of the headers in the package of each
// adapter that serves a route under the prefix. Without the ban, a handler
// could read a name that the description does not declare, and no check would
// see it.
func TestEveryDescribedAdapterReadsThroughApidesc(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(lintConfig)
	if err != nil {
		t.Fatalf("read %s: %v", lintConfig, err)
	}

	config := string(raw)

	for _, pattern := range []string{
		`- pattern: ^url\.URL\.(Query|RawQuery|ForceQuery|RequestURI)$`,
		`- pattern: ^url\.ParseQuery$`,
		`- pattern: ^http\.Request\.(Header|Form|PostForm|MultipartForm|RequestURI|Trailer|FormValue|`,
	} {
		if !strings.Contains(config, pattern) {
			t.Errorf("%s does not ban %s", lintConfig, pattern)
		}
	}

	scope := banScope(t, config)
	srv := newEveryModuleServer(t)
	packages := srv.HandlerPackages()

	for _, source := range srv.RouteSources() {
		if !slices.ContainsFunc(source.Patterns, underPrefix) {
			continue
		}

		dir := strings.TrimPrefix(packages[source.Name], modulePath)
		if !scope.MatchString(dir + "/handlers.go") {
			t.Errorf("the adapter %s serves routes under %s, and the ban on direct reads in %s does not cover %s: "+
				"add the package to the path-except of the forbidigo exclusion", source.Name, apidesc.Prefix, lintConfig, dir)
		}
	}
}

// banScope returns the regular expression of the packages where the ban on
// direct reads applies: the path-except of the exclusion that names the ban.
func banScope(t *testing.T, config string) *regexp.Regexp {
	t.Helper()

	lines := strings.Split(config, "\n")

	for i, line := range lines {
		expr, ok := strings.CutPrefix(strings.TrimSpace(line), "- path-except: ")
		if !ok {
			continue
		}

		end := min(i+6, len(lines))
		if !slices.ContainsFunc(lines[i:end], func(l string) bool {
			return strings.TrimSpace(l) == "text: through apidesc"
		}) {
			continue
		}

		re, err := regexp.Compile(expr)
		if err != nil {
			t.Fatalf("the path-except %q of the ban does not compile: %v", expr, err)
		}

		return re
	}

	t.Fatalf("%s has no path-except for the ban on direct reads", lintConfig)

	return nil
}

// underPrefix reports whether a pattern "METHOD /path" is under the prefix.
func underPrefix(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")

	return strings.HasPrefix(path, apidesc.Prefix) && pattern != http.MethodGet+" "+apidesc.Prefix
}
