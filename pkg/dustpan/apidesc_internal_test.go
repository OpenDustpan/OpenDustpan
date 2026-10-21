package dustpan

import (
	"errors"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// operationsHandler is an adapter that lists the given operations.
type operationsHandler struct {
	http.Handler

	ops []apidesc.Operation
}

func (h operationsHandler) Operations() []apidesc.Operation { return h.ops }

func (h operationsHandler) RegisteredPatterns() []string {
	out := make([]string, 0, len(h.ops))
	for _, op := range h.ops {
		out = append(out, op.Method+" "+op.Path)
	}

	return out
}

// patternsHandler is an adapter that serves patterns and lists no operations.
type patternsHandler struct {
	http.Handler

	patterns []string
}

func (h patternsHandler) RegisteredPatterns() []string { return h.patterns }

// TestDescribeOperationsRefusesAHandlerThatHidesItsRoutes proves that the
// server refuses a mounted handler that serves a route under the prefix and
// lists no operations, and a handler that lists no patterns. Without this, a
// handler that a later change wraps in middleware would drop out of the
// description with no error.
func TestDescribeOperationsRefusesAHandlerThatHidesItsRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler http.Handler
		bad     bool
	}{
		{"routes under the prefix and no operations", patternsHandler{
			Handler: http.NotFoundHandler(), patterns: []string{"GET /api/dustpan/v1/things"},
		}, true},
		{"routes outside the prefix and no operations", patternsHandler{
			Handler: http.NotFoundHandler(), patterns: []string{"GET /api/stacks/{stack}"},
		}, false},
		{"a route with no method under the prefix and no operations", patternsHandler{
			Handler: http.NotFoundHandler(), patterns: []string{"/api/dustpan/v1/things"},
		}, true},
		{"a route with no method outside the prefix and no operations", patternsHandler{
			Handler: http.NotFoundHandler(), patterns: []string{"/api/stacks/{stack}"},
		}, false},
		{"a route with a host under the prefix and no operations", patternsHandler{
			Handler: http.NotFoundHandler(), patterns: []string{"GET localhost/api/dustpan/v1/things"},
		}, true},
		{"no patterns", http.NotFoundHandler(), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := describeHandlers([]namedHandler{{name: "wrapped", handler: tt.handler}})
			if tt.bad != errors.Is(err, errUnlisted) {
				t.Fatalf("describeHandlers() = %v, want a refusal: %v", err, tt.bad)
			}
		})
	}
}

// TestDescribeOperationsFailsClosed proves that the server refuses an adapter
// whose operations the description cannot hold, so it never starts with a
// partial description. An unnamed route is the case that a new route meets.
func TestDescribeOperationsFailsClosed(t *testing.T) {
	t.Parallel()

	good := apidesc.Operation{
		ID:     "getThing",
		Method: "GET",
		Path:   "/api/dustpan/v1/things",
		Tags:   []string{"things"},
		Actions: []string{
			"thing:read",
		},
		Public:          false,
		IdempotencyKey:  false,
		IfMatch:         false,
		IfMatchRequired: false,
		ETag:            false,
	}

	unnamed := good
	unnamed.ID = ""

	outside := good
	outside.Path = "/api/server/things"

	tests := []struct {
		name string
		ops  []apidesc.Operation
		bad  bool
	}{
		{"a named route", []apidesc.Operation{good}, false},
		{"a route with no name", []apidesc.Operation{unnamed}, true},
		{"a route outside the prefix", []apidesc.Operation{outside}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handlers := []namedHandler{
				{name: "server", handler: operationsHandler{Handler: http.NotFoundHandler(), ops: tt.ops}},
			}

			ops, err := describeHandlers(handlers)
			if tt.bad {
				if !errors.Is(err, apidesc.ErrInvalidOperation) || ops != nil {
					t.Fatalf("describeHandlers() = %v, %v; want ErrInvalidOperation and no operations", ops, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("describeHandlers() = %v", err)
			}
		})
	}
}

// TestPatternPathIgnoresTheHost proves that a host part never hides a path from
// the class check: the path starts at the first slash.
func TestPatternPathIgnoresTheHost(t *testing.T) {
	t.Parallel()

	tests := []struct{ pattern, want string }{
		{"GET /api/dustpan/v1/x", "/api/dustpan/v1/x"},
		{"/api/dustpan/v1/", "/api/dustpan/v1/"},
		{"GET localhost/api/dustpan/v1/hidden", "/api/dustpan/v1/hidden"},
		{"example.org/api/dustpan/v1/hidden", "/api/dustpan/v1/hidden"},
		{"POST [::1]/api/dustpan/v1/hidden", "/api/dustpan/v1/hidden"},
	}

	for _, tt := range tests {
		if got := patternPath(tt.pattern); got != tt.want {
			t.Errorf("patternPath(%q) = %q, want %q", tt.pattern, got, tt.want)
		}
	}
}
