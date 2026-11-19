package audit_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	apiaudit "opendustpan.org/opendustpan/internal/api/audit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// getList sends a signed-in GET request for target and returns the answer.
func getList(t *testing.T, handler *apiaudit.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, target))

	return rec
}

// decodeList decodes the answer of a list that must be 200.
func decodeList(t *testing.T, rec *httptest.ResponseRecorder) listEntriesDoc {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got listEntriesDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	return got
}

// seedMany appends n entries to acme with the subjects e-1 to e-n.
func seedMany(t *testing.T, entries *memAuditStore, n int) {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	for i := 1; i <= n; i++ {
		seedEntry(t, entries, ns, fmt.Sprintf("e-%d", i))
	}
}

// firstToken reads the first page of scope with pageSize 1 and returns the
// token that the server made.
func firstToken(t *testing.T, handler *apiaudit.Handler, scope string) string {
	t.Helper()

	got := decodeList(t, getList(t, handler, "/api/audit/entries?pageSize=1&organization="+url.QueryEscape(scope)))
	if got.NextPageToken == "" {
		t.Fatalf("no next page token for %s", scope)
	}

	return got.NextPageToken
}

// checkRefusal fails the test unless rec is a 400 with code and a text that
// names the one command that helps.
func checkRefusal(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, rec.Body.Bytes(),
		errcodetest.Want{Code: code, Args: nil, RequestBody: nil})

	if code != errcode.RequestInvalidQuery && !strings.Contains(rec.Body.String(), "send") {
		t.Fatalf("the refusal does not say what to send: %s", rec.Body.String())
	}
}

// TestListEntriesWalksPages proves that a log of 120 entries reads in three
// pages of 50, 50 and 20, that the last token is empty, and that no entry is
// twice in the walk.
func TestListEntriesWalksPages(t *testing.T) {
	t.Parallel()

	handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
	seedMany(t, entries, 120)

	seen := map[string]bool{}
	token := ""
	sizes := []int{}

	for range 5 {
		target := "/api/audit/entries?organization=acme&pageSize=50"
		if token != "" {
			target += "&pageToken=" + url.QueryEscape(token)
		}

		got := decodeList(t, getList(t, handler, target))
		sizes = append(sizes, len(got.Entries))

		for _, e := range got.Entries {
			if seen[e.Subject] {
				t.Fatalf("entry %s is in two pages", e.Subject)
			}

			seen[e.Subject] = true
		}

		token = got.NextPageToken
		if token == "" {
			break
		}
	}

	if fmt.Sprint(sizes) != "[50 50 20]" || len(seen) != 120 {
		t.Fatalf("pages = %v, entries = %d, want [50 50 20] and 120", sizes, len(seen))
	}
}

// TestListEntriesAnswersEffectivePageSize proves that no pageSize answers
// the default size, and that the answer names the size that the server used.
func TestListEntriesAnswersEffectivePageSize(t *testing.T) {
	t.Parallel()

	handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
	seedMany(t, entries, 3)

	if got := decodeList(t, getList(t, handler, "/api/audit/entries?organization=acme")); got.PageSize != 50 {
		t.Fatalf("pageSize = %d, want 50", got.PageSize)
	}

	if got := decodeList(t, getList(t, handler, "/api/audit/entries?organization=acme&pageSize=2")); got.PageSize != 2 {
		t.Fatalf("pageSize = %d, want 2", got.PageSize)
	}
}

// TestListEntriesHasNoTotal proves that the answer has no member total and
// always has nextPageToken, as the empty string on the last page.
func TestListEntriesHasNoTotal(t *testing.T) {
	t.Parallel()

	handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
	seedMany(t, entries, 2)

	rec := getList(t, handler, "/api/audit/entries?organization=acme")

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if _, has := raw["total"]; has {
		t.Fatalf("the answer has a total: %s", rec.Body.String())
	}

	if string(raw["nextPageToken"]) != `""` {
		t.Fatalf("nextPageToken = %s, want an empty string", raw["nextPageToken"])
	}
}

// TestListEntriesRefusesOldPagingNames proves that page[number] and
// page[size] answer 400 and that the store is not called.
func TestListEntriesRefusesOldPagingNames(t *testing.T) {
	t.Parallel()

	for _, q := range []string{"page[number]=1", "page[size]=1", "page[number]=2&page[size]=1", "page%5Bnumber%5D=1"} {
		t.Run(q, func(t *testing.T) {
			t.Parallel()

			handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
			seedMany(t, entries, 2)

			rec := getList(t, handler, "/api/audit/entries?organization=acme&"+q)
			checkRefusal(t, rec, errcode.RequestInvalidQuery)

			if entries.lists != 0 {
				t.Fatalf("the store was called %d times, want 0", entries.lists)
			}
		})
	}
}

// TestListEntriesTokenOfAnotherScopeAnswers400 proves that the token of a
// namespace does not read @server and the reverse, and that the store is not
// called.
func TestListEntriesTokenOfAnotherScopeAnswers400(t *testing.T) {
	t.Parallel()

	handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
	seedMany(t, entries, 3)
	seedServerEntry(t, entries, "ops-1")
	seedServerEntry(t, entries, "ops-2")

	acme := firstToken(t, handler, "acme")
	server := firstToken(t, handler, "@server")
	before := entries.lists

	for _, tc := range []struct{ org, token string }{{"@server", acme}, {"acme", server}} {
		rec := getList(t, handler, "/api/audit/entries?organization="+url.QueryEscape(tc.org)+"&pageToken="+url.QueryEscape(tc.token))
		checkRefusal(t, rec, errcode.RequestPageTokenInvalid)
	}

	if entries.lists != before {
		t.Fatalf("the store was called %d times for a refused token", entries.lists-before)
	}
}

// TestListEntriesDeniedCallerAnswers403BeforeTheToken proves that a caller
// who may not read the audit log gets 403 with a valid token, a changed
// token and no token, and that the token is not read.
func TestListEntriesDeniedCallerAnswers403BeforeTheToken(t *testing.T) {
	t.Parallel()

	open, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
	seedMany(t, entries, 3)

	valid := firstToken(t, open, "acme")

	denied, deniedEntries := newEnforcingHandler(t,
		selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionNamespaceReadAudit: true}})
	seedMany(t, deniedEntries, 3)

	for _, token := range []string{valid, "%%%not-a-token", ""} {
		rec := getList(t, denied, "/api/audit/entries?organization=acme&pageSize=0&pageToken="+url.QueryEscape(token))

		errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusForbidden, rec.Body.Bytes(),
			errcodetest.Want{Code: errcode.AccessDenied, Args: nil, RequestBody: nil})
	}

	if deniedEntries.lists != 0 {
		t.Fatalf("the store was called %d times for a denied caller", deniedEntries.lists)
	}
}

// TestListEntriesHostileQueryRows is the hostile table of the query values.
// Each row must answer 400 and reach no store.
func TestListEntriesHostileQueryRows(t *testing.T) {
	t.Parallel()

	token := func() string {
		h, e := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
		seedMany(t, e, 3)

		return url.QueryEscape(firstToken(t, h, "acme"))
	}()

	cases := []struct {
		name  string
		query string
		code  string
	}{
		{"pageSize empty", "pageSize=", errcode.RequestPageSizeInvalid},
		{"pageSize 0", "pageSize=0", errcode.RequestPageSizeInvalid},
		{"pageSize -1", "pageSize=-1", errcode.RequestPageSizeInvalid},
		{"pageSize 201", "pageSize=201", errcode.RequestPageSizeInvalid},
		{"pageSize leading zero", "pageSize=050", errcode.RequestPageSizeInvalid},
		{"pageSize plus", "pageSize=%2B5", errcode.RequestPageSizeInvalid},
		{"pageSize space", "pageSize=%205", errcode.RequestPageSizeInvalid},
		{"pageSize letter", "pageSize=5x", errcode.RequestPageSizeInvalid},
		{"pageSize exponent", "pageSize=1e2", errcode.RequestPageSizeInvalid},
		{"pageSize overflow", "pageSize=9999999999999999999", errcode.RequestPageSizeInvalid},
		{"pageSize twice", "pageSize=5&pageSize=6", errcode.RequestPageSizeInvalid},
		{"pageToken twice", "pageToken=" + token + "&pageToken=" + token, errcode.RequestPageTokenInvalid},
		{"organization missing", "pageSize=5", errcode.RequestInvalidQuery},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
			seedMany(t, entries, 3)

			target := "/api/audit/entries?" + tc.query
			if tc.name != "organization missing" {
				target += "&organization=acme"
			}

			checkRefusal(t, getList(t, handler, target), tc.code)

			if entries.lists != 0 {
				t.Fatalf("the store was called %d times", entries.lists)
			}
		})
	}
}

// forgeToken encodes a token body as the server would.
func forgeToken(body string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(body))
}

// TestPageTokenHostileRows is the hostile table of the page token. Each
// refused row answers 400 and reaches no store. The last rows are valid for
// their scope and read only entries of that scope.
func TestPageTokenHostileRows(t *testing.T) {
	t.Parallel()

	h, e := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
	seedMany(t, e, 3)

	var wire struct {
		F string `json:"f"`
		B int64  `json:"b"`
	}

	raw, err := base64.RawURLEncoding.DecodeString(firstToken(t, h, "acme"))
	if err != nil {
		t.Fatalf("decode the valid token: %v", err)
	}

	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode the valid token body: %v", err)
	}

	withB := func(b string) string { return forgeToken(fmt.Sprintf(`{"b":%s,"f":%q}`, b, wire.F)) }

	refused := []struct{ name, token string }{
		{"not base64url", "***"},
		{"base64url that is not JSON", forgeToken("not json")},
		{"JSON array", forgeToken(`[1,2]`)},
		{"empty object", forgeToken(`{}`)},
		{"unknown member", forgeToken(fmt.Sprintf(`{"b":1,"f":%q,"x":1}`, wire.F))},
		{"member of another type", forgeToken(fmt.Sprintf(`{"b":"1","f":%q}`, wire.F))},
		{"b of 0", withB("0")},
		{"b of -1", withB("-1")},
		{"b above int64", withB("9223372036854775808")},
		{"too long", strings.Repeat("A", 4097)},
		{"valid JSON over the limit", forgeToken(fmt.Sprintf(`{"b":1,"f":%q%s}`, wire.F, strings.Repeat(" ", 4100)))},
		{"trailing JSON", forgeToken(fmt.Sprintf(`{"b":1,"f":%q}{}`, wire.F))},
		{"fingerprint of another scope", forgeToken(`{"b":1,"f":"0000000000000000"}`)},
		{"no fingerprint", forgeToken(`{"b":1}`)},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
			seedMany(t, entries, 3)

			rec := getList(t, handler, "/api/audit/entries?organization=acme&pageToken="+url.QueryEscape(tc.token))
			checkRefusal(t, rec, errcode.RequestPageTokenInvalid)

			if entries.lists != 0 {
				t.Fatalf("the store was called %d times", entries.lists)
			}
		})
	}

	t.Run("forged token with the right fingerprint reads only the scope", func(t *testing.T) {
		t.Parallel()

		handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
		seedMany(t, entries, 3)
		seedServerEntry(t, entries, "ops-1")

		got := decodeList(t, getList(t, handler,
			"/api/audit/entries?organization=acme&pageToken="+url.QueryEscape(withB("9223372036854775807"))))

		if len(got.Entries) != 3 {
			t.Fatalf("entries = %d, want the 3 entries of acme", len(got.Entries))
		}

		for _, e := range got.Entries {
			if !strings.HasPrefix(e.Subject, "e-") {
				t.Fatalf("entry %q is not an entry of acme", e.Subject)
			}
		}
	})
}
