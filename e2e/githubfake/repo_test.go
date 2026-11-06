package githubfake_test

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e/githubfake"
)

type commitAnswer struct {
	SHA    string     `json:"sha"`
	Commit commitBody `json:"commit"`
}

type commitBody struct {
	Message   string     `json:"message"`
	Committer personBody `json:"committer"`
}

type personBody struct {
	Date string `json:"date"`
}

type contentsAnswer struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	SHA      string `json:"sha"`
	Size     int    `json:"size"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

func TestCommitsByRef(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)
	second := mustPush(t, f, "main", map[string][]byte{"a.txt": []byte("a")})

	if err := f.Tag("acme/infra", "v1", first.ID); err != nil {
		t.Fatal(err)
	}

	feature := mustPush(t, f, "feature/x", map[string][]byte{"x": []byte("x")})

	tests := []struct {
		name   string
		ref    string
		status int
		wantID string
	}{
		{"a branch", "main", http.StatusOK, second.ID},
		{"a tag", "v1", http.StatusOK, first.ID},
		{"a full commit ID", first.ID, http.StatusOK, first.ID},
		{"an abbreviated commit ID", second.ID[:10], http.StatusOK, second.ID},
		{"a branch with a slash", "feature/x", http.StatusOK, feature.ID},
		{"an unknown name", "nope", http.StatusUnprocessableEntity, ""},
		{"a too short commit ID", first.ID[:4], http.StatusUnprocessableEntity, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := c.do(http.MethodGet, "/repos/acme/infra/commits/"+tc.ref, f.UserToken(), "")
			wantStatus(t, a, tc.status)

			if tc.wantID == "" {
				return
			}

			out := decode[commitAnswer](t, a.body)
			if out.SHA != tc.wantID || out.Commit.Committer.Date == "" {
				t.Fatalf("commit = %+v, want %s with a date", out, tc.wantID)
			}
		})
	}
}

func TestCommitOfMissingRepository(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)

	wantStatus(
		t,
		newClient(t, f).do(http.MethodGet, "/repos/acme/none/commits/main", f.UserToken(), ""),
		http.StatusNotFound,
	)
}

func TestContentsOfFile(t *testing.T) {
	t.Parallel()

	f, first := newFake(t)
	c := newClient(t, f)
	second := mustPush(t, f, "main", map[string][]byte{"README.md": []byte("changed\n")})

	a := c.do(http.MethodGet, "/repos/acme/infra/contents/README.md?ref="+first.ID, f.UserToken(), "")
	wantStatus(t, a, http.StatusOK)

	out := decode[contentsAnswer](t, a.body)

	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
	if err != nil {
		t.Fatal(err)
	}

	if string(content) != "hello\n" || out.Type != "file" || out.Size != 6 || out.Encoding != "base64" {
		t.Fatalf("answer = %+v, content %q", out, content)
	}

	if out.SHA != githubfake.BlobID([]byte("hello\n")) || out.SHA == githubfake.BlobID([]byte("hello")) ||
		len(out.SHA) != 40 {
		t.Fatalf("blob ID = %s", out.SHA)
	}

	// The commit decides the content.
	b := c.do(http.MethodGet, "/repos/acme/infra/contents/README.md?ref="+second.ID, f.UserToken(), "")
	if got := decode[contentsAnswer](t, b.body).SHA; got != githubfake.BlobID([]byte("changed\n")) {
		t.Fatalf("blob ID at the second commit = %s", got)
	}

	// With no ref, the default branch answers.
	d := c.do(http.MethodGet, "/repos/acme/infra/contents/README.md", f.UserToken(), "")
	if got := decode[contentsAnswer](t, d.body).SHA; got != githubfake.BlobID([]byte("changed\n")) {
		t.Fatalf("blob ID with no ref = %s", got)
	}
}

func TestContentsOfDirectory(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)

	tests := []struct {
		name  string
		path  string
		names []string
		types []string
	}{
		{"a directory", "stacks", []string{"prod.yaml", "stage.yaml"}, []string{"file", "file"}},
		{"the root", "", []string{"README.md", "stacks"}, []string{"file", "dir"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := c.do(http.MethodGet, "/repos/acme/infra/contents/"+tc.path+"?ref=main", f.UserToken(), "")
			wantStatus(t, a, http.StatusOK)

			out := decode[[]contentsAnswer](t, a.body)
			if len(out) != len(tc.names) {
				t.Fatalf("answer = %s", a.body)
			}

			for i, e := range out {
				if e.Name != tc.names[i] || e.Type != tc.types[i] || e.Content != "" || e.Encoding != "" {
					t.Errorf("entry %d = %+v, want %s %s with no content", i, e, tc.names[i], tc.types[i])
				}
			}
		})
	}
}

func TestContentsNotFound(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)

	tests := map[string]string{
		"a missing file":         "/repos/acme/infra/contents/none.txt?ref=main",
		"a missing commit":       "/repos/acme/infra/contents/README.md?ref=" + strings.Repeat("0", 40),
		"a missing file below":   "/repos/acme/infra/contents/stacks/none.yaml?ref=main",
		"a missing repository":   "/repos/acme/none/contents/README.md?ref=main",
		"an encoded parent part": "/repos/acme/infra/contents/stacks%2F..%2FREADME.md?ref=main",
		"a part of a name":       "/repos/acme/infra/contents/READ?ref=main",
	}

	for name, path := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			wantStatus(t, c.do(http.MethodGet, path, f.UserToken(), ""), http.StatusNotFound)
		})
	}
}

func TestContentsAboveOneMiBAnswersTheBlob(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)
	big := bytes.Repeat([]byte("x"), 1<<20+1)
	mustPush(t, f, "big", map[string][]byte{"big.bin": big})

	a := c.do(http.MethodGet, "/repos/acme/infra/contents/big.bin?ref=big", f.UserToken(), "")
	wantStatus(t, a, http.StatusOK)

	out := decode[contentsAnswer](t, a.body)
	if out.Encoding != "none" || out.Content != "" || out.Size != len(big) {
		t.Fatalf("answer = %+v", out)
	}

	b := c.do(http.MethodGet, "/repos/acme/infra/git/blobs/"+out.SHA, f.UserToken(), "")
	wantStatus(t, b, http.StatusOK)

	got, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(decode[contentsAnswer](t, b.body).Content, "\n", ""))
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("blob content differs: err %v, %d bytes", err, len(got))
	}
}

func TestMissingBlobIsNotFound(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	a := newClient(t, f).do(http.MethodGet, "/repos/acme/infra/git/blobs/"+strings.Repeat("0", 40), f.UserToken(), "")

	wantStatus(t, a, http.StatusNotFound)
}

func TestContentsAboveTheLimitAnswerTooLarge(t *testing.T) {
	t.Parallel()

	f := githubfake.New(t, githubfake.Config{
		AppID: "", AppSlug: "", InstallationID: "", Owner: githubfake.Account{ID: 0, Login: ""},
		User: githubfake.Account{ID: 0, Login: ""}, UserToken: userValue, ClassicToken: false,
		Permissions: nil, ContentsLimit: 10, Key: nil, Now: nil, WebhookClient: nil,
	})

	files := map[string][]byte{"small": []byte("12345"), "large": []byte("12345678901")}
	if _, err := f.AddRepo("acme/infra", files); err != nil {
		t.Fatal(err)
	}

	c := newClient(t, f)

	wantStatus(t, c.do(http.MethodGet, "/repos/acme/infra/contents/small?ref=main", f.UserToken(), ""), http.StatusOK)

	a := c.do(http.MethodGet, "/repos/acme/infra/contents/large?ref=main", f.UserToken(), "")
	wantStatus(t, a, http.StatusForbidden)

	if !strings.Contains(string(a.body), `"code":"too_large"`) {
		t.Fatalf("body = %s", a.body)
	}
}

func TestRenamedRepositoryRedirects(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	c := newClient(t, f)

	if err := f.RenameRepo("acme/infra", "acme/platform"); err != nil {
		t.Fatal(err)
	}

	a := c.do(http.MethodGet, "/repos/acme/infra/contents/README.md?ref=main", f.UserToken(), "")
	wantStatus(t, a, http.StatusMovedPermanently)

	if got := a.header.Get("Location"); got != "/repos/acme/platform" {
		t.Fatalf("Location = %q", got)
	}

	wantStatus(
		t,
		c.do(http.MethodGet, "/repos/acme/platform/contents/README.md?ref=main", f.UserToken(), ""),
		http.StatusOK,
	)
}
