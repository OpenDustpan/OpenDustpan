package s3_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/s3"
)

// listPage builds a ListObjectsV2 answer with keys modified at modified. A
// non-empty next marks the page truncated with that continuation token.
func listPage(next string, modified time.Time, keys ...string) string {
	var doc strings.Builder

	doc.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult>`)

	if next != "" {
		doc.WriteString(`<IsTruncated>true</IsTruncated><NextContinuationToken>` + next + `</NextContinuationToken>`)
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

// pagedHandler answers two pages: the first holds a valid key and a name
// that is not a blob key, the second one more key. It checks that only the
// second request carries the continuation token.
func pagedHandler(t *testing.T, modified time.Time) func(*http.Request, []byte, int) (int, string) {
	t.Helper()

	return func(r *http.Request, _ []byte, call int) (int, string) {
		token := r.URL.Query().Get("continuation-token")

		if call == 0 {
			if token != "" {
				t.Errorf("first page sent continuation token %q", token)
			}

			return http.StatusOK, listPage("next-1", modified,
				"pfx/tofu/ws-1/sv-2/a1", "pfx/tofu/ws-1/bad//name")
		}

		if token != "next-1" {
			t.Errorf("continuation token = %q, want next-1", token)
		}

		return http.StatusOK, listPage("", modified, "pfx/tofu/ws-1/sv-1/a1")
	}
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

func TestListFollowsPagesAndStripsPrefix(t *testing.T) {
	t.Parallel()

	modified := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

	fake := newFakeServer(t, pagedHandler(t, modified))
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s := openStore(t, server, testSigner(server.URL, nil), "pfx")

	got, err := s.List(t.Context(), "tofu/ws-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	checkListed(t, got, modified, []string{"tofu/ws-1/sv-1/a1", "tofu/ws-1/sv-2/a1"})

	if calls := fake.calls(); calls != 2 {
		t.Fatalf("server saw %d calls, want 2", calls)
	}

	checkListRequest(t, fake.first(), "pfx/tofu/ws-1/")
}

// failFirstList returns a fake server whose first list answers status with
// code and whose later lists hold a/b, and a store that uses it.
func failFirstList(t *testing.T, status int, code string) (*fakeServer, *s3.Store) {
	t.Helper()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, call int) (int, string) {
		if call == 0 {
			return status, errorDoc(code)
		}

		return http.StatusOK, listPage("", time.Now(), "a/b")
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	return fake, openStore(t, server, testSigner(server.URL, nil), "")
}

func TestListRetriesServerError(t *testing.T) {
	t.Parallel()

	fake, s := failFirstList(t, http.StatusServiceUnavailable, "SlowDown")

	got, err := s.List(t.Context(), "a/")
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

	if _, err := s.List(t.Context(), "a/"); err == nil {
		t.Fatal("list after access denied = nil, want error")
	}

	if calls := fake.calls(); calls != 1 {
		t.Fatalf("server saw %d calls, want 1", calls)
	}
}
