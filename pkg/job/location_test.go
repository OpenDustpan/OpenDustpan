package job_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

func TestLocationValidate(t *testing.T) {
	t.Parallel()

	good := job.Location{
		Connection: "frg-1", Repository: "acme/infra", Ref: "refs/heads/main", Directory: "envs/prod",
		Revision: "0123abcd",
	}

	tests := []struct {
		name   string
		mutate func(*job.Location)
		ok     bool
	}{
		{"a full location", func(*job.Location) {}, true},
		{"no connection", func(l *job.Location) { l.Connection = "" }, true},
		{"no directory", func(l *job.Location) { l.Directory = "" }, true},
		{"no revision", func(l *job.Location) { l.Revision = "" }, true},
		{"no repository", func(l *job.Location) { l.Repository = "" }, false},
		{"no ref", func(l *job.Location) { l.Ref = "" }, false},
		{"a repository like an option", func(l *job.Location) { l.Repository = "--upload-pack=x" }, false},
		{"a ref like an option", func(l *job.Location) { l.Ref = "-x" }, false},
		{"a connection like an option", func(l *job.Location) { l.Connection = "-x" }, false},
		{"a space in the ref", func(l *job.Location) { l.Ref = "a b" }, false},
		{"a line break in the repository", func(l *job.Location) { l.Repository = "a\nb" }, false},
		{"a NUL byte in the ref", func(l *job.Location) { l.Ref = "a\x00b" }, false},
		{"a backslash in the repository", func(l *job.Location) { l.Repository = `a\b` }, false},
		{"dots in the ref", func(l *job.Location) { l.Ref = "a..b" }, false},
		{"a long repository", func(l *job.Location) { l.Repository = strings.Repeat("a", 256) }, false},
		{"an absolute directory", func(l *job.Location) { l.Directory = "/etc" }, false},
		{"a directory above the checkout", func(l *job.Location) { l.Directory = "../x" }, false},
		{"a directory that is two dots", func(l *job.Location) { l.Directory = ".." }, false},
		{"a directory that is a dot", func(l *job.Location) { l.Directory = "." }, false},
		{"a directory that leaves and enters", func(l *job.Location) { l.Directory = "a/../../b" }, false},
		{"a directory with a double slash", func(l *job.Location) { l.Directory = "a//b" }, false},
		{"a directory with a trailing slash", func(l *job.Location) { l.Directory = "a/" }, false},
		{"a directory with a backslash", func(l *job.Location) { l.Directory = `a\b` }, false},
		{"a directory with a control character", func(l *job.Location) { l.Directory = "a\tb" }, false},
		{"a long directory", func(l *job.Location) { l.Directory = strings.Repeat("a", 513) }, false},
		{"a revision that is a name", func(l *job.Location) { l.Revision = "main" }, false},
		{"a short revision", func(l *job.Location) { l.Revision = "abc12" }, false},
		{"an upper case revision", func(l *job.Location) { l.Revision = "ABCDEF0" }, false},
		{"a long revision", func(l *job.Location) { l.Revision = strings.Repeat("a", 65) }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := good
			tt.mutate(&l)

			err := l.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate = %v, want nil", err)
			}

			if !tt.ok && !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("Validate = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestSourceKinds(t *testing.T) {
	t.Parallel()

	for _, k := range job.SourceKinds() {
		got, err := job.ParseSourceKind(string(k))
		if err != nil || got != k || !k.Valid() {
			t.Errorf("ParseSourceKind(%q) = %q, %v; want the kind", k, got, err)
		}
	}

	for _, bad := range []string{"", "API", "cron", "api ", "schedule\n"} {
		if _, err := job.ParseSourceKind(bad); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("ParseSourceKind(%q) = %v, want ErrInvalid", bad, err)
		}
	}
}
