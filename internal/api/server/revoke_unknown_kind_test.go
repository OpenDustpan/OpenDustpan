package server_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

// TestUnknownKindTokenAnswers checks, through the HTTP handler and the real
// token service, the answer of each token route for a stored token of a kind
// that this version does not know. The own routes answer the owner 409 and
// every other person 404 with the body of an ID that does not exist: a
// person learns nothing about the row of another person. The administrator
// routes answer 409. No 409 body names the token ID, and no change reaches
// the row. Each hostile kind is one run.
func TestUnknownKindTokenAnswers(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{
		"trust", "", "Personal", "PERSONAL", "personal ", " personal", "personal\x00", "../personal",
	} {
		t.Run(strconv.Quote(kind), func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			f.seedService(t, "deploy")

			var personal, service tokenDoc

			f.as(t, "alice", apiCall{
				http.MethodPost, "/api/dustpan/v1/me/tokens", `{"name":"laptop"}`, http.StatusCreated,
			}, &personal)
			f.as(t, "admin", apiCall{
				http.MethodPost, "/api/server/principals/prn-deploy/tokens", `{"name":"ci"}`, http.StatusCreated,
			}, &service)

			setStoredTokenKind(t, f.path, personal.ID, kind)
			setStoredTokenKind(t, f.path, service.ID, kind)

			const own = "/api/dustpan/v1/me/tokens/"

			p, svc := personal.ID, service.ID
			conflict, notFound := http.StatusConflict, http.StatusNotFound

			for _, c := range []unknownKindCase{
				{"own revoke, owner", "alice", http.MethodDelete, own + p, "", p, conflict, errcode.TokenUnknownKind},
				{"own revoke, non-owner", "bob", http.MethodDelete, own + p, "", p, notFound, errcode.TokenNotFound},
				{"own rotate, owner", "alice", http.MethodPost, own + p + "/rotate", `{}`, p, conflict, errcode.TokenUnknownKind},
				{"own rotate, non-owner", "bob", http.MethodPost, own + p + "/rotate", `{}`, p, notFound, errcode.TokenNotFound},
				{"admin revoke", "admin", http.MethodDelete, "/api/server/tokens/" + p, "", p, conflict, errcode.TokenUnknownKind},
				{"admin get", "admin", http.MethodGet, "/api/server/tokens/" + p, "", p, conflict, errcode.TokenUnknownKind},
				{
					"service route rotate", "admin", http.MethodPost, "/api/server/service-tokens/" + svc + "/rotate", `{}`,
					svc, conflict, errcode.TokenUnknownKind,
				},
				{
					"principal route revoke", "admin", http.MethodDelete, "/api/server/principals/prn-deploy/tokens/" + svc, "",
					svc, conflict, errcode.TokenUnknownKind,
				},
			} {
				f.checkUnknownKindCase(t, c)
			}
		})
	}
}

// unknownKindCase is one request of TestUnknownKindTokenAnswers.
type unknownKindCase struct {
	name     string
	subject  string
	method   string
	path     string
	body     string
	token    string
	want     int
	wantCode string
}

// checkUnknownKindCase sends c and checks its status, its error code, that a
// 409 body does not name the token ID, and that a 404 body equals the body for
// an ID that does not exist.
func (f accessFixture) checkUnknownKindCase(t *testing.T, c unknownKindCase) {
	t.Helper()

	body := f.rawAs(t, c.subject, apiCall{c.method, c.path, c.body, c.want})

	if !strings.Contains(body, c.wantCode) {
		t.Errorf("%s: body %q lacks the code %q", c.name, body, c.wantCode)
	}

	if c.want == http.StatusConflict && strings.Contains(body, c.token) {
		t.Errorf("%s: body %q names the token ID", c.name, body)
	}

	if c.want != http.StatusNotFound {
		return
	}

	missingPath := strings.Replace(c.path, c.token, "tok-missing", 1)
	missing := f.rawAs(t, c.subject, apiCall{c.method, missingPath, c.body, c.want})

	if strings.ReplaceAll(body, c.token, "ID") != strings.ReplaceAll(missing, "tok-missing", "ID") {
		t.Errorf("%s: body %q differs from the body for an unknown ID %q", c.name, body, missing)
	}
}

// rawAs sends c as subject, fails unless it answers c.want, and returns the
// body.
func (f accessFixture) rawAs(t *testing.T, subject string, c apiCall) string {
	t.Helper()

	var raw json.RawMessage

	f.as(t, subject, c, &raw)

	return string(raw)
}
