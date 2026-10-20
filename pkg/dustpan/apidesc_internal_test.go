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

// TestDescribeOperationsFailsClosed proves that the server refuses an adapter
// whose operations the description cannot hold, so it never starts with a
// partial description. An unnamed route is the case that a new route meets.
func TestDescribeOperationsFailsClosed(t *testing.T) {
	t.Parallel()

	good := apidesc.Operation{
		ID: "getThing", Method: "GET", Path: "/api/dustpan/v1/things", Tags: []string{"things"},
		Actions: []string{"thing:read"}, Public: false, IdempotencyKey: false, IfMatch: false, ETag: false,
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
