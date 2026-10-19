package gatetest

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// recordRunners returns the runners of the cases of gate.Records.
func recordRunners() map[string]runner {
	return map[string]runner{
		"record-round-trip":                once(caseRecordRoundTrip),
		"record-other-namespace-not-found": once(caseRecordOtherNamespace),
		"record-used-id":                   once(caseRecordUsedID),
		"record-fence":                     once(caseRecordFence),
		"record-of-installation-object":    once(caseRecordInstallation),
		"list-by-change-oldest-first":      once(caseRecordsByChange),
		"query-window-and-limit":           once(caseRecordsQuery),
		"note-refusal-coalesces":           once(caseRefusalCoalesces),
		"note-refusal-new-record":          once(caseRefusalNew),
	}
}

// recordTime returns the time of the first record of a case.
func recordTime() time.Time {
	return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
}

// recordUnit returns the reference of the unit web/prod of acme.
func recordUnit(t *testing.T) gate.UnitRef {
	t.Helper()

	obj, err := identity.UnitObject("pulumi", "acme", "web/prod")
	mustNil(t, "UnitObject()", err)

	return gate.UnitRef{Object: obj}
}

// record returns a record of ns with one unit and one gate result, at time at.
func record(t *testing.T, ns namespace.Name, change gate.ChangeRef, at time.Time) gate.Record {
	t.Helper()

	schema := gate.SchemaVersion{Subject: string(gate.PointChangePlanned), Major: 1, Minor: 0}
	scope := gate.NamespaceScope("acme")

	return gate.Record{
		ID: gate.NewRecordID(), Point: gate.PointChangePlanned, Change: change, Namespace: ns, At: at,
		Subject: "alice", Schema: schema,
		PathSets: []gate.PathSetVersion{{Scope: scope, ID: "network", Version: 3}},
		Units: []gate.UnitResult{{
			Unit:   recordUnit(t),
			Facts:  recordFacts(t, schema),
			Digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Gates: []gate.PolicyResult{
				{
					Policy: gate.PolicyID{Scope: gate.InstallationScope(), ID: "builtin/plan-readable"}, Version: 1,
					Mode: gate.ModeEnforce, Outcome: gate.OutcomeDeny, Matched: false, Error: false, Diagnostic: "",
					Skipped: false, BrokenBy: "", Message: "",
				},
				{
					Policy: gate.PolicyID{Scope: scope, ID: "cost-cap"}, Version: 2, Mode: gate.ModeEvaluate,
					Outcome: gate.ApprovalOutcome("dba-review"), Matched: true, Error: false, Diagnostic: "",
					Skipped: false, BrokenBy: "audit-7", Message: "cost goes up by 150",
				},
			},
			Result: gate.ResultAwaitingApproval,
		}},
		Requirements: []gate.Requirement{{
			Name: gate.RuleRequirement("dba-review"), Version: 2,
			Approvers: gate.Approvers{Groups: []string{"dba"}, Users: nil, UnitOwners: true}, Count: 2,
			AuthorMayApprove: false, FreshAuth: gate.FreshAuth{Set: true, Seconds: 300}, TimeoutSeconds: 3600,
			Signatures: []gate.Signature{{
				Subject: "bob", At: at,
				CarriedFrom: &gate.CarriedFrom{Revision: 1, Digest: "abc", At: at.Add(-time.Hour), Scope: scope},
			}},
			Evaluate: false,
		}},
		Warnings: []string{"budget at 82 percent"}, AutoApply: false,
		Controls:   []gate.FixedControl{{Kind: gate.ControlFreeze, Passed: true, Detail: "freeze-1"}},
		BreakGlass: "audit-9", Result: gate.ResultAwaitingApproval, Count: 1, LastAt: at,
	}
}

// recordFacts returns the fact document of the schema, with the zero value
// of each group.
func recordFacts(t *testing.T, schema gate.SchemaVersion) gate.Facts {
	t.Helper()

	var f gate.Facts

	doc := `{"schema":"` + schema.String() + `"}`
	if err := json.Unmarshal([]byte(doc), &f); err != nil {
		t.Fatalf("Unmarshal(%s) = %v", doc, err)
	}

	f.Schema = schema

	return f
}

// accessRecord returns the record of a refusal of the access limit
// limit, for subject, at time at.
func accessRecord(t *testing.T, ns namespace.Name, subject string, limit gate.PolicyID, at time.Time) gate.Record {
	t.Helper()

	r := record(t, ns, gate.ChangeRef{Kind: gate.ChangeAccess, ID: ""}, at)
	r.Point = ""
	r.Subject = subject
	r.Units[0].Gates = []gate.PolicyResult{{
		Policy: limit, Version: 1, Mode: gate.ModeEnforce, Outcome: gate.OutcomeDeny, Matched: true, Error: false,
		Diagnostic: "", Skipped: false, BrokenBy: "", Message: "",
	}}
	r.Requirements = nil
	r.Result = gate.ResultDeny

	return r
}

func (e *env) putRecord(r gate.Record) error {
	e.t.Helper()

	fence := zeroFence()
	if r.Namespace == "acme" {
		fence = e.acme
	}

	if r.Namespace == "globex" {
		fence = e.globex
	}

	return e.h.Store.Records().Put(e.t.Context(), r, fence)
}

// sameJSON fails the test when a and b have another JSON form.
func sameJSON(t *testing.T, what string, got, want any) {
	t.Helper()

	g, err := json.Marshal(got)
	mustNil(t, "Marshal(got)", err)

	w, err := json.Marshal(want)
	mustNil(t, "Marshal(want)", err)

	if !bytes.Equal(g, w) {
		t.Fatalf("%s = %s, want %s", what, g, w)
	}
}

func caseRecordRoundTrip(t *testing.T, e *env) {
	t.Helper()

	r := record(t, "acme", gate.ChangeRef{Kind: gate.ChangeJob, ID: "job-42"}, recordTime())
	mustNil(t, "Put()", e.putRecord(r))

	got, err := e.h.Store.Records().Get(t.Context(), "acme", r.ID)
	mustNil(t, "Get()", err)
	sameJSON(t, "Get()", got, r)

	if !got.At.Equal(r.At) || !got.LastAt.Equal(r.LastAt) {
		t.Fatalf("Get() times = %v and %v, want %v and %v", got.At, got.LastAt, r.At, r.LastAt)
	}
}

func caseRecordOtherNamespace(t *testing.T, e *env) {
	t.Helper()

	r := record(t, "acme", gate.ChangeRef{Kind: gate.ChangeJob, ID: "job-42"}, recordTime())
	mustNil(t, "Put()", e.putRecord(r))

	_, err := e.h.Store.Records().Get(t.Context(), "globex", r.ID)
	wantErr(t, "Get(other namespace)", err, gate.ErrNotFound)

	_, err = e.h.Store.Records().Get(t.Context(), "acme", gate.NewRecordID())
	wantErr(t, "Get(unknown ID)", err, gate.ErrNotFound)

	_, err = e.h.Store.Records().Get(t.Context(), "nobody", r.ID)
	wantErr(t, "Get(unknown namespace)", err, gate.ErrNotFound)
}

func caseRecordUsedID(t *testing.T, e *env) {
	t.Helper()

	first := record(t, "acme", gate.ChangeRef{Kind: gate.ChangeJob, ID: "job-1"}, recordTime())
	mustNil(t, "Put(first)", e.putRecord(first))

	second := record(t, "acme", gate.ChangeRef{Kind: gate.ChangeJob, ID: "job-2"}, recordTime())
	second.ID = first.ID
	second.Subject = "mallory"

	wantErr(t, "Put(used ID)", e.putRecord(second), gate.ErrConflict)

	other := second
	other.Namespace = "globex"
	wantErr(t, "Put(used ID of another namespace)", e.putRecord(other), gate.ErrConflict)

	got, err := e.h.Store.Records().Get(t.Context(), "acme", first.ID)
	mustNil(t, "Get()", err)

	if got.Subject != "alice" {
		t.Fatalf("Get().Subject = %q, want the first record", got.Subject)
	}
}

func caseRecordFence(t *testing.T, e *env) {
	t.Helper()

	r := record(t, "acme", gate.ChangeRef{Kind: gate.ChangeJob, ID: "job-1"}, recordTime())
	rs := e.h.Store.Records()

	wantErr(t, "Put(fence of globex)", rs.Put(t.Context(), r, e.globex), gate.ErrInvalid)
	wantErr(t, "Put(zero fence)", rs.Put(t.Context(), r, zeroFence()), gate.ErrInvalid)

	stale := e.acme

	_, err := e.h.Domain.BumpFence(t.Context(), e.acme)
	mustNil(t, "BumpFence()", err)

	wantErr(t, "Put(stale fence)", rs.Put(t.Context(), r, stale), namespace.ErrFenced)

	_, err = rs.Get(t.Context(), "acme", r.ID)
	wantErr(t, "Get(refused put)", err, gate.ErrNotFound)

	unknown := record(t, "nobody", gate.ChangeRef{Kind: gate.ChangeJob, ID: "job-1"}, recordTime())
	wantErr(
		t,
		"Put(unknown namespace)",
		rs.Put(t.Context(), unknown, namespace.Fence{Namespace: "nobody", FailoverVersion: 1}),
		namespace.ErrNotFound,
	)
}

func caseRecordInstallation(t *testing.T, e *env) {
	t.Helper()

	limit := gate.PolicyID{Scope: e.fx.installation, ID: "no-agent-tokens"}
	r := accessRecord(t, audit.ServerScope, "agent-1", limit, recordTime())
	rs := e.h.Store.Records()

	id, err := rs.NoteRefusal(t.Context(), r, gate.AccessRefusalWindow, zeroFence())
	mustNil(t, "NoteRefusal(installation)", err)

	if id != r.ID {
		t.Fatalf("NoteRefusal() = %s, want %s", id, r.ID)
	}

	got, err := rs.Get(t.Context(), audit.ServerScope, id)
	mustNil(t, "Get(@server)", err)

	if got.Namespace != audit.ServerScope || got.Count != 1 {
		t.Fatalf("Get(@server) = namespace %q count %d, want @server and 1", got.Namespace, got.Count)
	}

	_, err = rs.Get(t.Context(), "acme", id)
	wantErr(t, "Get(acme)", err, gate.ErrNotFound)

	other := accessRecord(t, audit.ServerScope, "agent-2", limit, recordTime())
	_, err = rs.NoteRefusal(t.Context(), other, gate.AccessRefusalWindow, e.acme)
	wantErr(t, "NoteRefusal(installation, fence of acme)", err, gate.ErrInvalid)
}

func caseRecordsByChange(t *testing.T, e *env) {
	t.Helper()

	job1 := gate.ChangeRef{Kind: gate.ChangeJob, ID: "job-1"}
	job2 := gate.ChangeRef{Kind: gate.ChangeJob, ID: "job-2"}
	order := gate.ChangeRef{Kind: gate.ChangeChangeOrder, ID: "job-1"}

	times := []time.Duration{2 * time.Minute, 0, time.Minute}
	for _, d := range times {
		r := record(t, "acme", job1, recordTime().Add(d))
		mustNil(t, "Put()", e.putRecord(r))
	}

	for _, c := range []gate.ChangeRef{job2, order} {
		mustNil(t, "Put(other change)", e.putRecord(record(t, "acme", c, recordTime())))
	}

	mustNil(t, "Put(other namespace)", e.putRecord(record(t, "globex", job1, recordTime())))

	got, err := e.h.Store.Records().ListByChange(t.Context(), "acme", job1)
	mustNil(t, "ListByChange()", err)

	if len(got) != 3 {
		t.Fatalf("ListByChange() = %d records, want 3", len(got))
	}

	for i := range got {
		if want := recordTime().Add(time.Duration(i) * time.Minute); !got[i].At.Equal(want) {
			t.Fatalf("ListByChange()[%d].At = %v, want %v", i, got[i].At, want)
		}
	}

	none, err := e.h.Store.Records().
		ListByChange(t.Context(), "acme", gate.ChangeRef{Kind: gate.ChangeJob, ID: "job-9"})
	mustNil(t, "ListByChange(none)", err)

	if len(none) != 0 {
		t.Fatalf("ListByChange(none) = %d records, want 0", len(none))
	}
}

func caseRecordsQuery(t *testing.T, e *env) {
	t.Helper()

	change := gate.ChangeRef{Kind: gate.ChangeStateWrite, ID: ""}

	for i := range 6 {
		r := record(t, "acme", change, recordTime().Add(time.Duration(i)*time.Minute))
		r.Point = gate.PointStateWrite

		mustNil(t, "Put()", e.putRecord(r))
	}

	planned := record(t, "acme", change, recordTime().Add(time.Minute))
	mustNil(t, "Put(other point)", e.putRecord(planned))

	rs := e.h.Store.Records()
	q := gate.RecordQuery{
		Point: gate.PointStateWrite,
		From:  recordTime().Add(time.Minute),
		To:    recordTime().Add(5 * time.Minute),
		Limit: 10,
	}

	got, err := rs.Query(t.Context(), "acme", q)
	mustNil(t, "Query()", err)

	if len(got) != 4 {
		t.Fatalf("Query() = %d records, want 4 (the window holds [From, To))", len(got))
	}

	for i := range got {
		if want := recordTime().Add(time.Duration(4-i) * time.Minute); !got[i].At.Equal(want) {
			t.Fatalf("Query()[%d].At = %v, want %v, newest first", i, got[i].At, want)
		}
	}

	q.Limit = 2

	got, err = rs.Query(t.Context(), "acme", q)
	mustNil(t, "Query(limit 2)", err)

	if len(got) != 2 || !got[0].At.Equal(recordTime().Add(4*time.Minute)) {
		t.Fatalf("Query(limit 2) = %d records, want the 2 newest", len(got))
	}

	for _, limit := range []int{0, -1, gate.MaxSimulateRecords + 1} {
		q.Limit = limit
		_, err = rs.Query(t.Context(), "acme", q)
		wantErr(t, "Query(limit)", err, gate.ErrInvalid)
	}

	q.Limit = gate.MaxSimulateRecords

	_, err = rs.Query(t.Context(), "acme", q)
	mustNil(t, "Query(largest limit)", err)
}

func caseRefusalCoalesces(t *testing.T, e *env) {
	t.Helper()

	limit := gate.PolicyID{Scope: e.fx.acme, ID: "no-agent-secrets"}
	rs := e.h.Store.Records()
	first := accessRecord(t, "acme", "agent-1", limit, recordTime())

	id, err := rs.NoteRefusal(t.Context(), first, gate.AccessRefusalWindow, e.acme)
	mustNil(t, "NoteRefusal(first)", err)

	if id != first.ID {
		t.Fatalf("NoteRefusal(first) = %s, want %s", id, first.ID)
	}

	// The window runs from the last refusal, so a steady stream shares one record.
	for i, at := range []time.Duration{30 * time.Second, 80 * time.Second, 130 * time.Second} {
		next := accessRecord(t, "acme", "agent-1", limit, recordTime().Add(at))

		got, nerr := rs.NoteRefusal(t.Context(), next, gate.AccessRefusalWindow, e.acme)
		mustNil(t, "NoteRefusal(next)", nerr)

		if got != first.ID {
			t.Fatalf("NoteRefusal(next %d) = %s, want the first ID %s", i, got, first.ID)
		}
	}

	stored, err := rs.Get(t.Context(), "acme", first.ID)
	mustNil(t, "Get()", err)

	if stored.Count != 4 || !stored.LastAt.Equal(recordTime().Add(130*time.Second)) {
		t.Fatalf(
			"Get() = count %d last %v, want 4 and %v",
			stored.Count,
			stored.LastAt,
			recordTime().Add(130*time.Second),
		)
	}

	if !stored.At.Equal(recordTime()) {
		t.Fatalf("Get().At = %v, want the time of the first refusal", stored.At)
	}
}

func caseRefusalNew(t *testing.T, e *env) {
	t.Helper()

	limit := gate.PolicyID{Scope: e.fx.acme, ID: "no-agent-secrets"}
	otherLimit := gate.PolicyID{Scope: e.fx.acme, ID: "no-destroy"}
	rs := e.h.Store.Records()
	first := accessRecord(t, "acme", "agent-1", limit, recordTime())

	_, err := rs.NoteRefusal(t.Context(), first, gate.AccessRefusalWindow, e.acme)
	mustNil(t, "NoteRefusal(first)", err)

	cases := map[string]gate.Record{
		"after the window": accessRecord(t, "acme", "agent-1", limit, recordTime().Add(2*gate.AccessRefusalWindow)),
		"other subject":    accessRecord(t, "acme", "agent-2", limit, recordTime().Add(time.Second)),
		"other limit":      accessRecord(t, "acme", "agent-1", otherLimit, recordTime().Add(time.Second)),
		"other namespace":  accessRecord(t, "globex", "agent-1", limit, recordTime().Add(time.Second)),
	}

	for name := range cases {
		r := cases[name]
		fence := e.acme
		if r.Namespace == "globex" {
			fence = e.globex
		}

		got, nerr := rs.NoteRefusal(t.Context(), r, gate.AccessRefusalWindow, fence)
		mustNil(t, "NoteRefusal("+name+")", nerr)

		if got != r.ID {
			t.Fatalf("NoteRefusal(%s) = %s, want its own ID %s", name, got, r.ID)
		}

		stored, gerr := rs.Get(t.Context(), r.Namespace, got)
		mustNil(t, "Get("+name+")", gerr)

		if stored.Count != 1 {
			t.Fatalf("Get(%s).Count = %d, want 1", name, stored.Count)
		}
	}

	again, err := rs.Get(t.Context(), "acme", first.ID)
	mustNil(t, "Get(first)", err)

	if again.Count != 1 {
		t.Fatalf("Get(first).Count = %d, want 1: no later refusal joined it", again.Count)
	}
}
