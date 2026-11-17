//go:build e2e

package e2e_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// proxyUpstream is the upstream proxy that the test saves for the pool, and
// proxyPassword the password of its credential.
const (
	proxyUpstream = "http://upstream.example.test:3128"
	proxyUser     = "upstream-user"
	proxyPassword = "upstream-secret-password"
)

// TestJobProxySession drives the three proxy session paths with the harness
// session client against the real server binary: a runner asks for the proxy
// handoff token of its claim, the proxy opens a proxy session and reads the
// assignment and the upstream credential. A launcher session and a proxy
// session each get wrong-session-kind on the path of the other kind.
func TestJobProxySession(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			proxySessionRoundTrip(t, e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store))
		})
	}
}

// proxySessionRoundTrip saves the proxy settings of a pool with the API, claims a
// phase, and drives the proxy session and the launcher session of the claim.
func proxySessionRoundTrip(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	c := claimAPhase(t, env)

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "save the proxy settings of the pool", Method: http.MethodPut,
		Path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/runner-pools/" + c.pool + "/proxy-settings",
		Body: `{"allowedRanges":["10.0.0.0/8"],"upstreamProxy":"` + proxyUpstream + `",` +
			`"upstreamCredential":{"username":"` + proxyUser + `","password":"` + proxyPassword + `"}}`,
		Want: http.StatusOK, Scheme: "token", Check: nil,
	}, map[string]string{"Content-Type": "application/json"})

	proxyHandoff := c.runner.ProxyHandoff(ctx, t, c.operation)
	launcher := env.OpenSession(ctx, t, c.handoff)

	// A launcher handoff token opens no proxy session: the exchange gets the
	// answer of a token that does not authenticate the path.
	if _, status, body := env.OpenProxySession(ctx, t, c.handoff); status == http.StatusOK {
		t.Fatalf("a launcher handoff token opened a proxy session: %s", body)
	}

	proxy := opensProxySession(t, env, proxyHandoff)

	status, body := proxy.UpstreamCredential(ctx, t)
	if status != http.StatusOK || !strings.Contains(string(body), proxyPassword) {
		t.Fatalf("upstream credential = %d, want 200 with the credential of the pool", status)
	}

	// A launcher session holds no right on the credential path, and a proxy
	// session holds none on the credentials path of a launcher session.
	wantWrongKind(t, "a launcher session asks the upstream credential", func() (int, []byte) {
		return launcher.Post(ctx, t, protocol.PathProxyUpstreamCredential, nil)
	})
	wantWrongKind(t, "a proxy session asks the job token of a step", func() (int, []byte) {
		return proxy.Post(ctx, t, protocol.PathSessionCredentials)
	})

	if env.InProcess() {
		return
	}

	if found := env.ServerLogHolds(t, proxyHandoff, proxyPassword); len(found) > 0 {
		t.Fatalf("the server log holds %d secrets of the proxy session: %q", len(found), found)
	}
}

// opensProxySession exchanges the proxy handoff token for a proxy session and
// checks the assignment against the settings that the test saved. It returns
// the proxy.
func opensProxySession(t *testing.T, env *e2e.Env, handoff string) *e2e.FakeProxy {
	t.Helper()

	ctx := t.Context()

	proxy, status, body := env.OpenProxySession(ctx, t, handoff)
	if status != http.StatusOK {
		t.Fatalf("open the proxy session = %d %s, want 200", status, body)
	}

	if proxy.Assignment.Namespace != e2e.Namespace || proxy.Assignment.Operation != "plan" ||
		proxy.Assignment.UpstreamURL != proxyUpstream || len(proxy.Assignment.AllowedRanges) == 0 ||
		proxy.Assignment.RulesDigest == "" {
		t.Fatalf("proxy assignment = %+v, want the settings of the pool", proxy.Assignment)
	}

	if strings.Contains(string(body), proxyPassword) {
		t.Fatal("the proxy session answer holds the upstream password")
	}

	return proxy
}

// wantWrongKind calls send and fails the test when its status is not 403
// with the code wrong-session-kind.
func wantWrongKind(t *testing.T, name string, send func() (int, []byte)) {
	t.Helper()

	status, body := send()
	if status != http.StatusForbidden || !strings.Contains(string(body), protocol.ErrorWrongSessionKind) {
		t.Fatalf("%s = %d %s, want 403 %s", name, status, body, protocol.ErrorWrongSessionKind)
	}
}
