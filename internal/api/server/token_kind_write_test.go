package server_test

import (
	"database/sql"
	"net/http"
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
	Tokens []tokenNameDoc `json:"tokens"`
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
