package unmanagedtest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
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

	_, err = h.Scans.AdvanceScan(t.Context(), "acme", first.ID, unmanaged.ScanEventMatched,
		unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""}, acme, auditOf("acme", "namespace:start-sweep"))
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

	_, err = h.Scans.AdvanceScan(t.Context(), "acme", second.ID, unmanaged.ScanEventMatched,
		unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""}, acme, auditOf("acme", "namespace:start-sweep"))
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
