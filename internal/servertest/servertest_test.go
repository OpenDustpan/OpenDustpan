package servertest_test

import (
	"net"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/servertest"
)

// request is one request of a test to one server of a cluster, and the
// status it must answer.
type request struct {
	method string
	path   string
	body   string
	server int
	want   int
}

// TestTwoServersShareOneDatabase proves that two servers serve one
// database: a write on one server reads on the other, and a killed server
// serves again after a restart.
func TestTwoServersShareOneDatabase(t *testing.T) {
	t.Parallel()

	c := servertest.Start(t, 2)

	send(
		t,
		c,
		request{
			server: 0,
			method: http.MethodPost,
			path:   "/api/server/namespaces",
			body:   `{"name":"acme"}`,
			want:   http.StatusCreated,
		},
		request{
			server: 0,
			method: http.MethodPost,
			path:   "/api/stacks/acme/web",
			body:   `{"stackName":"prod"}`,
			want:   http.StatusOK,
		},
		request{server: 1, method: http.MethodGet, path: "/api/stacks/acme/web/prod", body: "", want: http.StatusOK},
	)

	killed := strings.TrimPrefix(c.URL(0), "http://")
	c.Kill(t, 0)

	//nolint:exhaustruct_v5 // The probe dials with defaults only.
	if conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", killed); err == nil {
		_ = conn.Close() //nolint:errcheck // The probe sends nothing.

		t.Fatal("the killed server still accepts connections")
	}

	send(
		t,
		c,
		request{
			server: 1,
			method: http.MethodPost,
			path:   "/api/stacks/acme/web",
			body:   `{"stackName":"dev"}`,
			want:   http.StatusOK,
		},
	)

	c.Restart(t, 0)

	send(
		t,
		c,
		request{server: 0, method: http.MethodGet, path: "/api/stacks/acme/web/dev", body: "", want: http.StatusOK},
		request{
			server: 0,
			method: http.MethodPost,
			path:   "/api/stacks/acme/web",
			body:   `{"stackName":"test"}`,
			want:   http.StatusOK,
		},
		request{server: 1, method: http.MethodGet, path: "/api/stacks/acme/web/test", body: "", want: http.StatusOK},
	)
}

// send sends each request in order and fails the test on the first status
// that is not the one the request wants.
func send(t *testing.T, c *servertest.Cluster, requests ...request) {
	t.Helper()

	for _, r := range requests {
		if status, body := c.Do(t, r.server, r.method, r.path, r.body); status != r.want {
			t.Fatalf(
				"%s %s on server %d: status %d, want %d, body %s",
				r.method,
				r.path,
				r.server,
				status,
				r.want,
				body,
			)
		}
	}
}
