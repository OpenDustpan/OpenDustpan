//go:build e2e

package e2e_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// sourceFilterAgent is the User-Agent of the source filter requests. The route
// coverage check counts requests that do not carry the harness agent.
const sourceFilterAgent = "opendustpan-e2e-source-filter"

// sourceFilterPath is the source filter route of the e2e namespace.
const sourceFilterPath = "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/source-filter"

// The documents that the steps send and expect. The empty document is the
// filter of a namespace that never set it.
const (
	sourceFilterEmpty = `{"submodules":[],"largeFiles":[],"failOnDenied":false,"protectedBranches":[]}`
	sourceFilterFull  = `{"submodules":["https://git.example.com/platform/*"],` +
		`"largeFiles":["https://git.example.com/acme/infra"],"failOnDenied":true,` +
		`"protectedBranches":["main","release/1"]}`
)

// sourceFilterEquals checks that the body is the document, with no other text.
func sourceFilterEquals(want string) func(string) error {
	return func(body string) error {
		if strings.TrimSpace(body) != want {
			return fmt.Errorf("%w: got %s, want %s", errAnswerLacksFragment, body, want)
		}

		return nil
	}
}

// sourceFilterCall is one source filter request of the scenario.
type sourceFilterCall struct {
	check  func(string) error
	name   string
	method string
	path   string
	body   string
	want   int
}

// TestSourceFilterAPI reads and changes the source filter of a namespace
// through the API on a server of each store. It reads the empty filter of a
// namespace that never set it, refuses bodies and filters that are not valid
// with no change, changes the filter, reads it back, and answers 404 for a
// namespace that does not exist.
func TestSourceFilterAPI(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()
			skipWithoutStore(t, store)

			// The runner adapter needs the public URL, which the sign-in setup gives.
			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			sourceFilterScenario(t, env)
		})
	}
}

// sourceFilterGet returns a read of the filter at path.
func sourceFilterGet(name, path string, want int, check func(string) error) sourceFilterCall {
	return sourceFilterCall{check: check, name: name, method: http.MethodGet, path: path, body: "", want: want}
}

// sourceFilterPut returns a change of the filter of the e2e namespace.
func sourceFilterPut(name, body string, want int, check func(string) error) sourceFilterCall {
	return sourceFilterCall{
		check: check, name: name, method: http.MethodPut, path: sourceFilterPath, body: body, want: want,
	}
}

func sourceFilterScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	absent := "/api/dustpan/v1/namespaces/absent/source-filter"

	bad := http.StatusBadRequest
	ok := http.StatusOK
	empty := sourceFilterEquals(sourceFilterEmpty)
	full := sourceFilterEquals(sourceFilterFull)

	calls := []sourceFilterCall{
		sourceFilterGet("read the empty filter", sourceFilterPath, ok, empty),
		sourceFilterPut("a glob branch is refused",
			`{"submodules":[],"largeFiles":[],"failOnDenied":false,"protectedBranches":["release/*"]}`, bad, nil),
		sourceFilterPut("an http pattern is refused",
			`{"submodules":["http://git.example.com/a"],"largeFiles":[],"failOnDenied":false,"protectedBranches":[]}`,
			bad, nil),
		sourceFilterPut("a missing field is refused", `{"submodules":[],"largeFiles":[],"failOnDenied":false}`,
			bad, nil),
		sourceFilterPut("an unknown field is refused",
			`{"submodules":[],"largeFiles":[],"failOnDenied":false,"protectedBranches":[],"x":1}`, bad, nil),
		sourceFilterGet("the refused changes left the filter empty", sourceFilterPath, ok, empty),
		sourceFilterPut("change the filter", sourceFilterFull, ok, full),
		sourceFilterGet("read the changed filter", sourceFilterPath, ok, full),
		sourceFilterPut("replace it with the empty filter", sourceFilterEmpty, ok, empty),
		sourceFilterGet("a namespace that does not exist is not found", absent, http.StatusNotFound, nil),
	}

	for _, c := range calls {
		env.APIStepA(t.Context(), t, e2e.APIStepSpec{
			UserAgent: sourceFilterAgent, Name: c.name, Method: c.method, Path: c.path, Body: c.body,
			Want: c.want, Check: c.check, Scheme: "token",
		})
	}
}
