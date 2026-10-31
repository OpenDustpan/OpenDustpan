package githubfake_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/githubfake"
)

func TestRequestsAreRecorded(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)

	c.do(http.MethodGet, "/app/installations/9001", jwt(t, f.PrivateKey(), f.AppID(), 9*time.Minute), "")
	token := installationToken(t, c, "")
	c.do(http.MethodGet, "/repos/acme/infra/contents/README.md?ref="+first.ID, token, "")
	c.do(http.MethodGet, "/user", "github_pat_wrong", "")
	c.do(http.MethodGet, "/user", "", "")
	c.do(http.MethodGet, "/repos/acme/infra/contents/stacks/../README.md?ref=main", f.UserToken(), "")

	want := []struct {
		method string
		uri    string
		bearer githubfake.BearerKind
		status int
	}{
		{"GET", "/app/installations/9001", githubfake.BearerApp, http.StatusOK},
		{"POST", "/app/installations/9001/access_tokens", githubfake.BearerApp, http.StatusCreated},
		{"GET", "/repos/acme/infra/contents/README.md?ref=" + first.ID, githubfake.BearerInstallation, http.StatusOK},
		{"GET", "/user", githubfake.BearerInvalid, http.StatusUnauthorized},
		{"GET", "/user", githubfake.BearerNone, http.StatusUnauthorized},
		// The mux redirects an unclean path. The record shows the request as sent.
		{
			"GET", "/repos/acme/infra/contents/stacks/../README.md?ref=main", githubfake.BearerUser,
			http.StatusTemporaryRedirect,
		},
	}

	got := f.Requests()
	if f.Calls() != len(got) || len(got) != len(want) {
		t.Fatalf("calls = %d, requests = %d, want %d", f.Calls(), len(got), len(want))
	}

	for i, w := range want {
		r := got[i]
		if r.Seq != i+1 || r.Method != w.method || r.URI != w.uri || r.Bearer != w.bearer || r.Status != w.status {
			t.Errorf("request %d = {%d %s %s %s %d}, want %+v", i+1, r.Seq, r.Method, r.URI, r.Bearer, r.Status, w)
		}
	}
}

func TestRecordHoldsNoCredential(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	token := installationToken(t, c, "")
	c.do(http.MethodGet, "/user", f.UserToken(), "")
	c.do(http.MethodGet, "/user", "github_pat_wrong", "")

	secrets := []string{token, f.UserToken(), "github_pat_wrong"}

	for _, r := range f.Requests() {
		if got := r.Header.Get("Authorization"); got != "<redacted>" {
			t.Errorf("request %d: Authorization = %q", r.Seq, got)
		}

		for name, values := range r.Header {
			if holdsAny(values, secrets) {
				t.Errorf("request %d holds a credential in %s", r.Seq, name)
			}
		}
	}
}

func holdsAny(values, secrets []string) bool {
	for _, v := range values {
		for _, s := range secrets {
			if strings.Contains(v, s) {
				return true
			}
		}
	}

	return false
}

func TestRecordHoldsTheBody(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	body := `{"permissions":{"metadata":"read"}}`
	installationToken(t, newClient(t, f), body)

	if r := f.Requests(); len(r) != 1 || string(r[0].Body) != body {
		t.Fatalf("requests = %+v", r)
	}
}

func TestResetRequests(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	newClient(t, f).do(http.MethodGet, "/user", "", "")
	f.ResetRequests()

	if f.Calls() != 0 || len(f.Requests()) != 0 {
		t.Fatal("the record is not empty")
	}
}

func TestFaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		inject func(f *githubfake.Fake) func()
		status int
		header map[string]string
		body   string
	}{
		{"refuse", func(f *githubfake.Fake) func() { return f.Refuse() }, http.StatusUnauthorized, nil, ""},
		{"garble", func(f *githubfake.Fake) func() { return f.Garble() }, http.StatusOK, nil, "not json {"},
		{
			"unavailable",
			func(f *githubfake.Fake) func() { return f.Unavailable(0) },
			http.StatusServiceUnavailable,
			nil,
			"",
		},
		{
			"rate limit", func(f *githubfake.Fake) func() { return f.Unavailable(30 * time.Second) },
			http.StatusTooManyRequests,
			map[string]string{"Retry-After": "30"},
			"",
		},
		{
			"status", func(f *githubfake.Fake) func() {
				return f.Respond(http.StatusForbidden, map[string]string{"X-Ratelimit-Remaining": "0"})
			},
			http.StatusForbidden,
			map[string]string{"X-Ratelimit-Remaining": "0"},
			"",
		},
		{
			"redirect", func(f *githubfake.Fake) func() { return f.Redirect("https://example.invalid/x") },
			http.StatusFound,
			map[string]string{"Location": "https://example.invalid/x"},
			"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, _ := newFake(t)
			c := newClient(t, f)

			restore := tc.inject(f)
			a := c.do(http.MethodGet, "/user", f.UserToken(), "")

			wantStatus(t, a, tc.status)

			for k, v := range tc.header {
				if got := a.header.Get(k); got != v {
					t.Errorf("header %s = %q, want %q", k, got, v)
				}
			}

			if tc.body != "" && string(a.body) != tc.body {
				t.Errorf("body = %q, want %q", a.body, tc.body)
			}

			if len(f.Requests()) != 1 {
				t.Error("the request of the fault is not recorded")
			}

			restore()
			wantStatus(t, c.do(http.MethodGet, "/user", f.UserToken(), ""), http.StatusOK)
		})
	}
}

// TestDeterministic runs one script on two fakes and expects the same IDs.
func TestDeterministic(t *testing.T) {
	t.Parallel()

	script := func() []string {
		f, first := newFake(t)
		second := mustPush(t, f, "main", map[string][]byte{"a": []byte("a")})
		hook := githubfake.Hook{ID: 1, Secret: []byte(strings.Repeat("k", 32))}

		return []string{
			first.ID, second.ID, second.Time.String(),
			installationToken(t, newClient(t, f), ""),
			f.NewDelivery(hook, f.Ping(hook, githubfake.Sender{ID: 1, Login: "x"})).ID,
		}
	}

	a, b := script(), script()
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("value %d differs between runs: %q and %q", i, a[i], b[i])
		}
	}

	if want := githubfake.Epoch().Add(2 * time.Second).String(); a[2] != want {
		t.Errorf("the second commit has the time %s, want %s", a[2], want)
	}
}
