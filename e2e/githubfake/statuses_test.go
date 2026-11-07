package githubfake_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e/githubfake"
)

const statusesPath = "/repos/acme/infra/statuses/"

func TestCreateStatusAndReplace(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)

	a := c.do(http.MethodPost, statusesPath+first.ID, userValue,
		`{"state":"pending","context":"ci/plan","description":"Runs","target_url":"https://x.example/1"}`)
	wantStatus(t, a, http.StatusCreated)

	for _, body := range []string{
		`{"state":"success","context":"ci/plan","description":"Done"}`,
		`{"state":"failure","context":"ci/policy"}`,
		`{"state":"error"}`,
	} {
		wantStatus(t, c.do(http.MethodPost, statusesPath+first.ID, userValue, body), http.StatusCreated)
	}

	got, err := f.Statuses("acme/infra", first.ID)
	if err != nil {
		t.Fatal(err)
	}

	want := []githubfake.CommitStatus{
		{State: "success", Context: "ci/plan", Description: "Done", TargetURL: "", Creator: "acme"},
		{State: "failure", Context: "ci/policy", Description: "", TargetURL: "", Creator: "acme"},
		{State: "error", Context: "default", Description: "", TargetURL: "", Creator: "acme"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Statuses = %+v, want %+v", got, want)
	}

	if n, countErr := f.StatusCount("acme/infra", first.ID, "ci/plan"); countErr != nil || n != 2 {
		t.Fatalf("StatusCount = %d, %v; want 2 (the replaced status counts)", n, countErr)
	}
}

func TestCreateStatusRefusals(t *testing.T) {
	t.Parallel()

	f, first := newFakeWith(t, map[string]string{"contents": "read", "metadata": "read", "statuses": "write"}, false)
	c := newClient(t, f)
	statusOnly := installationToken(t, c, `{"permissions":{"statuses":"write"}}`)
	contentsOnly := installationToken(t, c, `{"permissions":{"contents":"read"}}`)
	statusRead := installationToken(t, c, `{"permissions":{"statuses":"read"}}`)
	ok := `{"state":"success","context":"ci/plan"}`

	tests := []struct {
		name   string
		path   string
		bearer string
		body   string
		status int
	}{
		{"a token with the permission for statuses only", statusesPath + first.ID, statusOnly, ok, http.StatusCreated},
		{
			"a token with the permission for contents only",
			statusesPath + first.ID,
			contentsOnly,
			ok,
			http.StatusForbidden,
		},
		{"a token that reads statuses", statusesPath + first.ID, statusRead, ok, http.StatusForbidden},
		{"no credential", statusesPath + first.ID, "", ok, http.StatusUnauthorized},
		{"a credential that is not known", statusesPath + first.ID, "ghs_nope", ok, http.StatusUnauthorized},
		{"a missing repository", "/repos/acme/none/statuses/" + first.ID, userValue, ok, http.StatusNotFound},
		{"a missing commit", statusesPath + strings.Repeat("0", 40), userValue, ok, http.StatusUnprocessableEntity},
		{
			"an unknown state",
			statusesPath + first.ID,
			userValue,
			`{"state":"done","context":"x"}`,
			http.StatusUnprocessableEntity,
		},
		{"a body that is not JSON", statusesPath + first.ID, userValue, `{`, http.StatusBadRequest},
	}

	for _, tt := range tests {
		a := c.do(http.MethodPost, tt.path, tt.bearer, tt.body)
		if a.status != tt.status {
			t.Errorf("%s: status = %d, want %d (body %s)", tt.name, a.status, tt.status, a.body)
		}
	}

	// Only the first call set a status.
	if n, err := f.StatusCount("acme/infra", first.ID, "ci/plan"); err != nil || n != 1 {
		t.Fatalf("StatusCount = %d, %v; want 1", n, err)
	}
}

func TestCreateStatusMostPerContext(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)
	body := `{"state":"success","context":"ci/plan"}`

	for i := range 1000 {
		if a := c.do(http.MethodPost, statusesPath+first.ID, userValue, body); a.status != http.StatusCreated {
			t.Fatalf("status %d = %d, want 201", i+1, a.status)
		}
	}

	a := c.do(http.MethodPost, statusesPath+first.ID, userValue, body)
	if a.status != http.StatusUnprocessableEntity || !strings.Contains(string(a.body), "maximum number of statuses") {
		t.Fatalf("status 1001 = %d %s, want 422 with the text of the limit", a.status, a.body)
	}

	// Another context has its own count.
	wantStatus(t, c.do(http.MethodPost, statusesPath+first.ID, userValue, `{"state":"success","context":"ci/other"}`),
		http.StatusCreated)
}

func TestCompareFilesAndPages(t *testing.T) {
	t.Parallel()

	f, base := newFake(t)
	c := newClient(t, f)
	head := mustPush(t, f, "main", map[string][]byte{
		"README.md":         []byte("hello\nworld\n"),
		"stacks/stage.yaml": nil,
		"stacks/new.yaml":   []byte("config: stage\n"),
		"extra.txt":         []byte("x\n"),
	})

	a := c.do(http.MethodGet, fmt.Sprintf("/repos/acme/infra/compare/%s...%s", base.ID, head.ID), userValue, "")
	wantStatus(t, a, http.StatusOK)

	type file = map[string]string

	got := decode[struct {
		Status string `json:"status"`
		Files  []file `json:"files"`
	}](t, a.body)

	// A deleted path and an added path with the same content are one rename.
	want := []string{"README.md||modified", "extra.txt||added", "stacks/new.yaml|stacks/stage.yaml|renamed"}
	have := make([]string, 0, len(got.Files))

	for _, x := range got.Files {
		have = append(have, x["filename"]+"|"+x["previous_filename"]+"|"+x["status"])
	}

	if got.Status != "ahead" || !slices.Equal(have, want) {
		t.Fatalf("status %q, files %v, want ahead and %v", got.Status, have, want)
	}

	if got.Files[0]["patch"] != "@@ -1,1 +1,2 @@\n-hello\n+hello\n+world\n" || got.Files[2]["patch"] != "" {
		t.Fatalf("patches = %q and %q", got.Files[0]["patch"], got.Files[2]["patch"])
	}

	// The pages cut the list of files.
	page := func(n int) []file {
		p := c.do(
			http.MethodGet,
			fmt.Sprintf("/repos/acme/infra/compare/%s...%s?per_page=2&page=%d", base.ID, head.ID, n),
			userValue,
			"",
		)
		wantStatus(t, p, http.StatusOK)

		return decode[struct {
			Files []file `json:"files"`
		}](t, p.body).Files
	}

	if first, second, third := page(1), page(2), page(3); len(first) != 2 || len(second) != 1 || len(third) != 0 {
		t.Fatalf("pages of 2 files have %d, %d and %d files", len(first), len(second), len(third))
	}
}
