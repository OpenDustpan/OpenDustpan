package blobtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
)

// runPages runs the cases of the paged list.
func runPages(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, s blob.Store)
	}{
		{"list page holds at most the limit ordered by key", testPageLimit},
		{"list pages walk every object once", testPageWalk},
		{"list page of a missing prefix is empty", testPageMissing},
		{"list page of the empty prefix lists the whole store", testPageEmptyPrefix},
		{"list page with after past the end is empty", testPageAfterPastEnd},
		{"list page with a limit out of range is ErrInvalidPage", testPageBadLimit},
		{"list page with an invalid after key is ErrInvalidKey", testPageBadAfter},
		{"list page stays inside its prefix", testPageInsidePrefix},
		{"list page with an after key outside the prefix is ErrInvalidKey", testPageAfterOutside},
		{"list walk sees a key added during the walk once or not at all", testPageAddedDuringWalk},
		{"list pages keep the byte order of the keys", testPageByteOrder},
		{"list page with a canceled context fails", testPageCanceled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newStore(t))
		})
	}
}

// putUnit stores n objects under "unit/" with names that sort in order of i.
func putUnit(t *testing.T, s blob.Store, n int) []string {
	t.Helper()

	want := make([]string, 0, n)

	for i := range n {
		name := fmt.Sprintf("unit/obj-%03d", i)
		put(t, s, mustKey(t, name), []byte("x"))
		want = append(want, name)
	}

	return want
}

// page reads one page and fails the test on an error.
func page(t *testing.T, s blob.Store, req blob.PageRequest) blob.Page {
	t.Helper()

	got, err := s.ListPage(t.Context(), req)
	if err != nil {
		t.Fatalf("list page %+v: %v", req, err)
	}

	return got
}

// testPageLimit proves page boundaries: a page holds at most the limit, a
// page that is not the last one has a Next, and the last page has none, also
// when the count of objects is a multiple of the limit.
func testPageLimit(t *testing.T, s blob.Store) {
	t.Helper()

	want := putUnit(t, s, 4)

	cases := []struct {
		name  string
		limit int
		pages [][]string
	}{
		{"limit 1", 1, [][]string{want[0:1], want[1:2], want[2:3], want[3:4]}},
		{"limit 3 leaves a short last page", 3, [][]string{want[0:3], want[3:4]}},
		{"limit 2 divides the count", 2, [][]string{want[0:2], want[2:4]}},
		{"limit above the count", blob.MaxPageLimit, [][]string{want}},
	}

	for _, tc := range cases {
		var after blob.Key

		for i, wantKeys := range tc.pages {
			got := page(t, s, blob.PageRequest{Prefix: "unit/", After: after, Limit: tc.limit})

			if keys := objectKeys(got.Objects); !slices.Equal(keys, wantKeys) {
				t.Fatalf("%s: page %d = %v, want %v", tc.name, i, keys, wantKeys)
			}

			last := i == len(tc.pages)-1

			wantNext := blob.Key("")
			if !last {
				wantNext = got.Objects[len(got.Objects)-1].Key
			}

			if got.Next != wantNext {
				t.Fatalf("%s: page %d next = %q, want %q", tc.name, i, got.Next, wantNext)
			}

			after = got.Next
		}
	}
}

// testPageWalk proves a walk sees each object once, in order.
func testPageWalk(t *testing.T, s blob.Store) {
	t.Helper()

	want := putUnit(t, s, 7)

	var (
		got   []string
		after blob.Key
	)

	for pages := 0; ; pages++ {
		if pages > len(want) {
			t.Fatal("walk does not end")
		}

		p := page(t, s, blob.PageRequest{Prefix: "unit", After: after, Limit: 2})
		got = append(got, objectKeys(p.Objects)...)

		if p.Next == "" {
			break
		}

		after = p.Next
	}

	if !slices.Equal(got, want) {
		t.Fatalf("walk = %v, want %v", got, want)
	}

	all, err := ListAll(t.Context(), s, "unit/")
	if err != nil || !slices.Equal(objectKeys(all), want) {
		t.Fatalf("Walk = %v, %v; want %v", objectKeys(all), err, want)
	}
}

// testPageMissing proves a prefix that names nothing gives an empty page.
func testPageMissing(t *testing.T, s blob.Store) {
	t.Helper()

	put(t, s, mustKey(t, "unit/a"), []byte("x"))

	got := page(t, s, blob.PageRequest{Prefix: "ghost/", After: "", Limit: 10})
	if len(got.Objects) != 0 || got.Next != "" {
		t.Fatalf("page of a missing prefix = %v, next %q; want empty", objectKeys(got.Objects), got.Next)
	}
}

// testPageEmptyPrefix proves the empty prefix: an empty store gives an empty
// page, and a store with objects lists them all.
func testPageEmptyPrefix(t *testing.T, s blob.Store) {
	t.Helper()

	empty := page(t, s, blob.PageRequest{Prefix: "", After: "", Limit: 10})
	if len(empty.Objects) != 0 || empty.Next != "" {
		t.Fatalf("page of an empty store = %v, next %q; want empty", objectKeys(empty.Objects), empty.Next)
	}

	put(t, s, mustKey(t, "a/1"), []byte("x"))
	put(t, s, mustKey(t, "b/1"), []byte("x"))

	got := page(t, s, blob.PageRequest{Prefix: "", After: "", Limit: 10})
	if want := []string{"a/1", "b/1"}; !slices.Equal(objectKeys(got.Objects), want) || got.Next != "" {
		t.Fatalf("page of the whole store = %v, next %q; want %v", objectKeys(got.Objects), got.Next, want)
	}
}

// testPageAfterPastEnd proves an After at or after the last key gives an
// empty last page.
func testPageAfterPastEnd(t *testing.T, s blob.Store) {
	t.Helper()

	want := putUnit(t, s, 3)

	for _, after := range []string{want[2], "unit/zzz"} {
		got := page(t, s, blob.PageRequest{Prefix: "unit/", After: mustKey(t, after), Limit: 10})
		if len(got.Objects) != 0 || got.Next != "" {
			t.Fatalf("page after %q = %v, next %q; want empty", after, objectKeys(got.Objects), got.Next)
		}
	}
}

// testPageBadLimit proves a limit out of range fails with ErrInvalidPage and
// that the failure happens before any listing.
func testPageBadLimit(t *testing.T, s blob.Store) {
	t.Helper()

	putUnit(t, s, 1)

	for _, limit := range []int{-1, 0, blob.MaxPageLimit + 1} {
		_, err := s.ListPage(t.Context(), blob.PageRequest{Prefix: "unit/", After: "", Limit: limit})
		if !errors.Is(err, blob.ErrInvalidPage) {
			t.Fatalf("limit %d: err = %v, want ErrInvalidPage", limit, err)
		}
	}
}

// testPageBadAfter proves an After that is not a valid key fails with
// ErrInvalidKey.
func testPageBadAfter(t *testing.T, s blob.Store) {
	t.Helper()

	putUnit(t, s, 1)

	for _, after := range []blob.Key{"unit/../x", "unit/A", "/unit/a", "unit//a"} {
		_, err := s.ListPage(t.Context(), blob.PageRequest{Prefix: "unit/", After: after, Limit: 10})
		if !errors.Is(err, blob.ErrInvalidKey) {
			t.Fatalf("after %q: err = %v, want ErrInvalidKey", after, err)
		}
	}
}

// testPageInsidePrefix proves a page holds only keys of the directory that
// the prefix names, not keys that share only a string prefix with it.
func testPageInsidePrefix(t *testing.T, s blob.Store) {
	t.Helper()

	put(t, s, mustKey(t, "unit/a"), []byte("x"))
	put(t, s, mustKey(t, "unit-2/a"), []byte("x"))
	put(t, s, mustKey(t, "unit/b/c"), []byte("x"))

	for _, prefix := range []string{"unit/", "unit"} {
		got := page(t, s, blob.PageRequest{Prefix: prefix, After: "", Limit: 10})
		if want := []string{"unit/a", "unit/b/c"}; !slices.Equal(objectKeys(got.Objects), want) {
			t.Fatalf("page of %q = %v, want %v", prefix, objectKeys(got.Objects), want)
		}
	}
}

// testPageAfterOutside proves an After outside the prefix fails with
// ErrInvalidKey, also when it only shares a string prefix with it.
func testPageAfterOutside(t *testing.T, s blob.Store) {
	t.Helper()

	putUnit(t, s, 1)

	for _, after := range []string{"other/a", "unit-2/a", "unit"} {
		_, err := s.ListPage(t.Context(), blob.PageRequest{Prefix: "unit/", After: mustKey(t, after), Limit: 10})
		if !errors.Is(err, blob.ErrInvalidKey) {
			t.Fatalf("after %q: err = %v, want ErrInvalidKey", after, err)
		}
	}
}

// testPageAddedDuringWalk proves a key that a writer adds during a walk
// shows once or not at all, and the keys that existed for the whole walk show
// once each.
func testPageAddedDuringWalk(t *testing.T, s blob.Store) {
	t.Helper()

	want := putUnit(t, s, 4)

	first := page(t, s, blob.PageRequest{Prefix: "unit/", After: "", Limit: 2})
	if first.Next == "" {
		t.Fatal("first page has no next")
	}

	// One key sorts before the cursor, one after it.
	put(t, s, mustKey(t, "unit/obj-000a"), []byte("x"))
	put(t, s, mustKey(t, "unit/obj-999"), []byte("x"))

	got := objectKeys(first.Objects)
	after := first.Next

	for pages := 0; after != ""; pages++ {
		if pages > 10 {
			t.Fatal("walk does not end")
		}

		p := page(t, s, blob.PageRequest{Prefix: "unit/", After: after, Limit: 2})
		got = append(got, objectKeys(p.Objects)...)
		after = p.Next
	}

	seen := map[string]int{}
	for _, k := range got {
		seen[k]++
	}

	for _, k := range want {
		if seen[k] != 1 {
			t.Fatalf("key %s seen %d times in %v, want once", k, seen[k], got)
		}
	}

	for k, n := range seen {
		if n != 1 {
			t.Fatalf("key %s seen %d times in %v, want at most once", k, n, got)
		}
	}

	if !slices.IsSorted(got) {
		t.Fatalf("walk = %v, want key order", got)
	}
}

// testPageCanceled proves a canceled context fails the page.
func testPageCanceled(t *testing.T, s blob.Store) {
	t.Helper()

	putUnit(t, s, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.ListPage(ctx, blob.PageRequest{Prefix: "unit/", After: "", Limit: 10}); err == nil {
		t.Fatal("page with a canceled context = nil error, want error")
	}
}

// testPageByteOrder proves that every page follows the byte order of the key
// text, not the order of a directory tree: "a-c" sorts before "a/b", because
// "-" is byte 0x2d and "/" is 0x2f. A page can start after a key inside a
// directory and after the key of a whole directory.
func testPageByteOrder(t *testing.T, s blob.Store) {
	t.Helper()

	names := []string{
		"unit/a/b", "unit/a-c", "unit/a.b", "unit/a_b", "unit/a0", "unit/a/b-1", "unit/a/d/e", "unit/a/c", "unit/b",
	}

	for _, name := range names {
		put(t, s, mustKey(t, name), []byte("x"))
	}

	want := slices.Clone(names)
	slices.Sort(want)

	for _, limit := range []int{1, 2, 3, len(names)} {
		if got := walkPages(t, s, limit); !slices.Equal(got, want) {
			t.Fatalf("limit %d: walk = %v, want %v", limit, got, want)
		}
	}

	// A page that starts after any key, also one that is not stored, holds the
	// stored keys after it.
	for _, after := range append(slices.Clone(want), "unit/a/aa", "unit/a-", "unit/a/d/f", "unit/a0z") {
		var wantAfter []string

		for _, name := range want {
			if name > after {
				wantAfter = append(wantAfter, name)
			}
		}

		got := page(t, s, blob.PageRequest{Prefix: "unit/", After: blob.Key(after), Limit: blob.MaxPageLimit})
		if keys := objectKeys(got.Objects); !slices.Equal(keys, wantAfter) && (len(keys) != 0 || len(wantAfter) != 0) {
			t.Fatalf("after %q: page = %v, want %v", after, keys, wantAfter)
		}
	}
}

// walkPages reads every page under "unit/" with the limit and returns the
// keys in the order that the pages gave them.
func walkPages(t *testing.T, s blob.Store, limit int) []string {
	t.Helper()

	var (
		got   []string
		after blob.Key
	)

	for range blob.MaxPageLimit {
		p := page(t, s, blob.PageRequest{Prefix: "unit/", After: after, Limit: limit})
		got = append(got, objectKeys(p.Objects)...)

		if p.Next == "" {
			return got
		}

		after = p.Next
	}

	t.Fatal("walk does not end")

	return nil
}
