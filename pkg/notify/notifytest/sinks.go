package notifytest

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// SinkHarness is one fresh, empty database for one case of RunSinks.
type SinkHarness struct {
	Sinks  notify.SinkStore
	Domain Domain
}

// SinkFactory returns a fresh sink harness for one case.
type SinkFactory func(t *testing.T) SinkHarness

// RunSinks executes the conformance suite of notify.SinkStore against
// harnesses from newHarness. A store that implements notify.Subscriptions
// runs Run too. RunSinks needs neither an outbox nor a subscription:
//
//	func TestSinkStoreConformance(t *testing.T) {
//		t.Parallel()
//		notifytest.RunSinks(t, func(t *testing.T) notifytest.SinkHarness { return newSinkHarness(t) })
//	}
func RunSinks(t *testing.T, newHarness SinkFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h SinkHarness)
	}{
		{"sinks of a scope are listed by ID with the platform and the version", testSinksList},
		{"a put on a version that is not stored is ErrConflict", testSinksVersion},
		{"a replace keeps the kind and the platform of the sink", testSinksPlatformFixed},
		{"put refuses a sink that fails Validate and stores nothing", testSinksValidate},
		{"the store keeps the sealed values and a ref names one value for good", testSinksSecrets},
		{"the store refuses a value sealed for another sink or scope", testSinksForeignSeals},
		{"a webhook sink keeps its URL as a sealed item and fails closed without it", testSinksWebhookURL},
		{"a delete removes the sink and its values", testSinksDelete},
		{"a sink of one scope is invisible in another", testSinksScopes},
		{"each write appends one audit entry in the scope of the sink", testSinksAudit},
		{"a test record appends one entry for a sink of the scope and changes nothing else", testSinksRecordTest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}

	runSinkFences(t, newHarness)
}

// chatSink returns a valid chat sink of the platform Slack.
func chatSink(id string) notify.Sink {
	return notify.Sink{
		ID: id, Scope: shop, Kind: notify.SinkChat, Name: "Ops chat", Platform: "slack", Target: "hooks.slack.com",
		SecretRefs: []string{"itm-" + id}, CreatedBy: "alice", Version: 0,
	}
}

func sinkSetup(t *testing.T, h SinkHarness) namespace.Fence {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return fence
}

func putNew(t *testing.T, h SinkHarness, fence namespace.Fence, s notify.Sink) {
	t.Helper()

	if err := h.Sinks.PutSink(t.Context(), s, fresh(t.Context(), s), fence, manage()); err != nil {
		t.Fatalf("PutSink %s: %v", s.ID, err)
	}
}

func sinksOf(t *testing.T, h SinkHarness, scope namespace.Name) []notify.Sink {
	t.Helper()

	got, err := h.Sinks.Sinks(t.Context(), scope)
	if err != nil {
		t.Fatalf("Sinks %s: %v", scope, err)
	}

	return got
}

func testSinksList(t *testing.T, h SinkHarness) {
	t.Helper()

	fence := sinkSetup(t, h)

	if got := sinksOf(t, h, shop); len(got) != 0 {
		t.Fatalf("Sinks of an empty scope = %+v, want none", got)
	}

	putNew(t, h, fence, chatSink("snk-2"))
	putNew(t, h, fence, sink("snk-1", shop))

	got := sinksOf(t, h, shop)
	if len(got) != 2 || got[0].ID != "snk-1" || got[1].ID != "snk-2" {
		t.Fatalf("Sinks = %+v, want snk-1 and snk-2 in the order of their IDs", got)
	}

	want := chatSink("snk-2")
	want.Version = 1

	if fmt.Sprintf("%+v", got[1]) != fmt.Sprintf("%+v", want) {
		t.Fatalf("Sinks[1] = %+v, want %+v: kind, platform, host, refs and creator as stored, version 1", got[1], want)
	}

	if got[0].Platform != "" || got[0].Version != 1 {
		t.Fatalf("Sinks[0] = %+v, want no platform and version 1", got[0])
	}
}

func testSinksVersion(t *testing.T, h SinkHarness) {
	t.Helper()

	fence := sinkSetup(t, h)
	s := sink("snk-1", shop)
	putNew(t, h, fence, s)

	err := h.Sinks.PutSink(
		t.Context(),
		s,
		fresh(t.Context(), s),
		fence,
		manage(),
	)
	if !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("create of a sink that exists: err = %v, want ErrConflict", err)
	}

	renamed := s
	renamed.Version, renamed.Name = 1, "Ops team"

	if err := h.Sinks.PutSink(t.Context(), renamed, nil, fence, manage()); err != nil {
		t.Fatalf("replace at the stored version: %v", err)
	}

	if got := sinksOf(t, h, shop); len(got) != 1 || got[0].Version != 2 || got[0].Name != "Ops team" {
		t.Fatalf("Sinks after the replace = %+v, want version 2 and the new name", got)
	}

	if err := h.Sinks.PutSink(t.Context(), renamed, nil, fence, manage()); !errors.Is(err, notify.ErrConflict) {
		t.Fatalf("replace at the old version: err = %v, want ErrConflict", err)
	}
}

func testSinksPlatformFixed(t *testing.T, h SinkHarness) {
	t.Helper()

	fence := sinkSetup(t, h)
	putNew(t, h, fence, chatSink("snk-1"))

	teams := chatSink("snk-1")
	teams.Version, teams.Platform = 1, "teams"

	hook := sink("snk-1", shop)
	hook.Version = 1
	hook.SecretRefs = []string{"itm-snk-1-b-url", "itm-snk-1-b"}

	cases := []struct {
		name    string
		s       notify.Sink
		secrets []notify.SealedSecret
	}{
		{"a new platform", teams, nil},
		{"a new kind", hook, fresh(t.Context(), hook)},
	}

	for i := range cases {
		tc := &cases[i]

		if err := h.Sinks.PutSink(t.Context(), tc.s, tc.secrets, fence, manage()); !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("replace with %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}

	if got := sinksOf(t, h, shop); len(got) != 1 || got[0].Platform != "slack" || got[0].Kind != notify.SinkChat ||
		got[0].Version != 1 {
		t.Fatalf("Sinks after the refused replaces = %+v, want the first chat sink at version 1", got)
	}
}

func testSinksValidate(t *testing.T, h SinkHarness) {
	t.Helper()

	fence := sinkSetup(t, h)

	noPlatform := chatSink("snk-1")
	noPlatform.Platform = ""

	badPlatform := chatSink("snk-1")
	badPlatform.Platform = "irc"

	hookPlatform := sink("snk-1", shop)
	hookPlatform.Platform = "slack"

	badID := sink("sink-1", shop)

	secrets := fresh(t.Context(), sink("snk-1", shop))

	refused := map[string]notify.Sink{
		"a chat sink with no platform":      noPlatform,
		"a chat sink with an unknown one":   badPlatform,
		"a webhook sink with a platform":    hookPlatform,
		"a sink ID without the prefix snk-": badID,
	}

	for name := range refused {
		err := h.Sinks.PutSink(t.Context(), refused[name], secrets, fence, manage())
		if !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("PutSink of %s: err = %v, want ErrInvalid", name, err)
		}
	}

	if got := sinksOf(t, h, shop); len(got) != 0 {
		t.Fatalf("Sinks after the refused puts = %+v, want none", got)
	}

	if entries, err := h.Domain.AuditEntries(t.Context(), shop); err != nil || len(entries) != 0 {
		t.Fatalf("audit entries after the refused puts = %d (%v), want none", len(entries), err)
	}
}

func testSinksSecrets(t *testing.T, h SinkHarness) {
	t.Helper()

	fence := sinkSetup(t, h)
	s := withRefs(sink("snk-1", shop), "itm-snk-1", "itm-snk-1-b")
	put := fresh(t.Context(), s)

	if err := h.Sinks.PutSink(t.Context(), s, put, fence, manage()); err != nil {
		t.Fatalf("PutSink: %v", err)
	}

	got, err := h.Sinks.SinkSecrets(t.Context(), shop, "snk-1")
	if err != nil || len(got) != len(put) {
		t.Fatalf("SinkSecrets = %d items (%v), want %d", len(got), err, len(put))
	}

	for i := range got {
		if got[i].Ref() != put[i].Ref() || got[i].Scope() != shop || got[i].Sink() != "snk-1" ||
			!bytes.Equal(got[i].Ciphertext(), put[i].Ciphertext()) {
			t.Fatalf("SinkSecrets item %d = %s of %s/%s, want the sealed value of %s as PutSink got it", i,
				got[i].Ref(), got[i].Scope(), got[i].Sink(), put[i].Ref())
		}

		if bytes.Contains(got[i].Ciphertext(), []byte(plaintext(got[i].Ref()))) {
			t.Fatalf("SinkSecrets item %d holds the plaintext value", i)
		}
	}

	if _, err = h.Sinks.SinkSecrets(t.Context(), shop, "snk-9"); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("SinkSecrets of an unknown sink: err = %v, want ErrNotFound", err)
	}

	checkRefRules(t, h, fence)
}

// checkRefRules checks that a ref names one value for good: no new value
// under a stored ref, no ref with no value, no ref that a deleted sink held.
func checkRefRules(t *testing.T, h SinkHarness, fence namespace.Fence) {
	t.Helper()

	base := withRefs(sink("snk-1", shop), "itm-snk-1", "itm-snk-1-b")
	base.Version = 1

	refused := []struct {
		name    string
		s       notify.Sink
		secrets []notify.SealedSecret
	}{
		{"a new value under a stored ref", base, []notify.SealedSecret{sealed(t.Context(), base, "itm-snk-1", "new")}},
		{"a ref with no value", withRefs(base, "itm-snk-1", "itm-x"), nil},
		{"a new sink with no value", sink("snk-2", shop), nil},
		{
			"a new sink that names the ref of another sink",
			withRefs(sink("snk-2", shop), "itm-snk-1", "itm-snk-1-b"),
			nil,
		},
	}

	for i := range refused {
		tc := &refused[i]

		if err := h.Sinks.PutSink(t.Context(), tc.s, tc.secrets, fence, manage()); !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("PutSink with %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}

	rotated := withRefs(base, "itm-snk-1", "itm-snk-1-c")
	err := h.Sinks.PutSink(t.Context(), rotated, []notify.SealedSecret{secret(t.Context(), rotated, "itm-snk-1-c")},
		fence, manage())
	if err != nil {
		t.Fatalf("PutSink with a new ref: %v", err)
	}

	got, err := h.Sinks.SinkSecrets(t.Context(), shop, "snk-1")
	if err != nil || len(got) != 2 || got[1].Ref() != "itm-snk-1-c" {
		t.Fatalf("SinkSecrets after the rotation = %d items (%v), want itm-snk-1 and itm-snk-1-c", len(got), err)
	}

	back := withRefs(base, "itm-snk-1", "itm-snk-1-b")
	back.Version = 2

	err = h.Sinks.PutSink(t.Context(), back, []notify.SealedSecret{secret(t.Context(), back, "itm-snk-1-b")}, fence,
		manage())
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("PutSink back to a dropped ref: err = %v, want ErrInvalid", err)
	}
}

func testSinksForeignSeals(t *testing.T, h SinkHarness) {
	t.Helper()

	fence := sinkSetup(t, h)

	if _, err := h.Domain.CreateNamespace(t.Context(), other); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	s := sink("snk-2", shop)

	cases := []struct {
		name string
		item notify.SealedSecret
	}{
		{"a value sealed for another sink", secret(t.Context(), sink("snk-1", shop), "itm-snk-2")},
		{"a value sealed for another scope", secret(t.Context(), sink("snk-2", other), "itm-snk-2")},
	}

	for _, tc := range cases {
		err := h.Sinks.PutSink(t.Context(), s, []notify.SealedSecret{tc.item}, fence, manage())
		if !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("PutSink with %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}

	if got := sinksOf(t, h, shop); len(got) != 0 {
		t.Fatalf("Sinks after the refused puts = %+v, want none", got)
	}
}

func testSinksDelete(t *testing.T, h SinkHarness) {
	t.Helper()

	fence := sinkSetup(t, h)
	putNew(t, h, fence, sink("snk-1", shop))

	if err := h.Sinks.DeleteSink(t.Context(), "snk-1", fence, manage()); err != nil {
		t.Fatalf("DeleteSink: %v", err)
	}

	if got := sinksOf(t, h, shop); len(got) != 0 {
		t.Fatalf("Sinks after the delete = %+v, want none", got)
	}

	if _, err := h.Sinks.SinkSecrets(t.Context(), shop, "snk-1"); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("SinkSecrets after the delete: err = %v, want ErrNotFound", err)
	}

	for _, id := range []string{"snk-1", "snk-9"} {
		if err := h.Sinks.DeleteSink(t.Context(), id, fence, manage()); !errors.Is(err, notify.ErrNotFound) {
			t.Fatalf("DeleteSink of %s again: err = %v, want ErrNotFound", id, err)
		}
	}

	again := sink("snk-1", shop)
	err := h.Sinks.PutSink(
		t.Context(),
		again,
		fresh(t.Context(), again),
		fence,
		manage(),
	)
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("PutSink of a sink with the ref of a deleted sink: err = %v, want ErrInvalid", err)
	}
}

func testSinksScopes(t *testing.T, h SinkHarness) {
	t.Helper()

	fence := sinkSetup(t, h)

	otherFence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	putNew(t, h, fence, sink("snk-1", shop))

	if got := sinksOf(t, h, other); len(got) != 0 {
		t.Fatalf("Sinks of other = %+v, want none", got)
	}

	if _, err = h.Sinks.SinkSecrets(t.Context(), other, "snk-1"); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("SinkSecrets of another scope: err = %v, want ErrNotFound", err)
	}

	steal := sink("snk-1", other)
	steal.Version = 1

	err = h.Sinks.PutSink(t.Context(), steal, fresh(t.Context(), steal), otherFence, entry(other))
	if !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("replace of the sink of another scope: err = %v, want ErrNotFound", err)
	}

	if err = h.Sinks.DeleteSink(t.Context(), "snk-1", otherFence, entry(other)); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("delete of the sink of another scope: err = %v, want ErrNotFound", err)
	}

	if got := sinksOf(t, h, shop); len(got) != 1 || got[0].Version != 1 {
		t.Fatalf("Sinks of shop = %+v, want the first sink unchanged", got)
	}
}

func testSinksAudit(t *testing.T, h SinkHarness) {
	t.Helper()

	fence := sinkSetup(t, h)

	s := sink("snk-1", shop)
	e := manageBy("bob")

	if err := h.Sinks.PutSink(t.Context(), s, fresh(t.Context(), s), fence, e); err != nil {
		t.Fatalf("PutSink: %v", err)
	}

	renamed := s
	renamed.Version, renamed.Name = 1, "Ops team"

	if err := h.Sinks.PutSink(t.Context(), renamed, nil, fence, e); err != nil {
		t.Fatalf("PutSink replace: %v", err)
	}

	if err := h.Sinks.DeleteSink(t.Context(), "snk-1", fence, e); err != nil {
		t.Fatalf("DeleteSink: %v", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 3 {
		t.Fatalf("audit entries = %d (%v), want one for each write", len(entries), err)
	}

	for i := range entries {
		if entries[i].Subject != "bob" || entries[i].Action != e.Action || entries[i].Namespace != shop ||
			entries[i].Kind != audit.KindChange {
			t.Fatalf("audit entry %d = %+v, want the entry of the write", i, entries[i])
		}
	}

	refuseForeignEntry(t, h, fence, s)
}

// refuseForeignEntry checks that a write with an audit entry of another scope
// changes nothing and appends nothing.
func refuseForeignEntry(t *testing.T, h SinkHarness, fence namespace.Fence, s notify.Sink) {
	t.Helper()

	if err := h.Sinks.PutSink(t.Context(), s, fresh(t.Context(), s), fence, entry(other)); err == nil {
		t.Fatal("PutSink with an audit entry of another scope: err = nil, want an error")
	}

	if got := sinksOf(t, h, shop); len(got) != 0 {
		t.Fatalf("Sinks after the refused put = %+v, want none", got)
	}

	if after, err := h.Domain.AuditEntries(t.Context(), shop); err != nil || len(after) != 3 {
		t.Fatalf("audit entries after the refused put = %d (%v), want 3", len(after), err)
	}
}

// sinkFenceCase is a fence that a write must refuse.
type sinkFenceCase struct {
	name string
	// fence returns the fence to try, given the current fence of shop.
	fence func(t *testing.T, h SinkHarness, current namespace.Fence) namespace.Fence
	// put and del are the errors of PutSink, whose caller gives the scope of
	// the row, and of DeleteSink, whose caller names a stored row by its ID.
	put, del error
}

func sinkFenceCases() []sinkFenceCase {
	return []sinkFenceCase{
		{
			"the zero fence",
			func(*testing.T, SinkHarness, namespace.Fence) namespace.Fence { return zeroFence() },
			notify.ErrInvalid, notify.ErrNotFound,
		},
		{
			"the fence of another namespace",
			func(t *testing.T, h SinkHarness, _ namespace.Fence) namespace.Fence {
				t.Helper()

				f, err := h.Domain.CreateNamespace(t.Context(), other)
				if err != nil {
					t.Fatalf("CreateNamespace: %v", err)
				}

				return f
			},
			notify.ErrInvalid, notify.ErrNotFound,
		},
		{
			"a stale fence",
			func(t *testing.T, h SinkHarness, current namespace.Fence) namespace.Fence {
				t.Helper()

				if _, err := h.Domain.BumpFence(t.Context(), current); err != nil {
					t.Fatalf("BumpFence: %v", err)
				}

				return current
			},
			namespace.ErrFenced, namespace.ErrFenced,
		},
	}
}

func runSinkFences(t *testing.T, newHarness SinkFactory) {
	t.Helper()

	for _, f := range sinkFenceCases() {
		t.Run("a write with "+f.name, func(t *testing.T) {
			t.Parallel()
			testSinkFence(t, newHarness(t), f)
		})
	}
}

func testSinkFence(t *testing.T, h SinkHarness, f sinkFenceCase) {
	t.Helper()

	good := sinkSetup(t, h)
	putNew(t, h, good, sink("snk-1", shop))

	before, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil {
		t.Fatalf("AuditEntries: %v", err)
	}

	bad := f.fence(t, h, good)

	// The audit entry names the namespace of the fence, as the entry of a
	// request does. A zero fence names none, so its entry names shop.
	e := manage()
	if bad.Namespace != "" {
		e = entry(bad.Namespace)
	}

	fresh2 := sink("snk-2", shop)
	if err = h.Sinks.PutSink(t.Context(), fresh2, fresh(t.Context(), fresh2), bad, e); !errors.Is(err, f.put) {
		t.Fatalf("create: err = %v, want %v", err, f.put)
	}

	renamed := sink("snk-1", shop)
	renamed.Version, renamed.Name = 1, "Ops team"

	if err = h.Sinks.PutSink(t.Context(), renamed, nil, bad, e); !errors.Is(err, f.put) {
		t.Fatalf("replace: err = %v, want %v", err, f.put)
	}

	if err = h.Sinks.DeleteSink(t.Context(), "snk-1", bad, e); !errors.Is(err, f.del) {
		t.Fatalf("delete: err = %v, want %v", err, f.del)
	}

	if err = h.Sinks.RecordSinkTest(t.Context(), "snk-1", bad, e); !errors.Is(err, f.del) {
		t.Fatalf("test record: err = %v, want %v", err, f.del)
	}

	after, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(after) != len(before) {
		t.Fatalf("audit entries = %d (%v), want %d", len(after), err, len(before))
	}

	if got := sinksOf(t, h, shop); len(got) != 1 || got[0].Version != 1 || got[0].Name != "Ops" {
		t.Fatalf("Sinks after the refused writes = %+v, want the first sink unchanged", got)
	}
}

func testSinksRecordTest(t *testing.T, h SinkHarness) {
	t.Helper()

	fence := sinkSetup(t, h)

	otherFence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	putNew(t, h, fence, sink("snk-1", shop))

	before, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil {
		t.Fatalf("AuditEntries: %v", err)
	}

	if err = h.Sinks.RecordSinkTest(t.Context(), "snk-1", fence, manageBy("bob")); err != nil {
		t.Fatalf("RecordSinkTest: %v", err)
	}

	after, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(after) != len(before)+1 || after[len(after)-1].Subject != "bob" {
		t.Fatalf("audit entries = %d (%v), want one more, of bob", len(after), err)
	}

	if got := sinksOf(t, h, shop); len(got) != 1 || got[0].Version != 1 {
		t.Fatalf("Sinks after the test record = %+v, want the sink unchanged", got)
	}

	for name, id := range map[string]string{"an unknown sink": "snk-9", "a sink of another scope": "snk-1"} {
		err = h.Sinks.RecordSinkTest(
			t.Context(),
			id,
			otherFence,
			entry(other),
		)
		if !errors.Is(err, notify.ErrNotFound) {
			t.Fatalf("RecordSinkTest of %s: err = %v, want ErrNotFound", name, err)
		}
	}
}
