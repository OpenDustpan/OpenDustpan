package backup_test

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestCleanSubject sends hostile subjects to the cleaner alone, as if every
// other layer had failed (decision 2e).
func TestCleanSubject(t *testing.T) {
	t.Parallel()

	long := "dev:" + strings.Repeat("a", 10_000)
	multi := "dev:" + strings.Repeat("é", 200) // 2 bytes each, 404 bytes

	tests := []struct {
		name    string
		subject string
		want    string
		wantCut bool
	}{
		{name: "plain", subject: "dev:admin", want: "dev:admin"},
		{name: "empty", subject: "", want: ""},
		{name: "CR and LF", subject: "dev:a\r\nb", want: "dev:ab"},
		{name: "NUL", subject: "dev:\x00a", want: "dev:a"},
		{name: "other control characters", subject: "dev:\x07\x08\x7f\u0085a", want: "dev:a"},
		{name: "escape sequence", subject: "dev:\x1b[31mred", want: "dev:[31mred"},
		{name: "bidirectional override", subject: "dev:‮admin", want: "dev:admin"},
		{name: "line and paragraph separators", subject: "dev: a b", want: "dev:ab"},
		{name: "invalid UTF-8", subject: "dev:\xffa\xc3", want: "dev:a"},
		{name: "10 000 bytes", subject: long, want: long[:backup.MaxReportedSubjectBytes], wantCut: true},
		{
			name:    "cut at a character boundary",
			subject: multi,
			want:    multi[:backup.MaxReportedSubjectBytes-1],
			wantCut: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, cut := backup.CleanSubject(tc.subject)
			if got != tc.want || cut != tc.wantCut {
				t.Fatalf("CleanSubject(%q) = %q, %v, want %q, %v", tc.subject, got, cut, tc.want, tc.wantCut)
			}

			if len(got) > backup.MaxReportedSubjectBytes || !utf8.ValidString(got) {
				t.Fatalf("CleanSubject(%q) = %q: too long or not UTF-8", tc.subject, got)
			}

			for _, r := range got {
				if unicode.IsControl(r) {
					t.Fatalf("CleanSubject(%q) = %q holds control character %U", tc.subject, got, r)
				}
			}
		})
	}
}
