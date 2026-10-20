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

		time.Sleep(50 * time.Millisecond)
	}
}

// checkpointObjects counts the checkpoints of every scope in epoch 0 of the
// blob store.
func checkpointObjects(t *testing.T, store *fs.Store) int {
	t.Helper()

	key, err := audit.CheckpointKey(0, "acme", 1)
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
// mismatch that one server finds stops signing on the other.
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

	if keys, ops := queryInt(t, c, "SELECT count(DISTINCT start_key) FROM durable_operations WHERE kind = $1",
		"audit-chain-check"), queryInt(t, c, "SELECT count(*) FROM durable_operations WHERE kind = $1",
		"audit-chain-check"); keys != ops {
		t.Fatalf("operations of the check = %d with %d tick keys, want one operation for each tick", ops, keys)
	}

	for i := range 2 {
		if got := invalidOn(t, c, i); got != 0 {
			t.Fatalf("server %d exports %d invalid scopes before the change, want 0", i, got)
		}
	}

	// Server 0 runs the checks alone. A checkpoint above the end of the log
	// is a cut log: server 0 finds it and stores the mark.
	c.Kill(t, 1)

	badKey, err := audit.CheckpointKey(0, "acme", 999_999)
	if err != nil {
		t.Fatalf("checkpoint key: %v", err)
	}

	if err := blobs.Put(t.Context(), badKey, strings.NewReader("{}")); err != nil {
		t.Fatalf("put bad checkpoint: %v", err)
	}

	eventually(t, "mark of the mismatch", func() bool {
		return queryInt(t, c, "SELECT count(*) FROM audit_chain_check WHERE marked") == 1
	})

	// Server 1 starts after the mark, with no memory of it. Server 0 stops,
	// so server 1 runs the next checks alone.
	c.Restart(t, 1)
	c.Kill(t, 0)

	if err := blobs.Delete(t.Context(), badKey); err != nil {
		t.Fatalf("delete bad checkpoint: %v", err)
	}

	before := checkpointObjects(t, blobs)

	send(t, c, request{
		server: 1, method: http.MethodPost, path: "/api/server/namespaces", body: `{"name":"later"}`,
		want: http.StatusCreated,
	})

	ended := queryInt(t, c, "SELECT count(*) FROM durable_operations WHERE kind = $1 AND state = 'succeeded'",
		"audit-chain-check")

	eventually(t, "two checks of server 1 after the mark", func() bool {
		return queryInt(t, c, "SELECT count(*) FROM durable_operations WHERE kind = $1 AND state = 'succeeded'",
			"audit-chain-check") >= ended+2
	})

	if got := checkpointObjects(t, blobs); got != before {
		t.Fatalf("checkpoints after the checks of server 1 = %d, want %d: the mark stops signing", got, before)
	}

	if got := invalidOn(t, c, 1); got == 0 {
		t.Fatalf("server 1 exports %d invalid scopes, want the scopes that the mark stops", got)
	}

	// Server 0 starts again and exports the same stored value.
	c.Restart(t, 0)

	eventually(t, "same value on both servers", func() bool {
		return invalidOn(t, c, 0) == invalidOn(t, c, 1)
	})

}
