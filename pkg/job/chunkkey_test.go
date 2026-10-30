package job_test

import (
	"errors"
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

	tests := []struct {
		name    string
		seq     int
		stored  string
		want    string
		invalid bool
	}{
		{name: "no stored key", seq: 3, stored: "", invalid: true},
		{name: "the key of an attempt", seq: 3, stored: base + "-jla-abc123", want: base + "-jla-abc123"},
		{name: "the key of another chunk", seq: 3, stored: "job/job-abc/log/jlv-one/00000004-jla-abc", invalid: true},
		{name: "the key of another version", seq: 3, stored: "job/job-abc/log/jlv-two/00000003-jla-abc", invalid: true},
		{name: "the key of another job", seq: 3, stored: "job/job-xyz/log/jlv-one/00000003-jla-abc", invalid: true},
		{name: "the shared prefix with no attempt", seq: 3, stored: base, invalid: true},
		{name: "an empty attempt", seq: 3, stored: base + "-", invalid: true},
		{name: "an attempt of another prefix", seq: 3, stored: base + "-xyz-abc", invalid: true},
		{name: "an attempt with a slash", seq: 3, stored: base + "-jla-a/b", invalid: true},
		{name: "an attempt with a dot segment", seq: 3, stored: base + "-jla-..", invalid: true},
		{name: "an attempt with upper case", seq: 3, stored: base + "-jla-ABC", invalid: true},
		{name: "an attempt that is too long", seq: 3, stored: base + "-jla-" + string(make([]byte, 80)), invalid: true},
		{name: "a text that is no key", seq: 3, stored: "not a key", invalid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := job.ChunkBlobKey(id, version, tt.seq, tt.stored)
			if tt.invalid {
				if !errors.Is(err, job.ErrInvalid) {
					t.Fatalf("ChunkBlobKey(%q) = %q, %v; want ErrInvalid", tt.stored, got, err)
				}

				return
			}

			if err != nil || got.String() != tt.want {
				t.Fatalf("ChunkBlobKey(%q) = %q, %v; want %q", tt.stored, got, err, tt.want)
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
