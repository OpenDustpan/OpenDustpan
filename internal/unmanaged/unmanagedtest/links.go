package unmanagedtest

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/unmanaged"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// linkCases returns the cases of the links between namespaces. Without a
// link, a namespace reads its own units only. Each case is one way that a
// namespace could reach into another namespace without a link.
func linkCases() []suiteCase {
	return []suiteCase{
		{"links: a link is two-way and refuses itself", testLinkRules},
		{"links: a namespace cannot enumerate the keys of another", testLinkEnumeration},
		{"links: another namespace cannot resolve a finding in silence", testLinkSilentResolve},
		{"links: another namespace cannot flood findings of managed twice", testLinkFlooding},
		{"links: an unlink leaves out the other namespace and the state check resolves", testUnlinkResolves},
		{"links: a namespace under a purged name gets no link", testLinkPurgedName},
		{"links: a soft delete of a namespace stops its links at once", testLinkSoftDelete},
		{"links: a restore of a namespace brings its links back", testLinkRestore},
	}
}

// linkedPair makes the namespaces acme and other, links them, and makes a
// unit of each that holds key. It returns the fence of acme.
func linkedPair(t *testing.T, h Harness, key unmanaged.ResourceKey) namespace.Fence {
	t.Helper()

	acme, other := h.Namespace(t, "acme"), h.Namespace(t, "other")
	a, x := Unit("acme", "a"), Unit("other", "x")

	must(t, "replace a", h.Index.ReplaceUnit(t.Context(), a, 1, []unmanaged.IndexEntry{entry(a, key)}, acme))
	must(t, "replace x", h.Index.ReplaceUnit(t.Context(), x, 1, []unmanaged.IndexEntry{entry(x, key)}, other))
	must(t, "link", h.Links.Link(t.Context(), "acme", "other", auditOf("", "server:link-namespaces")))

	return acme
}

// stateCheck runs a state check of acme as the server does: it classifies
// the contested keys and the key of each finding that is not resolved,
// records them, resolves the findings that it did not observe, and ends the
// sweep.
func stateCheck(t *testing.T, h Harness, acme namespace.Fence) {
	t.Helper()

	contested, err := h.Index.Contested(t.Context(), "acme", unmanaged.ResourceKey{Provider: "", ID: ""}, 10)
	must(t, "contested", err)

	var keys []unmanaged.ResourceKey
	for _, c := range contested {
		keys = append(keys, c.Key)
	}

	all := listAll(t, h)
	for i := range all {
		if all[i].Status != unmanaged.StatusResolved && !slices.Contains(keys, all[i].Key) {
			keys = append(keys, all[i].Key)
		}
	}

	s := matchingScan(t, h, acme, unmanaged.ScanState, nil)
	if len(keys) > 0 {
		_, err = h.Findings.Record(t.Context(), s, classify(t, h, nil, keys...), at(), acme)
		must(t, "record", err)
	}

	_, err = h.Findings.ResolveUnseen(t.Context(), s, at(), acme)
	must(t, "resolve unseen", err)

	_, err = h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
		Namespace: "acme", ID: s.ID, Event: unmanaged.ScanEventMatched,
		Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
	}, acme, auditOf("acme", "namespace:start-sweep"))
	must(t, "end the state check", err)
}

// wantHolders checks that the holders of key in ns are exactly the units of
// want, and that the contests of ns name only units of want.
func wantHolders(t *testing.T, h Harness, ns namespace.Name, key unmanaged.ResourceKey, want ...graph.Unit) {
	t.Helper()

	got, err := h.Index.Holders(t.Context(), ns, []unmanaged.ResourceKey{key})
	must(t, "holders", err)

	units := make([]graph.Unit, 0, len(got[key]))
	for _, hd := range got[key] {
		units = append(units, hd.Unit)
	}

	if !slices.Equal(units, want) {
		t.Errorf("holders of %s in %s: %v, want %v", key, ns, units, want)
	}

	contested, err := h.Index.Contested(t.Context(), ns, unmanaged.ResourceKey{Provider: "", ID: ""}, 10)
	must(t, "contested", err)

	for _, c := range contested {
		for _, hd := range c.Holders {
			if !slices.Contains(want, hd.Unit) {
				t.Errorf("contest of %s in %s names %s, want only %v", c.Key, ns, hd.Unit, want)
			}
		}
	}
}

func testUnlinkResolves(t *testing.T, h Harness) {
	t.Helper()

	key := AWSKey("shared")
	acme := linkedPair(t, h, key)
	a, x := Unit("acme", "a"), Unit("other", "x")

	stateCheck(t, h, acme)

	if fd := firstFinding(t, h); fd.Kind != unmanaged.KindManagedTwice || fd.Status != unmanaged.StatusOpen ||
		!fd.Elsewhere {
		t.Fatalf("finding %+v with a link, want open, managed twice and elsewhere", fd)
	}

	must(t, "unlink", h.Links.Unlink(t.Context(), "acme", "other", auditOf("", "server:link-namespaces")))

	// The reads leave out the other namespace at once.
	wantHolders(t, h, "acme", key, a)
	wantHolders(t, h, "other", key, x)

	// The next state check observes the key again: one live unit of acme
	// holds it, and the finding resolves.
	stateCheck(t, h, acme)

	if fd := firstFinding(t, h); fd.Status != unmanaged.StatusResolved {
		t.Errorf("finding %+v after the unlink and a state check, want resolved", fd)
	}
}

func testLinkPurgedName(t *testing.T, h Harness) {
	t.Helper()

	key := AWSKey("shared")
	linkedPair(t, h, key)
	a := Unit("acme", "a")

	// The purge of other drops its units and its links, then deletes it.
	must(t, "drop x", h.Index.DropUnit(t.Context(), Unit("other", "x"), namespace.Fence{
		Namespace: "other", FailoverVersion: 1,
	}))
	must(t, "drop the links", h.Links.DropNamespace(t.Context(), "other", auditOf("", "server:purge-namespace")))

	if got, err := h.Links.Linked(t.Context(), "acme"); err != nil || len(got) != 0 {
		t.Errorf("linked with acme after the purge dropped the links: %v, %v; want none", got, err)
	}

	h.PurgeNamespace(t, "other")

	// A new namespace under the name gets nothing of the purged one.
	other := h.Namespace(t, "other")
	y := Unit("other", "y")
	must(t, "replace y", h.Index.ReplaceUnit(t.Context(), y, 1, []unmanaged.IndexEntry{entry(y, key)}, other))

	for _, ns := range []namespace.Name{"acme", "other"} {
		got, err := h.Links.Linked(t.Context(), ns)
		must(t, "linked", err)

		if len(got) != 0 {
			t.Errorf("linked with %s after the purge: %v, want none", ns, got)
		}
	}

	wantHolders(t, h, "acme", key, a)
	wantHolders(t, h, "other", key, y)
}

// softDeleteOther soft deletes the namespace other of linkedPair and checks
// that its link classifies nothing.
func softDeleteOther(t *testing.T, h Harness, key unmanaged.ResourceKey) {
	t.Helper()

	must(t, "soft delete", h.Links.SetNamespaceDeleted(t.Context(), "other", true))

	got, err := h.Links.Linked(t.Context(), "acme")
	must(t, "linked", err)

	if len(got) != 0 {
		t.Errorf("linked with acme while other is deleted: %v, want none", got)
	}

	wantHolders(t, h, "acme", key, Unit("acme", "a"))

	obs := classify(t, h, nil, key)
	if obs[0].Class != unmanaged.ClassManaged {
		t.Errorf("class of %s while other is deleted: %s, want managed", key, obs[0].Class)
	}
}

func testLinkSoftDelete(t *testing.T, h Harness) {
	t.Helper()

	key := AWSKey("shared")
	linkedPair(t, h, key)
	softDeleteOther(t, h, key)
}

func testLinkRestore(t *testing.T, h Harness) {
	t.Helper()

	key := AWSKey("shared")
	linkedPair(t, h, key)
	softDeleteOther(t, h, key)

	must(t, "restore", h.Links.SetNamespaceDeleted(t.Context(), "other", false))

	got, err := h.Links.Linked(t.Context(), "acme")
	must(t, "linked", err)

	if !slices.Equal(got, []namespace.Name{"other"}) {
		t.Errorf("linked with acme after the restore: %v, want other", got)
	}

	wantHolders(t, h, "acme", key, Unit("acme", "a"), Unit("other", "x"))

	if obs := classify(t, h, nil, key); obs[0].Class != unmanaged.ClassManagedTwice {
		t.Errorf("class of %s after the restore: %s, want managed twice", key, obs[0].Class)
	}
}

// classify runs the classification of the match of a sweep in namespace
// acme for keys, as the server does: the holders from the index, the links,
// then Classify. record gives the record of a cloud sweep that saw a key, and
// is nil for a state check.
func classify(
	t *testing.T,
	h Harness,
	record func(unmanaged.ResourceKey) *unmanaged.Record,
	keys ...unmanaged.ResourceKey,
) []unmanaged.Observation {
	t.Helper()

	const ns namespace.Name = "acme"

	holders, err := h.Index.Holders(t.Context(), ns, keys)
	must(t, "holders", err)

	linked, err := h.Links.Linked(t.Context(), ns)
	must(t, "linked", err)

	obs := make([]unmanaged.Observation, 0, len(keys))
	for _, k := range keys {
		o := observation(k, unmanaged.Classify(ns, linked, record != nil, holders[k]), holders[k]...)
		if record != nil {
			o.Record = record(k)
		}

		obs = append(obs, o)
	}

	return obs
}

// seenBucket is the record of a bucket that a cloud sweep of the account
// 111111111111 saw.
func seenBucket(k unmanaged.ResourceKey) *unmanaged.Record {
	return &unmanaged.Record{
		Provider: k.Provider, ID: k.ID, Type: "AWS::S3::Bucket", Tags: nil, Region: "", Name: "",
		Scope: "111111111111",
	}
}

func testLinkRules(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	h.Namespace(t, "other")
	e := auditOf("", "server:link-namespaces")

	wantErr(t, "link to itself", h.Links.Link(t.Context(), "acme", "acme", e), unmanaged.ErrInvalidLink)
	wantErr(t, "link to an unknown namespace", h.Links.Link(t.Context(), "acme", "none", e), unmanaged.ErrNotFound)
	wantErr(t, "unlink with no link", h.Links.Unlink(t.Context(), "acme", "other", e), unmanaged.ErrNotFound)

	must(t, "link", h.Links.Link(t.Context(), "acme", "other", e))
	must(t, "link again", h.Links.Link(t.Context(), "other", "acme", e))

	for _, ns := range []namespace.Name{"acme", "other"} {
		got, err := h.Links.Linked(t.Context(), ns)
		must(t, "linked", err)

		if len(got) != 1 {
			t.Errorf("linked with %s: %v, want the other namespace", ns, got)
		}
	}

	must(t, "unlink", h.Links.Unlink(t.Context(), "other", "acme", e))

	if got, err := h.Links.Linked(t.Context(), "acme"); err != nil || len(got) != 0 {
		t.Errorf("linked after unlink: %v, %v; want none", got, err)
	}
}

func testLinkEnumeration(t *testing.T, h Harness) {
	t.Helper()

	acme, other := h.Namespace(t, "acme"), h.Namespace(t, "other")
	a, x := Unit("acme", "a"), Unit("other", "x")

	must(
		t,
		"replace a",
		h.Index.ReplaceUnit(t.Context(), a, 1, []unmanaged.IndexEntry{entry(a, AWSKey("shared"))}, acme),
	)
	must(t, "replace x", h.Index.ReplaceUnit(t.Context(), x, 1, []unmanaged.IndexEntry{
		entry(x, AWSKey("shared")), entry(x, AWSKey("secret-name")),
	}, other))

	// acme guesses the names of the buckets of other. The index answers no
	// holder of other for either key.
	got, err := h.Index.Holders(t.Context(), "acme", []unmanaged.ResourceKey{AWSKey("shared"), AWSKey("secret-name")})
	must(t, "holders", err)

	if hs := got[AWSKey("shared")]; len(hs) != 1 || hs[0].Unit != a {
		t.Errorf("holders of shared %+v, want a only", hs)
	}

	if hs, ok := got[AWSKey("secret-name")]; ok {
		t.Errorf("holders of a key of other only: %+v, want none", hs)
	}

	contested, err := h.Index.Contested(t.Context(), "acme", unmanaged.ResourceKey{Provider: "", ID: ""}, 10)
	must(t, "contested", err)

	if len(contested) != 0 {
		t.Errorf("contested %+v with no link, want none", contested)
	}
}

func testLinkSilentResolve(t *testing.T, h Harness) {
	t.Helper()

	acme, other := h.Namespace(t, "acme"), h.Namespace(t, "other")
	key := AWSKey("orphan")
	scopes := []unmanaged.ReportScope{{Provider: "aws", Scope: "111111111111", Complete: true, Error: "", Regions: nil}}

	first := matchingScan(t, h, acme, unmanaged.ScanCloud, scopes)
	_, err := h.Findings.Record(t.Context(), first, classify(t, h, seenBucket, key), at(), acme)
	must(t, "record first", err)

	_, err = h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
		Namespace: "acme", ID: first.ID, Event: unmanaged.ScanEventMatched,
		Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
	}, acme, auditOf("acme", "namespace:start-sweep"))
	must(t, "end first", err)

	// A unit of other, which acme did not link, starts to hold the key. The
	// next sweep of acme still classifies it as not in any unit.
	x := Unit("other", "x")
	must(t, "replace x", h.Index.ReplaceUnit(t.Context(), x, 1, []unmanaged.IndexEntry{entry(x, key)}, other))

	second := matchingScan(t, h, acme, unmanaged.ScanCloud, scopes)
	counts, err := h.Findings.Record(t.Context(), second, classify(t, h, seenBucket, key), at(), acme)
	must(t, "record second", err)

	if fd := firstFinding(t, h); fd.Status != unmanaged.StatusOpen || fd.Elsewhere || counts.Resolved != 0 {
		t.Errorf("finding %+v counts %+v, want open and not elsewhere with no link", fd, counts)
	}

	_, err = h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
		Namespace: "acme", ID: second.ID, Event: unmanaged.ScanEventMatched,
		Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
	}, acme, auditOf("acme", "namespace:start-sweep"))
	must(t, "end second", err)

	// With a link, the unit of other manages the resource, and the finding
	// resolves.
	must(t, "link", h.Links.Link(t.Context(), "acme", "other", auditOf("", "server:link-namespaces")))

	third := matchingScan(t, h, acme, unmanaged.ScanCloud, scopes)
	_, err = h.Findings.Record(t.Context(), third, classify(t, h, seenBucket, key), at(), acme)
	must(t, "record third", err)

	if fd := firstFinding(t, h); fd.Status != unmanaged.StatusResolved {
		t.Errorf("finding %+v with a link, want resolved", fd)
	}
}

func testLinkFlooding(t *testing.T, h Harness) {
	t.Helper()

	acme, other := h.Namespace(t, "acme"), h.Namespace(t, "other")
	a := Unit("acme", "a")
	keys := []unmanaged.ResourceKey{AWSKey("k1"), AWSKey("k2"), AWSKey("k3")}

	entries := make([]unmanaged.IndexEntry, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, entry(a, k))
	}

	must(t, "replace a", h.Index.ReplaceUnit(t.Context(), a, 1, entries, acme))

	// other copies every key of acme into units of its own.
	for _, id := range []string{"x", "y"} {
		u := Unit("other", id)

		copies := make([]unmanaged.IndexEntry, 0, len(keys))
		for _, k := range keys {
			copies = append(copies, entry(u, k))
		}

		must(t, "replace "+id, h.Index.ReplaceUnit(t.Context(), u, 1, copies, other))
	}

	contested, err := h.Index.Contested(t.Context(), "acme", unmanaged.ResourceKey{Provider: "", ID: ""}, 10)
	must(t, "contested", err)

	if len(contested) != 0 {
		t.Errorf("contested in acme %+v, want none with no link", contested)
	}

	s := matchingScan(t, h, acme, unmanaged.ScanState, nil)
	_, err = h.Findings.Record(t.Context(), s, classify(t, h, nil, keys...), at(), acme)
	must(t, "record", err)

	if got := listAll(t, h); len(got) != 0 {
		t.Errorf("findings of acme %+v, want none: a unit of acme manages each key once", got)
	}

	// The copies are managed twice in other, between its own units only.
	theirs, err := h.Index.Contested(t.Context(), "other", unmanaged.ResourceKey{Provider: "", ID: ""}, 10)
	must(t, "contested in other", err)

	for _, c := range theirs {
		for _, hd := range c.Holders {
			if hd.Unit.Namespace != "other" {
				t.Errorf("contest of other %+v names a unit of %s", c, hd.Unit.Namespace)
			}
		}
	}
}
