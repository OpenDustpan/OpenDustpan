package unitimporttest

import (
	"fmt"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// uploadDir is the unit directory of the fixtures: the hex of a digest.
const uploadDir = "0123456789abcdef0123456789abcdef"

// uploadCommit returns the commit row of the version seq of the import imp-1,
// for the attempt attempt.
func uploadCommit(dir string, seq int, attempt string) unitimport.UploadCommit {
	return unitimport.UploadCommit{
		ImportID: "imp-1", UnitDir: dir, Sequence: seq,
		Facts: fmt.Sprintf("%010d-%s.json", seq, attempt), CommittedAt: at(),
	}
}

func mustCommit(t *testing.T, h Harness, fence namespace.Fence, c unitimport.UploadCommit) {
	t.Helper()

	if err := h.Uploads.CommitUploadVersion(t.Context(), fence, c); err != nil {
		t.Fatalf("commit upload version %d: %v", c.Sequence, err)
	}
}

func mustUploadCommits(t *testing.T, h Harness, importID, dir string) []unitimport.UploadCommit {
	t.Helper()

	rows, err := h.Uploads.UploadCommits(t.Context(), "acme", importID, dir)
	if err != nil {
		t.Fatalf("upload commits of %s: %v", importID, err)
	}

	return rows
}

func testUploadCommit(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	if rows := mustUploadCommits(t, h, "imp-1", uploadDir); len(rows) != 0 {
		t.Fatalf("upload commits before a commit = %+v, want none", rows)
	}

	mustCommit(t, h, fence, uploadCommit(uploadDir, 2, "bbb"))
	mustCommit(t, h, fence, uploadCommit(uploadDir, 1, "aaa"))

	rows := mustUploadCommits(t, h, "imp-1", uploadDir)
	if len(rows) != 2 || rows[0] != uploadCommit(uploadDir, 1, "aaa") || rows[1] != uploadCommit(uploadDir, 2, "bbb") {
		t.Fatalf("upload commits = %+v, want the two rows in the order of the sequences", rows)
	}
}

func testUploadCommitRepeated(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	mustCommit(t, h, fence, uploadCommit(uploadDir, 1, "aaa"))

	// Another attempt of the same version, and a retry of the same attempt.
	for _, attempt := range []string{"bbb", "aaa"} {
		err := h.Uploads.CommitUploadVersion(t.Context(), fence, uploadCommit(uploadDir, 1, attempt))
		mustIs(t, "second commit of attempt "+attempt, err, unitimport.ErrUploadExists)
	}

	rows := mustUploadCommits(t, h, "imp-1", uploadDir)
	if len(rows) != 1 || rows[0].Facts != "0000000001-aaa.json" {
		t.Fatalf("upload commits = %+v, want the row of the first attempt only", rows)
	}
}

func testUploadCommitRace(t *testing.T, h Harness) {
	t.Helper()

	const attempts = 8

	fence := setup(t, h)
	errs := make([]error, attempts)

	var wg sync.WaitGroup

	for n := range attempts {
		wg.Go(func() {
			errs[n] = h.Uploads.CommitUploadVersion(
				t.Context(),
				fence,
				uploadCommit(uploadDir, 1, fmt.Sprintf("a%d", n)),
			)
		})
	}

	wg.Wait()

	winner := -1

	for n, err := range errs {
		switch {
		case err == nil && winner < 0:
			winner = n
		case err == nil:
			t.Errorf("attempts %d and %d both committed version 1", winner, n)
		default:
			mustIs(t, fmt.Sprintf("attempt %d", n), err, unitimport.ErrUploadExists)
		}
	}

	rows := mustUploadCommits(t, h, "imp-1", uploadDir)
	if winner < 0 || len(rows) != 1 || rows[0].Facts != fmt.Sprintf("0000000001-a%d.json", winner) {
		t.Fatalf("winner = %d, upload commits = %+v, want one row of the winner", winner, rows)
	}
}

func testUploadCommitIsolation(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	_, err := h.Store.Create(t.Context(), fence, newImport("acme", "imp-2"), entry("acme", "import:run"))
	if err != nil {
		t.Fatalf("create import: %v", err)
	}

	other := "fedcba9876543210fedcba9876543210"
	second := uploadCommit(uploadDir, 1, "aaa")
	second.ImportID = "imp-2"

	// One sequence in two units and in two imports is four versions.
	mustCommit(t, h, fence, uploadCommit(uploadDir, 1, "aaa"))
	mustCommit(t, h, fence, uploadCommit(other, 1, "aaa"))
	mustCommit(t, h, fence, second)

	for _, tc := range []struct{ importID, dir string }{{"imp-1", uploadDir}, {"imp-1", other}, {"imp-2", uploadDir}} {
		rows := mustUploadCommits(t, h, tc.importID, tc.dir)
		if len(rows) != 1 || rows[0].ImportID != tc.importID || rows[0].UnitDir != tc.dir {
			t.Errorf("upload commits of %s/%s = %+v, want its one row", tc.importID, tc.dir, rows)
		}
	}
}

func testUploadCommitInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	cases := map[string]unitimport.UploadCommit{
		"no import":       changedCommit(func(c *unitimport.UploadCommit) { c.ImportID = "" }),
		"empty directory": changedCommit(func(c *unitimport.UploadCommit) { c.UnitDir = "" }),
		"directory path":  changedCommit(func(c *unitimport.UploadCommit) { c.UnitDir = "../" + uploadDir[3:] }),
		"upper case directory": changedCommit(
			func(c *unitimport.UploadCommit) { c.UnitDir = "0123456789ABCDEF0123456789ABCDEF" },
		),
		"sequence zero":     changedCommit(func(c *unitimport.UploadCommit) { c.Sequence = 0 }),
		"negative sequence": changedCommit(func(c *unitimport.UploadCommit) { c.Sequence = -1 }),
		"huge sequence": changedCommit(
			func(c *unitimport.UploadCommit) { c.Sequence = unitimport.MaxUploadSequence + 1 },
		),
		"no facts name":        changedCommit(func(c *unitimport.UploadCommit) { c.Facts = "" }),
		"facts path":           changedCommit(func(c *unitimport.UploadCommit) { c.Facts = "../0000000001-aaa.json" }),
		"facts in a directory": changedCommit(func(c *unitimport.UploadCommit) { c.Facts = "x/0000000001-aaa.json" }),
		"old facts name":       changedCommit(func(c *unitimport.UploadCommit) { c.Facts = "0000000001.json" }),
		"facts of a body":      changedCommit(func(c *unitimport.UploadCommit) { c.Facts = "0000000001-aaa.bin" }),
	}
	cases["no time"] = changedCommit(func(c *unitimport.UploadCommit) { *c = zeroTime(*c) })

	for name, c := range cases {
		mustIs(t, name, h.Uploads.CommitUploadVersion(t.Context(), fence, c), unitimport.ErrInvalid)
	}

	if rows := mustUploadCommits(t, h, "imp-1", uploadDir); len(rows) != 0 {
		t.Errorf("upload commits after the refused commits = %+v, want none", rows)
	}
}

func changedCommit(fn func(c *unitimport.UploadCommit)) unitimport.UploadCommit {
	c := uploadCommit(uploadDir, 1, "aaa")
	fn(&c)

	return c
}

func zeroTime(c unitimport.UploadCommit) unitimport.UploadCommit {
	var none unitimport.UploadCommit

	c.CommittedAt = none.CommittedAt

	return c
}

func testUploadCommitImport(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	unknown := uploadCommit(uploadDir, 1, "aaa")
	unknown.ImportID = "imp-none"
	mustIs(t, "unknown import", h.Uploads.CommitUploadVersion(t.Context(), fence, unknown), unitimport.ErrNotFound)

	_, err := h.Uploads.UploadCommits(t.Context(), "acme", "imp-none", uploadDir)
	mustIs(t, "read of an unknown import", err, unitimport.ErrNotFound)

	mustCommit(t, h, fence, uploadCommit(uploadDir, 1, "aaa"))

	cancel := unitimport.Move{From: unitimport.StatePlanning, To: unitimport.StateCanceled, Reason: "stop"}
	if err = h.Store.Transition(t.Context(), fence, "imp-1", cancel, entry("acme", "import:cancel")); err != nil {
		t.Fatalf("cancel import: %v", err)
	}

	err = h.Uploads.CommitUploadVersion(t.Context(), fence, uploadCommit(uploadDir, 2, "aaa"))
	mustIs(t, "commit to an ended import", err, unitimport.ErrTransition)

	if rows := mustUploadCommits(t, h, "imp-1", uploadDir); len(rows) != 1 {
		t.Errorf("upload commits of an ended import = %+v, want the one row from before the end", rows)
	}
}

func testUploadCommitFenced(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	stale := namespace.Fence{Namespace: "acme", FailoverVersion: fence.FailoverVersion + 1}

	mustIs(t, "stale fence", h.Uploads.CommitUploadVersion(t.Context(), stale, uploadCommit(uploadDir, 1, "aaa")),
		namespace.ErrFenced)

	missing := namespace.Fence{Namespace: "nowhere", FailoverVersion: fence.FailoverVersion}

	mustIs(
		t,
		"unknown namespace",
		h.Uploads.CommitUploadVersion(t.Context(), missing, uploadCommit(uploadDir, 1, "aaa")),
		namespace.ErrNotFound,
	)

	if rows := mustUploadCommits(t, h, "imp-1", uploadDir); len(rows) != 0 {
		t.Errorf("upload commits after the refused commits = %+v, want none", rows)
	}
}
