package githubfake_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type gqlOut struct {
	Data   gqlData  `json:"data"`
	Errors []gqlErr `json:"errors"`
}

type gqlData struct {
	Create *gqlCreate `json:"createCommitOnBranch"`
}

type gqlCreate struct {
	Commit gqlCommit `json:"commit"`
}

type gqlCommit struct {
	OID string `json:"oid"`
}

type gqlErr struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// mutation returns the call that commits content to path on branch and
// deletes stacks/stage.yaml.
func mutation(t *testing.T, branch, head, path, content string) string {
	t.Helper()

	in := map[string]any{
		"branch":          map[string]string{"repositoryNameWithOwner": "acme/infra", "branchName": branch},
		"message":         map[string]string{"headline": "Edit", "body": "Dustpan-Job: j-1"},
		"expectedHeadOid": head,
		"fileChanges": map[string]any{
			"additions": []map[string]string{
				{"path": path, "contents": base64.StdEncoding.EncodeToString([]byte(content))},
			},
			"deletions": []map[string]string{{"path": "stacks/stage.yaml"}},
		},
	}

	body, err := json.Marshal(map[string]any{
		"query":     "mutation($input: CreateCommitOnBranchInput!) { createCommitOnBranch(input: $input) { commit { oid } } }",
		"variables": map[string]any{"input": in},
	})
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

func TestCreateCommitOnBranch(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)

	a := c.do(http.MethodPost, "/graphql", f.UserToken(), mutation(t, "main", first.ID, "new.txt", "new"))
	wantStatus(t, a, http.StatusOK)

	out := decode[gqlOut](t, a.body)
	if out.Data.Create == nil {
		t.Fatalf("answer = %s", a.body)
	}

	head, err := f.Head("acme/infra", "main")
	if err != nil || head.ID != out.Data.Create.Commit.OID || head.Parent != first.ID {
		t.Fatalf("head = %+v, err %v, want %s on top of %s", head, err, out.Data.Create.Commit.OID, first.ID)
	}

	if !strings.Contains(head.Message, "Dustpan-Job: j-1") {
		t.Fatalf("message = %q", head.Message)
	}

	wantStatus(
		t,
		c.do(http.MethodGet, "/repos/acme/infra/contents/stacks/stage.yaml?ref=main", f.UserToken(), ""),
		http.StatusNotFound,
	)
	wantStatus(t, c.do(http.MethodGet, "/repos/acme/infra/contents/new.txt?ref=main", f.UserToken(), ""), http.StatusOK)
}

func TestCreateCommitOnBranchErrors(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)

	mustPush(t, f, "stale", map[string][]byte{"p": []byte("p")})
	locked := mustPush(t, f, "locked", map[string][]byte{"p": []byte("p")})

	if err := f.Protect("acme/infra", "locked"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		branch string
		head   string
		want   string
	}{
		{"a head that moved is stale", "stale", first.ID, "STALE_DATA"},
		{"a missing branch is not found", "nope", first.ID, "NOT_FOUND"},
		{"a protected branch is refused", "locked", locked.ID, "FORBIDDEN"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := c.do(http.MethodPost, "/graphql", f.UserToken(), mutation(t, tc.branch, tc.head, "x", "x"))

			out := decode[gqlOut](t, a.body)
			if len(out.Errors) != 1 || out.Errors[0].Type != tc.want || out.Data.Create != nil {
				t.Fatalf("answer = %s, want the type %s", a.body, tc.want)
			}
		})
	}
}

func TestCreateCommitOnBranchNeedsWriteAccess(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)
	token := installationToken(t, c, `{"permissions":{"contents":"read","metadata":"read"}}`)

	out := decode[gqlOut](t, c.do(http.MethodPost, "/graphql", token, mutation(t, "main", first.ID, "x", "x")).body)
	if len(out.Errors) != 1 || out.Errors[0].Type != "FORBIDDEN" {
		t.Fatalf("answer = %+v", out)
	}
}

func TestGraphQLOtherQueriesAndNoCredential(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)

	out := decode[gqlOut](t, c.do(http.MethodPost, "/graphql", f.UserToken(), `{"query":"{ viewer { login } }"}`).body)
	if len(out.Errors) != 1 || !strings.Contains(out.Errors[0].Message, "serves only createCommitOnBranch") {
		t.Fatalf("answer = %+v", out)
	}

	wantStatus(
		t,
		c.do(http.MethodPost, "/graphql", "", mutation(t, "main", first.ID, "x", "x")),
		http.StatusUnauthorized,
	)
}

func TestCreateCommitOnBranchRefusesTheDeleteOfAMissingFile(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)

	// The mutation deletes stacks/stage.yaml. The second call deletes it again.
	done := decode[gqlOut](
		t,
		c.do(http.MethodPost, "/graphql", f.UserToken(), mutation(t, "main", first.ID, "x", "x")).body,
	)
	if done.Data.Create == nil {
		t.Fatalf("first answer = %+v", done)
	}

	again := decode[gqlOut](
		t,
		c.do(
			http.MethodPost,
			"/graphql",
			f.UserToken(),
			mutation(t, "main", done.Data.Create.Commit.OID, "y", "y"),
		).body,
	)
	if len(again.Errors) != 1 || again.Errors[0].Type != "UNPROCESSABLE" || again.Data.Create != nil {
		t.Fatalf("second answer = %+v", again)
	}

	if head, err := f.Head("acme/infra", "main"); err != nil || head.ID != done.Data.Create.Commit.OID {
		t.Fatalf("head = %+v, %v; want the first commit", head, err)
	}
}
