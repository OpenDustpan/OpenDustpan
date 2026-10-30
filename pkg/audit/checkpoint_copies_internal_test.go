package audit

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
)

// TestParseCopyNameRefusesHostileNames sends names that no writer of the
// server makes straight to the parser of copy keys.
func TestParseCopyNameRefusesHostileNames(t *testing.T) {
	t.Parallel()

	dir := checkpointDir(0, "acme")
	seq := "00000000000000000003"
	attempt := strings.Repeat("a", 32)

	cases := []struct {
		name string
		key  string
		want int64
	}{
		{name: "the first layout", key: dir + seq + ".json", want: 3},
		{name: "a copy", key: dir + seq + "-" + attempt + ".json", want: 3},
		{name: "another directory", key: checkpointDir(0, "other") + seq + ".json", want: 0},
		{name: "a subdirectory", key: dir + "x/" + seq + ".json", want: 0},
		{name: "a parent segment", key: dir + "../" + seq + ".json", want: 0},
		{name: "no suffix", key: dir + seq, want: 0},
		{name: "another suffix", key: dir + seq + ".txt", want: 0},
		{name: "a short number", key: dir + "3.json", want: 0},
		{name: "a long number", key: dir + "0" + seq + ".json", want: 0},
		{name: "a sign", key: dir + "+0000000000000000003.json", want: 0},
		{name: "sequence 0", key: dir + strings.Repeat("0", 20) + ".json", want: 0},
		{name: "a number above int64", key: dir + strings.Repeat("9", 20) + ".json", want: 0},
		{name: "no dash", key: dir + seq + attempt + ".json", want: 0},
		{name: "an empty attempt", key: dir + seq + "-.json", want: 0},
		{name: "a short attempt", key: dir + seq + "-" + attempt[:31] + ".json", want: 0},
		{name: "a capital attempt", key: dir + seq + "-" + strings.ToUpper(attempt) + ".json", want: 0},
		{name: "two attempts", key: dir + seq + "-" + attempt + "-" + attempt + ".json", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseCopyName(blob.Key(tc.key), dir, checkpointSequenceDigits)
			if got != tc.want || ok != (tc.want > 0) {
				t.Fatalf("parse %q: got %d, %v, want %d", tc.key, got, ok, tc.want)
			}
		})
	}
}

// TestVerifyCopyRefusesAKeyOfAnotherPlace sends a key outside the directory
// of the scope, or a key of another sequence, straight to verifyCopy, as if
// the parsed list of the directory had failed. It reads nothing: the store
// is nil.
func TestVerifyCopyRefusesAKeyOfAnotherPlace(t *testing.T) {
	t.Parallel()

	c := NewCheckpoints(CheckpointsConfig{Blobs: nil, Entries: nil, Signer: nil, Marks: nil, Now: nil})
	gk := goodKey{scope: "acme", epoch: 1}
	attempt := strings.Repeat("b", 32)

	keys := []struct {
		name string
		key  func() (blob.Key, error)
	}{
		{name: "another scope", key: func() (blob.Key, error) { return CheckpointKey(1, "other", 3, attempt) }},
		{name: "another epoch", key: func() (blob.Key, error) { return CheckpointKey(2, "acme", 3, attempt) }},
		{name: "another sequence", key: func() (blob.Key, error) { return CheckpointKey(1, "acme", 4, attempt) }},
		{name: "an epoch copy", key: func() (blob.Key, error) { return EpochKey(1, attempt) }},
		{
			name: "a key of a unit",
			key:  func() (blob.Key, error) { return blob.ParseKey("namespaces/acme/00000000000000000003.json") },
		},
	}

	for _, tc := range keys {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key, err := tc.key()
			if err != nil {
				t.Fatalf("key: %v", err)
			}

			if _, err := c.verifyCopy(t.Context(), gk, 3, key); !errors.Is(err, ErrCheckpointMismatch) {
				t.Fatalf("verify copy %s: got %v, want ErrCheckpointMismatch", key, err)
			}
		})
	}
}

// TestVerifySequenceRefusesNoCopy sends a sequence with no copy straight to
// verifySequence, as if the plan had failed. It must never count as a match.
func TestVerifySequenceRefusesNoCopy(t *testing.T) {
	t.Parallel()

	c := NewCheckpoints(CheckpointsConfig{Blobs: nil, Entries: nil, Signer: nil, Marks: nil, Now: nil})

	checked, err := c.verifySequence(t.Context(), goodKey{scope: "acme", epoch: 0}, 3, nil)
	if !errors.Is(err, ErrCheckpointMismatch) || checked != 0 {
		t.Fatalf("verify sequence: got %d, %v, want ErrCheckpointMismatch", checked, err)
	}
}

// TestVerifyEpochCopyRefusesAKeyOfAnotherPlace sends a key outside the
// epoch directory, or a key of another epoch, straight to verifyEpochCopy,
// as if the parsed list of the directory had failed.
func TestVerifyEpochCopyRefusesAKeyOfAnotherPlace(t *testing.T) {
	t.Parallel()

	c := NewCheckpoints(CheckpointsConfig{Blobs: nil, Entries: nil, Signer: nil, Marks: nil, Now: nil})
	attempt := strings.Repeat("c", 32)

	keys := []struct {
		name string
		key  func() (blob.Key, error)
	}{
		{name: "another epoch", key: func() (blob.Key, error) { return EpochKey(3, attempt) }},
		{name: "a checkpoint copy", key: func() (blob.Key, error) { return CheckpointKey(2, "acme", 2, attempt) }},
		{name: "a key of a unit", key: func() (blob.Key, error) { return blob.ParseKey("namespaces/00000002.json") }},
	}

	for _, tc := range keys {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key, err := tc.key()
			if err != nil {
				t.Fatalf("key: %v", err)
			}

			if err := c.verifyEpochCopy(t.Context(), key, 2); !errors.Is(err, ErrCheckpointMismatch) {
				t.Fatalf("verify epoch copy %s: got %v, want ErrCheckpointMismatch", key, err)
			}
		})
	}
}
