package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
)

// maxLocalUserBodyBytes is the limit of the body of a local user route.
const maxLocalUserBodyBytes = 8192

// padTo returns body with spaces after it, so that it is size bytes long and
// still one valid JSON object.
func padTo(body string, size int) string {
	return body + strings.Repeat(" ", size-len(body))
}

// TestLocalUserBodyTooLarge proves that a body over the limit answers 413
// request.body-too-large with the limit on each kind of local user route: a
// public sign-in route, a public code route and the administrator routes. A
// body of exactly the limit is read.
func TestLocalUserBodyTooLarge(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	carol := createRouteLocalUser(t, f)

	routes := []struct {
		send func(body string) *httptest.ResponseRecorder
		name string
		body string
	}{
		{
			name: "password sign-in", body: `{"userName":"carol","password":"wrong-password-1"}`,
			send: func(body string) *httptest.ResponseRecorder {
				return publicPost(t, f, "/api/dustpan/v1/sign-in/password", body)
			},
		},
		{
			name: "redeem", body: `{"oneTimeCode":"x","password":"` + routePassword + `"}`,
			send: func(body string) *httptest.ResponseRecorder {
				return publicPost(t, f, "/api/dustpan/v1/local-users/redeem", body)
			},
		},
		{
			name: "create", body: `{"userName":"dave","displayName":"Dave","reason":"new team member"}`,
			send: func(body string) *httptest.ResponseRecorder {
				return f.sessionPost(t, "admin", "/api/dustpan/v1/local-users", body)
			},
		},
		{
			name: "unlock", body: `{"principal":"` + carol + `","reason":"the person called"}`,
			send: func(body string) *httptest.ResponseRecorder {
				return f.sessionPost(t, "admin", "/api/dustpan/v1/local-users/unlock", body)
			},
		},
		{
			name: "reset", body: `{"principal":"` + carol + `","reason":"lost the password"}`,
			send: func(body string) *httptest.ResponseRecorder {
				return f.sessionPost(t, "admin", "/api/dustpan/v1/local-users/reset", body)
			},
		},
	}

	for _, rt := range routes {
		exact := rt.send(padTo(rt.body, maxLocalUserBodyBytes))
		if exact.Code == http.StatusRequestEntityTooLarge || errorCodeOf(t, nonEmpty(exact)) == "request.body-invalid" {
			t.Fatalf(
				"%s: body of %d bytes = %d %s, want it read",
				rt.name,
				maxLocalUserBodyBytes,
				exact.Code,
				exact.Body,
			)
		}

		over := rt.send(padTo(rt.body, maxLocalUserBodyBytes+1))
		if over.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s: body of %d bytes = %d %s, want 413", rt.name, maxLocalUserBodyBytes+1, over.Code, over.Body)
		}

		var got apiError
		if err := json.Unmarshal(over.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: answer %q is not an error: %v", rt.name, over.Body, err)
		}

		if got.ErrorCode != "request.body-too-large" || got.ErrorArgs["maxBytes"] != float64(maxLocalUserBodyBytes) {
			t.Fatalf("%s: answer = %+v, want request.body-too-large with maxBytes %d",
				rt.name, got, maxLocalUserBodyBytes)
		}

		if strings.Contains(over.Body.String(), "wrong-password-1") ||
			strings.Contains(over.Body.String(), routePassword) {
			t.Fatalf("%s: the answer echoes the body: %s", rt.name, over.Body)
		}
	}
}

// nonEmpty returns the body of rec, or an empty JSON object for an answer
// with no body.
func nonEmpty(rec *httptest.ResponseRecorder) string {
	if rec.Body.Len() == 0 {
		return "{}"
	}

	return rec.Body.String()
}

// TestLocalUserReasonLimit proves that the create, unlock and reset routes
// answer 400 audit.reason-invalid for a reason over the limit of the audit
// log, in characters and not in bytes, and 400 local-user.reason-required for
// no reason.
func TestLocalUserReasonLimit(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	carol := createRouteLocalUser(t, f)

	cases := []struct {
		name   string
		reason string
		want   int
		code   string
	}{
		{name: "500 ASCII characters", reason: strings.Repeat("a", audit.MaxReasonLen), want: 0, code: ""},
		{name: "500 three-byte characters", reason: strings.Repeat("界", audit.MaxReasonLen), want: 0, code: ""},
		{
			name: "501 ASCII characters", reason: strings.Repeat("a", audit.MaxReasonLen+1),
			want: http.StatusBadRequest, code: "audit.reason-invalid",
		},
		{
			name: "501 two-byte characters", reason: strings.Repeat("é", audit.MaxReasonLen+1),
			want: http.StatusBadRequest, code: "audit.reason-invalid",
		},
		{
			name: "1025 ASCII characters", reason: strings.Repeat("a", 1025),
			want: http.StatusBadRequest, code: "audit.reason-invalid",
		},
		{name: "only space", reason: " ", want: http.StatusBadRequest, code: "local-user.reason-required"},
	}

	for i, tc := range cases {
		reason, err := json.Marshal(tc.reason)
		if err != nil {
			t.Fatalf("encode the reason: %v", err)
		}

		routes := map[string]struct {
			path, body string
			ok         int
		}{
			"create": {
				path: "/api/dustpan/v1/local-users", ok: http.StatusCreated,
				body: `{"userName":"user` + strings.Repeat("x", i) + `","reason":` + string(reason) + `}`,
			},
			"unlock": {
				path: "/api/dustpan/v1/local-users/unlock", ok: http.StatusOK,
				body: `{"principal":"` + carol + `","reason":` + string(reason) + `}`,
			},
			"reset": {
				path: "/api/dustpan/v1/local-users/reset", ok: http.StatusCreated,
				body: `{"principal":"` + carol + `","reason":` + string(reason) + `}`,
			},
		}

		for name, rt := range routes {
			rec := f.sessionPost(t, "admin", rt.path, rt.body)
			checkReasonAnswer(t, name+" with "+tc.name, rec, reasonAnswer{ok: rt.ok, want: tc.want, code: tc.code})
		}
	}
}

// reasonAnswer is the answer that a reason case wants: the success status ok
// when want is 0, else the status want with the error code.
type reasonAnswer struct {
	code     string
	ok, want int
}

// checkReasonAnswer fails t when rec is not the answer a.
func checkReasonAnswer(t *testing.T, name string, rec *httptest.ResponseRecorder, a reasonAnswer) {
	t.Helper()

	if a.want == 0 {
		if rec.Code != a.ok {
			t.Fatalf("%s = %d %s, want %d", name, rec.Code, rec.Body, a.ok)
		}

		return
	}

	if got := errorCodeOf(t, rec.Body.String()); rec.Code != a.want || got != a.code {
		t.Fatalf("%s = %d %s, want %d %s", name, rec.Code, got, a.want, a.code)
	}
}
