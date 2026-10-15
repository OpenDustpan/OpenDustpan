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

// waitForToxiproxy waits until GET /version answers 200 or the test context ends.
func waitForToxiproxy(t *testing.T, api string) {
	t.Helper()

	ctx := t.Context()
	deadline := time.Now().Add(10 * time.Second)
	client := &http.Client{Timeout: time.Second}

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

	client := &http.Client{Timeout: 5 * time.Second}

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

// createProxy creates a proxy named name from listen host port 0 to
// upstream (host:port) and returns its dialable listen address. The proxy
// is removed at test cleanup so a shared server stays clean.
func createProxy(t *testing.T, api, name, upstream string) string {
	t.Helper()

	listen := net.JoinHostPort(toxiproxyListenHost(), "0")
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
	client := &http.Client{Timeout: 5 * time.Second}

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
