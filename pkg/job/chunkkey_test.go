package job_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

func TestChunkBlobKey(t *testing.T) {
	t.Parallel()

	const (
		id      = job.ID("job-abc")
		version = "jlv-one"
		base    = "job/job-abc/log/jlv-one/00000003"
	)

	t.Run("the key of an attempt", func(t *testing.T) {
		t.Parallel()

		got, err := job.ChunkBlobKey(id, version, 3, base+"-jla-abc123")
		if err != nil || got.String() != base+"-jla-abc123" {
			t.Fatalf("ChunkBlobKey = %q, %v; want %q", got, err, base+"-jla-abc123")
		}
	})

	invalid := []struct{ name, stored string }{
		{"no stored key", ""},
		{"the key of another chunk", "job/job-abc/log/jlv-one/00000004-jla-abc"},
		{"the key of another version", "job/job-abc/log/jlv-two/00000003-jla-abc"},
		{"the key of another job", "job/job-xyz/log/jlv-one/00000003-jla-abc"},
		{"the shared prefix with no attempt", base},
		{"an empty attempt", base + "-"},
		{"an attempt of another prefix", base + "-xyz-abc"},
		{"an attempt with a slash", base + "-jla-a/b"},
		{"an attempt with a dot segment", base + "-jla-.."},
		{"an attempt with upper case", base + "-jla-ABC"},
		{"an attempt that is too long", base + "-jla-" + strings.Repeat("a", 80)},
		{"a text that is no key", "not a key"},
	}

	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := job.ChunkBlobKey(id, version, 3, tt.stored)
			if !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("ChunkBlobKey(%q) = %q, %v; want ErrInvalid", tt.stored, got, err)
			}
		})
	}
}

func TestLogChunkAttemptKey(t *testing.T) {
	t.Parallel()

	got, err := job.LogChunkAttemptKey("job-abc", "jlv-one", 7, "jla-xyz")
	if err != nil || got.String() != "job/job-abc/log/jlv-one/00000007-jla-xyz" {
		t.Fatalf("LogChunkAttemptKey = %q, %v", got, err)
	}

	for _, attempt := range []string{"", "jla-", "xyz", "jla-A", "jla-a/b", "jla-a b"} {
		if _, err = job.LogChunkAttemptKey("job-abc", "jlv-one", 7, attempt); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("LogChunkAttemptKey(%q) = %v, want ErrInvalid", attempt, err)
		}
	}
}
