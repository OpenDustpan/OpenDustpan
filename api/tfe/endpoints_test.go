package tfe_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndpointListMatchesRoutes proves the published endpoint list matches
// the production route table: every served route has a list entry and every
// list entry names a served route. A route without a list entry fails here,
// so the document cannot go stale.
func TestEndpointListMatchesRoutes(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	served := map[string]bool{}
	for _, route := range handler.Routes() {
		served[route.Method+" "+route.Path] = true
	}

	listed, err := readEndpointList(filepath.Join("..", "..", "docs", "tfe-api.md"))
	if err != nil {
		t.Fatalf("%v", err)
	}

	for pattern := range served {
		if !listed[pattern] {
			t.Errorf("route %q is served but not listed in docs/tfe-api.md", pattern)
		}
	}

	for pattern := range listed {
		if !served[pattern] {
			t.Errorf("route %q is listed in docs/tfe-api.md but not served", pattern)
		}
	}
}

// endpointRef is one method and path pair of the published endpoint table.
type endpointRef struct {
	method string
	path   string
}

// readEndpointList returns the method and path pairs of the endpoint table
// in the published document.
func readEndpointList(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // The test reads its own published list by a fixed path.
	if err != nil {
		return nil, fmt.Errorf("read endpoint list: %w", err)
	}

	out := map[string]bool{}

	for line := range strings.Lines(string(raw)) {
		if ref, ok := parseEndpointRow(line); ok {
			out[ref.method+" "+ref.path] = true
		}
	}

	return out, nil
}

// parseEndpointRow reads one Markdown table row with a method and a path.
// It answers false for header rows, separator rows, and other text.
func parseEndpointRow(line string) (endpointRef, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return endpointRef{method: "", path: ""}, false
	}

	cells := strings.Split(trimmed, "|")
	if len(cells) < 4 {
		return endpointRef{method: "", path: ""}, false
	}

	ref := endpointRef{method: strings.TrimSpace(cells[1]), path: strings.TrimSpace(cells[2])}

	switch ref.method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		return endpointRef{method: "", path: ""}, false
	}

	if ref.path == "" || strings.HasPrefix(ref.path, "-") {
		return endpointRef{method: "", path: ""}, false
	}

	return ref, true
}
