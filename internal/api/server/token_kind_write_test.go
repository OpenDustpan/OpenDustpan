package server_test

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// countStoredTokens returns the number of rows in access_tokens, read with a
// second connection to the database file of the fixture.
func (f accessFixture) countStoredTokens(t *testing.T) int {
	t.Helper()

	conn, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	})

	var n int
	if scanErr := conn.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM access_tokens").Scan(&n); scanErr != nil {
		t.Fatalf("count access tokens: %v", scanErr)
	}

	return n
}

// tokenNameDoc is the part of a token that the list test reads.
type tokenNameDoc struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// tokenNamesDoc is the part of the token list that the list test reads.
type tokenNamesDoc struct {
	NextCursor string         `json:"nextCursor"`
	Tokens     []tokenNameDoc `json:"tokens"`
}

// TestCreateTokenRefusesAKindField sends hostile bodies to the create route of
// a token of a service principal. The request has no field for the kind of the
// token: the strict decode of the body answers 400, and the store holds no
// token after each request.
func TestCreateTokenRefusesAKindField(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedService(t, "svc-plain")

	for _, body := range []string{
		`{"name":"ci","kind":"trust"}`,
		`{"name":"ci","kind":""}`,
		`{"name":"ci","kind":"Service"}`,
		`{"name":"ci","kind":"service "}`,
		`{"name":"ci","kind":"service\u0000"}`,
		`{"name":"ci","kind":"../service"}`,
		`{"name":"ci","Kind":"trust"}`,
	} {
		rec := f.postWithSessionOf(t, "ann", "/api/server/principals/prn-svc-plain/tokens", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("create with %s = %d %s, want %d", body, rec.Code, rec.Body.String(), http.StatusBadRequest)
		}

		if n := f.countStoredTokens(t); n != 0 {
			t.Errorf("create with %s left %d stored tokens, want 0", body, n)
		}
	}
}

// TestListTokensSkipsAStoredTokenOfAnUnknownKind plants a row of a kind that
// no port writes, through a second connection, and checks that the list route
// answers 200 with the other tokens and does not show the row.
func TestListTokensSkipsAStoredTokenOfAnUnknownKind(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedService(t, "svc-plain")

	rec := f.postWithSessionOf(t, "ann", "/api/server/principals/prn-svc-plain/tokens", `{"name":"good"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s, want %d", rec.Code, rec.Body.String(), http.StatusCreated)
	}

	conn, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	})

	const plant = `INSERT INTO access_tokens (id, principal_id, name, kind, digest, created_at_ms, expires_at_ms, created_by)
		SELECT 'tok-bad-kind', principal_id, 'bad', 'trust', 'sha256:' || hex(randomblob(32)), created_at_ms, expires_at_ms, created_by
		FROM access_tokens LIMIT 1`
	if _, execErr := conn.ExecContext(t.Context(), plant); execErr != nil {
		t.Fatalf("plant a token of an unknown kind: %v", execErr)
	}

	var doc tokenNamesDoc

	f.as(t, "ann", apiCall{http.MethodGet, "/api/server/tokens", "", http.StatusOK}, &doc)

	if len(doc.Tokens) != 1 || doc.Tokens[0].Name != "good" {
		t.Fatalf("list = %+v, want only the token good", doc.Tokens)
	}

	if strings.Contains(doc.Tokens[0].ID, "bad") {
		t.Error("list shows the token of an unknown kind")
	}
}

// TestListTokensPagesPastRowsOfAnUnknownKind plants a row of an unknown kind
// as the newest row, and between live tokens, and pages the list route with
// limit 2. Each live token must show on one page, and no page may end the
// list before the last live token.
func TestListTokensPagesPastRowsOfAnUnknownKind(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedService(t, "svc-plain")

	for _, name := range []string{"good0", "good1", "good2"} {
		rec := f.postWithSessionOf(t, "ann", "/api/server/principals/prn-svc-plain/tokens", `{"name":"`+name+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s = %d %s, want %d", name, rec.Code, rec.Body.String(), http.StatusCreated)
		}
	}

	plantTokensOfUnknownKind(t, f.path, "1", "2")

	names := pageTokenNames(t, f, 2)

	if len(names) != 3 {
		t.Fatalf("pages hold %q, want the three live tokens", names)
	}

	for _, name := range names {
		if name == "bad" {
			t.Error("a page shows the token of an unknown kind")
		}
	}
}

// plantTokensOfUnknownKind stores one row of the kind "trust" for each
// suffix, through a second connection. Each row is newer than every other
// token, so the first batch of a store read starts with the planted rows.
func plantTokensOfUnknownKind(t *testing.T, path string, suffixes ...string) {
	t.Helper()

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	})

	const plant = `INSERT INTO access_tokens (id, principal_id, name, kind, digest, created_at_ms, expires_at_ms, created_by)
		SELECT 'tok-zz-bad-' || ?, principal_id, 'bad', 'trust', 'sha256:' || hex(randomblob(32)),
			(SELECT MAX(created_at_ms) FROM access_tokens) + 1, expires_at_ms, created_by
		FROM access_tokens LIMIT 1`

	for _, n := range suffixes {
		if _, execErr := conn.ExecContext(t.Context(), plant, n); execErr != nil {
			t.Fatalf("plant a token of an unknown kind: %v", execErr)
		}
	}
}

// pageTokenNames follows the cursors of the list route with the page size
// limit and returns the token names of every page, in order.
func pageTokenNames(t *testing.T, f accessFixture, limit int) []string {
	t.Helper()

	var names []string

	path := "/api/server/tokens?limit=" + strconv.Itoa(limit)

	for range 10 {
		var doc tokenNamesDoc

		f.as(t, "ann", apiCall{http.MethodGet, path, "", http.StatusOK}, &doc)

		for _, tok := range doc.Tokens {
			names = append(names, tok.Name)
		}

		if doc.NextCursor == "" {
			return names
		}

		path = "/api/server/tokens?limit=" + strconv.Itoa(limit) + "&cursor=" + doc.NextCursor
	}

	t.Fatal("the pages do not end")

	return nil
}
