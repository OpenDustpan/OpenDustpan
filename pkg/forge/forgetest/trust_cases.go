package forgetest

import (
	"errors"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

// Party says who made, or who committed, a commit that a case pushes.
type Party int

// The parties of a pushed commit.
const (
	// PartyNobody is an email that no account of the forge has.
	PartyNobody Party = iota
	// PartyOther is a person with push access who is not the connection.
	PartyOther
	// PartyConnection is the identity that the connection commits as: the bot
	// account of the app, or the user of the token.
	PartyConnection
	// PartyForge is the forge itself, as for a commit that the forge makes
	// for the connection.
	PartyForge
)

// Origin says who made a pushed commit and whether the forge verifies it.
type Origin struct {
	Author    Party
	Committer Party
	Verified  bool
}

// EntryKind is the kind of an entry that is not a regular file.
type EntryKind int

// The kinds of entry that Fixture.PushEntry makes.
const (
	// EntrySymlink is a symbolic link.
	EntrySymlink EntryKind = iota + 1
	// EntrySubmodule is a submodule.
	EntrySubmodule
	// EntryDirectory is a directory at the path.
	EntryDirectory
)

func (k EntryKind) String() string {
	switch k {
	case EntrySymlink:
		return "symlink"
	case EntrySubmodule:
		return "submodule"
	case EntryDirectory:
		return "directory"
	default:
		return "entry " + strconv.Itoa(int(k))
	}
}

// The connection, as a party, makes the commit of the connection: the author
// is the connection and the committer is the forge, as for a commit from the
// mutation createCommitOnBranch.
func ownCommit() Origin {
	return Origin{Author: PartyConnection, Committer: PartyForge, Verified: true}
}

// caseFindCommitIgnoresPushedTrailer pushes commits that carry the trailer of a
// job, and checks that only a commit of the connection counts. Each hostile row
// differs from the commit of the connection in one field, so that each check
// stops its row alone.
func caseFindCommitIgnoresPushedTrailer(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	tests := []struct {
		name   string
		origin Origin
		want   error
	}{
		{"the commit of the connection", ownCommit(), nil},
		{
			"the connection as author and as committer",
			Origin{Author: PartyConnection, Committer: PartyConnection, Verified: true},
			nil,
		},
		{"another author", Origin{Author: PartyOther, Committer: PartyForge, Verified: true}, forge.ErrUntrustedCommit},
		{
			"no account as author",
			Origin{Author: PartyNobody, Committer: PartyForge, Verified: true},
			forge.ErrUntrustedCommit,
		},
		{
			"the forge as author",
			Origin{Author: PartyForge, Committer: PartyForge, Verified: true},
			forge.ErrUntrustedCommit,
		},
		{
			"another committer",
			Origin{Author: PartyConnection, Committer: PartyOther, Verified: true},
			forge.ErrUntrustedCommit,
		},
		{
			"no account as committer",
			Origin{Author: PartyConnection, Committer: PartyNobody, Verified: true},
			forge.ErrUntrustedCommit,
		},
		{
			"a commit that the forge does not verify",
			Origin{Author: PartyConnection, Committer: PartyForge, Verified: false},
			forge.ErrUntrustedCommit,
		},
		{
			"a person who pushes it all",
			Origin{Author: PartyOther, Committer: PartyOther, Verified: false},
			forge.ErrUntrustedCommit,
		},
	}

	for i, tt := range tests {
		trailer := jobTrailer("j-" + strconv.Itoa(30+i))

		files := map[string][]byte{"f" + strconv.Itoa(i) + ".txt": []byte("x\n")}
		pushed := pushAs(t, h, files, "Update the stacks\n\n"+trailer, tt.origin)

		got, findErr := h.Forge.FindCommit(ctx, h.Repo, "main", trailer, head.Time)
		if problem := judgeFind(got, findErr, pushed.ID, tt.want); problem != "" {
			t.Errorf("%s: %s", tt.name, problem)
		}
	}
}

// judgeFind compares the answer of FindCommit with the answer that a case
// wants: the commit wantID when wantErr is nil, and else no commit and wantErr.
// It returns the problem as a text, or an empty text.
func judgeFind(got forge.Commit, err error, wantID string, wantErr error) string {
	switch {
	case wantErr == nil && err != nil:
		return "FindCommit = " + err.Error() + ", want the commit " + wantID
	case wantErr == nil && got.ID != wantID:
		return "FindCommit gave the commit " + got.ID + ", want " + wantID
	case wantErr != nil && !errors.Is(err, wantErr):
		return "FindCommit gave the error " + errText(err) + ", want " + wantErr.Error()
	case wantErr != nil && got.ID != "":
		return "FindCommit gave the commit " + got.ID + " with an error"
	default:
		return ""
	}
}

// notFoundOrInvalid returns the sentinel that err matches, forge.ErrInvalid or
// else forge.ErrNotFound, so that a case accepts either for a trailer that the
// commit does not have.
func notFoundOrInvalid(err error) error {
	if errors.Is(err, forge.ErrInvalid) {
		return forge.ErrInvalid
	}

	return forge.ErrNotFound
}

// errText returns the text of err, or "none" for nil.
func errText(err error) string {
	if err == nil {
		return "none"
	}

	return err.Error()
}

// pushAs pushes a commit with the origin and fails the case on an error.
func pushAs(t *testing.T, h Harness, files map[string][]byte, message string, origin Origin) forge.Commit {
	t.Helper()

	c, err := h.Fixture.PushAs(t.Context(), "main", files, message, origin)
	if err != nil {
		t.Fatalf("PushAs = %v", err)
	}

	return c
}

// caseFindCommitPushedTrailerBeforeOwn checks that a pushed commit with the
// trailer, newer than the commit of the connection, is not skipped in silence.
func caseFindCommitPushedTrailerBeforeOwn(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)
	trailer := jobTrailer("j-50")

	pushAs(t, h, map[string][]byte{"a.txt": []byte("1\n")}, "Update the stacks\n\n"+trailer, ownCommit())
	pushAs(
		t, h, map[string][]byte{"b.txt": []byte("2\n")}, "Cherry-pick\n\n"+trailer,
		Origin{Author: PartyOther, Committer: PartyOther, Verified: false},
	)

	got, err := h.Forge.FindCommit(ctx, h.Repo, "main", trailer, head.Time)
	if !errors.Is(err, forge.ErrUntrustedCommit) || got.ID != "" {
		t.Fatalf("FindCommit = %+v, %v; want ErrUntrustedCommit", got, err)
	}
}

// caseFindCommitTrailerIsExact checks that the trailer line must equal the
// line of the commit: no prefix, no suffix, no other value of the same key.
func caseFindCommitTrailerIsExact(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	req := commitOn("main", head.ID, "j-40", writeFile("a.txt", "1\n"))
	req.Message = "Update the stacks\n\n" + jobTrailer("j-40") + "\nDustpan-Phase: write-back-2"

	got, err := h.Forge.Commit(ctx, h.Repo, req)
	if err != nil {
		t.Fatalf("Commit = %v", err)
	}

	tests := []struct {
		trailer string
		want    bool
	}{
		{jobTrailer("j-40"), true},
		{"Dustpan-Phase: write-back-2", true},
		{jobTrailer("j-4"), false},
		{jobTrailer("j-400"), false},
		{jobTrailer("j-40 "), false},
		{jobTrailer("J-40"), false},
		{"Dustpan-Phase: write-back", false},
		{"Dustpan-Phase: write-back-20", false},
		{"Dustpan-Phase: write-back-1", false},
	}

	for _, tt := range tests {
		found, findErr := h.Forge.FindCommit(ctx, h.Repo, "main", tt.trailer, head.Time)

		var wantErr error
		if !tt.want {
			// A trailer that the commit does not have is not found. A trailer
			// with a space at its end is not valid, and no call is made.
			wantErr = notFoundOrInvalid(findErr)
		}

		if problem := judgeFind(found, findErr, got.ID, wantErr); problem != "" {
			t.Errorf("trailer %q: %s", tt.trailer, problem)
		}
	}
}

// caseCommitMovedHeadEntry moves the head of the branch to a commit that has a
// symlink, a submodule or a directory at a path of the changes, in each way
// that the base and the head can differ. Each is a conflict, and the commit
// is not made.
func caseCommitMovedHeadEntry(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	n := 0

	for _, kind := range []EntryKind{EntrySymlink, EntrySubmodule, EntryDirectory} {
		scenarios := []struct {
			name string
			// setup returns the base, after it moved the head.
			setup func(path string) (base forge.Commit)
		}{
			{"the head has the entry and the base has no path", func(path string) forge.Commit {
				base := mainHead(t, h)
				mustEntry(t, h, path, kind, "one")

				return base
			}},
			{"the head has another entry than the base", func(path string) forge.Commit {
				base := mustEntry(t, h, path, kind, "one")
				mustEntry(t, h, path, kind, "two")

				return base
			}},
			{"the head has the same entry as the base", func(path string) forge.Commit {
				base := mustEntry(t, h, path, kind, "one")
				push(t, h, map[string][]byte{"other-" + path + ".txt": []byte("other\n")})

				return base
			}},
			{"the head has the entry and the base has a file", func(path string) forge.Commit {
				base := push(t, h, map[string][]byte{path: []byte("a: 1\n")})
				mustEntry(t, h, path, kind, "one")

				return base
			}},
			{"the head has a file and the base has the entry", func(path string) forge.Commit {
				base := mustEntry(t, h, path, kind, "one")
				push(t, h, map[string][]byte{path: []byte("a: 2\n")})

				return base
			}},
		}

		for _, sc := range scenarios {
			n++
			path := "stacks/s" + strconv.Itoa(n) + ".yaml"
			base := sc.setup(path)
			moved := mainHead(t, h)

			_, err := h.Forge.Commit(
				ctx,
				h.Repo,
				commitOn("main", base.ID, "j-"+strconv.Itoa(60+n), writeFile(path, "mine\n")),
			)
			if !errors.Is(err, forge.ErrConflict) {
				t.Errorf("%s, %s: Commit = %v, want ErrConflict", kind, sc.name, err)
			}

			if now := mainHead(t, h); now.ID != moved.ID {
				t.Errorf(
					"%s, %s: head of main = %q, want %q: the commit overwrote the change",
					kind,
					sc.name,
					now.ID,
					moved.ID,
				)
			}
		}
	}
}

func mustEntry(t *testing.T, h Harness, path string, kind EntryKind, variant string) forge.Commit {
	t.Helper()

	c, err := h.Fixture.PushEntry(t.Context(), "main", path, kind, variant)
	if err != nil {
		t.Fatalf("PushEntry of a %s = %v", kind, err)
	}

	return c
}
