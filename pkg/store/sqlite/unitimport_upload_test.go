package sqlite_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/internal/unitimport/unitimporttest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestUploadCommitStatementPredicates runs the commit statement alone, as if
// the fence check and the checks of the caller had failed, and proves that
// each predicate of the statement stops a commit by itself. The first case
// is the control: the same values commit, so a refusal below comes from the
// one value that the case changes.
func TestUploadCommitStatementPredicates(t *testing.T) {
	t.Parallel()

	const (
		deadline = 30 * time.Minute
		skew     = 2 * time.Minute
	)

	cases := []struct {
		name   string
		change func(p *sqlite.UploadStatement)
		want   int64
	}{
		{"the control commits", func(*sqlite.UploadStatement) {}, 1},
		{"a stale failover version", func(p *sqlite.UploadStatement) { p.FailoverVersion++ }, 0},
		{"an older failover version", func(p *sqlite.UploadStatement) { p.FailoverVersion-- }, 0},
		{"another namespace", func(p *sqlite.UploadStatement) { p.Namespace = "elsewhere" }, 0},
		{"an unknown import", func(p *sqlite.UploadStatement) { p.ImportID = "imp-none" }, 0},
		{"an attempt past the deadline", func(p *sqlite.UploadStatement) {
			p.StartedAtMs = time.Now().Add(-deadline - time.Minute).UnixMilli()
		}, 0},
		{"an attempt that starts beyond the skew", func(p *sqlite.UploadStatement) {
			p.StartedAtMs = time.Now().Add(skew + time.Minute).UnixMilli()
		}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := open(t)
			fence := contextNamespace(t, s, "acme")
			imports := s.Imports()

			imp := unitimporttest.UploadImportFixture("acme", "imp-1")
			_, err := imports.Create(t.Context(), fence, imp, unitimporttest.EntryFixture("acme", "import:run"))
			if err != nil {
				t.Fatalf("create import: %v", err)
			}

			p := sqlite.UploadStatement{
				Namespace: "acme", FailoverVersion: fence.FailoverVersion, ImportID: "imp-1",
				UnitDir: "0123456789abcdef0123456789abcdef", Facts: "0000000001-aaa.json", Sequence: 1,
				StartedAtMs: time.Now().UnixMilli(), DeadlineMs: deadline.Milliseconds(), SkewMs: skew.Milliseconds(),
			}
			tc.change(&p)

			got, err := imports.RunUploadStatement(t.Context(), p)
			if err != nil || got != tc.want {
				t.Fatalf("rows = %d, %v, want %d", got, err, tc.want)
			}
		})
	}
}

// TestUploadCommitStatementEndedImport proves that the state predicate stops a
// commit for an import that ended, with the statement run alone.
func TestUploadCommitStatementEndedImport(t *testing.T) {
	t.Parallel()

	s := open(t)
	fence := contextNamespace(t, s, "acme")
	imports := s.Imports()

	imp := unitimporttest.UploadImportFixture("acme", "imp-1")
	_, err := imports.Create(t.Context(), fence, imp, unitimporttest.EntryFixture("acme", "import:run"))
	if err != nil {
		t.Fatalf("create import: %v", err)
	}

	p := sqlite.UploadStatement{
		Namespace: "acme", FailoverVersion: fence.FailoverVersion, ImportID: "imp-1",
		UnitDir: "0123456789abcdef0123456789abcdef", Facts: "0000000001-aaa.json", Sequence: 1,
		StartedAtMs: time.Now().UnixMilli(), DeadlineMs: (30 * time.Minute).Milliseconds(),
		SkewMs: (2 * time.Minute).Milliseconds(),
	}

	cancel := unitimport.Move{From: unitimport.StatePlanning, To: unitimport.StateCanceled, Reason: "stop"}
	err = imports.Transition(t.Context(), fence, "imp-1", cancel, unitimporttest.EntryFixture("acme", "import:cancel"))
	if err != nil {
		t.Fatalf("cancel import: %v", err)
	}

	if got, err := imports.RunUploadStatement(t.Context(), p); err != nil || got != 0 {
		t.Fatalf("rows = %d, %v, want 0 for an ended import", got, err)
	}
}
