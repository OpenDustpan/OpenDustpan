package blob_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// stuckStore answers a fixed page for each request, whatever the cursor is.
type stuckStore struct {
	blob.Store

	page blob.Page
	err  error
}

func (s stuckStore) ListPage(context.Context, blob.PageRequest) (blob.Page, error) {
	return s.page, s.err
}

func objects(keys ...string) []blob.Object {
	out := make([]blob.Object, 0, len(keys))
	for _, k := range keys {
		out = append(out, blob.Object{Key: blob.Key(k), ModifiedAt: time.Time{}})
	}

	return out
}

func TestPageOf(t *testing.T) {
	t.Parallel()

	all := objects("a/1", "a/2", "a/3", "a/4")

	cases := []struct {
		name     string
		after    blob.Key
		limit    int
		want     []string
		wantNext blob.Key
	}{
		{"first page", "", 2, []string{"a/1", "a/2"}, "a/2"},
		{"middle page", "a/2", 1, []string{"a/3"}, "a/3"},
		{"last page exact", "a/2", 2, []string{"a/3", "a/4"}, ""},
		{"limit above count", "", 10, []string{"a/1", "a/2", "a/3", "a/4"}, ""},
		{"after between keys", "a/15", 2, []string{"a/2", "a/3"}, "a/3"},
		{"after before first", "a/0", 1, []string{"a/1"}, "a/1"},
		{"after the last", "a/4", 2, nil, ""},
		{"after past the end", "b/1", 2, nil, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := blob.PageOf(all, blob.PageRequest{Prefix: "a/", After: tc.after, Limit: tc.limit})

			keys := make([]string, 0, len(got.Objects))
			for _, o := range got.Objects {
				keys = append(keys, o.Key.String())
			}

			if !slices.Equal(keys, tc.want) && (len(keys) != 0 || len(tc.want) != 0) {
				t.Fatalf("page = %v, want %v", keys, tc.want)
			}

			if got.Next != tc.wantNext {
				t.Fatalf("next = %q, want %q", got.Next, tc.wantNext)
			}
		})
	}
}

func TestPageRequestValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  blob.PageRequest
		want error
	}{
		{"valid", blob.PageRequest{Prefix: "a/", After: "a/x", Limit: 1}, nil},
		{"valid at the largest page", blob.PageRequest{Prefix: "a", After: "", Limit: blob.MaxPageLimit}, nil},
		{"empty prefix takes any after", blob.PageRequest{Prefix: "", After: "z/x", Limit: 1}, nil},
		{"limit zero", blob.PageRequest{Prefix: "a/", After: "", Limit: 0}, blob.ErrInvalidPage},
		{"limit negative", blob.PageRequest{Prefix: "a/", After: "", Limit: -1}, blob.ErrInvalidPage},
		{
			"limit above the page",
			blob.PageRequest{Prefix: "a/", After: "", Limit: blob.MaxPageLimit + 1},
			blob.ErrInvalidPage,
		},
		{"after not a key", blob.PageRequest{Prefix: "a/", After: "a/../x", Limit: 1}, blob.ErrInvalidKey},
		{"after with a capital", blob.PageRequest{Prefix: "a/", After: "a/X", Limit: 1}, blob.ErrInvalidKey},
		{"after outside the prefix", blob.PageRequest{Prefix: "a/", After: "b/x", Limit: 1}, blob.ErrInvalidKey},
		{"after shares a string prefix", blob.PageRequest{Prefix: "a/", After: "a-2/x", Limit: 1}, blob.ErrInvalidKey},
		{"after is the directory itself", blob.PageRequest{Prefix: "a/", After: "a", Limit: 1}, blob.ErrInvalidKey},
	}

	for _, tc := range cases {
		err := tc.req.Validate()
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

// errStop is the error that a walk callback returns to end the walk.
var errStop = errors.New("stop the walk")

// putKeys stores n objects under "a/" in a new store.
func putKeys(t *testing.T, n int) *memStore {
	t.Helper()

	mem := newMemStore()

	for i := range n {
		key, err := blob.ParseKey(fmt.Sprintf("a/k-%06d", i))
		if err != nil {
			t.Fatalf("parse key: %v", err)
		}

		if err := mem.Put(t.Context(), key, strings.NewReader("x")); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	return mem
}

func TestWalkVisitsEveryObjectOnceInOrder(t *testing.T) {
	t.Parallel()

	for _, n := range []int{0, 3, blob.MaxPageLimit, blob.MaxPageLimit + 7, blob.MaxPageLimit * 2} {
		var seen []blob.Key

		err := blob.Walk(t.Context(), putKeys(t, n), "a/", func(o blob.Object) error {
			seen = append(seen, o.Key)

			return nil
		})
		if err != nil {
			t.Fatalf("%d objects: walk: %v", n, err)
		}

		if len(seen) != n || !slices.IsSorted(blobStrings(seen)) {
			t.Fatalf("%d objects: walk saw %d, sorted %v", n, len(seen), slices.IsSorted(blobStrings(seen)))
		}
	}
}

func TestWalkStopsOnTheErrorOfTheCallback(t *testing.T) {
	t.Parallel()

	calls := 0

	err := blob.Walk(t.Context(), putKeys(t, 5), "a/", func(blob.Object) error {
		calls++

		if calls == 3 {
			return errStop
		}

		return nil
	})

	if !errors.Is(err, errStop) || calls != 3 {
		t.Fatalf("err = %v after %d calls, want errStop after 3", err, calls)
	}
}

func TestWalkFailsClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		store blob.Store
		want  error
		calls int
	}{
		{
			"a store error ends the walk",
			stuckStore{Store: nil, page: blob.Page{Objects: nil, Next: ""}, err: blob.ErrThrottled},
			blob.ErrThrottled, 0,
		},
		{
			"a next key that does not advance",
			stuckStore{Store: nil, page: blob.Page{Objects: objects("a/1"), Next: "a/1"}, err: nil},
			blob.ErrInvalidPage, 2,
		},
		{
			"a next key with an empty page",
			stuckStore{Store: nil, page: blob.Page{Objects: nil, Next: "a/1"}, err: nil},
			blob.ErrInvalidPage, 0,
		},
	}

	for _, tc := range cases {
		calls := 0

		err := blob.Walk(t.Context(), tc.store, "a/", func(blob.Object) error {
			calls++

			return nil
		})

		if !errors.Is(err, tc.want) || calls != tc.calls {
			t.Errorf("%s: err = %v after %d calls, want %v after %d", tc.name, err, calls, tc.want, tc.calls)
		}
	}
}

func blobStrings(keys []blob.Key) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.String())
	}

	return out
}
