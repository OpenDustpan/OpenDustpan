package unmanagedtest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
)

// indexCases returns the cases of the resource index.
func indexCases() []suiteCase {
	return []suiteCase{
		{"index: holders of a linked namespace only", testHoldersAcross},
		{"index: an older state item changes nothing", testIndexOrder},
		{"index: a soft delete marks the holders", testIndexDeleted},
		{"index: a purge drops the unit", testIndexDrop},
		{"index: contested keys of a namespace", testContested},
		{"index: a stale fence refuses each write", testIndexFenced},
	}
}

func testHoldersAcross(t *testing.T, h Harness) {
	t.Helper()

	acme, other := h.Namespace(t, "acme"), h.Namespace(t, "other")
	a, b := Unit("acme", "a"), Unit("other", "b")

	must(t, "replace a", h.Index.ReplaceUnit(t.Context(), a, 1, []unmanaged.IndexEntry{entry(a, AWSKey("logs"))}, acme))
	must(
		t,
		"replace b",
		h.Index.ReplaceUnit(t.Context(), b, 1, []unmanaged.IndexEntry{entry(b, AWSKey("logs"))}, other),
	)

	alone, err := h.Index.Holders(t.Context(), "acme", []unmanaged.ResourceKey{AWSKey("logs")})
	must(t, "holders before the link", err)

	if hs := alone[AWSKey("logs")]; len(hs) != 1 || hs[0].Unit != a {
		t.Errorf("holders %+v before the link, want a only", hs)
	}

	must(t, "link", h.Links.Link(t.Context(), "acme", "other", auditOf("", "server:link-namespaces")))

	got, err := h.Index.Holders(t.Context(), "acme", []unmanaged.ResourceKey{AWSKey("logs"), AWSKey("none")})
	must(t, "holders", err)

	if hs := got[AWSKey("logs")]; len(hs) != 2 || hs[0].Unit != a || hs[1].Unit != b {
		t.Errorf("holders %+v, want a then b", hs)
	}

	if _, ok := got[AWSKey("none")]; ok {
		t.Error("a key with no holder is in the map")
	}

	_, err = h.Index.Holders(t.Context(), "acme", []unmanaged.ResourceKey{{Provider: "aws", ID: "logs"}})
	wantErr(t, "holders of an invalid key", err, unmanaged.ErrInvalidKey)
}

func testIndexOrder(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	a := Unit("acme", "a")

	must(t, "replace 2", h.Index.ReplaceUnit(t.Context(), a, 2, []unmanaged.IndexEntry{entry(a, AWSKey("new"))}, f))
	must(t, "replace 1", h.Index.ReplaceUnit(t.Context(), a, 1, []unmanaged.IndexEntry{entry(a, AWSKey("old"))}, f))

	got, err := h.Index.Holders(t.Context(), "acme", []unmanaged.ResourceKey{AWSKey("new"), AWSKey("old")})
	must(t, "holders", err)

	if len(got[AWSKey("new")]) != 1 || len(got[AWSKey("old")]) != 0 {
		t.Errorf("holders %+v, want only the key of the newer item", got)
	}
}

func testIndexDeleted(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	a := Unit("acme", "a")

	must(t, "replace", h.Index.ReplaceUnit(t.Context(), a, 1, []unmanaged.IndexEntry{entry(a, AWSKey("logs"))}, f))
	must(t, "set deleted", h.Index.SetDeleted(t.Context(), a, true, f))

	got, err := h.Index.Holders(t.Context(), "acme", []unmanaged.ResourceKey{AWSKey("logs")})
	must(t, "holders", err)

	if hs := got[AWSKey("logs")]; len(hs) != 1 || !hs[0].Deleted {
		t.Errorf("holders %+v, want one deleted holder", hs)
	}

	must(t, "restore", h.Index.SetDeleted(t.Context(), a, false, f))
	must(t, "unit with no entry", h.Index.SetDeleted(t.Context(), Unit("acme", "none"), true, f))
}

func testIndexDrop(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	a := Unit("acme", "a")

	must(t, "replace", h.Index.ReplaceUnit(t.Context(), a, 1, []unmanaged.IndexEntry{entry(a, AWSKey("logs"))}, f))
	must(t, "drop", h.Index.DropUnit(t.Context(), a, f))

	got, err := h.Index.Holders(t.Context(), "acme", []unmanaged.ResourceKey{AWSKey("logs")})
	must(t, "holders", err)

	if len(got) != 0 {
		t.Errorf("holders %+v, want none after the purge", got)
	}
}

func testContested(t *testing.T, h Harness) {
	t.Helper()

	acme, other := h.Namespace(t, "acme"), h.Namespace(t, "other")
	c, gone := Unit("acme", "c"), Unit("acme", "gone")

	must(t, "link", h.Links.Link(t.Context(), "acme", "other", auditOf("", "server:link-namespaces")))

	for _, w := range []struct {
		fence namespace.Fence
		unit  string
		key   string
	}{
		{acme, "a", "twice"},
		{acme, "b", "twice"},
		{acme, "c", "single"},
		{acme, "gone", "left"},
		{other, "x", "across"},
		{acme, "c", "across"},
	} {
		u := Unit(w.fence.Namespace.String(), w.unit)
		must(
			t,
			"replace",
			h.Index.ReplaceUnit(t.Context(), u, 1, []unmanaged.IndexEntry{entry(u, AWSKey(w.key))}, w.fence),
		)
	}

	// c holds two keys in two entries. "across" is managed twice across two
	// linked namespaces, and "single" is managed once.
	must(t, "replace c", h.Index.ReplaceUnit(t.Context(), c, 2,
		[]unmanaged.IndexEntry{entry(c, AWSKey("single")), entry(c, AWSKey("across"))}, acme))
	must(t, "delete gone", h.Index.SetDeleted(t.Context(), gone, true, acme))

	got, err := h.Index.Contested(t.Context(), "acme", unmanaged.ResourceKey{Provider: "", ID: ""}, 10)
	must(t, "contested", err)

	keys := make([]string, 0, len(got))
	for _, c := range got {
		keys = append(keys, c.Key.ID)
	}

	want := []string{"arn:aws:s3:::across", "arn:aws:s3:::left", "arn:aws:s3:::twice"}
	if len(keys) != len(want) || keys[0] != want[0] || keys[1] != want[1] || keys[2] != want[2] {
		t.Errorf("contested %v, want %v", keys, want)
	}

	if len(got) < 2 {
		t.Fatalf("contested %d keys, want 2 or more for the page", len(got))

		return
	}

	page, err := h.Index.Contested(t.Context(), "acme", got[0].Key, 1)
	must(t, "contested page", err)

	if len(page) != 1 || page[0].Key != got[1].Key {
		t.Errorf("page %+v, want the second key", page)
	}
}

func testIndexFenced(t *testing.T, h Harness) {
	t.Helper()

	f := stale(h.Namespace(t, "acme"))
	a := Unit("acme", "a")

	wantErr(t, "replace", h.Index.ReplaceUnit(t.Context(), a, 1, nil, f), namespace.ErrFenced)
	wantErr(t, "set deleted", h.Index.SetDeleted(t.Context(), a, true, f), namespace.ErrFenced)
	wantErr(t, "drop", h.Index.DropUnit(t.Context(), a, f), namespace.ErrFenced)
}
