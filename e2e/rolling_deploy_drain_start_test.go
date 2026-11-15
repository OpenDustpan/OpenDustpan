//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	drainStartPollEvery   = 50 * time.Millisecond
	drainStartReadyWait   = 30 * time.Second
	drainStartStopTimeout = 2 * time.Minute
	drainStartMinRetry    = 1
	drainStartMaxRetry    = 15
)

// TestDrainRefusesNewBackupStart sends SIGTERM to one real server binary and
// posts POST /api/dustpan/v1/backups with an access token during the drain.
// The instance must answer 503 with the code instance.draining and a
// Retry-After header that holds the drain period in whole seconds.
func TestDrainRefusesNewBackupStart(t *testing.T) {
	t.Parallel()

	requireBinaryDrain(t)

	env := e2e.NewEnv(t.Context(), t, e2e.NoCLI(), e2e.StorePostgres)
	cluster := env.HA(t)
	victim := 0

	stopDone := make(chan error, 1)

	go func() { stopDone <- cluster.Stop(victim) }()

	waitDrainingProbe(t, cluster, victim)

	resp := sendBackupStart(t, cluster, victim, env.TokenA())
	checkDrainingStartAnswer(t, resp)
	waitStop(t, stopDone, victim)
}

// requireBinaryDrain skips the drain test unless the environment can send a
// signal to a real server binary with the store and the instances it needs.
func requireBinaryDrain(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skipf("the drain test needs the binary server and Linux, not %s", runtime.GOOS)
	}

	if !e2e.UseBinaryServer() {
		t.Skip("the drain test sends SIGTERM to the real binary: run with E2E_SERVER=binary")
	}

	instances, err := e2e.Instances()
	if err != nil {
		t.Fatalf("instances: %v", err)
	}

	if instances < 2 {
		t.Skip("needs E2E_INSTANCES above 1: run make e2e-ha")
	}

	if e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the drain test")
	}

	if e2e.UseS3Blobs(t) {
		if _, ok := e2e.LookupS3Config(); !ok {
			t.Skip(e2e.S3SkipReason())
		}
	}
}

// sendBackupStart posts POST /api/dustpan/v1/backups with an access token to
// the draining instance and returns its answer.
func sendBackupStart(t *testing.T, cluster *e2e.HACluster, victim int, token string) e2e.InstanceResponse {
	t.Helper()

	resp, err := cluster.Send(t.Context(), victim, e2e.InstanceRequest{
		Method: http.MethodPost,
		Path:   "/api/dustpan/v1/backups",
		Header: http.Header{"Authorization": {"Bearer " + token}},
		Body:   "",
	})
	if err != nil {
		t.Fatalf("POST /api/dustpan/v1/backups on the draining instance: %v", err)
	}

	return resp
}

// checkDrainingStartAnswer fails the test unless the answer is the 503 of a
// draining instance with a Retry-After header that holds whole seconds and
// the code instance.draining.
func checkDrainingStartAnswer(t *testing.T, resp e2e.InstanceResponse) {
	t.Helper()

	if resp.Status != http.StatusServiceUnavailable {
		t.Fatalf("POST /api/dustpan/v1/backups while draining = %d %s, want 503", resp.Status, resp.Body)
	}

	retry := resp.Header.Get("Retry-After")

	seconds, parseErr := strconv.Atoi(retry)
	if parseErr != nil || seconds < drainStartMinRetry || seconds > drainStartMaxRetry {
		t.Fatalf("Retry-After = %q, want whole seconds from %d to %d",
			retry, drainStartMinRetry, drainStartMaxRetry)
	}

	var doc struct {
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal([]byte(resp.Body), &doc); err != nil {
		t.Fatalf("decode %q: %v", resp.Body, err)
	}

	if doc.ErrorCode != "instance.draining" {
		t.Fatalf("code = %q in %s, want instance.draining", doc.ErrorCode, resp.Body)
	}
}

// waitStop fails the test unless the stopped instance returns within the
// stop timeout.
func waitStop(t *testing.T, stopDone <-chan error, victim int) {
	t.Helper()

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("stop instance %d: %v", victim, err)
		}
	case <-time.After(drainStartStopTimeout):
		t.Fatalf("instance %d did not stop within %s", victim, drainStartStopTimeout)
	}
}

// waitDrainingProbe waits until instance i answers 503 with the check
// draining on GET /health/ready. It fails the test when the drain does not
// start in time or the instance stops before it answers.
func waitDrainingProbe(t *testing.T, cluster *e2e.HACluster, i int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), drainStartReadyWait)
	defer cancel()

	for {
		resp, err := cluster.Send(
			ctx,
			i,
			e2e.InstanceRequest{Method: http.MethodGet, Path: "/health/ready", Header: nil, Body: ""},
		)
		if err == nil && resp.Status == http.StatusServiceUnavailable && strings.Contains(resp.Body, "draining") {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("instance %d did not answer 503 with draining (last: %+v, %v)", i, resp, err)
		case <-time.After(drainStartPollEvery):
		}
	}
}
