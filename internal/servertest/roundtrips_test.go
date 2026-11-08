package servertest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/servertest"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// This file holds the regression benchmark of database round trips: for each
// flow, the number of round trips that the server makes to PostgreSQL. A
// budget bounds each count from above, so a change that adds a round trip
// fails. For a request flow the budget also bounds the count from below, so a
// change that removes round trips must lower the budget, and the budgets only
// go down. The test log shows the measured count of each flow. Each flow runs
// on its own server, in a parallel subtest.
//
// The test runs on PostgreSQL only: the tracer is a pgx tracer. It does not see
// a loop inside the SQLite adapter.
//
// A backup and a restore have two checks, and neither one needs an edit when a
// migration adds a table with the usual cost. The count must be exactly the
// count of the cost model, a function of the table lists of pkg/backup and of
// the number of migrations (see costModel). The growth check runs the flow
// with a small and a large data set and requires that no statement grows with
// the rows, except the statements that backupGrowthAllowance names. The
// tracer does not count the claims and heartbeats of the durable runner, whose
// turn count follows the clock (see stackFacts), and the count leaves out the
// prepares and four statements whose count follows the clock or the pool (see
// counted). The servers of the flow have a long lease (see roundTripLease), so
// no sweep and no renewal runs in a flow. So a run on a slow machine counts
// the same as a run on a fast one.
//
// The growth rule sees a statement that grows with the stacks, the workspaces,
// and the rows that the seed makes for each one: updates, checkpoints, events,
// history, state versions, locks and audit entries. It does not see a statement
// that grows with a row that the seed does not make, such as outputs or
// secrets. Add the row to seedUnits when a new table needs a check.

// budgetSlack is the number of round trips that a count of a request flow may
// be below its budget before the test asks for a lower budget.
const budgetSlack = 3

// eventBatchSize is the number of events in the batch of the event flow: the
// largest batch that the server takes.
const eventBatchSize = 1000

// warmCycles is the number of runs of a request flow before the measured run,
// so that the statement cache of each connection does not change the count.
const warmCycles = 3

// requestLimit bounds one request of a flow.
const requestLimit = 30 * time.Second

// settleLimit bounds the wait for a backup or a restore. settleEvery is the
// period of the poll of its state.
const (
	settleLimit = 2 * time.Minute
	settleEvery = 20 * time.Millisecond
)

// roundTripLease is the lease of the durable operations of the backup flow.
// The sweeps that mark ended backups and restores failed run once in each
// lease period, and the claims renew three times in it. A lease far above the
// length of a run keeps both timers out of the count, so the count follows
// the work of the flow and not the clock.
const roundTripLease = time.Hour

// traceMode selects which round trips the tracer counts.
type traceMode int

const (
	// traceOff counts nothing.
	traceOff traceMode = iota
	// traceRequest counts the round trips on the HTTP serve goroutine of a
	// request. A background poller runs on another goroutine, so it does not count.
	traceRequest
	// traceBackup counts the round trips with a pkg/backup frame on a
	// goroutine that does not serve a request. The poll of the status of an
	// operation serves a request, so it does not count.
	traceBackup
)

// Function name parts that the tracer reads in the stack of a round trip.
const (
	serveFrame      = "net/http.(*conn).serve"
	backupFrame     = "/pkg/backup."
	storeFrame      = "/pkg/store/postgres."
	maxTraceFrames  = 96
	maxStatementLen = 110
)

// sqlName finds the name that sqlc puts in the first comment of a statement.
var sqlName = regexp.MustCompile(`^\s*--\s*name:\s*(\w+)`)

// stateSums returns the MD5 sum of the state document of each serial, from
// serial 1. The TFE-compatible API names a state version by the MD5 sum of its
// content. The table keeps the test free of an MD5 call. A test that needs more
// uploads than the table holds fails and asks for more entries.
func stateSums() []string {
	return []string{
		"02f7c7cea77e8c9d7becab0bdec9b105",
		"c9a63e560cf641678261447a86deacdc",
		"2952e01dd9e09cef866c14c52b8050d6",
		"401927323935d78616c453fbfdaef158",
		"874d860ad78c1759a3bba6cf98f604f6",
		"d300876b1617aeecc1ceafb5ae9d4683",
		"575773cfe0a1ef77351b5ef3246a2b71",
		"14703f8fe82aed0e74c11cb6f075cfab",
		"9882b71ab4702fa2103b44457cd24904",
		"7f57f0257a7deeb077d3effb3fc3736b",
		"60e3a4a3b6138fbb705a0edf7015f1a7",
		"bb798fc5e5b7187e266d1877923bebed",
		"56011973fcb9ca04513065894dae4887",
		"46033bb0d3c047936bd180a317a3920f",
		"b2fee75a8e7f4435bede5a1973a3f881",
		"ac1db9572622ed1ad745372d0162a141",
		"823562885c473f036b59dc94b07164fc",
		"7f00d1cf55ccac9d4f0e22a5734b8063",
		"cd05daf652c59feafc36c0ae38b43563",
		"e1c936e409652de6254face40fde67d0",
		"d4f7581c727e170cd001f29da90a3fde",
		"cbb103df4343c4105ecdddf6cb7ec935",
		"027756c75a185a8512dfe94de25895fc",
		"bbd295c9b4aabe66c5113cd0f7d8b51d",
	}
}

// uploadURLMember finds the capability URL in the answer of a state version create.
var uploadURLMember = regexp.MustCompile(`"hosted-state-upload-url":\s*"([^"]+)"`)

// roundTrip is one counted round trip.
type roundTrip struct {
	kind  string
	sql   string
	frame string
}

// roundTripTracer implements pgx.QueryTracer, pgx.BatchTracer,
// pgx.PrepareTracer and pgx.CopyFromTracer. It counts one round trip for each
// statement, batch, prepare and copy that the mode accepts.
type roundTripTracer struct {
	mu    sync.Mutex
	mode  traceMode
	trips []roundTrip
	// swapHook, when set, is the one-shot hook of the next statement that
	// locks the durable control tables of a swap. The tracer runs the hook on
	// the goroutine that sends the statement, before the statement goes out.
	swapHook func()
}

var (
	_ pgx.QueryTracer    = (*roundTripTracer)(nil)
	_ pgx.BatchTracer    = (*roundTripTracer)(nil)
	_ pgx.PrepareTracer  = (*roundTripTracer)(nil)
	_ pgx.CopyFromTracer = (*roundTripTracer)(nil)
)

func newRoundTripTracer() *roundTripTracer {
	return &roundTripTracer{mu: sync.Mutex{}, mode: traceOff, trips: nil, swapHook: nil}
}

// statementLabel shortens a statement to its sqlc name or its first characters.
func statementLabel(sql string) string {
	if m := sqlName.FindStringSubmatch(sql); m != nil {
		return m[1]
	}

	flat := strings.Join(strings.Fields(sql), " ")
	if len(flat) > maxStatementLen {
		flat = flat[:maxStatementLen] + "..."
	}

	return flat
}

// TraceQueryStart implements pgx.QueryTracer. A swap hook runs before the
// statement is recorded, so the hook adds no round trip of its own to the
// count.
func (r *roundTripTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	if hook := r.takeSwapHook(data.SQL); hook != nil {
		hook()
	}

	r.record("query", data.SQL)

	return ctx
}

// TraceQueryEnd implements pgx.QueryTracer.
func (*roundTripTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TraceBatchStart implements pgx.BatchTracer. A batch is one round trip.
func (r *roundTripTracer) TraceBatchStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceBatchStartData,
) context.Context {
	r.record("batch", fmt.Sprintf("batch of %d statements", data.Batch.Len()))

	return ctx
}

// TraceBatchQuery implements pgx.BatchTracer. The batch start counted it.
func (*roundTripTracer) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}

// TraceBatchEnd implements pgx.BatchTracer.
func (*roundTripTracer) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

// TracePrepareStart implements pgx.PrepareTracer.
func (r *roundTripTracer) TracePrepareStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TracePrepareStartData,
) context.Context {
	r.record("prepare", data.SQL)

	return ctx
}

// TracePrepareEnd implements pgx.PrepareTracer.
func (*roundTripTracer) TracePrepareEnd(context.Context, *pgx.Conn, pgx.TracePrepareEndData) {}

// TraceCopyFromStart implements pgx.CopyFromTracer.
func (r *roundTripTracer) TraceCopyFromStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceCopyFromStartData,
) context.Context {
	r.record("copy", "copy into "+strings.Join(data.TableName, "."))

	return ctx
}

// TraceCopyFromEnd implements pgx.CopyFromTracer.
func (*roundTripTracer) TraceCopyFromEnd(context.Context, *pgx.Conn, pgx.TraceCopyFromEndData) {}

// stackFacts is what the tracer reads in the stack of a round trip.
type stackFacts struct {
	serving bool
	backing bool
	// polling is true for a round trip of a loop whose turn count follows the
	// clock: a claim attempt or a heartbeat of the durable runner.
	polling bool
	caller  string
}

// inspectStack reads the stack of the goroutine that makes a round trip: does
// it serve an HTTP request, does it run in pkg/backup, and which function of
// the store calls the driver.
func inspectStack() stackFacts {
	pcs := make([]uintptr, maxTraceFrames)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])

	var facts stackFacts

	for {
		frame, more := frames.Next()

		facts.serving = facts.serving || strings.HasPrefix(frame.Function, serveFrame)
		facts.backing = facts.backing || strings.Contains(frame.Function, backupFrame)
		facts.polling = facts.polling || isTimerLoopFrame(frame.Function)

		if facts.caller == "" && strings.Contains(frame.Function, storeFrame) {
			facts.caller = fmt.Sprintf("%s:%d", frame.Function[strings.LastIndex(frame.Function, "/")+1:], frame.Line)
		}

		if !more {
			return facts
		}
	}
}

// isTimerLoopFrame reports whether a function of a stack is a store function
// of a loop that follows the clock. A claim attempt of the runner of durable
// operations runs on a timer: an idle worker claims once in each poll period.
// A heartbeat also runs on a timer. The number of attempts and of
// beats is the length of the run, so each of them, and the transaction around
// it, do not count.
//
// The match is exact: Claim and Beat, and the closures inside them. A method
// whose name only starts with Claim, such as ClaimTick, is work that counts.
func isTimerLoopFrame(function string) bool {
	return strings.Contains(function, storeFrame) && timerLoopMethod.MatchString(function)
}

// timerLoopMethod finds the end of the name of a function that is Claim or Beat
// of the durable operations of the store, or a closure in one of them.
var timerLoopMethod = regexp.MustCompile(`\)\.(?:Claim|Beat)(?:\.func\d+(?:\.\d+)*)?$`)

// accepts reports whether a round trip with the facts counts in the mode.
func (m traceMode) accepts(facts stackFacts) bool {
	switch m {
	case traceRequest:
		return facts.serving
	case traceBackup:
		return facts.backing && !facts.serving && !facts.polling
	case traceOff:
		return false
	}

	return false
}

// begin clears the count and begins to count in mode.
func (r *roundTripTracer) begin(mode traceMode) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.mode = mode
	r.trips = nil
}

// end ends the count and returns the round trips that it saw.
func (r *roundTripTracer) end() []roundTrip {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.mode = traceOff

	return r.trips
}

// record counts one round trip when the stack of the caller fits the mode.
func (r *roundTripTracer) record(kind, sql string) {
	r.mu.Lock()
	mode := r.mode
	r.mu.Unlock()

	if mode == traceOff {
		return
	}

	facts := inspectStack()
	if !mode.accepts(facts) {
		return
	}

	r.mu.Lock()
	r.trips = append(r.trips, roundTrip{kind: kind, sql: statementLabel(sql), frame: facts.caller})
	r.mu.Unlock()
}

// setSwapHook sets hook as the one-shot hook of the next statement that locks
// the durable control tables of a swap. The hook runs on the goroutine that
// sends the statement, before the statement goes out.
func (r *roundTripTracer) setSwapHook(hook func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.swapHook = hook
}

// takeSwapHook returns the hook of the first statement that locks the durable
// control tables of a swap, and clears it. It returns nil for every other
// statement and after the hook ran once, so the hook runs at most one time.
func (r *roundTripTracer) takeSwapHook(sql string) func() {
	if !strings.HasPrefix(sql, "LOCK TABLE ") || !strings.Contains(sql, ".durable_operations,") {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	hook := r.swapHook
	r.swapHook = nil

	return hook
}

// storeOptions returns the options that give the store of a cluster the tracer.
func (r *roundTripTracer) storeOptions() []postgres.Option {
	return []postgres.Option{postgres.WithQueryTracer(r)}
}

// checkBudget fails the test when the count is above the budget, or more than
// budgetSlack below it. It prints the statements of the flow.
func checkBudget(t *testing.T, name string, trips []roundTrip, budget int) {
	t.Helper()

	got := len(trips)

	switch {
	case got > budget:
		t.Errorf("%s: %d round trips, budget %d\n%s", name, got, budget, listTrips(trips))
	case got < budget-budgetSlack:
		t.Errorf("%s: %d round trips, budget %d: lower the budget to %d\n%s", name, got, budget, got, listTrips(trips))
	default:
		t.Logf("%s: %d round trips, budget %d", name, got, budget)
	}
}

// withoutPrepares returns the round trips that are not a prepare. A prepare
// happens once for each statement on each connection of the pool. Under load
// the pool serves a request on a connection that the warm-up did not use, and
// that connection prepares its statements again, so the number of prepares
// changes from run to run. The statement list keeps them.
func withoutPrepares(trips []roundTrip) []roundTrip {
	return filterTrips(trips, func(trip roundTrip) bool { return trip.kind != "prepare" })
}

// withoutTimerReads returns the round trips that are not one of four
// statements whose count follows the clock and not the work of the flow. The
// statement list keeps them. The count uses this list. The growth rule uses
// withoutTimerLoops, which keeps the two cache reads, so a read of either one
// for each row shows.
//
//   - ClaimDurableOperation and EndExpiredCanceledDurableOperations: the runner
//     of durable operations claims on a timer. A backup or a restore holds a
//     pkg/backup frame while it runs, so each tick of the timer in the run counts
//     as a round trip of the flow. The number of ticks is the length of the run.
//   - GetAuditChainState and GetInstallationState: the audit chain gate and the
//     maintenance gate cache their stored state for a limit. The first request,
//     or step of an operation, after the limit passes reads the state again. The
//     number of reads is the number of times the limit passed in the run.
func withoutTimerReads(trips []roundTrip) []roundTrip {
	return filterTrips(withoutTimerLoops(trips), func(trip roundTrip) bool {
		return trip.sql != "GetAuditChainState" && trip.sql != "GetInstallationState"
	})
}

// withoutTimerLoops returns the round trips that are not a statement of a loop
// that only polls the clock: the claim and the cleanup of the runner of durable
// operations. No row count changes the number of these statements.
func withoutTimerLoops(trips []roundTrip) []roundTrip {
	return filterTrips(trips, func(trip roundTrip) bool {
		return trip.sql != "ClaimDurableOperation" && trip.sql != "EndExpiredCanceledDurableOperations"
	})
}

// filterTrips returns the round trips that keep accepts.
func filterTrips(trips []roundTrip, keep func(roundTrip) bool) []roundTrip {
	out := make([]roundTrip, 0, len(trips))

	for _, trip := range trips {
		if keep(trip) {
			out = append(out, trip)
		}
	}

	return out
}

// listTrips returns the statement list of a flow, in order.
func listTrips(trips []roundTrip) string {
	var sb strings.Builder

	for i, trip := range trips {
		fmt.Fprintf(&sb, "%4d %-7s %-45s %s\n", i+1, trip.kind, trip.sql, trip.frame)
	}

	return sb.String()
}

// rtHarness drives the requests of the round trip flows.
type rtHarness struct {
	t       *testing.T
	cluster *servertest.Cluster
	client  *http.Client
	tracer  *roundTripTracer
	// warm makes measure run its body with no count.
	warm bool
	// mode is the mode that measure counts in.
	mode traceMode
	// trips holds the round trips of the last measure.
	trips []roundTrip

	serial    int
	workspace string
	stack     string
	nextStack int
}

// newRoundTripHarness starts a cluster whose store has the tracer, and a client
// for it.
func newRoundTripHarness(t *testing.T, mode traceMode, opts ...dustpan.Option) *rtHarness {
	t.Helper()

	tracer := newRoundTripTracer()
	cluster := servertest.StartWithStore(t, 1, tracer.storeOptions(), opts...)

	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)

	h := &rtHarness{
		t: t, cluster: cluster, client: &http.Client{Transport: transport, Timeout: requestLimit}, tracer: tracer,
		warm: false, mode: mode, trips: nil, serial: 0, workspace: "", stack: "", nextStack: 0,
	}

	h.call(http.MethodPost, "/api/server/namespaces", `{"name":"acme"}`, http.StatusCreated)
	h.call(http.MethodPost, "/api/dustpan/v1/namespaces/acme/collections", `{"name":"everything"}`, http.StatusCreated)
	h.call(
		http.MethodPost, "/api/dustpan/v1/namespaces/acme/placement-rules",
		`{"tool":"pulumi","project":"*","stack":"*","collection":"everything"}`, http.StatusCreated,
	)
	h.call(
		http.MethodPost, "/api/dustpan/v1/namespaces/acme/placement-rules",
		`{"tool":"opentofu","project":"*","stack":"*","collection":"everything"}`, http.StatusCreated,
	)

	return h
}

// measure runs body, and counts its round trips unless the flow runs warm-up.
func (h *rtHarness) measure(body func()) {
	if h.warm {
		body()

		return
	}

	h.tracer.begin(h.mode)
	body()
	h.trips = h.tracer.end()
}

// send sends a request and returns the status and the body.
func (h *rtHarness) send(method, target, auth, body string) (int, string) {
	h.t.Helper()

	req, err := http.NewRequestWithContext(h.t.Context(), method, target, strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("new request: %v", err)
	}

	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, target, err)
	}

	data, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil || readErr != nil {
		h.t.Fatalf("read answer of %s %s: %v %v", method, target, readErr, closeErr)
	}

	return resp.StatusCode, string(data)
}

// call sends a request with the token of the admin and fails the test when the
// status is not want.
func (h *rtHarness) call(method, path, body string, want int) string {
	h.t.Helper()

	scheme := "token "
	if strings.HasPrefix(path, "/api/server/") || strings.HasPrefix(path, "/api/dustpan/") ||
		strings.HasPrefix(path, "/api/v2/") {
		scheme = "Bearer "
	}

	status, answer := h.send(method, h.cluster.URL(0)+path, scheme+servertest.Token, body)
	if status != want {
		h.t.Fatalf("%s %s = %d %s, want %d", method, path, status, answer, want)
	}

	return answer
}

// decode reads the JSON answer into v.
func (h *rtHarness) decode(answer string, v any) {
	h.t.Helper()

	if err := json.Unmarshal([]byte(answer), v); err != nil {
		h.t.Fatalf("decode %s: %v", answer, err)
	}
}

// createWorkspace creates the workspace name and returns its ID.
func (h *rtHarness) createWorkspace(name string) string {
	h.t.Helper()

	answer := h.call(http.MethodPost, "/api/v2/organizations/acme/workspaces",
		`{"data":{"type":"workspaces","attributes":{"name":"`+name+`"}}}`, http.StatusCreated)

	var doc workspaceAnswer

	h.decode(answer, &doc)

	if doc.Data.ID == "" {
		h.t.Fatalf("workspace answer %s has no id", answer)
	}

	return doc.Data.ID
}

// createStack creates the stack name in the project web.
func (h *rtHarness) createStack(name string) {
	h.t.Helper()

	h.call(http.MethodPost, "/api/stacks/acme/web", `{"stackName":"`+name+`"}`, http.StatusOK)
}

// lock locks the workspace.
func (h *rtHarness) lock() {
	h.t.Helper()

	h.call(http.MethodPost, "/api/v2/workspaces/"+h.workspace+"/actions/lock",
		`{"data":{"attributes":{"reason":"round trips"}}}`, http.StatusOK)
}

// unlock unlocks the workspace.
func (h *rtHarness) unlock() {
	h.t.Helper()

	h.call(http.MethodPost, "/api/v2/workspaces/"+h.workspace+"/actions/unlock", "", http.StatusOK)
}

// upload declares a state version and puts its content: two requests.
func (h *rtHarness) upload() {
	h.t.Helper()

	h.serial++

	sums := stateSums()
	if h.serial > len(sums) {
		h.t.Fatalf("the flows upload %d states, and the table of sums holds %d", h.serial, len(sums))
	}

	content := `{"version":4,"terraform_version":"1.9.0","serial":` + strconv.Itoa(h.serial) +
		`,"lineage":"round-trips","outputs":{},"resources":[]}`

	answer := h.call(http.MethodPost, "/api/v2/workspaces/"+h.workspace+"/state-versions",
		`{"data":{"attributes":{"serial":`+strconv.Itoa(h.serial)+`,"lineage":"round-trips","md5":"`+
			sums[h.serial-1]+`"}}}`, http.StatusCreated)

	match := uploadURLMember.FindStringSubmatch(answer)
	if match == nil {
		h.t.Fatalf("state version answer %s has no upload URL", answer)
	}

	// The URL names the public host of the server. The test sends the PUT to
	// the listener of the server.
	target, err := url.Parse(match[1])
	if err != nil {
		h.t.Fatalf("upload URL %q: %v", match[1], err)
	}

	status, put := h.send(http.MethodPut, h.cluster.URL(0)+target.RequestURI(), "", content)
	if status != http.StatusOK {
		h.t.Fatalf("PUT upload = %d %s, want %d", status, put, http.StatusOK)
	}
}

// deployment returns a Pulumi deployment of n resources.
func deployment(n int) string {
	var sb strings.Builder

	sb.WriteString(`{"manifest":{"version":3},"resources":[`)
	sb.WriteString(
		`{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev","type":"pulumi:pulumi:Stack","outputs":{}}`,
	)

	for i := range n {
		fmt.Fprintf(&sb, `,{"urn":"urn:pulumi:dev::web::aws:s3/bucket:Bucket::res-%[1]d",`+
			`"type":"aws:s3/bucket:Bucket","custom":true,"id":"res-%[1]d","inputs":{"name":"res-%[1]d"},"outputs":{}}`, i)
	}

	sb.WriteString(`]}`)

	return sb.String()
}

// updateRun is one started update.
type updateRun struct {
	path  string
	lease string
}

// startUpdate creates and starts an update of the stack of the harness: two
// requests.
func (h *rtHarness) startUpdate() updateRun {
	h.t.Helper()

	base := "/api/stacks/acme/web/" + h.stack + "/update"

	var created struct {
		UpdateID string `json:"updateID"`
	}

	h.decode(h.call(http.MethodPost, base, "{}", http.StatusOK), &created)

	var started struct {
		Token string `json:"token"`
	}

	h.decode(h.call(http.MethodPost, base+"/"+created.UpdateID, "{}", http.StatusOK), &started)

	if started.Token == "" {
		h.t.Fatal("the start of the update gave no lease token")
	}

	return updateRun{path: base + "/" + created.UpdateID, lease: started.Token}
}

// save sends a verbatim checkpoint with the sequence number seq.
func (h *rtHarness) save(run updateRun, seq int) {
	h.t.Helper()

	body := `{"version":3,"untypedDeployment":{"version":3,"deployment":` + deployment(3) +
		`},"sequenceNumber":` + strconv.Itoa(seq) + `}`

	status, answer := h.send(http.MethodPatch, h.cluster.URL(0)+run.path+"/checkpointverbatim",
		"update-token "+run.lease, body)
	if status != http.StatusOK {
		h.t.Fatalf("checkpoint save %d = %d %s, want %d", seq, status, answer, http.StatusOK)
	}
}

// events posts one batch of n engine events, with the sequences 0 to n-1.
func (h *rtHarness) events(run updateRun, n int) {
	h.t.Helper()

	var b strings.Builder

	b.WriteString(`{"events":[`)

	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}

		b.WriteString(`{"sequence":` + strconv.Itoa(i) + `,"timestamp":1,"diagnosticEvent":{"message":"line ` +
			strconv.Itoa(i) + `","severity":"info"}}`)
	}

	b.WriteString(`]}`)

	status, answer := h.send(http.MethodPost, h.cluster.URL(0)+run.path+"/events/batch",
		"update-token "+run.lease, b.String())
	if status != http.StatusOK {
		h.t.Fatalf("event batch of %d = %d %s, want %d", n, status, answer, http.StatusOK)
	}
}

// complete ends the update.
func (h *rtHarness) complete(run updateRun) {
	h.t.Helper()

	status, answer := h.send(http.MethodPost, h.cluster.URL(0)+run.path+"/complete",
		"update-token "+run.lease, `{"status":"succeeded"}`)
	if status != http.StatusOK {
		h.t.Fatalf("complete = %d %s, want %d", status, answer, http.StatusOK)
	}
}

// newStack gives the harness a fresh stack, so that each run of a flow starts
// from the same state.
func (h *rtHarness) newStack() {
	h.t.Helper()

	h.nextStack++
	h.stack = "rt" + strconv.Itoa(h.nextStack)
	h.createStack(h.stack)
}

// measureStackCall measures one call of the admin to the stack of the harness,
// at the path of the stack with suffix.
func (h *rtHarness) measureStackCall(method, suffix, body string, want int) {
	h.t.Helper()

	path := "/api/stacks/acme/web/" + h.stack + suffix
	h.measure(func() { h.call(method, path, body, want) })
}

// requestFlow is one request flow and its budget. The run measures its body
// only: the setup and the cleanup of a flow do not count.
type requestFlow struct {
	name   string
	budget int
	run    func(h *rtHarness)
}

// requestFlows returns the request flows and their budgets, in the order that
// they run.
//
// A lock reads the facts of its unit in one read transaction and the gate
// rows of its unit in a second one, one time for the access limits and the
// gates together. Both key on the stable ID of the unit and not on its name:
// the read of the facts first reads that ID from the inventory, and the read
// of the gate rows finds it in its transaction. So "lock" and "lock, upload
// and unlock" each hold 2 round trips for those reads.
//
// Each save of a workspace lock also writes the last released lock of the
// workspace to its inventory row, with one update in the same transaction.
// So "lock" and "unlock" each hold 1 round trip for that write, and "lock,
// upload and unlock" holds 2.
//
// A lock and the declare of a state version refuse a workspace that no
// collection holds. Each check reads the unit and its membership with one
// statement and no transaction. So "lock" and "opentofu upload" each hold 1
// round trip for it, and "lock, upload and unlock" holds 2.
//
// Each check of the state.write gates first reads the principal record of the
// caller with one query, for its groups and its break-glass mark. So "lock",
// "lock, upload and unlock" and each pulumi flow that meets the gates hold 1
// round trip for that read.
func requestFlows() []requestFlow {
	return []requestFlow{
		{"opentofu upload", 42, func(h *rtHarness) {
			h.lock()
			h.measure(h.upload)
			h.unlock()
		}},
		{"lock", 34, func(h *rtHarness) {
			h.measure(h.lock)
			h.unlock()
		}},
		{"unlock", 11, func(h *rtHarness) {
			h.lock()
			h.measure(h.unlock)
		}},
		{"lock, upload and unlock", 91, func(h *rtHarness) {
			h.measure(func() {
				h.lock()
				h.upload()
				h.unlock()
			})
		}},
		{"pulumi checkpoint save", 13, func(h *rtHarness) {
			h.newStack()
			run := h.startUpdate()
			h.measure(func() { h.save(run, 1) })
			h.complete(run)
		}},
		// The budget is 62. The second save reads the blob guard after the
		// commit that replaces the previous checkpoint blob, so that a backup
		// that started before the commit cannot lose the blob. The
		// transaction of the create writes the record of the item key of the
		// update, which seals its event payloads at rest: a read of the
		// record, a read of the namespace, the insert and a read back.
		{"pulumi update cycle", 62, func(h *rtHarness) {
			h.newStack()
			h.measure(func() {
				run := h.startUpdate()
				h.save(run, 1)
				h.save(run, 2)
				h.complete(run)
			})
		}},
		// The budget is 1010. The batch opens the item key of the update one
		// time for all its events: a read of the key record and a read of
		// the namespace, then one seal for each event in memory. The store
		// writes each event with its own insert, so 1000 of the round trips
		// are the inserts of the rows.
		{"pulumi event batch of 1000 events", 1010, func(h *rtHarness) {
			h.newStack()
			run := h.startUpdate()
			h.measure(func() { h.events(run, eventBatchSize) })
			h.complete(run)
		}},
		// The stack rm, import, restore, rename and cancel each meet the gates
		// of state.write before the write: one read of the unit facts, one read
		// of the gate configuration for the access limits and the gates
		// together, and the write of the decision record with one statement in
		// its own fenced transaction are part of each budget. A stack is in a
		// collection, so each of these flows also reads or deletes the
		// membership of the unit: one more round trip in each budget.
		{"pulumi stack rm", 39, func(h *rtHarness) {
			h.newStack()
			h.measureStackCall(http.MethodDelete, "", "", http.StatusNoContent)
		}},
		{"pulumi stack import", 66, func(h *rtHarness) {
			h.newStack()
			body := `{"version":3,"deployment":` + deployment(3) + `}`
			h.measureStackCall(http.MethodPost, "/import", body, http.StatusOK)
		}},
		{"pulumi stack restore", 67, func(h *rtHarness) {
			h.newStack()
			run := h.startUpdate()
			h.save(run, 1)
			h.complete(run)
			h.measureStackCall(http.MethodPost, "/restore/1", "", http.StatusOK)
		}},
		{"pulumi stack rename", 36, func(h *rtHarness) {
			h.newStack()
			body := `{"newName":"` + h.stack + `-renamed"}`
			h.measureStackCall(http.MethodPost, "/rename", body, http.StatusOK)
		}},
		{"pulumi cancel", 32, func(h *rtHarness) {
			h.newStack()
			run := h.startUpdate()
			h.measure(func() { h.call(http.MethodPost, run.path+"/cancel", "", http.StatusOK) })
		}},
	}
}

// TestRoundTripsOfRequests counts the round trips of each request flow on a
// warm server. Each flow has its own server.
func TestRoundTripsOfRequests(t *testing.T) {
	t.Parallel()

	for _, flow := range requestFlows() {
		t.Run(flow.name, func(t *testing.T) {
			t.Parallel()

			h := newRoundTripHarness(t, traceRequest, dustpan.WithPublicURL("https://dustpan.example.com"))
			h.workspace = h.createWorkspace("web")
			h.createStack("base")

			h.warm = true
			for range warmCycles {
				flow.run(h)
			}

			h.warm = false
			h.trips = nil
			flow.run(h)

			if h.trips == nil {
				t.Fatalf("%s: the flow measured nothing", flow.name)
			}

			checkBudget(t, flow.name, withoutTimerReads(withoutPrepares(h.trips)), flow.budget)
		})
	}
}

// seedUnits creates stacks and workspaces until the namespace holds count of
// each. Each stack gets one finished update with one checkpoint, and each
// workspace gets one locked and unlocked state version, so that the tables of
// updates, checkpoints, events, history, state versions and audit entries hold
// rows that grow with the count, and not only the tables of the units.
func (h *rtHarness) seedUnits(count int) {
	h.t.Helper()

	for ; h.nextStack < count; h.nextStack++ {
		name := "s" + strconv.Itoa(h.nextStack)
		h.createStack(name)

		h.stack = name
		run := h.startUpdate()
		h.save(run, 1)
		h.complete(run)

		h.workspace = h.createWorkspace("w" + strconv.Itoa(h.nextStack))
		h.serial = 0

		h.lock()
		h.upload()
		h.unlock()
	}
}

// settle waits until the operation at path ends, and fails the test when it
// does not succeed. A state in running names an operation that still works.
func (h *rtHarness) settle(path string, running ...string) {
	h.t.Helper()

	deadline := time.Now().Add(settleLimit)

	for time.Now().Before(deadline) {
		var doc struct {
			State string `json:"state"`
			Error string `json:"error"`
		}

		h.decode(h.call(http.MethodGet, path, "", http.StatusOK), &doc)

		switch {
		case doc.State == "succeeded":
			return
		case slices.Contains(running, doc.State):
			select {
			case <-h.t.Context().Done():
				h.t.Fatalf("wait for %s: %v", path, h.t.Context().Err())
			case <-time.After(settleEvery):
			}
		default:
			h.t.Fatalf("%s ended in state %q: %s", path, doc.State, doc.Error)
		}
	}

	h.t.Fatalf("%s did not end in time", path)
}

// settleOperations waits until no backup or restore operation runs or waits.
// The state of a backup or a restore reads succeeded before the runner ends
// the claim of its operation, and the end of the claim makes 4 more round
// trips (lock, clock, end, commit). The end of the claim is the last write of
// the flow, so after this wait the trace holds all of the flow.
func (h *rtHarness) settleOperations() {
	h.t.Helper()

	deadline := time.Now().Add(settleLimit)

	for time.Now().Before(deadline) {
		ended := 0

		for _, kind := range []durable.Kind{backup.OperationKind, backup.RestoreOperationKind} {
			ops, err := h.cluster.Operations(h.t, 0).
				List(h.t.Context(), durable.Filter{Kind: kind, States: nil, Limit: 0})
			if err != nil {
				h.t.Fatalf("list %s operations: %v", kind, err)
			}

			if !slices.ContainsFunc(ops, func(op durable.Operation) bool { return !op.State.Terminal() }) {
				ended++
			}
		}

		if ended == 2 {
			return
		}

		select {
		case <-h.t.Context().Done():
			h.t.Fatalf("wait for the end of the operations: %v", h.t.Context().Err())
		case <-time.After(settleEvery):
		}
	}

	h.t.Fatal("the backup and restore operations did not end their claims in time")
}

// backupRun holds the round trips of a backup and of the restore of it.
type backupRun struct {
	backup  []roundTrip
	restore []roundTrip
}

// measuredRun holds the round trips of a backup and of a restore in two lists.
// The count check reads the first list. The growth rule reads the second, which
// keeps the cache reads, so that a read for each row shows.
type measuredRun struct {
	backup, restore             []roundTrip
	backupGrowth, restoreGrowth []roundTrip
}

// runBackupAndRestore runs a backup, then a restore of it.
func (h *rtHarness) runBackupAndRestore() backupRun {
	h.t.Helper()

	return h.runBackupAndRestoreWith(nil)
}

// runBackupAndRestoreWith runs a backup, then a restore of it. When
// beforeRestore is set, the restore starts after it, and the function that it
// returns runs after the restore ends, outside of the count.
func (h *rtHarness) runBackupAndRestoreWith(beforeRestore func() func()) backupRun {
	h.t.Helper()

	var out backupRun

	h.tracer.begin(traceBackup)

	var started struct {
		ID string `json:"id"`
	}

	h.decode(h.call(http.MethodPost, "/api/server/backups", "", http.StatusAccepted), &started)
	h.settle("/api/server/backups/"+started.ID, string(backup.StateRunning))
	h.settleOperations()

	out.backup = h.tracer.end()

	finish := func() {}
	if beforeRestore != nil {
		finish = beforeRestore()
	}

	h.tracer.begin(traceBackup)

	var restore struct {
		ID string `json:"id"`
	}

	h.decode(h.call(http.MethodPost, "/api/server/restores", `{"backupID":"`+started.ID+`"}`, http.StatusAccepted),
		&restore)
	h.settle("/api/server/restores/"+restore.ID, "running", "checking", "applying")
	h.settleOperations()

	out.restore = h.tracer.end()

	finish()

	return out
}

// backupSizes are the data sets of the backup flow. The large set is ten
// times the small one, so a statement for each row shows in the growth check.
func backupSizes() []backupSize {
	return []backupSize{
		{name: "3 stacks and 3 workspaces", units: 3},
		{name: "30 stacks and 30 workspaces", units: 30},
	}
}

// backupSize is one data set of the backup flow: units stacks and units
// workspaces.
type backupSize struct {
	name  string
	units int
}

// TestRoundTripsOfBackup counts the round trips of a backup and of a restore
// of two data sets, and checks them against the count of the cost model and
// against the growth rule. Each size has its own server.
func TestRoundTripsOfBackup(t *testing.T) {
	t.Parallel()

	sizes := backupSizes()
	runs := make([]measuredRun, len(sizes))

	// The parallel subtests end before the cleanup functions of this test
	// run, so the checks read the runs after every write.
	t.Cleanup(func() {
		if t.Failed() {
			return
		}

		checkBackupRuns(t, sizes, runs)
	})

	for i, size := range sizes {
		t.Run(size.name, func(t *testing.T) {
			t.Parallel()

			target, err := fs.Open(t.TempDir())
			if err != nil {
				t.Fatalf("open backup target: %v", err)
			}

			h := newRoundTripHarness(t, traceBackup, dustpan.WithBackupTarget(target, "test"),
				dustpan.WithOperationLease(roundTripLease))
			h.seedUnits(size.units)

			run := h.runBackupAndRestore()
			runs[i] = measuredRun{
				backup:        counted(run.backup),
				restore:       counted(run.restore),
				backupGrowth:  withoutTimerLoops(withoutPrepares(run.backup)),
				restoreGrowth: withoutSwapRetries(withoutTimerLoops(withoutPrepares(run.restore))),
			}
		})
	}
}

// checkBackupRuns checks the counted runs of each size against the counts of
// the cost models and against the growth rule.
func checkBackupRuns(t *testing.T, sizes []backupSize, runs []measuredRun) {
	t.Helper()

	for i, size := range sizes {
		schema := currentSchemaCounts(t, size.units)

		checkCount(t, "backup of "+size.name, runs[i].backup, backupModel().count(schema))
		checkCount(t, "restore of "+size.name, runs[i].restore, restoreModel().count(schema))
	}

	small, large := runs[0], runs[len(runs)-1]
	extraUnits := sizes[len(sizes)-1].units - sizes[0].units

	reportGrowth(t, "backup", small.backupGrowth, large.backupGrowth, backupGrowthAllowance(extraUnits))
	reportGrowth(t, "restore", small.restoreGrowth, large.restoreGrowth, restoreGrowthAllowance(extraUnits))

	// Mutation check on the measured runs: a statement for each stack and for
	// each workspace, added to the large run, must fail the growth rule. The
	// two cache reads are in the check, because the count leaves them out.
	for _, flow := range []struct {
		name         string
		small, large []roundTrip
		allowance    map[string]int
	}{
		{"backup", small.backupGrowth, large.backupGrowth, backupGrowthAllowance(extraUnits)},
		{"restore", small.restoreGrowth, large.restoreGrowth, restoreGrowthAllowance(extraUnits)},
	} {
		for _, statement := range []string{"ReadOneUnit", "GetAuditChainState", "GetInstallationState"} {
			mutated := slices.Concat(flow.large, perRowTrips(statement, 2*sizes[len(sizes)-1].units))
			if len(growthProblems(flow.small, mutated, flow.allowance)) == 0 {
				t.Errorf("%s: the growth rule did not catch %s for each row", flow.name, statement)
			}
		}
	}
}

// counted returns the round trips of a backup or a restore that depend on its
// work and not on the clock.
func counted(trips []roundTrip) []roundTrip {
	return withoutSwapRetries(withoutTimerReads(withoutPrepares(trips)))
}

// swapStartSQL is the first statement of each attempt of the swap step of a
// restore: the read that checks that the staged schema exists.
const swapStartSQL = "SELECT count(*) FROM pg_namespace WHERE nspname = $1"

// withoutSwapRetries returns the round trips of the last attempt of the swap
// step of a restore, and drops each earlier attempt. The swap transaction
// rolls back when PostgreSQL ends it to break a deadlock with another
// transaction on the durable tables, and the runner of durable operations
// then ends the claim of the step and runs the step again. Whether a
// transaction of the runner overlaps the swap depends on the timing of the
// machine, so a failed attempt is not work of the restore. An attempt runs
// from its first statement to the first statement of the next attempt, so
// the end of the claim after a failed attempt goes with it.
func withoutSwapRetries(trips []roundTrip) []roundTrip {
	var starts []int

	for i, trip := range trips {
		if strings.HasPrefix(trip.sql, swapStartSQL) {
			starts = append(starts, i)
		}
	}

	if len(starts) < 2 {
		return trips
	}

	last := starts[len(starts)-1]

	return slices.Concat(trips[:starts[0]], trips[last:])
}

// schemaCounts holds the sizes that the cost of a backup and of a restore
// follow.
type schemaCounts struct {
	// tables is the number of tables that a backup exports.
	tables int
	// otherTables is the number of tables that a backup leaves out: the control
	// tables and the emptied tables.
	otherTables int
	// migrations is the number of migrations that the binary embeds.
	migrations int
	// units is the number of stacks, which is also the number of workspaces,
	// in the data set. Each workspace holds one state version.
	units int
}

// currentSchemaCounts reads the sizes from the lists that a new table must
// join: the table lists of pkg/backup and the embedded migrations.
func currentSchemaCounts(t *testing.T, units int) schemaCounts {
	t.Helper()

	names, _, err := postgres.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("list embedded migrations: %v", err)
	}

	return schemaCounts{
		tables:      len(backup.Tables),
		otherTables: len(backup.ControlTables) + len(backup.EmptiedTables),
		migrations:  len(names),
		units:       units,
	}
}

// costModel is the cost of a flow in round trips, as a function of the schema.
// The coefficients are the measured cost of one more table, one more
// control table, one more migration and one more unit. The restore checks the columns of
// each table and counts its rows, and it applies each migration to a staging
// schema. The fixed part holds the rest of the flow.
//
// The count of a run is exact: every run counts the same round trips, also
// under load, so the test asks for the count of the model and allows no
// spread. A table with the usual cost needs no edit here. A table, a step or
// a statement that changes the count changes the model in the same change:
// change the coefficient or the fixed part, and say why.
type costModel struct {
	fixed        int
	perTable     int
	perOther     int
	perMigration int
	perUnit      int
}

// count returns the count of round trips of the flow on a schema.
func (m costModel) count(c schemaCounts) int {
	return m.fixed + m.perTable*c.tables + m.perOther*c.otherTables + m.perMigration*c.migrations +
		m.perUnit*c.units
}

// backupModel is the cost model of a backup. Its coefficients were fitted to
// three commits that differ in tables, control tables and migrations, and a
// fourth commit checks them (see TestCostModelFitsMeasuredHistory).
func backupModel() costModel {
	return costModel{fixed: 47, perTable: 3, perOther: 1, perMigration: 0, perUnit: stateVersionReads}
}

// restoreModel is the cost model of a restore, fitted the same way. The cost of
// a migration and of a unit (the maintenance scans and the state version
// reads) come from the statement counts of a run, and the commits fit the
// rest. A migration costs 2 round trips in the staging schema: the migration
// and its record in schema_migrations. The fixed part holds the 2 reads of
// the columns of schema_migrations, which the apply makes once.
//
// The fixed part holds the backup before the swap, with the work of its
// durable operation: the restore starts that backup with its own claim and
// runs it on its own goroutine, so the count holds the start, the steps, the
// end of the claim and the two reads of the wait for the row. It also holds 4
// round trips for the name locks: the restore keeps the name_locks and
// name_lock_key tables of the installation, so that a restore cannot clear a
// lockout, and each kept table costs one delete and one copy.
//
// The restore keeps the used_tickets table too, so that a restore cannot let
// a ticket that a server used work again. The measured cost of that table is
// 1 round trip more than perOther gives, so the fixed part holds 1 more.
//
// perOther counts 2 round trips for each table that a backup leaves out. An
// emptied table that the restore only reads for its column check costs 1. So
// the fixed part takes 1 off for passkey_challenges, the table of the open
// passkey ceremonies, which a restore leaves empty.
//
// The fixed part takes 2 off for each table of Tables that the dev strip never
// counts, group_mappings and member_removals. The run makes 5 round trips for
// one of those tables, 2 fewer than the cost of a table in the model: the dev
// strip counts no rows in it, and the other statements for it are the staging
// count and the read of the backup.
//
// The fixed part also holds forgeStripClassTrips: the dev strip counts the rows
// of the two Git host classes that a principal owns, one statement for each,
// as it counts the eleven classes that were there before them.
//
// The fixed part takes breakGlassReviewTrips off for break_glass_reviews.
//
// The fixed part also holds stagedJobCheckTrips: the staged check of the job
// rows reads the job rows of the staging schema in one statement, once for
// each restore.
//
// The fixed part also holds removalAlertTrips for member_removal_alerts, a
// table of Tables that the dev strip never counts either.
func restoreModel() costModel {
	return costModel{
		fixed:        342 + 1 + forgeStripClassTrips - breakGlassReviewTrips + stagedJobCheckTrips + removalAlertTrips,
		perTable:     7,
		perOther:     2,
		perMigration: 2,
		perUnit:      maintenanceScans + stateVersionReads,
	}
}

// forgeStripClassTrips is the number of round trips of the two classes of the
// Git host links that the dev strip counts for each principal class: the links
// (forge_account_links) and the link requests (forge_link_requests). The strip
// runs one COUNT per class, once for each run and once for each attempt of the
// swap step, and not once for each row or unit. The restore counts at 3 and at
// 30 units both hold these 2 round trips, so the count does not grow with the
// units.
const forgeStripClassTrips = 2

// breakGlassReviewTrips is the number of round trips that the restore of the
// table break_glass_reviews makes fewer than perTable gives. The measured run
// makes 6 round trips for that table, not 7, at 3 and at 30 units, so the
// difference does not grow with the units.
const breakGlassReviewTrips = 1

// stagedJobCheckTrips is the number of round trips of the staged check of the
// job rows: the check reads the job rows of the staging schema in one
// statement, once for each restore. It does not grow with the units or the
// tables.
const stagedJobCheckTrips = 1

// removalAlertTrips is the change in the fixed part for the table
// member_removal_alerts. Like member_removals, it costs 2 round trips fewer
// than a table in the model, because the dev strip counts no rows in it. The
// restore reads the staged rows of the table once more, to refuse an alert
// marker after the time of the restore, which adds 1 round trip.
const removalAlertTrips = -2 + 1

// checkCount fails the test when the count is not the count of the model,
// and shows the statement list of the run.
func checkCount(t *testing.T, name string, trips []roundTrip, want int) {
	t.Helper()

	if got := len(trips); got != want {
		t.Errorf("%s: %d round trips, want %d\n%s", name, got, want, listTrips(trips))

		return
	}

	t.Logf("%s: %d round trips, as the model gives", name, want)
}

// growthSlack is the number of round trips by which one statement may differ
// between the small and the large run, above its allowance. The runs are
// exact, so no statement may differ: each one differs by its allowance.
const growthSlack = 0

// maintenanceScans is the number of times that a restore reads the active work
// of the installation: when it enters maintenance mode, and when it leaves.
// Each scan reads the active update of each stack with one statement, so the
// restore makes this many reads for each stack.
const maintenanceScans = 2

// stateVersionReads is the number of statements that a backup, and the restore
// that holds one, makes for each state version: the read of the state version,
// of its workspace and of the secret key of its scope.
const stateVersionReads = 3

// backupGrowthAllowance names the statements of a backup that grow with the rows, and
// by how much. The two allowances are known reads for each row:
//
//   - A backup reads each state version, its workspace and its secret key with
//     one statement each (stateVersionReads in all). A restore holds a backup, so
//     it makes the same reads.
//   - Each maintenance scan of a restore reads the active update of each stack
//     with one statement.
//
// A batch read removes the allowance and the perUnit term of the cost model.
// extraUnits is the difference of units between the two runs.
func backupGrowthAllowance(extraUnits int) map[string]int {
	return map[string]int{
		"query GetStateVersion":   extraUnits,
		"query ReadWorkspaceByID": extraUnits,
		"query GetSecretKey":      extraUnits,
	}
}

// restoreGrowthAllowance is the allowance of a restore: the reads of the backup
// that it holds, and the reads of the maintenance scans.
func restoreGrowthAllowance(extraUnits int) map[string]int {
	out := backupGrowthAllowance(extraUnits)
	out["query ActiveUpdate"] = maintenanceScans * extraUnits

	return out
}

// quotedName finds a quoted identifier, which may be cut at the end of a
// shortened statement.
var quotedName = regexp.MustCompile(`"[^"]*("|$)`)

// growthLabel names a statement for the growth rule. The restore makes
// schemas with random names, so the label hides each quoted identifier.
func growthLabel(trip roundTrip) string {
	return trip.kind + " " + quotedName.ReplaceAllString(trip.sql, `"?"`)
}

// growthProblems compares the small run and the large run, statement by
// statement. It returns one line for each statement that grew by more than
// growthSlack plus its allowance.
func growthProblems(small, large []roundTrip, allowance map[string]int) []string {
	counts := map[string][2]int{}

	for _, trip := range small {
		c := counts[growthLabel(trip)]
		c[0]++
		counts[growthLabel(trip)] = c
	}

	for _, trip := range large {
		c := counts[growthLabel(trip)]
		c[1]++
		counts[growthLabel(trip)] = c
	}

	var out []string

	for label, c := range counts {
		if grown := c[1] - c[0]; grown > growthSlack+allowance[label] {
			out = append(out, fmt.Sprintf("%s: %d in the small run, %d in the large run, allowed growth %d",
				label, c[0], c[1], growthSlack+allowance[label]))
		}
	}

	slices.Sort(out)

	return out
}

// reportGrowth fails the test for each statement that grows with the rows.
func reportGrowth(t *testing.T, name string, small, large []roundTrip, allowance map[string]int) {
	t.Helper()

	for _, problem := range growthProblems(small, large, allowance) {
		t.Errorf("%s grows with the rows: %s", name, problem)
	}
}

// perRowTrips returns n round trips of one statement. A test uses it to model a
// loop that queries once for each row.
func perRowTrips(statement string, n int) []roundTrip {
	out := make([]roundTrip, n)
	for i := range out {
		out[i] = roundTrip{kind: "query", sql: statement, frame: "test"}
	}

	return out
}
