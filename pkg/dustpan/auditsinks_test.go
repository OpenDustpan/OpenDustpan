package dustpan_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/logsink/file"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// shipWait bounds how long the test waits for the shipper.
const shipWait = 10 * time.Second

// TestRunShipsAuditEntriesToAFileSink starts a server with a file sink,
// appends audit entries, and waits until the file holds each of them with
// its scope, sequence and hash. A second entry after the first round
// proves the cursor: the file never holds an entry twice.
func TestRunShipsAuditEntriesToAFileSink(t *testing.T) {
	t.Parallel()

	st := openAuditStore(t)
	acme := createAuditNamespace(t, st)
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	sink, err := file.Open(path)
	if err != nil {
		t.Fatalf("open file sink: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := sink.Close(); closeErr != nil {
			t.Errorf("close file sink: %v", closeErr)
		}
	})

	srv := newAuditSinkServer(t, st, audit.ShipSink{Sink: sink, Name: "file", Interval: 10 * time.Millisecond})

	ctx, cancel := context.WithCancel(t.Context())
	done := runServer(ctx, srv)

	first := appendAuditEntry(t, st, acme, "req-1")
	waitForLines(t, path, 1)

	second := appendAuditEntry(t, st, acme, "req-2")
	lines := waitForLines(t, path, 2)

	for i, want := range []audit.Entry{first, second} {
		for _, part := range []string{
			`"msg":"audit entry"`,
			`"scope":"acme"`,
			`"requestID":"` + want.RequestID + `"`,
			`"hash":"` + want.Hash + `"`,
		} {
			if !strings.Contains(lines[i], part) {
				t.Fatalf("line %d %s lacks %s", i, lines[i], part)
			}
		}
	}

	// The shipper counts a batch just after it moves the cursor, so wait for
	// the count before the server stops.
	waitForMetric(t, srv, `dustpan_audit_sink_entries_total{sink="file"} 2`)

	cancel()

	if runErr := <-done; runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
}

// TestNewServerRefusesTwoSinksWithOneName proves the sink names stay
// unique: a name keys the cursor of a sink.
func TestNewServerRefusesTwoSinksWithOneName(t *testing.T) {
	t.Parallel()

	st := openAuditStore(t)

	sink, err := file.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatalf("open file sink: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := sink.Close(); closeErr != nil {
			t.Errorf("close file sink: %v", closeErr)
		}
	})

	one := audit.ShipSink{Sink: sink, Name: "file", Interval: 0}

	_, err = dustpan.NewServer(append(auditSinkServerOptions(t, st), dustpan.WithAuditSinks(one, one))...)
	if !errors.Is(err, audit.ErrInvalidShipper) {
		t.Fatalf("new server with two sinks named file: got %v, want ErrInvalidShipper", err)
	}
}

// openAuditStore opens a fresh SQLite store that the test closes.
func openAuditStore(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}

// createAuditNamespace creates the namespace acme in st.
func createAuditNamespace(t *testing.T, st *sqlite.Store) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            "acme",
		HomeRegion:      region,
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return ns
}

// appendAuditEntry appends one reveal entry to ns and answers it as stored.
func appendAuditEntry(t *testing.T, st *sqlite.Store, ns namespace.Namespace, requestID string) audit.Entry {
	t.Helper()

	stored, err := st.Append(t.Context(), ns.Fence(), audit.Entry{
		At:         time.Now(),
		Namespace:  ns.Name,
		Subject:    "admin",
		IDPSubject: "",
		TokenID:    "",
		Action:     "unit:read-secrets",
		Object:     "opentofu:acme/prod",
		Version:    "sv-1",
		ValuePath:  "/outputs/db",
		ValueCount: nil,
		SourceIP:   "192.0.2.1",
		UserAgent:  "test",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindReveal,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  requestID,
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	})
	if err != nil {
		t.Fatalf("append audit entry: %v", err)
	}

	return stored
}

// auditSinkServerOptions answers the options of a server on st with a
// loopback listener.
func auditSinkServerOptions(t *testing.T, st *sqlite.Store) []dustpan.Option {
	t.Helper()

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return []dustpan.Option{
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(testWrapper(t)),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithListener(openTestListener(t)),
	}
}

// newAuditSinkServer builds a server on st that ships to sinks.
func newAuditSinkServer(t *testing.T, st *sqlite.Store, sinks ...audit.ShipSink) *dustpan.Server {
	t.Helper()

	srv, err := dustpan.NewServer(append(auditSinkServerOptions(t, st), dustpan.WithAuditSinks(sinks...))...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}

// waitForLines waits until the file at path holds exactly n lines, and
// answers them. It fails when more lines arrive or the wait ends first.
func waitForLines(t *testing.T, path string, n int) []string {
	t.Helper()

	deadline := time.Now().Add(shipWait)

	for {
		data, err := os.ReadFile(path) //nolint:gosec // The path is a test temp file.
		if err != nil {
			t.Fatalf("read sink file: %v", err)
		}

		lines := strings.Split(string(bytes.TrimSuffix(data, []byte("\n"))), "\n")
		if len(data) == 0 {
			lines = nil
		}

		switch {
		case len(lines) == n:
			return lines
		case len(lines) > n:
			t.Fatalf("sink file holds %d lines, want %d: %q", len(lines), n, lines)
		case time.Now().After(deadline):
			t.Fatalf("sink file holds %d lines after %s, want %d", len(lines), shipWait, n)
		default:
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("test ended while it waited for the sink file")
		case <-timer.C:
		}
	}
}

// waitForMetric waits until the metrics output of srv holds want.
func waitForMetric(t *testing.T, srv *dustpan.Server, want string) {
	t.Helper()

	deadline := time.Now().Add(shipWait)

	for {
		var out strings.Builder
		if err := srv.Metrics().Write(&out); err != nil {
			t.Fatalf("write metrics: %v", err)
		}

		if strings.Contains(out.String(), want) {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("metrics lack %s after %s:\n%s", want, shipWait, out.String())
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("test ended while it waited for the metric")
		case <-timer.C:
		}
	}
}
