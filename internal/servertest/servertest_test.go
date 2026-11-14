package servertest_test

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/servertest"
	"opendustpan.org/opendustpan/pkg/dustpan"
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

// TestMaintenanceStopsWritesOnEveryServer proves that maintenance mode stops
// writes on a second server in the next request, without a wait for its cache
// of the installation state. Server 1 reads the state first, so its cache says
// normal when server 0 enters. Then server 1 refuses a tool write and an
// installation API write with 503, and a read still answers. After the leave,
// both writes pass again.
func TestMaintenanceStopsWritesOnEveryServer(t *testing.T) {
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
		// Fill the cache of server 1 with the normal state.
		request{server: 1, method: http.MethodGet, path: "/api/stacks/acme/web/prod", body: "", want: http.StatusOK},
		request{
			server: 0,
			method: http.MethodPost,
			path:   "/api/dustpan/v1/maintenance/enter",
			body:   "",
			want:   http.StatusOK,
		},
		request{
			server: 1,
			method: http.MethodPost,
			path:   "/api/stacks/acme/web",
			body:   `{"stackName":"dev"}`,
			want:   http.StatusServiceUnavailable,
		},
		request{
			server: 1,
			method: http.MethodPost,
			path:   "/api/server/namespaces",
			body:   `{"name":"other"}`,
			want:   http.StatusServiceUnavailable,
		},
		request{server: 1, method: http.MethodGet, path: "/api/server/namespaces", body: "", want: http.StatusOK},
		request{
			server: 1,
			method: http.MethodPost,
			path:   "/api/dustpan/v1/maintenance/leave",
			body:   "",
			want:   http.StatusOK,
		},
	)

	// The cache of the HTTP gate on server 1 may still say maintenance for a
	// short time after the leave. The write goes through once it reads normal.
	waitForStatus(t, c, request{
		server: 1,
		method: http.MethodPost,
		path:   "/api/server/namespaces",
		body:   `{"name":"other"}`,
		want:   http.StatusCreated,
	})

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
}

// TestSplitTopology proves a split topology on one database: server 0 runs
// the api component, server 1 runs the worker component, and server 2 runs
// both. The api server serves the routes, the worker server answers 404 on
// each route except the probes and the metrics route, and a write on the api
// server reads on the server with both components.
func TestSplitTopology(t *testing.T) {
	t.Parallel()

	api, err := dustpan.ParseComponents("api")
	if err != nil {
		t.Fatal(err)
	}

	worker, err := dustpan.ParseComponents("worker")
	if err != nil {
		t.Fatal(err)
	}

	c := servertest.StartSplit(t, []dustpan.Components{api, worker, dustpan.AllComponents()})

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
		request{server: 2, method: http.MethodGet, path: "/api/server/namespaces", body: "", want: http.StatusOK},
		request{server: 1, method: http.MethodGet, path: "/api/server/namespaces", body: "", want: http.StatusNotFound},
		request{server: 1, method: http.MethodGet, path: "/api/stacks/acme/web", body: "", want: http.StatusNotFound},
		request{server: 1, method: http.MethodGet, path: "/health/live", body: "", want: http.StatusNoContent},
		request{server: 1, method: http.MethodGet, path: "/health/ready/worker", body: "", want: http.StatusOK},
		request{server: 1, method: http.MethodGet, path: "/health/ready/api", body: "", want: http.StatusNotFound},
		request{server: 1, method: http.MethodGet, path: "/api/server/metrics", body: "", want: http.StatusOK},
		request{server: 0, method: http.MethodGet, path: "/health/ready/api", body: "", want: http.StatusOK},
		request{server: 0, method: http.MethodGet, path: "/api/dustpan/v1/worker", body: "", want: http.StatusOK},
	)
}

// waitForStatus sends r until it answers the status that it wants, and fails
// the test when that takes more than 10 seconds.
func waitForStatus(t *testing.T, c *servertest.Cluster, r request) {
	t.Helper()

	timeout := time.After(10 * time.Second)

	for {
		status, body := c.Do(t, r.server, r.method, r.path, r.body)
		if status == r.want {
			return
		}

		select {
		case <-timeout:
			t.Fatalf(
				"%s %s on server %d: status %d, want %d, body %s",
				r.method,
				r.path,
				r.server,
				status,
				r.want,
				body,
			)
		case <-t.Context().Done():
			t.Fatalf("%s %s on server %d: %v", r.method, r.path, r.server, t.Context().Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
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

		if r.path == "/api/server/namespaces" && r.want == http.StatusCreated {
			seedPlacement(t, c, r.server, r.body)
		}
	}
}

// seedPlacement makes a collection, a placement rule for Pulumi stacks of any
// project and any name, and a placement rule for OpenTofu workspaces of any
// name in the namespace that body creates. The server refuses a new stack or
// workspace that no rule places.
func seedPlacement(t *testing.T, c *servertest.Cluster, server int, namespaceBody string) {
	t.Helper()

	name := strings.TrimSuffix(strings.TrimPrefix(namespaceBody, `{"name":"`), `"}`)
	base := "/api/dustpan/v1/namespaces/" + name

	for _, step := range []struct{ path, body string }{
		{base + "/collections", `{"name":"everything"}`},
		{base + "/placement-rules", `{"tool":"pulumi","project":"*","stack":"*","collection":"everything"}`},
		{base + "/placement-rules", `{"tool":"opentofu","project":"*","stack":"*","collection":"everything"}`},
	} {
		if status, body := c.Do(t, server, http.MethodPost, step.path, step.body); status != http.StatusCreated {
			t.Fatalf("POST %s = %d %s, want %d", step.path, status, body, http.StatusCreated)
		}
	}
}

// TestWritesRecoverAfterTheDatabaseConnectionsAreCut proves that a server
// answers 503 with Retry-After while every connection to its database is cut,
// and that its writes work again when the connections return, with no restart.
func TestWritesRecoverAfterTheDatabaseConnectionsAreCut(t *testing.T) {
	t.Parallel()

	c := servertest.Start(t, 1)

	send(t, c, request{
		server: 0, method: http.MethodPost, path: "/api/server/namespaces", body: `{"name":"before"}`,
		want: http.StatusCreated,
	})

	c.CutDatabase(t, 0)

	// The pool may hold a dead connection. The first write that meets it, and
	// each later write, must answer 503 and not 500.
	for range 3 {
		status, header, body := c.DoHeader(
			t, 0, http.MethodPost, "/api/server/namespaces", `{"name":"during"}`,
		)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("write while the connections are cut: status %d, want 503, body %s", status, body)
		}

		if got := header.Get("Retry-After"); got != "5" {
			t.Fatalf("write while the connections are cut: Retry-After %q, want 5", got)
		}

		if !strings.Contains(body, "namespace.unavailable") {
			t.Fatalf("write while the connections are cut: body %s does not name namespace.unavailable", body)
		}
	}

	c.RestoreDatabase(t, 0)

	waitForStatus(t, c, request{
		server: 0, method: http.MethodPost, path: "/api/server/namespaces", body: `{"name":"after"}`,
		want: http.StatusCreated,
	})

	status, body := c.Do(t, 0, http.MethodGet, "/api/server/namespaces", "")
	if status != http.StatusOK || !strings.Contains(body, `"before"`) || !strings.Contains(body, `"after"`) {
		t.Fatalf("list after the recovery: status %d, body %s, want 200 with both namespaces", status, body)
	}
}
