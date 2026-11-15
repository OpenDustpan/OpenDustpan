package forgetest

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
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
		Message: "Update the stacks\n\n" + jobTrailer(job), Trailer: jobTrailer(job), Author: ada(), Changes: changes,
	}
}

// anyDelivery is a delivery with the trailer and the base, on main, with the
// changes of one file. A case uses it for a call that must fail before the
// forge checks a proof.
func anyDelivery(trailer, base string) forge.Delivery {
	return forge.Delivery{
		Trailer: trailer, Base: base, Branch: "main",
		Changes: forge.ChangesDigest([]forge.FileChange{writeFile("a.txt", "1")}),
	}
}

// withTrailer returns d with the trailer line trailer.
func withTrailer(d forge.Delivery, trailer string) forge.Delivery {
	d.Trailer = trailer

	return d
}

// anyBase is a full commit ID that no repository of a case has. A case uses it
// for a call that must fail before the forge reads the base.
const anyBase = "0123456789abcdef0123456789abcdef01234567"

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

	req := commitOn(
		"main", head.ID, "j-1", writeFile("stacks/prod.yaml", "a: 1\n"), writeFile("stacks/dev.yaml", "a: 2\n"),
	)

	got, err := h.Forge.Commit(ctx, h.Repo, req)
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
	found, err := h.Forge.FindCommit(
		ctx,
		h.Repo,
		"main",
		forge.DeliveryOf(req),
		head.Time,
	)
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

func caseCommitOtherFile(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	base := push(t, h, map[string][]byte{"stacks/prod.yaml": []byte("a: 1\n")})
	moved := push(t, h, map[string][]byte{"other.txt": []byte("other\n")})

	got, err := h.Forge.Commit(ctx, h.Repo, commitOn("main", base.ID, "j-16", writeFile("stacks/prod.yaml", "a: 2\n")))
	if err != nil {
		t.Fatalf("Commit after a change of another file = %v, want a commit", err)
	}

	if got.ID == moved.ID || mainHead(t, h).ID != got.ID {
		t.Fatalf("head of main = %q, want the new commit %q on top of %q", mainHead(t, h).ID, got.ID, moved.ID)
	}

	// The commit keeps the other change and adds its own.
	for path, want := range map[string]string{"other.txt": "other\n", "stacks/prod.yaml": "a: 2\n"} {
		file, readErr := h.Forge.ReadFile(ctx, h.Repo, got.ID, path, 1<<20)
		if readErr != nil || !bytes.Equal(file.Content, []byte(want)) {
			t.Errorf("ReadFile of %q = %q, %v; want %q", path, file.Content, readErr, want)
		}
	}
}

func caseCommitNewBranchRace(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	var wg sync.WaitGroup

	results := make([]error, 2)
	commits := make([]forge.Commit, 2)

	for i := range results {
		wg.Go(func() {
			req := commitOn("dustpan/j-17", head.ID, "j-17", writeFile("a.txt", strconv.Itoa(i)))
			req.NewBranch = true
			commits[i], results[i] = h.Forge.Commit(ctx, h.Repo, req)
		})
	}

	wg.Wait()

	passed, conflicts := 0, 0

	for i, err := range results {
		switch {
		case err == nil:
			passed++

			onBranch, headErr := h.Forge.Head(ctx, h.Repo, "dustpan/j-17")
			if headErr != nil || onBranch.ID != commits[i].ID {
				t.Errorf("head of the branch = %+v, %v; want the commit %q", onBranch, headErr, commits[i].ID)
			}

		case errors.Is(err, forge.ErrConflict):
			conflicts++
		default:
			t.Errorf("Commit %d = %v, want a commit or ErrConflict", i, err)
		}
	}

	if passed != 1 || conflicts != 1 {
		t.Fatalf("%d commits passed and %d were conflicts, want one of each", passed, conflicts)
	}
}

func caseCommitForcePushed(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	base := push(t, h, map[string][]byte{"stacks/prod.yaml": []byte("a: 1\n")})

	// The history of main is replaced. The file that the commit changes has the
	// same content as at the base, so only the history shows the change.
	rewritten, err := h.Fixture.Rewrite(ctx, "main", map[string][]byte{
		"stacks/prod.yaml": []byte("a: 1\n"), "new.txt": []byte("new\n"),
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	_, err = h.Forge.Commit(ctx, h.Repo, commitOn("main", base.ID, "j-18", writeFile("stacks/prod.yaml", "a: 2\n")))
	if !errors.Is(err, forge.ErrConflict) {
		t.Fatalf("Commit after a force push = %v, want ErrConflict", err)
	}

	if now := mainHead(t, h); now.ID != rewritten.ID {
		t.Fatalf("head of main = %q, want %q", now.ID, rewritten.ID)
	}
}

func caseCommitRefused422(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	// GitHub cannot have the branches "dustpan" and "dustpan/j-19" together, and
	// answers 422.
	if _, err := h.Fixture.Push(ctx, "dustpan", map[string][]byte{"b.txt": []byte("b")}); err != nil {
		t.Fatalf("push: %v", err)
	}

	req := commitOn("dustpan/j-19", head.ID, "j-19", writeFile("a.txt", "a"))
	req.NewBranch = true

	if _, err := h.Forge.Commit(ctx, h.Repo, req); !errors.Is(err, forge.ErrInvalid) {
		t.Fatalf("Commit on a new branch with a name that collides = %v, want ErrInvalid", err)
	}

	// A base that the repository does not have is not found, also with 422.
	req = commitOn("dustpan2/j-19", missingCommit(), "j-19", writeFile("a.txt", "a"))
	req.NewBranch = true

	if _, err := h.Forge.Commit(ctx, h.Repo, req); !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("Commit on a new branch at a missing base = %v, want ErrNotFound", err)
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
		"an absolute path":         onPath("/etc/hosts"),
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

	firstReq := commitOn("main", head.ID, "j-11", writeFile("a.txt", "1"))

	first, err := h.Forge.Commit(ctx, h.Repo, firstReq)
	if err != nil {
		t.Fatalf("Commit = %v", err)
	}

	// A retry of the write-back asks for the commit of its job first.
	found, err := h.Forge.FindCommit(
		ctx,
		h.Repo,
		"main",
		forge.DeliveryOf(firstReq),
		head.Time,
	)
	if err != nil || found.ID != first.ID {
		t.Fatalf("FindCommit = %+v, %v; want the first commit %q", found, err, first.ID)
	}

	if found.Time.IsZero() {
		t.Fatal("FindCommit gave no time")
	}

	// Commits of other jobs do not hide it, and the newest commit of a trailer wins.
	otherReq := commitOn("main", first.ID, "j-12", writeFile("a.txt", "2"))

	other, err := h.Forge.Commit(ctx, h.Repo, otherReq)
	if err != nil {
		t.Fatalf("Commit of another job = %v", err)
	}

	againReq := commitOn("main", other.ID, "j-11", writeFile("a.txt", "3"))

	again, err := h.Forge.Commit(ctx, h.Repo, againReq)
	if err != nil {
		t.Fatalf("Commit of the first job again = %v", err)
	}

	// Each commit of a trailer is found with the base of its own request.
	for _, tt := range []struct {
		d    forge.Delivery
		want string
	}{
		{forge.DeliveryOf(otherReq), other.ID},
		{forge.DeliveryOf(againReq), again.ID},
	} {
		got, findErr := h.Forge.FindCommit(ctx, h.Repo, "main", tt.d, head.Time)
		if findErr != nil || got.ID != tt.want {
			t.Errorf("FindCommit of %q = %+v, %v; want %q", tt.d.Trailer, got, findErr, tt.want)
		}
	}
}

func caseFindCommitSince(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	req := commitOn("main", head.ID, "j-13", writeFile("a.txt", "1"))

	got, err := h.Forge.Commit(ctx, h.Repo, req)
	if err != nil {
		t.Fatalf("Commit = %v", err)
	}

	// At the time of the commit: found.
	found, err := h.Forge.FindCommit(
		ctx,
		h.Repo,
		"main",
		forge.DeliveryOf(req),
		got.Time,
	)
	if err != nil || found.ID != got.ID {
		t.Fatalf("FindCommit since the time of the commit = %+v, %v; want %q", found, err, got.ID)
	}

	// After it: not found.
	_, err = h.Forge.FindCommit(
		ctx, h.Repo, "main", forge.DeliveryOf(req), got.Time.Add(time.Hour),
	)
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
	quoting.Message = "Quote\n\n" + jobTrailer("j-15") + "\n\nThe line above is a quote.\n\n" + jobTrailer("j-14")

	if _, err := h.Forge.Commit(ctx, h.Repo, quoting); err != nil {
		t.Fatalf("Commit = %v", err)
	}

	for _, trailer := range []string{
		jobTrailer("j-99"), // no commit has it
		jobTrailer("j-15"), // a line in the text, not in the trailers
		jobTrailer("j-1"),  // a prefix of no trailer
	} {
		got, err := h.Forge.FindCommit(
			ctx, h.Repo, "main", withTrailer(forge.DeliveryOf(quoting), trailer), head.Time,
		)
		if !errors.Is(err, forge.ErrNotFound) {
			t.Errorf("FindCommit of %q = %+v, %v; want ErrNotFound", trailer, got, err)
		}
	}
}

func caseFindCommitMissingRef(t *testing.T, h Harness) {
	t.Helper()

	d := anyDelivery(jobTrailer("j-1"), anyBase)
	d.Branch = "no-such-ref"

	_, err := h.Forge.FindCommit(t.Context(), h.Repo, "no-such-ref", d, time.Unix(0, 0))
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
		_, err := h.Forge.FindCommit(
			ctx,
			h.Repo,
			"main",
			anyDelivery(trailer, anyBase),
			time.Unix(0, 0),
		)
		if !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("FindCommit with the trailer %q = %v, want ErrInvalid", trailer, err)
		}
	}

	for _, ref := range []string{"", "refs/heads/main", "a..b", "a b", "a\x00b", "-x", "a/", ".hidden"} {
		_, err := h.Forge.FindCommit(
			ctx,
			h.Repo,
			ref,
			anyDelivery(jobTrailer("j-1"), anyBase),
			time.Unix(0, 0),
		)
		if !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("FindCommit on the ref %q = %v, want ErrInvalid", ref, err)
		}
	}

	// The proof key is for the adapter only, and the base is a full commit ID.
	for _, base := range []string{"", "main", "0123456789abcdef", strings.ToUpper(anyBase), anyBase + "0"} {
		_, err := h.Forge.FindCommit(
			ctx,
			h.Repo,
			"main",
			anyDelivery(jobTrailer("j-1"), base),
			time.Unix(0, 0),
		)
		if !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("FindCommit with the base %q = %v, want ErrInvalid", base, err)
		}
	}

	_, err := h.Forge.FindCommit(
		ctx, h.Repo, "main", anyDelivery("Dustpan-Proof: abc", anyBase), time.Unix(0, 0),
	)
	if !errors.Is(err, forge.ErrInvalid) {
		t.Errorf("FindCommit with the key of the proof = %v, want ErrInvalid", err)
	}

	_, err = h.Forge.FindCommit(
		ctx, "acme/../infra", "main", anyDelivery(jobTrailer("j-1"), anyBase), time.Unix(0, 0),
	)
	if !errors.Is(err, forge.ErrInvalid) {
		t.Errorf("FindCommit in a bad repository path = %v, want ErrInvalid", err)
	}

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("an invalid FindCommit made %d calls to the forge, want none", after-before)
	}
}

// caseFindCommitInvalidDelivery checks that FindCommit refuses a delivery
// whose branch is not a branch name or whose changes are not a digest, before
// any call.
func caseFindCommitInvalidDelivery(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	before := callCount(ctx, t, h)

	// The branch of the delivery is a branch name, and its changes are a digest.
	for _, branch := range []string{"", "refs/heads/main", "a..b", "a b", "a\x00b", "-x", "a/", ".hidden"} {
		d := anyDelivery(jobTrailer("j-1"), anyBase)
		d.Branch = branch

		if _, err := h.Forge.FindCommit(ctx, h.Repo, "main", d, time.Unix(0, 0)); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("FindCommit with the branch %q = %v, want ErrInvalid", branch, err)
		}
	}

	// The search is on the branch of the delivery, never on another ref.
	for _, ref := range []string{"dustpan/j-1", "main2", "Main"} {
		_, err := h.Forge.FindCommit(ctx, h.Repo, ref, anyDelivery(jobTrailer("j-1"), anyBase), time.Unix(0, 0))
		if !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("FindCommit on the ref %q for a delivery of main = %v, want ErrInvalid", ref, err)
		}
	}

	for _, changes := range []string{
		"", "sha256:", "sha256:" + strings.Repeat("a", 63), "sha256:" + strings.Repeat("A", 64),
		"sha1:" + strings.Repeat("a", 40), strings.Repeat("a", 64), "sha256:" + strings.Repeat("a", 64) + "\x00",
	} {
		d := anyDelivery(jobTrailer("j-1"), anyBase)
		d.Changes = changes

		if _, err := h.Forge.FindCommit(ctx, h.Repo, "main", d, time.Unix(0, 0)); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("FindCommit with the changes %q = %v, want ErrInvalid", changes, err)
		}
	}

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("an invalid FindCommit made %d calls to the forge, want none", after-before)
	}
}
