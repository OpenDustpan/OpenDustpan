package githubfake_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

type listedAnswer struct {
	SHA string `json:"sha"`
}

type refAnswer struct {
	Ref    string    `json:"ref"`
	Object refObject `json:"object"`
}

type refObject struct {
	SHA string `json:"sha"`
}

// idsOf returns the commit IDs of the list in body.
func idsOf(t *testing.T, body []byte) []string {
	t.Helper()

	var list []listedAnswer
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}

	ids := make([]string, 0, len(list))
	for _, l := range list {
		ids = append(ids, l.SHA)
	}

	return ids
}

func TestListCommits(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)
	second := mustPush(t, f, "main", map[string][]byte{"a.txt": []byte("a")})
	third := mustPush(t, f, "main", map[string][]byte{"a.txt": []byte("b")})
	feature := mustPush(t, f, "feature/x", map[string][]byte{"x": []byte("x")})

	since := func(at time.Time) string { return "&since=" + url.QueryEscape(at.UTC().Format(time.RFC3339)) }

	tests := []struct {
		name   string
		query  string
		status int
		want   []string
	}{
		{"the default branch, newest first", "", http.StatusOK, []string{third.ID, second.ID, first.ID}},
		{"a branch", "?sha=feature%2Fx", http.StatusOK, []string{feature.ID, third.ID, second.ID, first.ID}},
		{"a commit ID", "?sha=" + second.ID, http.StatusOK, []string{second.ID, first.ID}},
		{"since a time", "?sha=main" + since(second.Time), http.StatusOK, []string{third.ID, second.ID}},
		{"since a time after all", "?sha=main" + since(third.Time.Add(time.Hour)), http.StatusOK, []string{}},
		{"the first page", "?per_page=2&page=1", http.StatusOK, []string{third.ID, second.ID}},
		{"the second page", "?per_page=2&page=2", http.StatusOK, []string{first.ID}},
		{"a page after the last", "?per_page=2&page=3", http.StatusOK, []string{}},
		{"a ref that does not exist", "?sha=no-such-ref", http.StatusNotFound, nil},
		{"a time that is not valid", "?since=yesterday", http.StatusUnprocessableEntity, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := c.do(http.MethodGet, "/repos/acme/infra/commits"+tt.query, userValue, "")
			if got.status != tt.status {
				t.Fatalf("status %d, want %d: %s", got.status, tt.status, got.body)
			}

			if tt.want != nil {
				if ids := idsOf(t, got.body); !slices.Equal(ids, tt.want) {
					t.Fatalf("commits %v, want %v", ids, tt.want)
				}
			}
		})
	}
}

func TestCreateRef(t *testing.T) {
	t.Parallel()

	ref := func(name, sha string) string { return `{"ref":"` + name + `","sha":"` + sha + `"}` }

	tests := []struct {
		name   string
		body   func(first string) string
		bearer string
		status int
	}{
		{
			"a new branch",
			func(id string) string { return ref("refs/heads/dustpan/j-1", id) },
			userValue,
			http.StatusCreated,
		},
		{
			"a name that is taken",
			func(id string) string { return ref("refs/heads/main", id) },
			userValue,
			http.StatusUnprocessableEntity,
		},
		{
			"a commit that does not exist",
			func(string) string { return ref("refs/heads/b", strings.Repeat("0", 40)) },
			userValue,
			http.StatusUnprocessableEntity,
		},
		{
			"a name that is not a branch",
			func(id string) string { return ref("refs/tags/t", id) },
			userValue,
			http.StatusUnprocessableEntity,
		},
		{"a body that does not parse", func(string) string { return `{` }, userValue, http.StatusBadRequest},
		{"no credential", func(id string) string { return ref("refs/heads/c", id) }, "", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f, first := newFake(t)

			got := newClient(t, f).do(http.MethodPost, "/repos/acme/infra/git/refs", tt.bearer, tt.body(first.ID))
			if got.status != tt.status {
				t.Fatalf("status %d, want %d: %s", got.status, tt.status, got.body)
			}
		})
	}
}

func TestReadRef(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)

	body := `{"ref":"refs/heads/dustpan/j-1","sha":"` + first.ID + `"}`
	wantStatus(t, c.do(http.MethodPost, "/repos/acme/infra/git/refs", userValue, body), http.StatusCreated)

	got := c.do(http.MethodGet, "/repos/acme/infra/git/ref/heads/dustpan/j-1", userValue, "")

	var ref refAnswer
	if err := json.Unmarshal(got.body, &ref); err != nil || got.status != http.StatusOK ||
		ref.Ref != "refs/heads/dustpan/j-1" || ref.Object.SHA != first.ID {
		t.Fatalf("the new branch: status %d, %+v, %v", got.status, ref, err)
	}

	for _, ref := range []string{"heads/none", "tags/v1"} {
		wantStatus(t, c.do(http.MethodGet, "/repos/acme/infra/git/ref/"+ref, userValue, ""), http.StatusNotFound)
	}
}
