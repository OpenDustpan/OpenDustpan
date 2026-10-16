package pulumi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestClassifyNamespaceSentinels proves each namespace sentinel answers its
// own status, never a 500: a fenced write answers 409, a namespace homed in
// another region 421, and a database that does not answer 503.
func TestClassifyNamespaceSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "fenced", err: namespace.ErrFenced, want: http.StatusConflict},
		{name: "not home", err: namespace.ErrNotHome, want: http.StatusMisdirectedRequest},
		{name: "unavailable", err: namespace.ErrUnavailable, want: http.StatusServiceUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, message := classify(fmt.Errorf("create stack: %w", tc.err))
			if status != tc.want {
				t.Fatalf("classify answers %d, want %d", status, tc.want)
			}

			if !strings.Contains(message, tc.err.Error()) {
				t.Fatalf("message = %q, want it to hold %q", message, tc.err.Error())
			}
		})
	}
}
