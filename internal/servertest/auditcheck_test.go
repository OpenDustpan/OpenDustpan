package servertest_test

import (
	"bytes"
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/servertest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// auditCheckEvery is the time between two checks in the cluster of the test:
// short enough for a fast test.
const auditCheckEvery = 300 * time.Millisecond

// auditCheckWait bounds each wait of the test.
const auditCheckWait = 30 * time.Second

// invalidLine matches the gauge of the scopes whose chain check failed.
var invalidLine = regexp.MustCompile(`(?m)^dustpan_audit_chain_invalid (\d+)$`)

// queryInt reads one integer from the database of c.
func queryInt(t *testing.T, c *servertest.Cluster, query string, args ...any) int64 {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), c.SchemaURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(context.WithoutCancel(t.Context())); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	var n int64
	if err := conn.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}

	return n
}

// invalidOn reads dustpan_audit_chain_invalid from server i.
func invalidOn(t *testing.T, c *servertest.Cluster, i int) int {
	t.Helper()

	status, body := c.Do(t, i, http.MethodGet, "/api/server/metrics", "")
	if status != http.StatusOK {
		t.Fatalf("metrics of server %d: status %d", i, status)
	}

	m := invalidLine.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("metrics of server %d hold no dustpan_audit_chain_invalid:\n%s", i, body)
	}

	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("metrics of server %d: %v", i, err)
	}

	return n
}

// eventually polls cond until it is true, or fails t after auditCheckWait.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(auditCheckWait)

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("no %s after %v", what, auditCheckWait)
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("test context done while waiting for %s: %v", what, t.Context().Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// scopeObjects counts the checkpoints of scope in epoch 0 of the blob store.
func scopeObjects(t *testing.T, store *fs.Store, scope namespace.Name) int {
	t.Helper()

	key, err := audit.FirstLayoutCheckpointKey(0, scope, 1)
	if err != nil {
		t.Fatalf("checkpoint key: %v", err)
	}

	objects, err := store.List(t.Context(), key.String()[:strings.LastIndex(key.String(), "/")+1])
	if err != nil {
		t.Fatalf("list checkpoints of %s: %v", scope, err)
	}

	return len(objects)
}

// checkpointObjects counts the checkpoints of every scope in epoch 0 of the
// blob store.
func checkpointObjects(t *testing.T, store *fs.Store) int {
	t.Helper()

	key, err := audit.FirstLayoutCheckpointKey(0, "acme", 1)
	if err != nil {
		t.Fatalf("checkpoint key: %v", err)
	}

	// The key names the epoch, the scope in hexadecimal and the sequence.
	epochDir := key.String()[:strings.Index(key.String(), "/")+10]

	objects, err := store.List(t.Context(), epochDir)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}

	return len(objects)
}

// TestAuditChainCheckRunsOnOneInstance proves, with two servers on one
// PostgreSQL database, that the audit chain check runs once per tick, that
// every server exports the same dustpan_audit_chain_invalid, and that a
// mismatch that one server finds stops signing of that scope on the other,
// while the other scopes keep signing.
func TestAuditChainCheckRunsOnOneInstance(t *testing.T) {
	t.Parallel()

	signer, err := audit.NewSigner(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatalf("signer: %v", err)
	}

	c := servertest.Start(t, 2,
		dustpan.WithAuditChain(audit.ChainOn),
		dustpan.WithAuditCheckpoints(signer),
		dustpan.WithAuditChainCheckInterval(auditCheckEvery))

	blobs, err := fs.Open(c.BlobDir())
	if err != nil {
		t.Fatalf("open blobs: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blobs: %v", closeErr)
		}
	})

	send(t, c, request{
		server: 0, method: http.MethodPost, path: "/api/server/namespaces", body: `{"name":"acme"}`,
		want: http.StatusCreated,
	})

	eventually(t, "a checkpoint", func() bool { return checkpointObjects(t, blobs) > 0 })

	// The two schedulers share each tick: one operation for each tick key,
	// and every operation ended well.
	eventually(t, "three ended checks", func() bool {
		return queryInt(t, c, "SELECT count(*) FROM durable_operations WHERE kind = $1 AND state = 'succeeded'",
			"audit-chain-check") >= 3
	})

	// One statement, so a tick between two reads cannot change the counts.
	shared := queryInt(
		t,
		c,
		"SELECT count(*) - count(DISTINCT start_key) FROM durable_operations WHERE kind = $1",
		"audit-chain-check",
	)

	if shared != 0 {
		t.Fatalf("%d operations of the check share a tick key, want one operation for each tick", shared)
	}

	for i := range 2 {
		if got := invalidOn(t, c, i); got != 0 {
			t.Fatalf("server %d exports %d invalid scopes before the change, want 0", i, got)
		}
	}

	// Both servers stay live. A checkpoint above the end of the log is a cut
	// log: whichever server checks first finds it and stores the mark.
	badKey, err := audit.FirstLayoutCheckpointKey(0, "acme", 999_999)
	if err != nil {
		t.Fatalf("checkpoint key: %v", err)
	}

	if err := blobs.Put(t.Context(), badKey, strings.NewReader("{}")); err != nil {
		t.Fatalf("put bad checkpoint: %v", err)
	}

	eventually(t, "mark of the mismatch", func() bool {
		return queryInt(t, c, "SELECT count(*) FROM audit_chain_mark") == 1
	})

	if err := blobs.Delete(t.Context(), badKey); err != nil {
		t.Fatalf("delete bad checkpoint: %v", err)
	}

	acmeBefore := checkMarkStopsOnlyAcme(t, c, blobs)

	// A server that starts after the mark has no memory of it.
	c.Kill(t, 1)
	c.Restart(t, 1)

	ended := queryInt(t, c, "SELECT count(*) FROM durable_operations WHERE kind = $1 AND state = 'succeeded'",
		"audit-chain-check")

	eventually(t, "two more checks", func() bool {
		return queryInt(t, c, "SELECT count(*) FROM durable_operations WHERE kind = $1 AND state = 'succeeded'",
			"audit-chain-check") >= ended+2
	})

	if got := scopeObjects(t, blobs, "acme"); got != acmeBefore {
		t.Fatalf("checkpoints of acme after a restart = %d, want %d", got, acmeBefore)
	}

	// Server 0 starts again and exports the same stored value.
	c.Kill(t, 0)
	c.Restart(t, 0)

	eventually(t, "one invalid scope on both servers after a restart", func() bool {
		return invalidOn(t, c, 0) == 1 && invalidOn(t, c, 1) == 1
	})
}

// checkMarkStopsOnlyAcme adds entries to acme and to @server after the mark of
// acme, waits for two checks, and proves that no server signs acme, that
// @server keeps signing, and that exactly one scope does not match on both
// servers. It answers the count of acme checkpoints.
func checkMarkStopsOnlyAcme(t *testing.T, c *servertest.Cluster, blobs *fs.Store) int {
	t.Helper()

	acmeBefore := scopeObjects(t, blobs, "acme")
	serverBefore := scopeObjects(t, blobs, audit.ServerScope)

	send(t, c,
		request{
			server: 1, method: http.MethodPost, path: "/api/stacks/acme/web", body: `{"stackName":"after"}`,
			want: http.StatusOK,
		},
		request{
			server: 0, method: http.MethodPost, path: "/api/server/namespaces", body: `{"name":"later"}`,
			want: http.StatusCreated,
		})

	ended := queryInt(t, c, "SELECT count(*) FROM durable_operations WHERE kind = $1 AND state = 'succeeded'",
		"audit-chain-check")

	eventually(t, "two checks after the mark", func() bool {
		return queryInt(t, c, "SELECT count(*) FROM durable_operations WHERE kind = $1 AND state = 'succeeded'",
			"audit-chain-check") >= ended+2
	})

	if got := scopeObjects(t, blobs, "acme"); got != acmeBefore {
		t.Fatalf("checkpoints of acme after the checks = %d, want %d: the mark stops signing of acme", got, acmeBefore)
	}

	eventually(t, "a checkpoint of another scope", func() bool {
		return scopeObjects(t, blobs, audit.ServerScope) > serverBefore
	})

	// Exactly one scope does not match, on both servers: acme.
	eventually(t, "one invalid scope on both servers", func() bool {
		return invalidOn(t, c, 0) == 1 && invalidOn(t, c, 1) == 1
	})

	return acmeBefore
}
