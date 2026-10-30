package s3_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/blob/s3"
)

// listPage builds a ListObjectsV2 answer with keys modified at modified. A
// true truncated marks the page truncated.
func listPage(truncated bool, modified time.Time, keys ...string) string {
	var doc strings.Builder

	doc.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult>`)

	if truncated {
		doc.WriteString(`<IsTruncated>true</IsTruncated>`)
	} else {
		doc.WriteString(`<IsTruncated>false</IsTruncated>`)
	}

	for _, key := range keys {
		doc.WriteString(`<Contents><Key>` + key + `</Key><LastModified>` +
			modified.Format(time.RFC3339) + `</LastModified></Contents>`)
	}

	doc.WriteString(`</ListBucketResult>`)

	return doc.String()
}

// listAnswer is one answer of a fake server in a paging case, and the
// start-after and max-keys parameters that the request for it must carry.
type listAnswer struct {
	truncated  bool
	keys       []string
	startAfter string
	maxKeys    string
}

// checkListed fails t unless got holds want in order, each modified at
// modified.
func checkListed(t *testing.T, got []blob.Object, modified time.Time, want []string) {
	t.Helper()

	keys := make([]string, 0, len(got))
	for i := range got {
		keys = append(keys, got[i].Key.String())

		if !got[i].ModifiedAt.Equal(modified) {
			t.Errorf("modification time of %s = %v, want %v", got[i].Key, got[i].ModifiedAt, modified)
		}
	}

	if !slices.Equal(keys, want) {
		t.Fatalf("list = %v, want %v", keys, want)
	}
}

// checkListRequest fails t unless req is a signed ListObjectsV2 request for
// prefix.
func checkListRequest(t *testing.T, req capturedRequest, prefix string) {
	t.Helper()

	if req.method != http.MethodGet || req.path != "/"+testBucket {
		t.Errorf("list request = %s %s, want GET /%s", req.method, req.path, testBucket)
	}

	query, err := url.ParseQuery(req.rawQuery)
	if err != nil {
		t.Fatalf("parse query %q: %v", req.rawQuery, err)
	}

	if query.Get("list-type") != "2" || query.Get("prefix") != prefix {
		t.Errorf("list query = %q, want list-type=2 and prefix %s", req.rawQuery, prefix)
	}

	if req.header.Get("Authorization") == "" {
		t.Error("list request is not signed")
	}
}

// TestListPageSendsStartAfterAndMaxKeys proves how one page maps to
// requests: the cursor goes out as the start-after parameter with the store
// prefix, the limit as max-keys, a name that is not a blob key is skipped and
// the rest of the page is asked for after it, and Next is the last valid key
// of a full page that the store says is truncated.
func TestListPageSendsStartAfterAndMaxKeys(t *testing.T) {
	t.Parallel()

	modified := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		after    blob.Key
		limit    int
		answers  []listAnswer
		wantKeys []string
		wantNext string
	}{
		{
			name:     "first page, truncated",
			limit:    2,
			answers:  []listAnswer{{true, []string{"pfx/tofu/ws-1/a", "pfx/tofu/ws-1/b"}, "", "2"}},
			wantKeys: []string{"tofu/ws-1/a", "tofu/ws-1/b"},
			wantNext: "tofu/ws-1/b",
		},
		{
			name:     "next page, last",
			after:    "tofu/ws-1/b",
			limit:    2,
			answers:  []listAnswer{{false, []string{"pfx/tofu/ws-1/c"}, "pfx/tofu/ws-1/b", "2"}},
			wantKeys: []string{"tofu/ws-1/c"},
			wantNext: "",
		},
		{
			name:  "a name that is not a key is skipped and the page is filled",
			limit: 2,
			answers: []listAnswer{
				{true, []string{"pfx/tofu/ws-1/a", "pfx/tofu/ws-1/bad//name"}, "", "2"},
				{false, []string{"pfx/tofu/ws-1/c"}, "pfx/tofu/ws-1/bad//name", "1"},
			},
			wantKeys: []string{"tofu/ws-1/a", "tofu/ws-1/c"},
			wantNext: "",
		},
		{
			name:  "a truncated page of only bad names asks again",
			limit: 1,
			answers: []listAnswer{
				{true, []string{"pfx/tofu/ws-1/bad//name"}, "", "1"},
				{false, []string{"pfx/tofu/ws-1/c"}, "pfx/tofu/ws-1/bad//name", "1"},
			},
			wantKeys: []string{"tofu/ws-1/c"},
			wantNext: "",
		},
		{
			name:     "an empty answer is an empty last page",
			limit:    5,
			answers:  []listAnswer{{false, nil, "", "5"}},
			wantKeys: []string{},
			wantNext: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeServer(t, func(r *http.Request, _ []byte, call int) (int, string) {
				if call >= len(tc.answers) {
					t.Errorf("server saw call %d, want %d calls", call, len(tc.answers))

					return http.StatusInternalServerError, errorDoc("InternalError")
				}

				want := tc.answers[call]
				query := r.URL.Query()

				if got := query.Get("start-after"); got != want.startAfter {
					t.Errorf("call %d: start-after = %q, want %q", call, got, want.startAfter)
				}

				if got := query.Get("max-keys"); got != want.maxKeys {
					t.Errorf("call %d: max-keys = %q, want %q", call, got, want.maxKeys)
				}

				if query.Get("continuation-token") != "" {
					t.Errorf("call %d: sent a continuation token", call)
				}

				return http.StatusOK, listPage(want.truncated, modified, want.keys...)
			})
			server := httptest.NewServer(fake)
			t.Cleanup(server.Close)

			s := openStore(t, server, testSigner(server.URL, nil), "pfx")

			page, err := s.ListPage(t.Context(), blob.PageRequest{Prefix: "tofu/ws-1", After: tc.after, Limit: tc.limit})
			if err != nil {
				t.Fatalf("list page: %v", err)
			}

			checkListed(t, page.Objects, modified, tc.wantKeys)

			if page.Next.String() != tc.wantNext {
				t.Fatalf("next = %q, want %q", page.Next, tc.wantNext)
			}

			if calls := fake.calls(); calls != len(tc.answers) {
				t.Fatalf("server saw %d calls, want %d", calls, len(tc.answers))
			}

			checkListRequest(t, fake.first(), "pfx/tofu/ws-1/")
		})
	}
}

// TestListPageRefusesBadRequestsBeforeTheStore proves a limit out of range
// and an After outside the prefix fail without a request.
func TestListPageRefusesBadRequestsBeforeTheStore(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(*http.Request, []byte, int) (int, string) {
		return http.StatusOK, listPage(false, time.Now())
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s := openStore(t, server, testSigner(server.URL, nil), "pfx")

	cases := []struct {
		name string
		req  blob.PageRequest
		want error
	}{
		{"limit zero", blob.PageRequest{Prefix: "a/", After: "", Limit: 0}, blob.ErrInvalidPage},
		{"limit above the page", blob.PageRequest{Prefix: "a/", After: "", Limit: blob.MaxPageLimit + 1}, blob.ErrInvalidPage},
		{"after outside the prefix", blob.PageRequest{Prefix: "a/", After: "b/x", Limit: 1}, blob.ErrInvalidKey},
		{"after not a key", blob.PageRequest{Prefix: "a/", After: "a/../x", Limit: 1}, blob.ErrInvalidKey},
	}

	for _, tc := range cases {
		if _, err := s.ListPage(t.Context(), tc.req); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	if calls := fake.calls(); calls != 0 {
		t.Fatalf("server saw %d calls, want 0", calls)
	}
}

// failFirstList returns a fake server whose first list answers status with
// code and whose later lists hold a/b, and a store that uses it.
func failFirstList(t *testing.T, status int, code string) (*fakeServer, *s3.Store) {
	t.Helper()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, call int) (int, string) {
		if call == 0 {
			return status, errorDoc(code)
		}

		return http.StatusOK, listPage(false, time.Now(), "a/b")
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	return fake, openStore(t, server, testSigner(server.URL, nil), "")
}

func TestListRetriesServerError(t *testing.T) {
	t.Parallel()

	fake, s := failFirstList(t, http.StatusServiceUnavailable, "SlowDown")

	got, err := blobtest.ListAll(t.Context(), s, "a/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(got) != 1 || got[0].Key.String() != "a/b" {
		t.Fatalf("list = %v, want a/b", got)
	}

	if calls := fake.calls(); calls != 2 {
		t.Fatalf("server saw %d calls, want 2", calls)
	}
}

func TestListFailsOnAccessDenied(t *testing.T) {
	t.Parallel()

	fake, s := failFirstList(t, http.StatusForbidden, "AccessDenied")

	if _, err := blobtest.ListAll(t.Context(), s, "a/"); err == nil {
		t.Fatal("list after access denied = nil, want error")
	}

	if calls := fake.calls(); calls != 1 {
		t.Fatalf("server saw %d calls, want 1", calls)
	}
}
