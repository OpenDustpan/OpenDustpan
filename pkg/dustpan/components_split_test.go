package dustpan_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// chainOperations lists the audit chain operations of the store.
func chainOperations(t *testing.T, ops durable.Store) []durable.Operation {
	t.Helper()

	all, err := ops.List(t.Context(), durable.Filter{Kind: "", States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}

	return all
}

// TestSplitTopologyWorkerFinishesWhatAPIStarted proves the two processes of a
// split installation on one database. The api process inserts the durable
// operation of an approved audit chain change and claims nothing. A separate
// worker process claims the operation and ends it succeeded.
func TestSplitTopologyWorkerFinishesWhatAPIStarted(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	api := runChainServer(t, st, audit.ChainOff,
		dustpan.WithComponents(mustParse(t, "api")),
		dustpan.WithDurableStore(st.DurableOperations()),
		dustpan.WithInstanceID("api-1"),
	)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })
	wantMaintenance(t, st, maintenance.StateMaintenance)

	if status, body := api.do(t, http.MethodPost, "/api/server/audit/chain"); status != http.StatusAccepted {
		t.Fatalf("approve through the api process: %d %s, want 202", status, body)
	}

	// Nobody runs the worker component yet: the operation exists and waits.
	pause(t, time.Second)

	ops := chainOperations(t, st.DurableOperations())
	if len(ops) != 1 || ops[0].State != durable.StatePending || ops[0].Attempt != 0 {
		t.Fatalf("operations after the approval = %+v, want one pending operation that nothing claimed", ops)
	}

	if rec, err := st.ChainRecord(t.Context()); err != nil || rec.State == audit.ChainStateOff {
		t.Fatalf("chain record = %+v, %v; want the state not changed by the api process", rec, err)
	}

	worker := runChainServer(t, st, audit.ChainOff,
		dustpan.WithComponents(mustParse(t, "worker")),
		dustpan.WithDurableStore(st.DurableOperations()),
		dustpan.WithInstanceID("worker-1"),
	)

	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.State == audit.ChainStateOff && !rec.Pending })
	waitForMaintenance(t, st, maintenance.StateNormal)

	ops = chainOperations(t, st.DurableOperations())
	if len(ops) != 1 || ops[0].State != durable.StateSucceeded || ops[0].Attempt != 1 {
		t.Fatalf("operations after the worker started = %+v, want one succeeded operation with one claim", ops)
	}

	// The worker listens for the probes and the metrics only.
	if status, _ := worker.do(t, http.MethodGet, "/api/server/audit/chain"); status != http.StatusNotFound {
		t.Fatalf("installation API on the worker process: %d, want 404", status)
	}
}

// readinessDoc is the body of GET /health/ready.
type readinessDoc struct {
	Status     string           `json:"status"`
	Failed     []string         `json:"failed"`
	Components []readinessState `json:"components"`
}

// readinessState is the state of one component in readinessDoc.
type readinessState struct {
	Name   string            `json:"name"`
	Status string            `json:"status"`
	Info   map[string]string `json:"info"`
}

// TestWaitingChangeIsInformationInReadiness proves that a change of the
// audit chain that waits for approval never makes a readiness probe answer
// 503. The body reports the state as information of the api component, and
// keeps the status and failed fields that the probe had before components.
func TestWaitingChangeIsInformationInReadiness(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	srv := runChainServer(t, st, audit.ChainOff)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })

	for _, path := range []string{"/health/ready", "/health/ready/api"} {
		status, body := srv.do(t, http.MethodGet, path)
		if status != http.StatusOK {
			t.Fatalf("GET %s while a change waits: %d %s, want 200", path, status, body)
		}

		var doc readinessDoc
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode %q: %v", body, err)
		}

		if doc.Status != "ok" || len(doc.Failed) != 0 || len(doc.Components) == 0 {
			t.Fatalf("GET %s: %q, want status ok, no failed check and the components", path, body)
		}

		if got := doc.Components[0].Info["audit-chain"]; got != "waiting" {
			t.Fatalf("GET %s: audit-chain info %q, want waiting (body %q)", path, got, body)
		}
	}
}

// TestWorkerBeatsWhileItRunsAChainMigration proves that a worker with a
// durable store and no job schedule writes its heartbeat when it claims the
// operation of an audit chain change, and that its readiness stays ready.
func TestWorkerBeatsWhileItRunsAChainMigration(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	api := runChainServer(t, st, audit.ChainOff,
		dustpan.WithComponents(mustParse(t, "api")),
		dustpan.WithDurableStore(st.DurableOperations()),
		dustpan.WithInstanceID("api-1"),
	)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })

	if status, body := api.do(t, http.MethodPost, "/api/server/audit/chain"); status != http.StatusAccepted {
		t.Fatalf("approve through the api process: %d %s, want 202", status, body)
	}

	if beat, err := st.DurableOperations().LastBeat(t.Context()); err != nil || beat.Owner != "" {
		t.Fatalf("heartbeat before the worker starts = %+v, %v; want none", beat, err)
	}

	worker := runChainServer(t, st, audit.ChainOff,
		dustpan.WithComponents(mustParse(t, "worker")),
		dustpan.WithDurableStore(st.DurableOperations()),
		dustpan.WithInstanceID("worker-1"),
		dustpan.WithOperationLease(time.Minute),
	)

	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.State == audit.ChainStateOff && !rec.Pending })

	beat, err := st.DurableOperations().LastBeat(t.Context())
	if err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}

	if beat.Owner != "worker-1" || beat.At.IsZero() {
		t.Fatalf("heartbeat after the migration = %+v, want one from worker-1", beat)
	}

	if status, body := worker.do(t, http.MethodGet, "/health/ready"); status != http.StatusOK {
		t.Fatalf("worker readiness: %d %s, want 200", status, body)
	}
}
