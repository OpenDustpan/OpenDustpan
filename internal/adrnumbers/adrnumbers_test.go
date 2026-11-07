package adrnumbers_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/adrnumbers"
)

// TestCheckFixtures runs Check on small trees under testdata. Each case
// names the findings that Check must return, in order, or an error.
func TestCheckFixtures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		root    string
		exempt  []string
		want    []string
		wantErr bool
	}{
		{
			name:    "clean tree",
			root:    "testdata/clean",
			exempt:  nil,
			want:    nil,
			wantErr: false,
		},
		{
			name:   "two ADR files with one number",
			root:   "testdata/duplicate-adr",
			exempt: nil,
			want: []string{
				"docs/adr: number 0300 is used by 0300-first-decision.md, 0300-second-decision.md",
			},
			wantErr: false,
		},
		{
			name:   "two RFC files with one number",
			root:   "testdata/duplicate-rfc",
			exempt: nil,
			want: []string{
				"docs/rfc: number 0007 is used by 0007-first.md, 0007-second.md",
			},
			wantErr: false,
		},
		{
			name:   "ADR cited without a file",
			root:   "testdata/dangling-adr",
			exempt: nil,
			want: []string{
				"docs/guides/setup.md:3: cites ADR 999, which has no file in docs/adr",
			},
			wantErr: false,
		},
		{
			name:    "exempt file is not checked for citations",
			root:    "testdata/dangling-adr",
			exempt:  []string{"docs/guides/setup.md"},
			want:    nil,
			wantErr: false,
		},
		{
			name:   "docs/adr missing",
			root:   "testdata/no-adr-dir",
			exempt: nil,
			// Check fails rather than report a clean tree it did not read.
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := adrnumbers.Check(tt.root, tt.exempt...)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Check(%q) returned no error, findings %q", tt.root, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Check(%q) returned error: %v", tt.root, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Check(%q) findings:\n got %q\nwant %q", tt.root, got, tt.want)
			}
		})
	}
}

// TestCheckRealTree runs Check on the repository. Every finding is a failure:
// a duplicate number, or an ADR cited without a file, must be fixed in the
// docs or in the number that cites it. Only the frozen ADRs are exempt.
func TestCheckRealTree(t *testing.T) {
	t.Parallel()

	// The ADR files below cite numbers with no file. Each one was accepted
	// before the edit rule, so its text cannot change.
	frozenADRs := []string{
		// frozen ADR, accepted before the edit rule
		"docs/adr/0012-a-backend-for-many-iac-tools.md",
		// frozen ADR, accepted before the edit rule
		"docs/adr/0013-separate-tool-models-shared-read-model-and-access.md",
	}
	got, err := adrnumbers.Check("../..", frozenADRs...)
	if err != nil {
		t.Fatalf("Check of the repository returned error: %v", err)
	}
	for _, finding := range got {
		t.Error(finding)
	}
}
