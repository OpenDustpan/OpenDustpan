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
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// This file holds the regression benchmark of database round trips: for each
// flow, the number of round trips that the server makes to PostgreSQL. A
// budget bounds each count from above, so a change that adds a round trip
// fails. For a request flow the budget also bounds the count from below, so a
// change that removes round trips must lower the budget, and the budgets only
// go down. A backup and a restore have only the upper bound, because their
// count changes from run to run with the timing of the operation. The test log
// shows the measured count of each flow. Each flow runs on its own server, in a
// parallel subtest.

// budgetSlack is the number of round trips that a count of a request flow may
// be below its budget before the test asks for a lower budget.
const budgetSlack = 3

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
}

var (
	_ pgx.QueryTracer    = (*roundTripTracer)(nil)
	_ pgx.BatchTracer    = (*roundTripTracer)(nil)
	_ pgx.PrepareTracer  = (*roundTripTracer)(nil)
	_ pgx.CopyFromTracer = (*roundTripTracer)(nil)
)

func newRoundTripTracer() *roundTripTracer {
	return &roundTripTracer{mu: sync.Mutex{}, mode: traceOff, trips: nil}
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

// TraceQueryStart implements pgx.QueryTracer.
func (r *roundTripTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
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

		if facts.caller == "" && strings.Contains(frame.Function, storeFrame) {
			facts.caller = fmt.Sprintf("%s:%d", frame.Function[strings.LastIndex(frame.Function, "/")+1:], frame.Line)
		}

		if !more {
			return facts
		}
	}
}

// accepts reports whether a round trip with the facts counts in the mode.
func (m traceMode) accepts(facts stackFacts) bool {
	switch m {
	case traceRequest:
		return facts.serving
	case traceBackup:
		return facts.backing && !facts.serving
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

// checkCeiling fails the test when the count is above the budget. A count below
// the budget passes, and the log shows it. The budget is the highest count of
// repeated runs under load plus 4.
func checkCeiling(t *testing.T, name string, trips []roundTrip, budget int) {
	t.Helper()

	got := len(trips)
	if got > budget {
		t.Errorf("%s: %d round trips, budget %d\n%s", name, got, budget, listTrips(trips))

		return
	}

	t.Logf("%s: %d round trips, budget %d", name, got, budget)
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
// statement list keeps them.
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
	return filterTrips(trips, func(trip roundTrip) bool {
		switch trip.sql {
		case "ClaimDurableOperation", "EndExpiredCanceledDurableOperations", "GetAuditChainState",
			"GetInstallationState":
			return false
		default:
			return true
		}
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
	if strings.HasPrefix(path, "/api/server/") || strings.HasPrefix(path, "/api/v2/") {
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

// requestFlow is one request flow and its budget. The run measures its body
// only: the setup and the cleanup of a flow do not count.
type requestFlow struct {
	name   string
	budget int
	run    func(h *rtHarness)
}

// requestFlows returns the request flows and their budgets, in the order that
// they run.
func requestFlows() []requestFlow {
	return []requestFlow{
		{"opentofu upload", 44, func(h *rtHarness) {
			h.lock()
			h.measure(h.upload)
			h.unlock()
		}},
		{"lock", 42, func(h *rtHarness) {
			h.measure(h.lock)
			h.unlock()
		}},
		{"unlock", 10, func(h *rtHarness) {
			h.lock()
			h.measure(h.unlock)
		}},
		{"lock, upload and unlock", 100, func(h *rtHarness) {
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
		{"pulumi update cycle", 60, func(h *rtHarness) {
			h.newStack()
			h.measure(func() {
				run := h.startUpdate()
				h.save(run, 1)
				h.save(run, 2)
				h.complete(run)
			})
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
// each.
func (h *rtHarness) seedUnits(count int) {
	h.t.Helper()

	for ; h.nextStack < count; h.nextStack++ {
		h.createStack("s" + strconv.Itoa(h.nextStack))
		h.createWorkspace("w" + strconv.Itoa(h.nextStack))
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

// backupRun holds the round trips of a backup and of the restore of it.
type backupRun struct {
	backup  []roundTrip
	restore []roundTrip
}

// runBackupAndRestore runs a backup, then a restore of it.
func (h *rtHarness) runBackupAndRestore() backupRun {
	h.t.Helper()

	var out backupRun

	h.tracer.begin(traceBackup)

	var started struct {
		ID string `json:"id"`
	}

	h.decode(h.call(http.MethodPost, "/api/server/backups", "", http.StatusAccepted), &started)
	h.settle("/api/server/backups/"+started.ID, string(backup.StateRunning))

	out.backup = h.tracer.end()

	h.tracer.begin(traceBackup)

	var restore struct {
		ID string `json:"id"`
	}

	h.decode(h.call(http.MethodPost, "/api/server/restores", `{"backupID":"`+started.ID+`"}`, http.StatusAccepted),
		&restore)
	h.settle("/api/server/restores/"+restore.ID, "running", "checking", "applying")

	out.restore = h.tracer.end()

	return out
}

// TestRoundTripsOfBackup counts the round trips of a backup and of a restore
// of 3 stacks and 3 workspaces, and of 30 stacks and 30 workspaces, so that a
// term that grows with the data shows. Each size has its own server.
func TestRoundTripsOfBackup(t *testing.T) {
	t.Parallel()

	for _, size := range []struct {
		name                        string
		units                       int
		backupBudget, restoreBudget int
	}{
		{"3 stacks and 3 workspaces", 3, 305, 1010},
		{"30 stacks and 30 workspaces", 30, 305, 1055},
	} {
		t.Run(size.name, func(t *testing.T) {
			t.Parallel()

			target, err := fs.Open(t.TempDir())
			if err != nil {
				t.Fatalf("open backup target: %v", err)
			}

			h := newRoundTripHarness(t, traceBackup, dustpan.WithBackupTarget(target, "test"))
			h.seedUnits(size.units)

			run := h.runBackupAndRestore()

			checkCeiling(t, "backup of "+size.name, withoutTimerReads(withoutPrepares(run.backup)), size.backupBudget)
			checkCeiling(
				t,
				"restore of "+size.name,
				withoutTimerReads(withoutPrepares(run.restore)),
				size.restoreBudget,
			)
		})
	}
}
