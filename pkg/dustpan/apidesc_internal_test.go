package dustpan

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
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
	for i := range h.ops {
		op := &h.ops[i]

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

// concreteHandler is a handler whose type is concrete, so a check that matches
// only the interface type exactly misses it.
type concreteHandler struct{}

func (concreteHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

// nestedHandler is a struct that is itself a handler and holds a concrete
// handler field, so the field check must traverse it.
type nestedHandler struct {
	Concrete concreteHandler
}

func (nestedHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

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

// goodOperation returns a valid operation of the description.
func goodOperation() apidesc.Operation {
	return apidesc.Operation{
		ID:     "getThing",
		Method: "GET",
		Path:   "/api/dustpan/v1/things",
		Tags:   []string{"things"},
		Actions: []string{
			"thing:read",
		},
		ActionObjects:   map[string]string{"thing:read": apidesc.KindNamespace},
		Public:          false,
		IdempotencyKey:  false,
		IfMatch:         false,
		IfMatchRequired: false,
		ETag:            false,
		Query:           nil,
		Statuses:        []int{200},
		Bodies:          &apidesc.Bodies{Request: apidesc.NoBody, Response: apidesc.NoBody},
	}
}

// describeOne runs describeHandlers on a server whose one adapter lists the
// operation.
func describeOne(op apidesc.Operation) ([]apidesc.Operation, error) {
	return describeHandlers([]namedHandler{
		{name: "server", handler: operationsHandler{Handler: http.NotFoundHandler(), ops: []apidesc.Operation{op}}},
	})
}

// TestDescribeOperationsFailsClosed proves that the server refuses an adapter
// whose operations the description cannot hold, so it never starts with a
// partial description. An unnamed route is the case that a new route meets.
func TestDescribeOperationsFailsClosed(t *testing.T) {
	t.Parallel()

	unnamed := goodOperation()
	unnamed.ID = ""

	outside := goodOperation()
	outside.Path = "/api/server/things"

	noObject := goodOperation()
	noObject.ActionObjects = nil

	tests := []struct {
		name string
		op   apidesc.Operation
		bad  bool
	}{
		{"a named route", goodOperation(), false},
		{"a route with no name", unnamed, true},
		{"a route outside the prefix", outside, true},
		{"an action with no object kind", noObject, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ops, err := describeOne(tt.op)
			if !tt.bad {
				if err != nil {
					t.Fatalf("describeHandlers() = %v", err)
				}

				return
			}

			if !errors.Is(err, apidesc.ErrInvalidOperation) || ops != nil {
				t.Fatalf("describeHandlers() = %v, %v; want ErrInvalidOperation and no operations", ops, err)
			}
		})
	}
}

// TestDescribeOperationsRefusesAnUnmarkedBody proves that a route which names
// no body type and does not say that it has no body stops the server, with its
// own error, so that the person who adds a route sees what to add.
func TestDescribeOperationsRefusesAnUnmarkedBody(t *testing.T) {
	t.Parallel()

	var unset apidesc.Body

	noResponse := goodOperation()
	noResponse.Bodies = &apidesc.Bodies{Request: apidesc.NoBody, Response: unset}

	post := goodOperation()
	post.ID, post.Method = "createThing", "POST"
	post.Bodies = &apidesc.Bodies{Request: unset, Response: apidesc.NoBody}

	noBodies := goodOperation()
	noBodies.Bodies = nil

	tests := map[string]apidesc.Operation{
		"a route with no response type": noResponse,
		"a POST with no request type":   post,
		"a route that names no bodies":  noBodies,
	}

	for name, op := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if ops, err := describeOne(op); !errors.Is(err, apidesc.ErrInvalidBody) || ops != nil {
				t.Fatalf("describeHandlers() = %v, %v; want ErrInvalidBody and no operations", ops, err)
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

// handlerFieldNames returns the lower-case names of the fields of t, a struct
// type, whose types implement http.Handler, with embedded structs included.
func handlerFieldNames(t reflect.Type) []string {
	var out []string

	for field := range t.Fields() {
		switch {
		case field.Anonymous && field.Type.Kind() == reflect.Struct:
			out = append(out, handlerFieldNames(field.Type)...)
		case field.Type.Implements(reflect.TypeFor[http.Handler]()):
			out = append(out, strings.ToLower(field.Name))
		default:
		}
	}

	return out
}

// unlistedFields returns the http.Handler fields of t that the names do not
// hold, and the names that no http.Handler field has.
func unlistedFields(t reflect.Type, names []string) []string {
	fields := handlerFieldNames(t)

	var problems []string

	for _, field := range fields {
		if !slices.Contains(names, field) {
			problems = append(problems, field+" is a handler field that the list lacks")
		}
	}

	for _, name := range names {
		if !slices.Contains(fields, name) {
			problems = append(problems, name+" is in the list and is no handler field")
		}
	}

	slices.Sort(problems)

	return problems
}

// TestEveryHandlerFieldIsListed proves that each handler field of the adapters
// that the server mounts has a row in allHandlers, which feeds the description
// and the class check, or is the readiness probe on the root mux. The check
// reads the types and not a server, so a module that a test server has off
// cannot hide a field from it. A handler that production mounts under the
// prefix and that the list lacks fails here.
func TestEveryHandlerFieldIsListed(t *testing.T) {
	t.Parallel()

	var a adapters

	names := make([]string, 0, len(a.allHandlers())+1)
	for _, h := range a.allHandlers() {
		names = append(names, h.name)
	}

	// The readiness probe serves the health routes on the root mux outside the
	// API prefix, so it feeds neither the description nor the class check.
	names = append(names, "ready")

	if problems := unlistedFields(reflect.TypeFor[adapters](), names); len(problems) > 0 {
		t.Fatalf("handlers of adapters and the list differ: %v", problems)
	}
}

// TestUnlistedHandlerFieldFails proves the check of TestEveryHandlerFieldIsListed
// with a struct that has a handler which its list lacks, and a list that has a
// name no field has.
func TestUnlistedHandlerFieldFails(t *testing.T) {
	t.Parallel()

	type mounts struct {
		Listed http.Handler
		Hidden http.Handler
		Note   string
	}

	type concreteMounts struct {
		Listed   http.Handler
		Concrete concreteHandler
		Note     string
	}

	type nestedMounts struct {
		nestedHandler

		Listed http.Handler
	}

	if got := unlistedFields(reflect.TypeFor[mounts](), []string{"listed"}); len(got) != 1 ||
		!strings.HasPrefix(got[0], "hidden ") {
		t.Fatalf("problems = %v, want the hidden field", got)
	}

	if got := unlistedFields(reflect.TypeFor[mounts](), []string{"listed", "hidden", "ghost"}); len(got) != 1 ||
		!strings.HasPrefix(got[0], "ghost ") {
		t.Fatalf("problems = %v, want the ghost name", got)
	}

	if got := unlistedFields(reflect.TypeFor[mounts](), []string{"listed", "hidden"}); len(got) != 0 {
		t.Fatalf("problems = %v, want none", got)
	}

	if got := unlistedFields(reflect.TypeFor[concreteMounts](), []string{"listed"}); len(got) != 1 ||
		!strings.HasPrefix(got[0], "concrete ") {
		t.Fatalf("problems = %v, want the concrete field", got)
	}

	if got := unlistedFields(reflect.TypeFor[concreteMounts](), []string{"listed", "concrete"}); len(got) != 0 {
		t.Fatalf("problems = %v, want none", got)
	}

	if got := unlistedFields(reflect.TypeFor[nestedMounts](), []string{"listed"}); len(got) != 1 ||
		!strings.HasPrefix(got[0], "concrete ") {
		t.Fatalf("problems = %v, want the concrete field of the embedded struct", got)
	}
}

// sentinelHandler is a handler that a test can recognize after a setter
// stores it in an adapter field.
type sentinelHandler struct{ name string }

func (*sentinelHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

// TestEveryHandlerNameHasASetter proves that each name that allHandlers lists
// has a setter in adapterHandlerSetter, and that the setter stores the handler
// under that name. Without this, a name that the list has and the setters lack
// would make WrappedRoute return the server unchanged.
func TestEveryHandlerNameHasASetter(t *testing.T) {
	t.Parallel()

	var zero adapters

	for _, h := range zero.allHandlers() {
		t.Run(h.name, func(t *testing.T) {
			t.Parallel()

			var a adapters

			sentinel := &sentinelHandler{name: h.name}

			set := adapterHandlerSetter(&a, h.name)
			if set == nil {
				t.Fatalf("adapterHandlerSetter(%q) = nil, want a setter", h.name)
			}

			set(sentinel)

			for _, got := range a.allHandlers() {
				if got.name != h.name {
					continue
				}

				if got.handler != http.Handler(sentinel) {
					t.Fatalf("allHandlers() entry %q = %T, want the sentinel", h.name, got.handler)
				}

				return
			}

			t.Fatalf("allHandlers() lists no entry named %q", h.name)
		})
	}
}
