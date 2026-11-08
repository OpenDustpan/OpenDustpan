package dustpan

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The errors of the fakes in this file.
var (
	errNamespaceRead = errors.New("namespace read failed")
	errAuditAppend   = errors.New("audit append failed")
)

// fakeAuditStore keeps the entries that the adapter appends.
type fakeAuditStore struct {
	audit.Store

	entries []audit.Entry
	fences  []namespace.Fence
	err     error
}

// Append implements audit.Store.
func (s *fakeAuditStore) Append(
	_ context.Context, fence namespace.Fence, e audit.Entry,
) (audit.Entry, error) {
	if s.err != nil {
		return audit.Entry{}, s.err
	}

	s.entries = append(s.entries, e)
	s.fences = append(s.fences, fence)

	return e, nil
}

// fakeAuditServerStore keeps the entries that the adapter appends in the
// installation scope.
type fakeAuditServerStore struct {
	audit.ServerStore

	entries []audit.Entry
	err     error
}

// AppendServer implements audit.ServerStore.
func (s *fakeAuditServerStore) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if s.err != nil {
		return audit.Entry{}, s.err
	}

	s.entries = append(s.entries, e)

	return e, nil
}

// fakeNamespaceStore returns the namespace of the test or one error.
type fakeNamespaceStore struct {
	namespace.Store

	ns  namespace.Namespace
	err error
}

// GetNamespace implements namespace.Store.
func (s *fakeNamespaceStore) GetNamespace(context.Context, namespace.Name) (namespace.Namespace, error) {
	if s.err != nil {
		return namespace.Namespace{}, s.err
	}

	return s.ns, nil
}

// jobTokenRefusalUnit returns the unit of the adapter test.
func jobTokenRefusalUnit(t *testing.T) identity.Object {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	unit, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	return unit.WithUnitID("unt-1")
}

// jobTokenTestNamespace returns the namespace of unit with failover version 7.
func jobTokenTestNamespace(unit identity.Object) namespace.Namespace {
	var ns namespace.Namespace

	ns.ID = namespace.ID("ns-1")
	ns.Name = unit.Namespace()
	ns.FailoverVersion = 7

	return ns
}

// zeroFence returns the fence with no value.
func zeroFence() namespace.Fence {
	return namespace.Fence{Namespace: "", FailoverVersion: 0}
}

// jobTokenTestSpec returns a spec of unit with op.
func jobTokenTestSpec(unit identity.Object, op job.Operation) job.Spec {
	var spec job.Spec

	spec.Unit = unit
	spec.Operation = op
	spec.Executor = job.ExecutorRunner

	return spec
}

// jobTokenTestJob returns a stored job of spec and source.
func jobTokenTestJob(spec job.Spec, source job.SourceKind) job.Job {
	var stored job.Job

	stored.ID = "job-1"
	stored.Spec = spec
	stored.Source = source

	return stored
}

// refusalRecordCase is one row of the refusal recorder table.
type refusalRecordCase struct {
	name        string
	rec         identity.JobTokenRefusal
	entries     *fakeAuditStore
	server      *fakeAuditServerStore
	namespaces  *fakeNamespaceStore
	noClock     bool
	wantErr     error
	wantNS      namespace.Name
	wantFence   namespace.Fence
	wantSubject string
}

// TestJobTokenRefusalsRecord proves that the adapter writes one security entry
// with the outcome denied, in the namespace of the unit or in the installation
// scope, and that it fails closed when it cannot read or append.
func TestJobTokenRefusalsRecord(t *testing.T) {
	t.Parallel()

	unit := jobTokenRefusalUnit(t)
	ns := jobTokenTestNamespace(unit)

	cases := []refusalRecordCase{
		{
			name: "the namespace of the unit", rec: identity.JobTokenRefusal{
				Subject: "alice", JobID: "job-1", Unit: unit, Action: identity.ActionUnitManage,
				Reason: "access.denied",
			},
			entries:    &fakeAuditStore{Store: nil, entries: nil, fences: nil, err: nil},
			server:     &fakeAuditServerStore{ServerStore: nil, entries: nil, err: nil},
			namespaces: &fakeNamespaceStore{Store: nil, ns: ns, err: nil},
			noClock:    false,
			wantErr:    nil, wantNS: ns.Name, wantFence: ns.Fence(), wantSubject: "alice",
		},
		{
			name: "the installation scope", rec: identity.JobTokenRefusal{
				Subject: "", JobID: "job-1", Unit: identity.Object{}, Action: identity.ActionUnitRead,
				Reason: "access.denied",
			},
			entries:    &fakeAuditStore{Store: nil, entries: nil, fences: nil, err: nil},
			server:     &fakeAuditServerStore{ServerStore: nil, entries: nil, err: nil},
			namespaces: &fakeNamespaceStore{Store: nil, ns: ns, err: nil},
			noClock:    false,
			wantErr:    nil, wantNS: audit.ServerScope, wantFence: zeroFence(),
			wantSubject: jobTokenFallbackName,
		},
		{
			name: "the namespace read fails", rec: identity.JobTokenRefusal{
				Subject: "alice", JobID: "job-1", Unit: unit, Action: identity.ActionUnitRead,
				Reason: "access.denied",
			},
			entries:    &fakeAuditStore{Store: nil, entries: nil, fences: nil, err: nil},
			server:     &fakeAuditServerStore{ServerStore: nil, entries: nil, err: nil},
			namespaces: &fakeNamespaceStore{Store: nil, ns: ns, err: errNamespaceRead},
			noClock:    false,
			wantErr:    errNamespaceRead, wantNS: "", wantFence: zeroFence(), wantSubject: "",
		},
		{
			name: "the append fails", rec: identity.JobTokenRefusal{
				Subject: "alice", JobID: "job-1", Unit: unit, Action: identity.ActionUnitRead,
				Reason: "access.denied",
			},
			entries:    &fakeAuditStore{Store: nil, entries: nil, fences: nil, err: errAuditAppend},
			server:     &fakeAuditServerStore{ServerStore: nil, entries: nil, err: nil},
			namespaces: &fakeNamespaceStore{Store: nil, ns: ns, err: nil},
			noClock:    false,
			wantErr:    errAuditAppend, wantNS: "", wantFence: zeroFence(), wantSubject: "",
		},
		{
			name: "no clock", rec: identity.JobTokenRefusal{
				Subject: "alice", JobID: "job-1", Unit: unit, Action: identity.ActionUnitRead,
				Reason: "access.denied",
			},
			entries:    &fakeAuditStore{Store: nil, entries: nil, fences: nil, err: nil},
			server:     &fakeAuditServerStore{ServerStore: nil, entries: nil, err: nil},
			namespaces: &fakeNamespaceStore{Store: nil, ns: ns, err: nil},
			noClock:    true,
			wantErr:    errNoJobTokenClock, wantNS: "", wantFence: zeroFence(), wantSubject: "",
		},
		{
			name: "no audit store", rec: identity.JobTokenRefusal{
				Subject: "alice", JobID: "job-1", Unit: unit, Action: identity.ActionUnitRead,
				Reason: "access.denied",
			},
			entries: nil, server: &fakeAuditServerStore{ServerStore: nil, entries: nil, err: nil},
			namespaces: &fakeNamespaceStore{Store: nil, ns: ns, err: nil},
			noClock:    false,
			wantErr:    errNoJobTokenAuditStore, wantNS: "", wantFence: zeroFence(), wantSubject: "",
		},
		{
			name: "no store for the installation scope", rec: identity.JobTokenRefusal{
				Subject: "", JobID: "job-1", Unit: identity.Object{}, Action: identity.ActionUnitRead,
				Reason: "access.denied",
			},
			entries: &fakeAuditStore{Store: nil, entries: nil, fences: nil, err: nil}, server: nil,
			namespaces: &fakeNamespaceStore{Store: nil, ns: ns, err: nil},
			noClock:    false,
			wantErr:    errNoJobTokenServerStore, wantNS: "", wantFence: zeroFence(), wantSubject: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runRefusalRecordCase(t, tc)
		})
	}
}

// runRefusalRecordCase runs one row of the refusal recorder table.
func runRefusalRecordCase(t *testing.T, tc refusalRecordCase) {
	t.Helper()

	now := time.UnixMilli(1_700_000_000_000).UTC()

	var entries audit.Store
	if tc.entries != nil {
		entries = tc.entries
	}

	var server audit.ServerStore
	if tc.server != nil {
		server = tc.server
	}

	var clock func() time.Time
	if !tc.noClock {
		clock = func() time.Time { return now }
	}

	recorder := jobTokenRefusals{
		entries: entries, server: server, namespaces: tc.namespaces,
		now: clock,
	}

	err := recorder.RecordJobTokenRefusal(t.Context(), tc.rec)
	if tc.wantErr != nil {
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("RecordJobTokenRefusal = %v, want %v", err, tc.wantErr)
		}

		return
	}

	if err != nil {
		t.Fatalf("RecordJobTokenRefusal = %v, want nil", err)
	}

	checkRefusalEntry(t, tc, now)
}

// checkRefusalEntry checks the entry of one successful row.
func checkRefusalEntry(t *testing.T, tc refusalRecordCase, now time.Time) {
	t.Helper()

	entry, fence := gotRefusalEntry(t, tc)

	if entry.Namespace != tc.wantNS {
		t.Errorf("namespace = %q, want %q", entry.Namespace, tc.wantNS)
	}

	if entry.Subject != tc.wantSubject {
		t.Errorf("subject = %q, want %q", entry.Subject, tc.wantSubject)
	}

	if entry.Kind != audit.KindSecurity || entry.Outcome != audit.OutcomeDenied {
		t.Errorf("kind = %q, outcome = %q, want security and denied", entry.Kind, entry.Outcome)
	}

	if entry.JobID != tc.rec.JobID || entry.Action != string(tc.rec.Action) || entry.Reason != tc.rec.Reason {
		t.Errorf("entry = %+v, want the job, action and reason of the refusal", entry)
	}

	if tc.rec.Unit.IsUnit() && entry.Object != tc.rec.Unit.String() {
		t.Errorf("object = %q, want %q", entry.Object, tc.rec.Unit.String())
	}

	if !entry.At.Equal(now) {
		t.Errorf("time = %s, want %s", entry.At, now)
	}

	if !slices.Equal([]namespace.Fence{fence}, []namespace.Fence{tc.wantFence}) {
		t.Errorf("fence = %+v, want %+v", fence, tc.wantFence)
	}
}

// gotRefusalEntry returns the entry that the recorder wrote, and the fence of
// the append in a namespace.
func gotRefusalEntry(t *testing.T, tc refusalRecordCase) (audit.Entry, namespace.Fence) {
	t.Helper()

	if tc.wantNS == audit.ServerScope {
		if tc.server == nil || len(tc.server.entries) != 1 {
			t.Fatalf("installation entries = %d, want 1", len(tc.server.entries))
		}

		return tc.server.entries[0], zeroFence()
	}

	if tc.entries == nil || len(tc.entries.entries) != 1 || len(tc.entries.fences) != 1 {
		t.Fatalf("namespace entries = %d and fences = %d, want 1 each",
			len(tc.entries.entries), len(tc.entries.fences))
	}

	return tc.entries.entries[0], tc.entries.fences[0]
}

// TestJobFactsOfMapsAJob proves that the adapter gives the unit, the change
// flag and the source kind of the stored job.
func TestJobFactsOfMapsAJob(t *testing.T) {
	t.Parallel()

	unit := jobTokenRefusalUnit(t)

	cases := []struct {
		name    string
		op      job.Operation
		source  job.SourceKind
		changes bool
	}{
		{name: "an apply", op: job.OperationApply, source: job.SourceAPI, changes: true},
		{name: "a plan", op: job.OperationPlan, source: job.SourceAgent, changes: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := jobTokenTestSpec(unit, tc.op)
			facts := jobFactsOf{store: &jobFactStore{
				Store: nil, stored: jobTokenTestJob(spec, tc.source), err: nil,
			}}

			got, err := facts.JobFact(t.Context(), "job-1")
			if err != nil {
				t.Fatalf("JobFact = %v, want nil", err)
			}

			if got.Unit != unit || got.Changes != tc.changes || got.Source != string(tc.source) {
				t.Fatalf("JobFact = %+v, want unit, changes %t and source %q", got, tc.changes, tc.source)
			}
		})
	}
}

// TestJobFactsOfWithNoStore proves that a job facts port with no job store
// refuses the read, so a job token then asks for no action.
func TestJobFactsOfWithNoStore(t *testing.T) {
	t.Parallel()

	facts := jobFactsOf{store: nil}

	if _, err := facts.JobFact(t.Context(), "job-1"); !errors.Is(err, errNoJobStore) {
		t.Fatalf("JobFact = %v, want %v", err, errNoJobStore)
	}
}
