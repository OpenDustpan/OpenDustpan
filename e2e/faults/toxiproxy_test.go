//go:build faults

package faults_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// toxiproxyAPI returns the Toxiproxy HTTP API base URL. If TOXIPROXY_URL
// is set it uses that running server and starts no binary. If not, it
// starts TOXIPROXY_BIN as a child process. If neither is set it fails the
// test with the make target that sets them.
func toxiproxyAPI(t *testing.T) string {
	t.Helper()

	if api := strings.TrimSpace(os.Getenv("TOXIPROXY_URL")); api != "" {
		api = strings.TrimRight(api, "/")
		waitForToxiproxy(t, api)

		return api
	}

	if bin := strings.TrimSpace(os.Getenv("TOXIPROXY_BIN")); bin != "" {
		return startToxiproxy(t, bin)
	}

	t.Fatal("set TOXIPROXY_URL or TOXIPROXY_BIN with make faults")

	return ""
}

// toxiproxyListenHost returns the host that new proxies listen on. It
// defaults to 127.0.0.1. Set TOXIPROXY_LISTEN_HOST to 0.0.0.0 when the
// Toxiproxy server runs in a container.
func toxiproxyListenHost() string {
	if host := strings.TrimSpace(os.Getenv("TOXIPROXY_LISTEN_HOST")); host != "" {
		return host
	}

	return "127.0.0.1"
}

// proxyPortOffset returns the fixed offset from TOXIPROXY_PORT_BASE of the
// proxy named name. A container publishes only the ports it declares, so
// each proxy that a test creates needs a known port. CI publishes the
// ports from the base to the base plus 3.
func proxyPortOffset(name string) (int, bool) {
	switch name {
	case "latency-proxy":
		return 0, true
	case "timeout-proxy":
		return 1, true
	case "limit-proxy":
		return 2, true
	case "pg-proxy":
		return 3, true
	default:
		return 0, false
	}
}

// proxyListenPort returns the port that the proxy named name listens on.
// Without TOXIPROXY_PORT_BASE it is "0", and Toxiproxy picks a free port.
// With it, the port is the base plus the offset of the proxy.
func proxyListenPort(t *testing.T, name string) string {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv("TOXIPROXY_PORT_BASE"))
	if raw == "" {
		return "0"
	}

	base, err := strconv.Atoi(raw)
	if err != nil || base <= 0 {
		t.Fatalf("TOXIPROXY_PORT_BASE %q is not a positive port number", raw)
	}

	offset, ok := proxyPortOffset(name)
	if !ok {
		t.Fatalf("proxy %s has no port offset; add it to proxyPortOffset", name)
	}

	return strconv.Itoa(base + offset)
}

// toxiproxyUpstreamHost returns the host that Toxiproxy dials to reach
// a service on the test machine, or "" to keep the host of the service.
// Set TOXIPROXY_UPSTREAM_HOST when the Toxiproxy server runs in a
// container, for example to host.docker.internal.
func toxiproxyUpstreamHost() string {
	return strings.TrimSpace(os.Getenv("TOXIPROXY_UPSTREAM_HOST"))
}

// upstreamAddr returns addr (host:port) with its host replaced by
// TOXIPROXY_UPSTREAM_HOST, if that variable is set.
func upstreamAddr(addr string) string {
	host := toxiproxyUpstreamHost()
	if host == "" {
		return addr
	}

	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}

	return net.JoinHostPort(host, port)
}

// freePort returns a free loopback port as a string. The caller binds it
// right away, so the usual port race stays small.
func freePort(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for a free port: %v", err)
	}

	port := ln.Addr().(*net.TCPAddr).Port

	if err := ln.Close(); err != nil {
		t.Fatalf("close free port listener: %v", err)
	}

	return strconv.Itoa(port)
}

// startToxiproxy starts toxiproxy-server as a child process on a free port
// and returns its HTTP API base URL. The process stops at test cleanup.
func startToxiproxy(t *testing.T, bin string) string {
	t.Helper()

	port := freePort(t)

	//nolint:gosec // The binary path comes from TOXIPROXY_BIN, which make faults sets.
	cmd := exec.Command(bin, "-host", "127.0.0.1", "-port", port)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		t.Fatalf("start toxiproxy-server: %v", err)
	}

	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill() //nolint:errcheck // The test ends; the process must stop.
			_ = cmd.Wait()         //nolint:errcheck // Wait releases the child after the kill.
		}
	})

	api := "http://" + net.JoinHostPort("127.0.0.1", port)

	waitForToxiproxy(t, api)

	return api
}

// toxiproxyClient returns a client for the Toxiproxy HTTP API that keeps
// no idle connection. A Toxiproxy server in a container runs after the
// tests end, so an idle connection to it would stay open after the tests.
func toxiproxyClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{DisableKeepAlives: true}}
}

// waitForToxiproxy waits until GET /version answers 200 or the test context ends.
func waitForToxiproxy(t *testing.T, api string) {
	t.Helper()

	ctx := t.Context()
	deadline := time.Now().Add(10 * time.Second)
	client := toxiproxyClient(time.Second)

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/version", http.NoBody)
		if err != nil {
			t.Fatalf("build toxiproxy version request: %v", err)
		}

		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body) //nolint:errcheck // The version body is not read.
			_ = resp.Body.Close()                 //nolint:errcheck // The body is drained above.

			if resp.StatusCode == http.StatusOK {
				return
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf("test ended before toxiproxy answered: %v", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}

	t.Fatalf("toxiproxy at %s did not answer GET /version", api)
}

// toxiproxyDo sends one JSON request to the Toxiproxy HTTP API and returns its status and body.
func toxiproxyDo(t *testing.T, method, url, body string) (int, []byte) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, url, reader)
	if err != nil {
		t.Fatalf("build toxiproxy request %s %s: %v", method, url, err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := toxiproxyClient(5 * time.Second)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("toxiproxy %s %s: %v", method, url, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below.
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read toxiproxy %s %s: %v", method, url, err)
	}

	return resp.StatusCode, data
}

// proxyListen holds the listen address of a created Toxiproxy proxy.
type proxyListen struct {
	Listen string `json:"listen"`
}

// createProxy creates a proxy named name to upstream (host:port) and
// returns its dialable listen address. The proxy is removed at test
// cleanup so a shared server stays clean.
func createProxy(t *testing.T, api, name, upstream string) string {
	t.Helper()

	listen := net.JoinHostPort(toxiproxyListenHost(), proxyListenPort(t, name))
	upstream = upstreamAddr(upstream)
	body := fmt.Sprintf(`{"name":%q,"listen":%q,"upstream":%q}`, name, listen, upstream)

	deleteProxy(api, name)

	status, data := toxiproxyDo(t, http.MethodPost, api+"/proxies", body)
	if status != http.StatusCreated {
		t.Fatalf("create proxy %s: status %d, want %d (body %s)", name, status, http.StatusCreated, data)
	}

	var got proxyListen

	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode proxy %s: %v", name, err)
	}

	if got.Listen == "" {
		t.Fatalf("create proxy %s: no listen address in %s", name, data)
	}

	t.Cleanup(func() { deleteProxy(api, name) })

	return dialListen(got.Listen)
}

// deleteProxy removes the proxy named name. It reports nothing: it runs
// at cleanup and before create to drop a stale proxy on a shared server.
func deleteProxy(api, name string) {
	client := toxiproxyClient(5 * time.Second)

	req, err := http.NewRequest(http.MethodDelete, api+"/proxies/"+name, http.NoBody)
	if err != nil {
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		return
	}

	_, _ = io.Copy(io.Discard, resp.Body) //nolint:errcheck // Best-effort cleanup.
	_ = resp.Body.Close()                 //nolint:errcheck // Best-effort cleanup.
}

// dialListen returns a dialable address for a proxy listen address. A
// proxy that listens on all interfaces (0.0.0.0 or ::) is reached through
// 127.0.0.1.
func dialListen(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}

	if host == "" || host == "0.0.0.0" || host == "::" {
		return net.JoinHostPort("127.0.0.1", port)
	}

	return listen
}

// addToxic adds a toxic of type typ on stream with attributes to proxy.
func addToxic(t *testing.T, api, proxy, name, typ, stream, attributes string) {
	t.Helper()

	body := fmt.Sprintf(
		`{"name":%q,"type":%q,"stream":%q,"toxicity":1.0,"attributes":%s}`,
		name,
		typ,
		stream,
		attributes,
	)

	status, data := toxiproxyDo(t, http.MethodPost, api+"/proxies/"+proxy+"/toxics", body)
	if status != http.StatusOK {
		t.Fatalf("add toxic %s: status %d, want %d (body %s)", name, status, http.StatusOK, data)
	}
}

// removeToxic removes the toxic named name from proxy.
func removeToxic(t *testing.T, api, proxy, name string) {
	t.Helper()

	status, data := toxiproxyDo(t, http.MethodDelete, api+"/proxies/"+proxy+"/toxics/"+name, "")
	if status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("remove toxic %s: status %d, want 204 or 200 (body %s)", name, status, data)
	}
}

// proxyURL returns the HTTP base URL of the proxy listening on listen (host:port).
func proxyURL(listen string) string {
	return "http://" + listen
}

// isTimeoutError reports whether err is a client timeout: a net timeout, a
// deadline exceed, or the http.Client timeout text.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}

	var netErr net.Error

	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	lower := strings.ToLower(err.Error())

	return strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "deadline") ||
		strings.Contains(lower, "client.timeout")
}

// discardBody drains and closes a response body.
func discardBody(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body) //nolint:errcheck // The caller checks the status, not the body.
	_ = resp.Body.Close()                 //nolint:errcheck // The body is drained above.
}

// readBody reads a response body to a string.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below.
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	return string(data)
}

// decodeJSON decodes data into out or fails the test.
func decodeJSON(t *testing.T, what string, data []byte, out any) {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(out); err != nil {
		t.Fatalf("decode %s: %v (body %s)", what, err, data)
	}
}
