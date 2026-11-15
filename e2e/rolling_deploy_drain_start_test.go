//go:build e2e

package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// drainStartSeconds is the value that the binary names in Retry-After: the
	// drain is DefaultDrainPeriod, 10 seconds, and the binary has no flag for
	// it.
	drainStartSeconds = "10"
	// drainStartCode is the error code of a durable start that an instance
	// refuses while it drains.
	drainStartCode = "instance.draining"
	// drainStartPath is the route that starts a backup. It is the shortest
	// durable start, because it takes no body.
	drainStartPath = "/api/server/backups"
)

// TestDrainRefusesNewBackupStart sends SIGTERM to a real serve process and
// starts a backup in its drain. The readiness probe and the refused start name
// the drain in Retry-After, the start answers the code instance.draining, no
// backup row exists, and the process stops clean at the end of the drain.
func TestDrainRefusesNewBackupStart(t *testing.T) {
	t.Parallel()

	t.Cleanup(http.DefaultClient.CloseIdleConnections)

	bin := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), "")
	server := startProduction(t, bin, t.TempDir(), t.TempDir())

	if err := server.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("stop serve: %v", err)
	}

	// The drain begins at once, and the process serves in it.
	_, header, body := waitDrainStart(t, server.url)

	if got := header.Get("Retry-After"); got != drainStartSeconds {
		t.Errorf("Retry-After of the readiness probe = %q, want %q", got, drainStartSeconds)
	}

	if !strings.Contains(body, "draining") {
		t.Errorf("the readiness body %q does not name the failed check draining", body)
	}

	// A durable start in the drain answers 503 with the drain time and the
	// code of the catalog.
	status, header, body := drainStartCall(t, server.url, http.MethodPost, drainStartPath, "")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("%s %s in the drain = %d %s, want %d", http.MethodPost, drainStartPath, status, body,
			http.StatusServiceUnavailable)
	}

	if got := header.Get("Retry-After"); got != drainStartSeconds {
		t.Errorf("Retry-After of the refused start = %q, want %q", got, drainStartSeconds)
	}

	var refused struct {
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal([]byte(body), &refused); err != nil {
		t.Fatalf("decode the refused start %q: %v", body, err)
	}

	if refused.ErrorCode != drainStartCode {
		t.Errorf("the refused start names the code %q, want %q", refused.ErrorCode, drainStartCode)
	}

	// The refusal stores nothing, and a read stays open.
	checkNoBackupRow(t, server.url)

	// The first signal ran the drain, so the test waits for the stop instead
	// of sending a second signal.
	if err := server.cmd.Wait(); err != nil {
		t.Fatalf("serve did not stop clean: %v\n%s", err, server.out.String())
	}
}

// waitDrainStart waits until the readiness probe of the draining instance
// answers 503, and answers its status, its headers and its body. It fails the
// test when the probe answers something else until devStopLimit.
func waitDrainStart(t *testing.T, base string) (int, http.Header, string) {
	t.Helper()

	deadline := time.Now().Add(devStopLimit)

	for time.Now().Before(deadline) {
		status, header, body := drainStartCall(t, base, http.MethodGet, healthReadyPath, "")
		if status == http.StatusServiceUnavailable {
			return status, header, body
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("wait for the drain: %v", t.Context().Err())
		case <-time.After(devPollInterval):
		}
	}

	t.Fatalf("GET %s answered no 503 in the %s after the stop signal", healthReadyPath, devStopLimit)

	return 0, nil, ""
}

// checkNoBackupRow fails the test unless the installation lists no backup.
func checkNoBackupRow(t *testing.T, base string) {
	t.Helper()

	status, _, body := drainStartCall(t, base, http.MethodGet, drainStartPath, "")
	if status != http.StatusOK {
		t.Fatalf("GET %s in the drain = %d %s, want %d", drainStartPath, status, body, http.StatusOK)
	}

	var list struct {
		Backups []json.RawMessage `json:"backups"`
	}

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode the backups %q: %v", body, err)
	}

	if len(list.Backups) != 0 {
		t.Errorf("the refused start stored %d backups, want 0", len(list.Backups))
	}
}

// drainStartCall sends one request to the installation API of the instance
// with the administrator token, and answers its status, its headers and its
// body.
func drainStartCall(t *testing.T, base, method, path, body string) (int, http.Header, string) {
	t.Helper()

	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, base+path, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+serveToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	raw, readErr := io.ReadAll(resp.Body)

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close the body of %s %s: %v", method, path, closeErr)
	}

	if readErr != nil {
		t.Fatalf("read the body of %s %s: %v", method, path, readErr)
	}

	return resp.StatusCode, resp.Header, string(raw)
}
