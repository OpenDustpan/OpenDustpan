package forgetest

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

// jobTrailer returns the trailer line of the job id.
func jobTrailer(id string) string { return "Dustpan-Job: " + id }

// ada is the person who made the changes of the commits of these cases.
func ada() forge.Person { return forge.Person{Name: "Ada Lovelace", Email: "ada@example.com"} }

// writeFile is a change that sets the content of a file.
func writeFile(path, content string) forge.FileChange {
	return forge.FileChange{Path: path, Content: []byte(content), Delete: false}
}

// deleteFile is a change that deletes a file.
func deleteFile(path string) forge.FileChange {
	return forge.FileChange{Path: path, Content: nil, Delete: true}
}

// commitOn returns a request for one commit on branch from base, with the
// trailer of the job id in the message.
func commitOn(branch, base, job string, changes ...forge.FileChange) forge.CommitRequest {
	return forge.CommitRequest{
		Branch: branch, NewBranch: false, Base: base,
		Message: "Update the stacks\n\n" + jobTrailer(job), Author: ada(), Changes: changes,
	}
}

// mainHead returns the head of main and fails the test on an error.
func mainHead(t *testing.T, h Harness) forge.Commit {
	t.Helper()

	head, err := h.Forge.Head(t.Context(), h.Repo, "main")
	if err != nil {
		t.Fatalf("Head = %v", err)
	}

	return head
}

// callCount returns the number of calls that the forge got.
func callCount(ctx context.Context, t *testing.T, h Harness) int {
	t.Helper()

	n, err := h.Fixture.Calls(ctx)
	if err != nil {
		t.Fatalf("calls: %v", err)
	}

	return n
}

func caseCommitLands(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	got, err := h.Forge.Commit(ctx, h.Repo, commitOn(
		"main", head.ID, "j-1", writeFile("stacks/prod.yaml", "a: 1\n"), writeFile("stacks/dev.yaml", "a: 2\n"),
	))
	if err != nil {
		t.Fatalf("Commit = %v", err)
	}

	if forge.ValidateCommitID(got.ID) != nil || got.ID == head.ID || got.Time.IsZero() {
		t.Fatalf("Commit = %+v, want a new commit with a time", got)
	}

	if now := mainHead(t, h); now.ID != got.ID {
		t.Fatalf("head of main = %q, want the commit %q", now.ID, got.ID)
	}

	// All changes are in the one commit, and a file that no change names stays.
	for path, want := range map[string]string{"stacks/prod.yaml": "a: 1\n", "stacks/dev.yaml": "a: 2\n", "README.md": "hello\n"} {
		file, readErr := h.Forge.ReadFile(ctx, h.Repo, got.ID, path, 1<<20)
		if readErr != nil || !bytes.Equal(file.Content, []byte(want)) {
			t.Errorf("ReadFile of %q = %q, %v; want %q", path, file.Content, readErr, want)
		}
	}

	// The trailer is on the commit, so that a retry finds it.
	found, err := h.Forge.FindCommit(ctx, h.Repo, "main", jobTrailer("j-1"), head.Time)
	if err != nil || found.ID != got.ID {
		t.Fatalf("FindCommit = %+v, %v; want the commit %q", found, err, got.ID)
	}
}

func caseCommitDelete(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	push(t, h, map[string][]byte{"old.txt": []byte("old\n")})
	head := mainHead(t, h)

	got, err := h.Forge.Commit(ctx, h.Repo, commitOn("main", head.ID, "j-2", deleteFile("old.txt")))
	if err != nil {
		t.Fatalf("Commit = %v", err)
	}

	if _, readErr := h.Forge.ReadFile(ctx, h.Repo, got.ID, "old.txt", 1<<20); !errors.Is(readErr, forge.ErrNotFound) {
		t.Fatalf("ReadFile of the deleted file = %v, want ErrNotFound", readErr)
	}

	if _, readErr := h.Forge.ReadFile(ctx, h.Repo, head.ID, "old.txt", 1<<20); readErr != nil {
		t.Fatalf("ReadFile of the file before the commit = %v, want the file", readErr)
	}
}

func caseCommitStale(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	base := mainHead(t, h)
	moved := push(t, h, map[string][]byte{"stacks/prod.yaml": []byte("other\n")})

	_, err := h.Forge.Commit(ctx, h.Repo, commitOn("main", base.ID, "j-3", writeFile("stacks/prod.yaml", "mine\n")))
	if !errors.Is(err, forge.ErrConflict) {
		t.Fatalf("Commit on a branch that moved = %v, want ErrConflict", err)
	}

	// The commit did not happen.
	if now := mainHead(t, h); now.ID != moved.ID {
		t.Fatalf("head of main = %q, want %q", now.ID, moved.ID)
	}
}

func caseCommitInvalidDelete(t *testing.T, h Harness) {
	t.Helper()

	head := mainHead(t, h)

	_, err := h.Forge.Commit(t.Context(), h.Repo, commitOn("main", head.ID, "j-4", deleteFile("no-such-file.txt")))
	if !errors.Is(err, forge.ErrInvalid) {
		t.Fatalf("Commit of a delete of a missing file = %v, want ErrInvalid", err)
	}

	if now := mainHead(t, h); now.ID != head.ID {
		t.Fatalf("head of main = %q, want %q", now.ID, head.ID)
	}
}

func caseCommitProtected(t *testing.T, h Harness) {
	t.Helper()

	head := mainHead(t, h)

	if err := h.Fixture.Protect(t.Context(), "main"); err != nil {
		t.Fatalf("protect: %v", err)
	}

	_, err := h.Forge.Commit(t.Context(), h.Repo, commitOn("main", head.ID, "j-5", writeFile("a.txt", "a")))
	if !errors.Is(err, forge.ErrRefused) {
		t.Fatalf("Commit to a protected branch = %v, want ErrRefused", err)
	}
}

func caseCommitMissingBranch(t *testing.T, h Harness) {
	t.Helper()

	head := mainHead(t, h)

	_, err := h.Forge.Commit(t.Context(), h.Repo, commitOn("no-such-branch", head.ID, "j-6", writeFile("a.txt", "a")))
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("Commit to a missing branch = %v, want ErrNotFound", err)
	}
}

func caseCommitNewBranch(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	req := commitOn("dustpan/j-7", head.ID, "j-7", writeFile("a.txt", "a"))
	req.NewBranch = true

	got, err := h.Forge.Commit(ctx, h.Repo, req)
	if err != nil {
		t.Fatalf("Commit on a new branch = %v", err)
	}

	onBranch, err := h.Forge.Head(ctx, h.Repo, "dustpan/j-7")
	if err != nil || onBranch.ID != got.ID {
		t.Fatalf("head of the new branch = %+v, %v; want the commit %q", onBranch, err, got.ID)
	}

	// The new branch starts at the base, and main does not move.
	if now := mainHead(t, h); now.ID != head.ID {
		t.Fatalf("head of main = %q, want %q", now.ID, head.ID)
	}

	if _, readErr := h.Forge.ReadFile(ctx, h.Repo, got.ID, "README.md", 1<<20); readErr != nil {
		t.Fatalf("ReadFile of a file of the base on the new branch = %v", readErr)
	}
}

func caseCommitNewBranchTaken(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	other, err := h.Fixture.Push(ctx, "dustpan/j-8", map[string][]byte{"b.txt": []byte("b")})
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	req := commitOn("dustpan/j-8", head.ID, "j-8", writeFile("a.txt", "a"))
	req.NewBranch = true

	if _, commitErr := h.Forge.Commit(ctx, h.Repo, req); !errors.Is(commitErr, forge.ErrConflict) {
		t.Fatalf("Commit on a new branch that has another head = %v, want ErrConflict", commitErr)
	}

	if now, headErr := h.Forge.Head(ctx, h.Repo, "dustpan/j-8"); headErr != nil || now.ID != other.ID {
		t.Fatalf("head of the branch = %+v, %v; want %q", now, headErr, other.ID)
	}
}

// hostileCommits lists requests that fail before any call to the forge.
func hostileCommits(base string) map[string]forge.CommitRequest {
	with := func(edit func(*forge.CommitRequest)) forge.CommitRequest {
		req := commitOn("main", base, "j-9", writeFile("a.txt", "a"))
		edit(&req)

		return req
	}

	onPath := func(p string) forge.CommitRequest {
		return with(func(r *forge.CommitRequest) { r.Changes = []forge.FileChange{writeFile(p, "x")} })
	}

	requests := map[string]forge.CommitRequest{
		"a path with ..":           onPath("../a.txt"),
		"a path that leaves":       onPath("stacks/../../a.txt"),
		"an absolute path":         onPath("/etc/passwd"),
		"a path with a NUL":        onPath("a\x00b"),
		"a path with a new line":   onPath("a\nb"),
		"a path with a backslash":  onPath(`a\b`),
		"a path in .git":           onPath(".git/config"),
		"a path in .GIT":           onPath("a/.GIT/b"),
		"a path with an empty dir": onPath("a//b"),
		"an empty path":            onPath(""),
		"a delete with content": with(func(r *forge.CommitRequest) {
			r.Changes = []forge.FileChange{{Path: "a.txt", Content: []byte("x"), Delete: true}}
		}),
		"a path twice": with(func(r *forge.CommitRequest) {
			r.Changes = []forge.FileChange{writeFile("a.txt", "1"), writeFile("a.txt", "2")}
		}),
		"no change":               with(func(r *forge.CommitRequest) { r.Changes = nil }),
		"a branch that is a ref":  with(func(r *forge.CommitRequest) { r.Branch = "refs/heads/main" }),
		"a branch with ..":        with(func(r *forge.CommitRequest) { r.Branch = "a..b" }),
		"a branch with a space":   with(func(r *forge.CommitRequest) { r.Branch = "a b" }),
		"a branch with a NUL":     with(func(r *forge.CommitRequest) { r.Branch = "a\x00b" }),
		"a base that is a branch": with(func(r *forge.CommitRequest) { r.Base = "main" }),
		"a short base":            with(func(r *forge.CommitRequest) { r.Base = base[:10] }),
		"a base in capitals":      with(func(r *forge.CommitRequest) { r.Base = strings.ToUpper(base) }),
		"no message":              with(func(r *forge.CommitRequest) { r.Message = "" }),
		"no first line":           with(func(r *forge.CommitRequest) { r.Message = "\n\nbody" }),
		"a message with a NUL":    with(func(r *forge.CommitRequest) { r.Message = "a\x00b" }),
		"an author with a new line": with(
			func(r *forge.CommitRequest) { r.Author.Name = "Ada\nCo-authored-by: Eve <e@example.com>" },
		),
		"an author with a bracket": with(func(r *forge.CommitRequest) { r.Author.Name = "Ada <x@example.com>" }),
		"an email with a bracket":  with(func(r *forge.CommitRequest) { r.Author.Email = "ada@example.com>" }),
		"an email with a new line": with(func(r *forge.CommitRequest) { r.Author.Email = "ada@example.com\nX: y" }),
		"an email with no @":       with(func(r *forge.CommitRequest) { r.Author.Email = "ada" }),
		"a name and no email":      with(func(r *forge.CommitRequest) { r.Author.Email = "" }),
	}

	return requests
}

func caseCommitInvalid(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	base := mainHead(t, h).ID
	before := callCount(ctx, t, h)

	hostile := hostileCommits(base)

	for name := range hostile {
		req := hostile[name]
		if _, err := h.Forge.Commit(ctx, h.Repo, req); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("Commit with %s = %v, want ErrInvalid", name, err)
		}
	}

	// A bad repository path is invalid too.
	for _, repo := range []string{"", "acme", "acme/", "/infra", "acme/../infra", "acme/infra/x", "ac me/infra"} {
		req := commitOn("main", validBase, "j-9", writeFile("a.txt", "a"))
		if _, err := h.Forge.Commit(ctx, repo, req); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("Commit in the repository %q = %v, want ErrInvalid", repo, err)
		}
	}

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("an invalid commit made %d calls to the forge, want none", after-before)
	}
}

func caseCommitTooLarge(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)
	before := callCount(ctx, t, h)

	tooLong := commitOn("main", head.ID, "j-10", writeFile("a.txt", "a"))
	tooLong.Message = strings.Repeat("m", forge.MaxMessageBytes+1)

	tooBig := commitOn("main", head.ID, "j-10", forge.FileChange{
		Path: "big.bin", Content: bytes.Repeat([]byte("x"), forge.MaxChangeBytes+1), Delete: false,
	})

	many := commitOn("main", head.ID, "j-10")

	for i := range forge.MaxChanges + 1 {
		many.Changes = append(many.Changes, writeFile("f"+strings.Repeat("x", i), "x"))
	}

	tests := []struct {
		name string
		req  *forge.CommitRequest
		want error
	}{
		{"a message above the limit", &tooLong, forge.ErrTooLarge},
		{"content above the limit", &tooBig, forge.ErrTooLarge},
		{"too many changes", &many, forge.ErrInvalid},
	}

	for _, tc := range tests {
		if _, err := h.Forge.Commit(ctx, h.Repo, *tc.req); !errors.Is(err, tc.want) {
			t.Errorf("Commit with %s = %v, want %v", tc.name, err, tc.want)
		}
	}

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("a commit above a limit made %d calls to the forge, want none", after-before)
	}
}

func caseFindCommit(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	first, err := h.Forge.Commit(ctx, h.Repo, commitOn("main", head.ID, "j-11", writeFile("a.txt", "1")))
	if err != nil {
		t.Fatalf("Commit = %v", err)
	}

	// A retry of the write-back asks for the commit of its job first.
	found, err := h.Forge.FindCommit(ctx, h.Repo, "main", jobTrailer("j-11"), head.Time)
	if err != nil || found.ID != first.ID {
		t.Fatalf("FindCommit = %+v, %v; want the first commit %q", found, err, first.ID)
	}

	if found.Time.IsZero() {
		t.Fatal("FindCommit gave no time")
	}

	// Commits of other jobs do not hide it, and the newest commit of a trailer wins.
	other, err := h.Forge.Commit(ctx, h.Repo, commitOn("main", first.ID, "j-12", writeFile("a.txt", "2")))
	if err != nil {
		t.Fatalf("Commit of another job = %v", err)
	}

	again, err := h.Forge.Commit(ctx, h.Repo, commitOn("main", other.ID, "j-11", writeFile("a.txt", "3")))
	if err != nil {
		t.Fatalf("Commit of the first job again = %v", err)
	}

	for trailer, want := range map[string]string{jobTrailer("j-12"): other.ID, jobTrailer("j-11"): again.ID} {
		got, findErr := h.Forge.FindCommit(ctx, h.Repo, "main", trailer, head.Time)
		if findErr != nil || got.ID != want {
			t.Errorf("FindCommit of %q = %+v, %v; want %q", trailer, got, findErr, want)
		}
	}
}

func caseFindCommitSince(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	got, err := h.Forge.Commit(ctx, h.Repo, commitOn("main", head.ID, "j-13", writeFile("a.txt", "1")))
	if err != nil {
		t.Fatalf("Commit = %v", err)
	}

	// At the time of the commit: found.
	found, err := h.Forge.FindCommit(ctx, h.Repo, "main", jobTrailer("j-13"), got.Time)
	if err != nil || found.ID != got.ID {
		t.Fatalf("FindCommit since the time of the commit = %+v, %v; want %q", found, err, got.ID)
	}

	// After it: not found.
	_, err = h.Forge.FindCommit(ctx, h.Repo, "main", jobTrailer("j-13"), got.Time.Add(time.Hour))
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("FindCommit since an hour after the commit = %v, want ErrNotFound", err)
	}
}

func caseFindCommitNoTrailer(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	// A commit that quotes a trailer line in its text, and has none.
	quoting := commitOn("main", head.ID, "j-14", writeFile("a.txt", "1"))
	quoting.Message = "Quote\n\n" + jobTrailer("j-15") + "\n\nThe line above is a quote."

	if _, err := h.Forge.Commit(ctx, h.Repo, quoting); err != nil {
		t.Fatalf("Commit = %v", err)
	}

	for _, trailer := range []string{
		jobTrailer("j-99"),  // no commit has it
		jobTrailer("j-15"),  // a line in the text, not in the trailers
		jobTrailer("j-1"),   // a prefix of no trailer
		"Dustpan-Job: j-14", // the trailer of the message, which the commit does not carry
	} {
		got, err := h.Forge.FindCommit(ctx, h.Repo, "main", trailer, head.Time)
		if !errors.Is(err, forge.ErrNotFound) {
			t.Errorf("FindCommit of %q = %+v, %v; want ErrNotFound", trailer, got, err)
		}
	}
}

func caseFindCommitMissingRef(t *testing.T, h Harness) {
	t.Helper()

	_, err := h.Forge.FindCommit(t.Context(), h.Repo, "no-such-ref", jobTrailer("j-1"), time.Unix(0, 0))
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("FindCommit on a missing ref = %v, want ErrNotFound", err)
	}
}

func caseFindCommitInvalid(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	before := callCount(ctx, t, h)

	for _, trailer := range []string{
		"", "Dustpan-Job", "Dustpan-Job:j-1", "Dustpan-Job: ", ": j-1", "Dustpan-Job: j-1\nX: y", "Dustpan-Job: j\r1",
		"Dustpan-Job: j\x001", "Dustpan Job: j-1", " Dustpan-Job: j-1", "Dustpan-Job: j-1 ", "Dustpan-Job: " + strings.Repeat("x", 300),
	} {
		_, err := h.Forge.FindCommit(ctx, h.Repo, "main", trailer, time.Unix(0, 0))
		if !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("FindCommit with the trailer %q = %v, want ErrInvalid", trailer, err)
		}
	}

	for _, ref := range []string{"", "refs/heads/main", "a..b", "a b", "a\x00b", "-x", "a/", ".hidden"} {
		_, err := h.Forge.FindCommit(ctx, h.Repo, ref, jobTrailer("j-1"), time.Unix(0, 0))
		if !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("FindCommit on the ref %q = %v, want ErrInvalid", ref, err)
		}
	}

	_, err := h.Forge.FindCommit(ctx, "acme/../infra", "main", jobTrailer("j-1"), time.Unix(0, 0))
	if !errors.Is(err, forge.ErrInvalid) {
		t.Errorf("FindCommit in a bad repository path = %v, want ErrInvalid", err)
	}

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("an invalid FindCommit made %d calls to the forge, want none", after-before)
	}
}
