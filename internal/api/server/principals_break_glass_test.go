package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// breakGlassAnswer is the part of a principal answer that the break-glass
// tests read.
type breakGlassAnswer struct {
	DisabledAt *string `json:"disabledAt"`
}

// errorAnswer is the error body of a refused request.
type errorAnswer struct {
	Message   string `json:"message"`
	ErrorCode string `json:"errorCode"`
	Code      int    `json:"code"`
}

// addBreakGlass stores an enabled break-glass person and returns its ID.
func addBreakGlass(t *testing.T, st *sqlite.Store, id, subject string) string {
	t.Helper()

	p := principal.Principal{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), DisabledAt: time.Time{},
		ID: id, Subject: subject, DisplayName: subject, Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}

	if err := st.CreatePrincipal(t.Context(), p, storetest.ServerChange(time.Unix(0, 0), "@server")); err != nil {
		t.Fatalf("create break-glass principal %q: %v", subject, err)
	}

	return id
}

// TestNoWorkingBreakGlassIsKept proves that the disable and the delete of the
// last enabled break-glass account answer 409 with the catalog code, its
// message and no change, while the disable of one of two works.
func TestNoWorkingBreakGlassIsKept(t *testing.T) {
	t.Parallel()

	principals, st := newTestPrincipalStore(t)
	h := newPrincipalHandler(t, principals)

	first := addBreakGlass(t, st, "prn-bg1", "first")
	second := addBreakGlass(t, st, "prn-bg2", "second")

	mustCall(t, h, apiCall{http.MethodPost, "/api/server/principals/" + first + "/disable", "", http.StatusOK}, nil)

	for _, c := range []apiCall{
		{http.MethodPost, "/api/server/principals/" + second + "/disable", "", http.StatusConflict},
		{http.MethodDelete, "/api/server/principals/" + second, "", http.StatusConflict},
	} {
		status, body := call(t, h, c)
		if status != http.StatusConflict {
			t.Fatalf("%s %s answers %d %q, want 409", c.method, c.path, status, body)
		}

		var got errorAnswer
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decode %q: %v", body, err)
		}

		if got.ErrorCode != errcode.PrincipalNoWorkingBreakGlass || got.Code != http.StatusConflict ||
			!strings.Contains(got.Message, "The change leaves too few working break-glass accounts") {
			t.Fatalf("%s %s answers %+v, want 409, code %q and the working break-glass message",
				c.method, c.path, got, errcode.PrincipalNoWorkingBreakGlass)
		}
	}

	var still breakGlassAnswer

	mustCall(t, h, apiCall{http.MethodGet, "/api/server/principals/" + second, "", http.StatusOK}, &still)

	if still.DisabledAt != nil {
		t.Fatalf("the last break-glass account is disabled at %q after the refused requests", *still.DisabledAt)
	}
}
