package forgetest

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

// push commits files on main through the fixture and fails the test on an
// error.
func push(t *testing.T, h Harness, files map[string][]byte) forge.Commit {
	t.Helper()

	c, err := h.Fixture.Push(t.Context(), "main", files)
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	return c
}

// missingCommit returns a full commit ID that no repository of the suite has.
func missingCommit() string { return strings.Repeat("0", 40) }

func caseMissingRepo(t *testing.T, h Harness) {
	t.Helper()

	listed := h.Forge.Capabilities()

	for _, m := range methods(h.EventSecret) {
		if m.need == forge.CapEvents || !listed.Has(m.need) {
			continue
		}

		if err := m.call(t.Context(), h.Forge, "acme/no-such-repository"); !errors.Is(err, forge.ErrNotFound) {
			t.Errorf("%s of a missing repository = %v, want ErrNotFound", m.name, err)
		}
	}
}

func caseHeadBranch(t *testing.T, h Harness) {
	t.Helper()

	c := push(t, h, map[string][]byte{"a.txt": []byte("a")})

	got, err := h.Forge.Head(t.Context(), h.Repo, "main")
	if err != nil {
		t.Fatalf("Head = %v", err)
	}

	if got.ID != c.ID {
		t.Fatalf("Head = %q, want %q", got.ID, c.ID)
	}

	if got.Time.IsZero() || !got.Time.Truncate(time.Second).Equal(c.Time.Truncate(time.Second)) {
		t.Fatalf("Head time = %v, want %v", got.Time, c.Time)
	}
}

func caseHeadTag(t *testing.T, h Harness) {
	t.Helper()

	tagged := push(t, h, map[string][]byte{"a.txt": []byte("a")})
	if err := h.Fixture.Tag(t.Context(), "v1", tagged.ID); err != nil {
		t.Fatalf("tag: %v", err)
	}

	// The branch moves on, and the tag stays.
	push(t, h, map[string][]byte{"a.txt": []byte("b")})

	got, err := h.Forge.Head(t.Context(), h.Repo, "v1")
	if err != nil {
		t.Fatalf("Head = %v", err)
	}

	if got.ID != tagged.ID {
		t.Fatalf("Head of the tag = %q, want %q", got.ID, tagged.ID)
	}
}

func caseHeadMissing(t *testing.T, h Harness) {
	t.Helper()

	if _, err := h.Forge.Head(t.Context(), h.Repo, "no-such-ref"); !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("Head of a missing ref = %v, want ErrNotFound", err)
	}
}

func caseReadFile(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	first := push(t, h, map[string][]byte{"stacks/prod.yaml": []byte("a: 1\n"), "other.txt": []byte("same\n")})
	second := push(t, h, map[string][]byte{"stacks/prod.yaml": []byte("a: 2\n")})

	old, err := h.Forge.ReadFile(ctx, h.Repo, first.ID, "stacks/prod.yaml", 1<<20)
	if err != nil {
		t.Fatalf("ReadFile at the first commit = %v", err)
	}

	if !bytes.Equal(old.Content, []byte("a: 1\n")) || old.Path != "stacks/prod.yaml" || old.BlobID == "" {
		t.Fatalf("file at the first commit = %+v", old)
	}

	// The commit decides the content, not the head of the branch.
	next, err := h.Forge.ReadFile(ctx, h.Repo, second.ID, "stacks/prod.yaml", 1<<20)
	if err != nil {
		t.Fatalf("ReadFile at the second commit = %v", err)
	}

	if !bytes.Equal(next.Content, []byte("a: 2\n")) || next.BlobID == old.BlobID {
		t.Fatalf("file at the second commit = %+v, first blob %q", next, old.BlobID)
	}

	// The same content has the same blob ID at two commits.
	a, errA := h.Forge.ReadFile(ctx, h.Repo, first.ID, "other.txt", 1<<20)
	b, errB := h.Forge.ReadFile(ctx, h.Repo, second.ID, "other.txt", 1<<20)

	if errA != nil || errB != nil || a.BlobID == "" || a.BlobID != b.BlobID {
		t.Fatalf("blob IDs of an unchanged file: %q, %q (errors %v, %v)", a.BlobID, b.BlobID, errA, errB)
	}
}

func caseReadMissingFile(t *testing.T, h Harness) {
	t.Helper()

	c := push(t, h, map[string][]byte{"stacks/prod.yaml": []byte("a: 1\n")})

	// A missing file, and a directory: there is no file at the path.
	for _, p := range []string{"none.txt", "stacks/none.yaml", "stacks"} {
		if _, err := h.Forge.ReadFile(t.Context(), h.Repo, c.ID, p, 1<<20); !errors.Is(err, forge.ErrNotFound) {
			t.Errorf("ReadFile of %q = %v, want ErrNotFound", p, err)
		}
	}
}

func caseReadMissingCommit(t *testing.T, h Harness) {
	t.Helper()

	push(t, h, map[string][]byte{"a.txt": []byte("a")})

	_, err := h.Forge.ReadFile(t.Context(), h.Repo, missingCommit(), "a.txt", 1<<20)
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("ReadFile at a missing commit = %v, want ErrNotFound", err)
	}
}

func caseReadTooLarge(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	large := bytes.Repeat([]byte("x"), 1<<20+1)
	c := push(t, h, map[string][]byte{"small.bin": bytes.Repeat([]byte("s"), 100), "large.bin": large})

	tests := []struct {
		name  string
		path  string
		limit int64
		size  int
		large bool
	}{
		{"a small file at its size", "small.bin", 100, 100, false},
		{"a small file one byte over", "small.bin", 99, 0, true},
		{"a file above one MiB at the limit", "large.bin", int64(len(large)), len(large), false},
		{"a file above one MiB one byte over", "large.bin", int64(len(large)) - 1, 0, true},
	}

	for _, tc := range tests {
		got, err := h.Forge.ReadFile(ctx, h.Repo, c.ID, tc.path, tc.limit)

		if tc.large {
			if !errors.Is(err, forge.ErrTooLarge) {
				t.Errorf("%s: ReadFile = %v, want ErrTooLarge", tc.name, err)
			}

			continue
		}

		if err != nil || len(got.Content) != tc.size {
			t.Errorf("%s: ReadFile = %d bytes, %v; want %d bytes", tc.name, len(got.Content), err, tc.size)
		}
	}
}

func caseReadBadPath(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	c := push(t, h, map[string][]byte{"a.txt": []byte("a")})

	before, err := h.Fixture.Calls(ctx)
	if err != nil {
		t.Fatalf("calls: %v", err)
	}

	for _, p := range []string{
		"", ".", "..", "../a.txt", "stacks/../../a.txt", "/a.txt", ".git/config", "a/.GIT/b", `a\b`, "a//b", "a/./b",
		"a/", "a\x00b", "a\nb",
	} {
		if _, readErr := h.Forge.ReadFile(ctx, h.Repo, c.ID, p, 1<<20); !errors.Is(readErr, forge.ErrInvalid) {
			t.Errorf("ReadFile of %q = %v, want ErrInvalid", p, readErr)
		}
	}

	for _, id := range []string{"", "main", "ABCDEF0123456789abcdef0123456789abcdef01", c.ID[:10]} {
		if _, readErr := h.Forge.ReadFile(ctx, h.Repo, id, "a.txt", 1<<20); !errors.Is(readErr, forge.ErrInvalid) {
			t.Errorf("ReadFile at %q = %v, want ErrInvalid", id, readErr)
		}
	}

	after, err := h.Fixture.Calls(ctx)
	if err != nil {
		t.Fatalf("calls: %v", err)
	}

	if after != before {
		t.Fatalf("an invalid read made %d calls to the forge, want none", after-before)
	}
}
