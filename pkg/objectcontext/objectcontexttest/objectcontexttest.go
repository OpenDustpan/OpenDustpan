// Package objectcontexttest is the conformance suite of objectcontext.Store.
package objectcontexttest

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// Harness is one fresh, empty store for one subtest. Namespace makes the
// namespace name in the store and returns its current fence.
//
// Collection makes the collection name in the namespace ns. Audited returns
// the audit entries of the context writes of the namespace ns, oldest first.
type Harness struct {
	Store      objectcontext.Store
	Namespace  func(t *testing.T, name string) namespace.Fence
	Collection func(t *testing.T, ns, name string)
	Audited    func(t *testing.T, ns string) []audit.Entry
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

// at returns the time of the fixtures.
func at() time.Time {
	return time.UnixMilli(1_800_000_000_000).UTC()
}

// Run runs every case of the suite.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, h Harness)
	}{
		{"set and read an API entry", testSetAndRead},
		{"a patch keeps the other fields", testPatchKeeps},
		{"the API cannot set a field from the code", testFieldFromCode},
		{"a newer output replaces an older one", testOutputOrder},
		{"replacements at the same time end with the newest item", testOutputConcurrent},
		{"an empty output deletes the output entries", testOutputEmpty},
		{"delete of a missing API entry", testDeleteMissing},
		{"a purge drops the unit and its resources", testDropUnit},
		{"list by owner in pages", testByOwner},
		{"the owner index follows the owners", testOwnerIndex},
		{"a write appends one audit entry with its changes", testAuditChanges},
		{"a stale fence appends no audit entry", testFencedNoAudit},
		{"one unit ID in two namespaces holds two entries", testNamespaceIsolation},
		{"a collection that does not exist", testCollectionMissing},
		{"an empty output keeps its order", testOutputEmptyOrder},
		{"a stale fence refuses each write", testFenced},
		{"invalid references", testInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newHarness(t))
		})
	}
}

// unitOf returns the unit id of tool "opentofu" in namespace acme.
func unitOf(id string) graph.Unit {
	return graph.Unit{Namespace: "acme", Tool: "opentofu", ID: id}
}

// entryAudit returns the audit entry of a context write in namespace acme.
func entryAudit() audit.Entry {
	return entryAuditIn("acme")
}

// entryAuditIn returns the audit entry of a context write in namespace ns.
func entryAuditIn(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At:           at(),
		Namespace:    ns,
		Subject:      "user:ada",
		Action:       "unit:set-context",
		IDPSubject:   "",
		TokenID:      "",
		Object:       ns.String(),
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "",
		UserAgent:    "",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
}

func list(s ...string) *[]string { return &s }

func entries(t *testing.T, h Harness, refs ...objectcontext.Ref) []objectcontext.Entry {
	t.Helper()

	got, err := h.Store.Entries(t.Context(), refs)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	return got
}

func testSetAndRead(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	h.Collection(t, "acme", "web")

	ref := objectcontext.CollectionRef("acme", "web")

	got, err := h.Store.SetAPI(
		t.Context(),
		ref,
		objectcontext.Patch{
			Summary: new("Web units"),
			Owners:  list("group:web"),
			Runbook: nil,
			OnCall:  nil,
			Links:   nil,
		},
		"user:ada",
		fence,
		entryAudit(),
	)
	if err != nil {
		t.Fatalf("SetAPI: %v", err)
	}

	if got.Source != objectcontext.SourceAPI || got.Fields.Summary != "Web units" || got.UpdatedBy != "user:ada" {
		t.Errorf("SetAPI = %+v", got)
	}

	read := entries(t, h, ref, objectcontext.NamespaceRef("acme"))
	if len(read) != 1 || read[0].Fields.Summary != "Web units" ||
		!slices.Equal(read[0].Fields.Owners, []string{"group:web"}) {
		t.Errorf("Entries = %+v, want the one API entry", read)
	}
}

func testPatchKeeps(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	ref := objectcontext.NamespaceRef("acme")

	for _, p := range []objectcontext.Patch{{Summary: new("a"), OnCall: new("pager:a"), Runbook: nil, Owners: nil, Links: nil}, {OnCall: new("pager:b"), Summary: nil, Runbook: nil, Owners: nil, Links: nil}} {
		if _, err := h.Store.SetAPI(t.Context(), ref, p, "user:ada", fence, entryAudit()); err != nil {
			t.Fatalf("SetAPI: %v", err)
		}
	}

	read := entries(t, h, ref)
	if len(read) != 1 || read[0].Fields.Summary != "a" || read[0].Fields.OnCall != "pager:b" {
		t.Errorf("Entries = %+v, want summary a and on-call pager:b", read)
	}
}

func testFieldFromCode(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	u := unitOf("ws-1")
	ref := objectcontext.UnitRef(u)
	out := objectcontext.Output{
		SchemaVersion: 1,
		Owners:        []string{"group:net"},
		Resources:     nil,
		Summary:       "",
		Runbook:       "",
		OnCall:        "",
		Links:         nil,
	}

	item := objectcontext.OutputItem{ID: "sv-1", Seq: 1}
	if err := h.Store.ReplaceOutput(t.Context(), ref, item, out.Entries(u, "", "sv-1", at()), fence); err != nil {
		t.Fatalf("ReplaceOutput: %v", err)
	}

	_, err := h.Store.SetAPI(
		t.Context(),
		ref,
		objectcontext.Patch{Owners: list("group:ops"), Summary: nil, Runbook: nil, OnCall: nil, Links: nil},
		"user:ada",
		fence,
		entryAudit(),
	)
	if !errors.Is(err, objectcontext.ErrFieldFromCode) {
		t.Errorf("SetAPI owners: %v, want ErrFieldFromCode", err)
	}

	_, err = h.Store.SetAPI(
		t.Context(),
		ref,
		objectcontext.Patch{OnCall: new("pager:net"), Summary: nil, Runbook: nil, Owners: nil, Links: nil},
		"user:ada",
		fence,
		entryAudit(),
	)
	if err != nil {
		t.Errorf("SetAPI on-call: %v, want a field that the code does not set to pass", err)
	}
}

func testOutputOrder(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	u := unitOf("ws-1")
	ref := objectcontext.UnitRef(u)

	put := func(seq int64, summary string) {
		out := objectcontext.Output{
			SchemaVersion: 1,
			Summary:       summary,
			Resources:     nil,
			Runbook:       "",
			Owners:        nil,
			OnCall:        "",
			Links:         nil,
		}
		item := objectcontext.OutputItem{ID: summary, Seq: seq}

		if err := h.Store.ReplaceOutput(t.Context(), ref, item, out.Entries(u, "", summary, at()), fence); err != nil {
			t.Fatalf("ReplaceOutput %d: %v", seq, err)
		}
	}

	put(2, "second")
	put(1, "first, late")

	read := entries(t, h, ref)
	if len(read) != 1 || read[0].Fields.Summary != "second" || read[0].Item != "second" {
		t.Errorf("Entries = %+v, want the output of the newer item", read)
	}
}

// testOutputConcurrent replaces the output of one unit from eight items at
// the same time. Each replacement runs in its own transaction, so the store
// must order them: the entries that remain come from the item with the
// highest Seq, whatever the order of the commits.
func testOutputConcurrent(t *testing.T, h Harness) {
	t.Helper()

	const items = 8

	fence := h.Namespace(t, "acme")
	u := unitOf("ws-1")
	ref := objectcontext.UnitRef(u)

	var (
		wg   sync.WaitGroup
		errs [items]error
	)

	for i := range items {
		wg.Go(func() {
			summary := "item " + strconv.Itoa(i+1)
			out := objectcontext.Output{
				SchemaVersion: 1,
				Summary:       summary,
				Resources:     nil,
				Runbook:       "",
				Owners:        nil,
				OnCall:        "",
				Links:         nil,
			}
			item := objectcontext.OutputItem{ID: summary, Seq: int64(i + 1)}

			errs[i] = h.Store.ReplaceOutput(t.Context(), ref, item, out.Entries(u, "", summary, at()), fence)
		})
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("ReplaceOutput %d: %v", i+1, err)
		}
	}

	read := entries(t, h, ref)
	if len(read) != 1 || read[0].Item != "item 8" || read[0].Fields.Summary != "item 8" {
		t.Errorf("Entries = %+v, want the output of the item with the highest Seq", read)
	}
}

func testOutputEmpty(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	u := unitOf("ws-1")
	ref := objectcontext.UnitRef(u)
	out := objectcontext.Output{
		SchemaVersion: 1,
		Summary:       "x",
		Resources: map[string]objectcontext.Fields{
			"aws_s3_bucket.logs": {Summary: "logs", Runbook: "", Owners: nil, OnCall: "", Links: nil},
		},
		Runbook: "",
		Owners:  nil,
		OnCall:  "",
		Links:   nil,
	}

	item := objectcontext.OutputItem{ID: "sv-1", Seq: 1}
	if err := h.Store.ReplaceOutput(t.Context(), ref, item, out.Entries(u, "", "sv-1", at()), fence); err != nil {
		t.Fatalf("ReplaceOutput: %v", err)
	}

	err := h.Store.ReplaceOutput(
		t.Context(),
		ref,
		objectcontext.OutputItem{ID: "sv-2", Seq: 2},
		nil,
		fence,
	)
	if err != nil {
		t.Fatalf("ReplaceOutput empty: %v", err)
	}

	if read := entries(t, h, ref, objectcontext.ResourceRef(u, "aws_s3_bucket.logs")); len(read) != 0 {
		t.Errorf("Entries = %+v, want none", read)
	}
}

func testDeleteMissing(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")

	err := h.Store.DeleteAPI(t.Context(), objectcontext.NamespaceRef("acme"), fence, entryAudit())
	if !errors.Is(err, objectcontext.ErrNotFound) {
		t.Errorf("DeleteAPI: %v, want ErrNotFound", err)
	}
}

func testDropUnit(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	u, other := unitOf("ws-1"), unitOf("ws-2")
	res := objectcontext.ResourceRef(u, "aws_s3_bucket.logs")

	for _, ref := range []objectcontext.Ref{objectcontext.UnitRef(u), res, objectcontext.UnitRef(other)} {
		_, err := h.Store.SetAPI(
			t.Context(),
			ref,
			objectcontext.Patch{Summary: new("x"), Runbook: nil, OnCall: nil, Owners: nil, Links: nil},
			"user:ada",
			fence,
			entryAudit(),
		)
		if err != nil {
			t.Fatalf("SetAPI %s: %v", ref.Key(), err)
		}
	}

	if err := h.Store.DropUnit(t.Context(), objectcontext.UnitRef(u), fence); err != nil {
		t.Fatalf("DropUnit: %v", err)
	}

	read := entries(t, h, objectcontext.UnitRef(u), res, objectcontext.UnitRef(other))
	if len(read) != 1 || read[0].Object != objectcontext.UnitRef(other) {
		t.Errorf("Entries = %+v, want only the other unit", read)
	}
}

func testByOwner(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")

	for _, id := range []string{"ws-1", "ws-2", "ws-3"} {
		ref := objectcontext.UnitRef(unitOf(id))
		_, err := h.Store.SetAPI(
			t.Context(),
			ref,
			objectcontext.Patch{Owners: list("group:web"), Summary: nil, Runbook: nil, OnCall: nil, Links: nil},
			"user:ada",
			fence,
			entryAudit(),
		)
		if err != nil {
			t.Fatalf("SetAPI: %v", err)
		}
	}

	first, next, err := h.Store.ByOwner(t.Context(), "acme", "group:web", "", 2)
	if err != nil || len(first) != 2 || next == "" {
		t.Fatalf("ByOwner page 1 = %d entries, cursor %q, %v; want 2 and a cursor", len(first), next, err)
	}

	second, next, err := h.Store.ByOwner(t.Context(), "acme", "group:web", next, 2)
	if err != nil || len(second) != 1 || second[0].Object.Unit.ID != "ws-3" || next != "" {
		t.Errorf("ByOwner page 2 = %+v, cursor %q, %v; want ws-3 and no cursor", second, next, err)
	}

	_, _, err = h.Store.ByOwner(t.Context(), "acme", "group:web", "", 0)
	if !errors.Is(err, objectcontext.ErrInvalidContext) {
		t.Errorf("ByOwner limit 0: %v, want ErrInvalidContext", err)
	}

	_, _, err = h.Store.ByOwner(t.Context(), "acme", "group:web", "not a cursor", 2)
	if !errors.Is(err, objectcontext.ErrInvalidContext) {
		t.Errorf("ByOwner with a bad cursor: %v, want ErrInvalidContext", err)
	}
}

func testFenced(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	stale := namespace.Fence{Namespace: fence.Namespace, FailoverVersion: fence.FailoverVersion + 1}
	u := unitOf("ws-1")
	ref := objectcontext.UnitRef(u)

	errs := map[string]error{}
	_, errs["SetAPI"] = h.Store.SetAPI(
		t.Context(),
		ref,
		objectcontext.Patch{Summary: new("x"), Runbook: nil, OnCall: nil, Owners: nil, Links: nil},
		"user:ada",
		stale,
		entryAudit(),
	)
	errs["ReplaceOutput"] = h.Store.ReplaceOutput(
		t.Context(),
		ref,
		objectcontext.OutputItem{ID: "sv", Seq: 1},
		nil,
		stale,
	)
	errs["DropUnit"] = h.Store.DropUnit(t.Context(), ref, stale)

	for name, err := range errs {
		if !errors.Is(err, namespace.ErrFenced) {
			t.Errorf("%s: %v, want namespace.ErrFenced", name, err)
		}
	}
}

func testInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	bad := objectcontext.Ref{
		Kind:      "stack",
		Namespace: "acme",
		Name:      "",
		Resource:  "",
		Unit:      graph.Unit{Namespace: "", Tool: "", ID: ""},
	}

	_, err := h.Store.Entries(
		t.Context(),
		[]objectcontext.Ref{bad},
	)
	if !errors.Is(err, objectcontext.ErrInvalidObject) {
		t.Errorf("Entries: %v, want ErrInvalidObject", err)
	}

	_, err = h.Store.SetAPI(
		t.Context(),
		bad,
		objectcontext.Patch{Summary: nil, Runbook: nil, OnCall: nil, Owners: nil, Links: nil},
		"user:ada",
		fence,
		entryAudit(),
	)
	if !errors.Is(err, objectcontext.ErrInvalidObject) {
		t.Errorf("SetAPI: %v, want ErrInvalidObject", err)
	}

	_, err = h.Store.SetAPI(
		t.Context(),
		objectcontext.NamespaceRef("acme"),
		objectcontext.Patch{Owners: list(""), Summary: nil, Runbook: nil, OnCall: nil, Links: nil},
		"user:ada",
		fence,
		entryAudit(),
	)
	if !errors.Is(err, objectcontext.ErrInvalidContext) {
		t.Errorf("SetAPI with an empty owner: %v, want ErrInvalidContext", err)
	}
}

func setSummary(t *testing.T, h Harness, ref objectcontext.Ref, fence namespace.Fence, summary string) {
	t.Helper()

	p := objectcontext.Patch{Summary: &summary, Runbook: nil, OnCall: nil, Owners: nil, Links: nil}

	_, err := h.Store.SetAPI(t.Context(), ref, p, "user:ada", fence, entryAuditIn(fence.Namespace))
	if err != nil {
		t.Fatalf("SetAPI %s: %v", ref.Key(), err)
	}
}

func ownerCount(t *testing.T, h Harness, owner string) int {
	t.Helper()

	got, _, err := h.Store.ByOwner(t.Context(), "acme", owner, "", objectcontext.MaxBatch)
	if err != nil {
		t.Fatalf("ByOwner %s: %v", owner, err)
	}

	return len(got)
}

func testOwnerIndex(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	ref := objectcontext.UnitRef(unitOf("ws-1"))

	steps := []struct {
		name   string
		owners []string
		a, b   int
	}{
		{"two owners", []string{"group:a", "group:b", "group:b"}, 1, 1},
		{"one owner left", []string{"group:b"}, 0, 1},
		{"no owner", nil, 0, 0},
	}

	for i := range steps {
		step := &steps[i]
		p := objectcontext.Patch{Owners: &step.owners, Summary: nil, Runbook: nil, OnCall: nil, Links: nil}
		if _, err := h.Store.SetAPI(t.Context(), ref, p, "user:ada", fence, entryAudit()); err != nil {
			t.Fatalf("%s: SetAPI: %v", step.name, err)
		}

		if a, b := ownerCount(t, h, "group:a"), ownerCount(t, h, "group:b"); a != step.a || b != step.b {
			t.Errorf("%s: owner counts = %d and %d, want %d and %d", step.name, a, b, step.a, step.b)
		}
	}

	owners := []string{"group:a"}
	p := objectcontext.Patch{Owners: &owners, Summary: nil, Runbook: nil, OnCall: nil, Links: nil}

	if _, err := h.Store.SetAPI(t.Context(), ref, p, "user:ada", fence, entryAudit()); err != nil {
		t.Fatalf("SetAPI: %v", err)
	}

	if err := h.Store.DeleteAPI(t.Context(), ref, fence, entryAudit()); err != nil {
		t.Fatalf("DeleteAPI: %v", err)
	}

	if n := ownerCount(t, h, "group:a"); n != 0 {
		t.Errorf("owner count after a delete = %d, want 0", n)
	}
}

func testAuditChanges(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	ref := objectcontext.NamespaceRef("acme")

	first := objectcontext.Patch{
		Summary: new("abc"), Owners: list("group:web"), OnCall: nil, Runbook: nil, Links: nil,
	}
	if _, err := h.Store.SetAPI(t.Context(), ref, first, "user:ada", fence, entryAudit()); err != nil {
		t.Fatalf("SetAPI: %v", err)
	}

	second := objectcontext.Patch{OnCall: new("pager:web"), Summary: nil, Runbook: nil, Owners: nil, Links: nil}
	if _, err := h.Store.SetAPI(t.Context(), ref, second, "user:ada", fence, entryAudit()); err != nil {
		t.Fatalf("SetAPI: %v", err)
	}

	if err := h.Store.DeleteAPI(t.Context(), ref, fence, entryAudit()); err != nil {
		t.Fatalf("DeleteAPI: %v", err)
	}

	got := h.Audited(t, "acme")
	if len(got) != 3 {
		t.Fatalf("audit entries = %d, want 3", len(got))
	}

	want := [][]string{{"owners", "summary"}, {"onCall"}, {"onCall", "owners", "summary"}}
	for i := range got {
		e := &got[i]
		keys := make([]string, 0, len(e.Changes))

		for _, c := range e.Changes {
			keys = append(keys, c.Key)
		}

		slices.Sort(keys)

		if !slices.Equal(keys, want[i]) {
			t.Errorf("audit entry %d changes = %v, want %v", i, keys, want[i])
		}
	}

	for _, c := range got[0].Changes {
		if c.Key == "summary" && c.New != "3 bytes" {
			t.Errorf("summary change = %q, want the length 3 bytes and no text", c.New)
		}
	}
}

func testFencedNoAudit(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	stale := namespace.Fence{Namespace: fence.Namespace, FailoverVersion: fence.FailoverVersion + 1}
	p := objectcontext.Patch{Summary: new("x"), Runbook: nil, OnCall: nil, Owners: nil, Links: nil}

	_, err := h.Store.SetAPI(t.Context(), objectcontext.NamespaceRef("acme"), p, "user:ada", stale, entryAudit())
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("SetAPI: %v, want namespace.ErrFenced", err)
	}

	if got := h.Audited(t, "acme"); len(got) != 0 {
		t.Errorf("audit entries = %d, want none for a refused write", len(got))
	}

	if read := entries(t, h, objectcontext.NamespaceRef("acme")); len(read) != 0 {
		t.Errorf("Entries = %+v, want none", read)
	}
}

func testNamespaceIsolation(t *testing.T, h Harness) {
	t.Helper()

	acme, beta := h.Namespace(t, "acme"), h.Namespace(t, "beta")
	inAcme := objectcontext.UnitRef(unitOf("ws-1"))
	inBeta := objectcontext.UnitRef(graph.Unit{Namespace: "beta", Tool: "opentofu", ID: "ws-1"})

	setSummary(t, h, inAcme, acme, "acme")

	if read := entries(t, h, inBeta); len(read) != 0 {
		t.Fatalf("Entries of beta = %+v, want none", read)
	}

	setSummary(t, h, inBeta, beta, "beta")

	if read := entries(t, h, inAcme); len(read) != 1 || read[0].Fields.Summary != "acme" {
		t.Errorf("Entries of acme = %+v, want the summary acme", read)
	}

	if read := entries(t, h, inBeta); len(read) != 1 || read[0].Fields.Summary != "beta" {
		t.Errorf("Entries of beta = %+v, want the summary beta", read)
	}

	if err := h.Store.DropUnit(t.Context(), inBeta, beta); err != nil {
		t.Fatalf("DropUnit: %v", err)
	}

	if read := entries(t, h, inAcme); len(read) != 1 {
		t.Errorf("Entries of acme after a drop in beta = %+v, want the entry to stay", read)
	}
}

func testCollectionMissing(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	ref := objectcontext.CollectionRef("acme", "absent")

	if read := entries(t, h, ref); len(read) != 0 {
		t.Errorf("Entries = %+v, want none for a collection that does not exist", read)
	}

	p := objectcontext.Patch{Summary: new("x"), Runbook: nil, OnCall: nil, Owners: nil, Links: nil}

	_, err := h.Store.SetAPI(t.Context(), ref, p, "user:ada", fence, entryAudit())
	if !errors.Is(err, objectcontext.ErrInvalidObject) {
		t.Errorf("SetAPI: %v, want ErrInvalidObject", err)
	}

	if got := h.Audited(t, "acme"); len(got) != 0 {
		t.Errorf("audit entries = %d, want none for a refused write", len(got))
	}
}

func testOutputEmptyOrder(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	u := unitOf("ws-1")
	ref := objectcontext.UnitRef(u)

	put := func(seq int64, summary string) {
		var es []objectcontext.Entry

		if summary != "" {
			out := objectcontext.Output{
				SchemaVersion: 1, Summary: summary, Resources: nil, Runbook: "", Owners: nil, OnCall: "", Links: nil,
			}
			es = out.Entries(u, "", summary, at())
		}

		item := objectcontext.OutputItem{ID: summary, Seq: seq}
		if err := h.Store.ReplaceOutput(t.Context(), ref, item, es, fence); err != nil {
			t.Fatalf("ReplaceOutput %d: %v", seq, err)
		}
	}

	put(2, "second")
	put(3, "")
	put(2, "second again, late")

	if read := entries(t, h, ref); len(read) != 0 {
		t.Errorf("Entries = %+v, want none: a late run must not bring back a cleared output", read)
	}
}
