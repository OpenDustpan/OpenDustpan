package audit_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// memEntries is an in-memory audit.EntryReader of the scope acme. reads
// counts the calls of EntryAt.
type memEntries struct {
	entries []audit.Entry
	reads   int
}

func (m *memEntries) EntryAt(_ context.Context, _ namespace.Name, seq int64) (audit.Entry, error) {
	m.reads++

	if seq < 1 || seq > int64(len(m.entries)) {
		return audit.Entry{}, audit.ErrEntryNotFound
	}

	return m.entries[seq-1], nil
}

func (m *memEntries) LastEntry(context.Context, namespace.Name) (audit.Entry, bool, error) {
	if len(m.entries) == 0 {
		var none audit.Entry

		return none, false, nil
	}

	return m.entries[len(m.entries)-1], true, nil
}

// add appends one chained entry.
func (m *memEntries) add() {
	e := walkEntry(int64(len(m.entries)) + 1)
	if len(m.entries) > 0 {
		e.PrevHash = m.entries[len(m.entries)-1].Hash
	}

	e.Hash = audit.ComputeHash(e, e.PrevHash)
	m.entries = append(m.entries, e)
}

// checkpointFixture holds a blob store, a store of chained entries of acme
// and the checkpoints over them.
type checkpointFixture struct {
	blobs   blob.Store
	entries *memEntries
	cps     *audit.Checkpoints
}

// newSigner returns a signer whose seed repeats b.
func newSigner(t *testing.T, b byte) *audit.Signer {
	t.Helper()

	signer, err := audit.NewSigner(bytes.Repeat([]byte{b}, 32))
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	return signer
}

func newCheckpointFixture(t *testing.T, n int) checkpointFixture {
	t.Helper()

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	entries := &memEntries{entries: nil, reads: 0}
	for range n {
		entries.add()
	}

	return checkpointFixture{
		blobs:   blobs,
		entries: entries,
		cps: audit.NewCheckpoints(audit.CheckpointsConfig{
			Blobs: blobs, Entries: entries, Signer: newSigner(t, 7), Marks: nil, Now: nil,
		}),
	}
}

// with returns checkpoints over the same blobs and entries with signer.
func (f checkpointFixture) with(signer *audit.Signer) *audit.Checkpoints {
	return audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs: f.blobs, Entries: f.entries, Signer: signer, Marks: nil, Now: nil,
	})
}

func (f checkpointFixture) write(t *testing.T) bool {
	t.Helper()

	wrote, err := f.cps.Write(t.Context(), "acme", time.Unix(1_700_000_000, 123_456_789))
	if err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}

	return wrote
}

func verifyWith(t *testing.T, cps *audit.Checkpoints) audit.CheckpointResult {
	t.Helper()

	got, err := cps.Verify(t.Context(), "acme")
	if err != nil {
		t.Fatalf("verify checkpoints: %v", err)
	}

	return got
}

func (f checkpointFixture) verify(t *testing.T) audit.CheckpointResult {
	t.Helper()

	return verifyWith(t, f.cps)
}

// replace stores raw under key in place of the object there.
func (f checkpointFixture) replace(t *testing.T, key blob.Key, raw []byte) {
	t.Helper()

	if err := f.blobs.Delete(t.Context(), key); err != nil {
		t.Fatalf("delete %s: %v", key, err)
	}

	if err := f.blobs.Put(t.Context(), key, bytes.NewReader(raw)); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

// key returns the key of the one stored copy of the checkpoint of acme at
// seq in epoch.
func (f checkpointFixture) key(t *testing.T, epoch, seq int64) blob.Key {
	t.Helper()

	keys := storedCopies(t, f.blobs, epoch, seq)
	if len(keys) != 1 {
		t.Fatalf("copies of acme %d in epoch %d: got %d, want 1", seq, epoch, len(keys))
	}

	return keys[0]
}

// storedCopies lists the keys of the stored copies of the checkpoint of
// acme at seq in epoch.
func storedCopies(t *testing.T, blobs blob.Store, epoch, seq int64) []blob.Key {
	t.Helper()

	probe, err := audit.CheckpointKey(epoch, "acme", seq, strings.Repeat("0", 32))
	if err != nil {
		t.Fatalf("checkpoint key: %v", err)
	}

	dir := probe.String()[:strings.LastIndexByte(probe.String(), '/')+1]
	name, _, _ := strings.Cut(probe.String()[len(dir):], "-")

	objects, err := blobs.List(t.Context(), dir)
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}

	var out []blob.Key

	for _, obj := range objects {
		if rest := strings.TrimPrefix(obj.Key.String(), dir); strings.HasPrefix(rest, name+"-") {
			out = append(out, obj.Key)
		}
	}

	return out
}

func TestCheckpointsWriteAndVerify(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)

	if !f.write(t) {
		t.Fatal("first write: no checkpoint written")
	}

	if f.write(t) {
		t.Fatal("second write at the same sequence: wrote again, want once")
	}

	f.entries.add()
	f.entries.add()

	if !f.write(t) {
		t.Fatal("write after two appends: no checkpoint written")
	}

	got := f.verify(t)
	if !got.Valid || got.Checked != 2 {
		t.Fatalf("verify: got %+v, want 2 valid checkpoints", got)
	}
}

func TestCheckpointsWriteSkipsAnUnchainedHead(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 0)
	if f.write(t) {
		t.Fatal("write on an empty scope: wrote a checkpoint")
	}

	f.entries.entries = append(f.entries.entries, walkEntry(1))
	if f.write(t) {
		t.Fatal("write on an unchained head: wrote a checkpoint")
	}
}

func TestCheckpointsFindARechainedLog(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)
	f.write(t)

	// An attacker changes entry 3 and computes its hash again. The chain
	// check cannot find this, the checkpoint can.
	e := f.entries.entries[2]
	e.Subject = "mallory"
	e.Hash = audit.ComputeHash(e, e.PrevHash)
	f.entries.entries[2] = e

	got := f.verify(t)
	if got.Valid || got.BadSequence != 3 {
		t.Fatalf("verify after a rechained entry: got %+v, want invalid at 3", got)
	}
}

func TestCheckpointsFindAChangedCheckpoint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		change func(c audit.Checkpoint) audit.Checkpoint
		name   string
	}{
		{name: "a changed hash", change: func(c audit.Checkpoint) audit.Checkpoint {
			c.Hash = strings.Repeat("0", len(c.Hash))

			return c
		}},
		{name: "another key", change: func(c audit.Checkpoint) audit.Checkpoint {
			other, err := audit.NewSigner(bytes.Repeat([]byte{9}, 32))
			if err != nil {
				panic(err)
			}

			return other.Sign(c)
		}},
		{name: "another sequence", change: func(c audit.Checkpoint) audit.Checkpoint {
			c.Sequence = 2

			return c
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newCheckpointFixture(t, 3)
			f.write(t)

			k := f.key(t, 0, 3)
			cp := readCheckpoint(t, f.blobs, k)

			raw, err := audit.MarshalCheckpoint(tc.change(cp))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			f.replace(t, k, raw)

			if got := f.verify(t); got.Valid || got.BadSequence != 3 {
				t.Fatalf("verify: got %+v, want invalid at 3", got)
			}
		})
	}
}

func TestCheckpointsFindACutAtTheEnd(t *testing.T) {
	t.Parallel()

	const (
		total = 200
		runs  = 50
	)

	// Each run starts fresh, so no mismatch from an earlier run sticks and
	// every run depends on the read of the newest checkpoint alone.
	for run := range runs {
		f := newCheckpointFixture(t, 0)

		for range total {
			f.entries.add()

			if !f.write(t) {
				t.Fatal("write: no checkpoint written")
			}
		}

		if got := f.verify(t); !got.Valid {
			t.Fatalf("run %d: verify before the cut: got %+v, want valid", run, got)
		}

		f.entries.entries = f.entries.entries[:total-5]

		if got := verifyWith(t, f.cps); got.Valid || got.BadSequence != total {
			t.Fatalf("run %d: verify after the cut: got %+v, want invalid at %d", run, got, total)
		}
	}
}

func TestCheckpointsReadNewestFirst(t *testing.T) {
	t.Parallel()

	const (
		total = 200
		runs  = 20
	)

	// Each run starts fresh, so no mismatch from an earlier run sticks and
	// every run depends on the read of the newest checkpoint alone. The
	// tail keeps its length, so only the newest hash shows the rewrite.
	for run := range runs {
		f := newCheckpointFixture(t, 0)

		for range total {
			f.entries.add()

			if !f.write(t) {
				t.Fatal("write: no checkpoint written")
			}
		}

		if got := f.verify(t); !got.Valid {
			t.Fatalf("run %d: verify before the rewrite: got %+v, want valid", run, got)
		}

		f.entries.entries = f.entries.entries[:total-5]

		for range 5 {
			f.entries.add()

			last := &f.entries.entries[len(f.entries.entries)-1]
			last.Subject = "mallory"
			last.Hash = audit.ComputeHash(*last, last.PrevHash)
		}

		if got := verifyWith(t, f.cps); got.Valid || got.BadSequence != total {
			t.Fatalf("run %d: verify after the rewrite: got %+v, want invalid at %d", run, got, total)
		}
	}
}

func TestCheckpointsWriteFindsReusedSequences(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 0)

	const total = 20

	for range total {
		f.entries.add()

		if !f.write(t) {
			t.Fatal("write: no checkpoint written")
		}
	}

	// A cut removes the last 5 entries, then new entries reuse the old
	// sequences with other hashes.
	f.entries.entries = f.entries.entries[:total-5]

	for range 5 {
		f.entries.add()

		last := &f.entries.entries[len(f.entries.entries)-1]
		last.Subject = "mallory"
		last.Hash = audit.ComputeHash(*last, last.PrevHash)
	}

	wrote, err := f.cps.Write(t.Context(), "acme", time.Unix(1_700_000_100, 0))
	if !errors.Is(err, audit.ErrCheckpointMismatch) {
		t.Fatalf("write over reused sequences: got %v, %v, want ErrCheckpointMismatch", wrote, err)
	}

	if wrote {
		t.Fatal("write over reused sequences: wrote a checkpoint")
	}

	if got := f.verify(t); got.Valid {
		t.Fatalf("verify after reused sequences: got %+v, want invalid", got)
	}
}

func TestCheckpointsFindADeletedNewestCheckpoint(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)
	f.write(t)

	if got := f.verify(t); !got.Valid {
		t.Fatalf("verify before the deletion: got %+v, want valid", got)
	}

	if err := f.blobs.Delete(t.Context(), f.key(t, 0, 3)); err != nil {
		t.Fatalf("delete newest checkpoint: %v", err)
	}

	f.entries.entries = f.entries.entries[:2]

	if got := f.verify(t); got.Valid || got.BadSequence != 3 {
		t.Fatalf("verify after the deletion: got %+v, want invalid at 3", got)
	}
}

func TestCheckpointsFindDeletedCheckpoints(t *testing.T) {
	t.Parallel()

	const total = 5

	f := newCheckpointFixture(t, 0)

	for range total {
		f.entries.add()

		if !f.write(t) {
			t.Fatal("write: no checkpoint written")
		}
	}

	if got := f.verify(t); !got.Valid {
		t.Fatalf("verify before the deletion: got %+v, want valid", got)
	}

	for seq := 1; seq <= total; seq++ {
		if err := f.blobs.Delete(t.Context(), f.key(t, 0, int64(seq))); err != nil {
			t.Fatalf("delete checkpoint %d: %v", seq, err)
		}
	}

	if got := f.verify(t); got.Valid || got.BadSequence != total {
		t.Fatalf("verify after the deletion: got %+v, want invalid at %d", got, total)
	}
}

func TestCheckpointsWriteRefusesACutBelowNewest(t *testing.T) {
	t.Parallel()

	const total = 5

	f := newCheckpointFixture(t, 0)

	for range total {
		f.entries.add()

		if !f.write(t) {
			t.Fatal("write: no checkpoint written")
		}
	}

	before := checkpointObjects(t, f.blobs)

	f.entries.entries = f.entries.entries[:total-2]

	wrote, err := f.cps.Write(t.Context(), "acme", time.Unix(1_700_000_100, 0))
	if !errors.Is(err, audit.ErrCheckpointMismatch) {
		t.Fatalf("write below the newest checkpoint: got %v, %v, want ErrCheckpointMismatch", wrote, err)
	}

	if wrote {
		t.Fatal("write below the newest checkpoint: wrote a checkpoint")
	}

	if got := checkpointObjects(t, f.blobs); got != before {
		t.Fatalf("checkpoints after the refused write: got %d objects, want %d", got, before)
	}
}

func TestCheckpointsMismatchSticksUntilANewEpoch(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)
	f.write(t)

	saved := append([]audit.Entry(nil), f.entries.entries...)

	e := f.entries.entries[2]
	e.Subject = "mallory"
	e.Hash = audit.ComputeHash(e, e.PrevHash)
	f.entries.entries[2] = e

	if got := f.verify(t); got.Valid || got.BadSequence != 3 {
		t.Fatalf("verify after the change: got %+v, want invalid at 3", got)
	}

	f.entries.entries = saved

	if got := f.verify(t); got.Valid {
		t.Fatalf("verify after the restore: got %+v, want still invalid", got)
	}

	stickyWrote, stickyErr := f.cps.Write(t.Context(), "acme", time.Unix(1_700_000_100, 0))
	if !errors.Is(stickyErr, audit.ErrCheckpointMismatch) {
		t.Fatalf("write after the mismatch: got %v, want ErrCheckpointMismatch", stickyErr)
	}

	if stickyWrote {
		t.Fatal("write after the mismatch: wrote a checkpoint")
	}

	epoch, err := f.cps.NewEpoch(t.Context(), time.Unix(1_700_000_100, 0))
	if err != nil || epoch != 1 {
		t.Fatalf("new epoch: got %d, %v, want 1", epoch, err)
	}

	if got := f.verify(t); !got.Valid || got.Checked != 0 {
		t.Fatalf("verify in the new epoch: got %+v, want valid with nothing to read", got)
	}
}

func TestCheckpointsWriteAgainAtTheSameSequence(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 2)

	if !f.write(t) {
		t.Fatal("first write: no checkpoint written")
	}

	wrote, err := f.cps.Write(t.Context(), "acme", time.Unix(1_700_000_100, 0))
	if err != nil {
		t.Fatalf("second write: got %v, want no error", err)
	}

	if wrote {
		t.Fatal("second write at the same sequence: wrote again, want once")
	}
}

func TestCheckpointsVerifyOnlyTheNewOnesAndASample(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 0)

	const older = 20
	for range older {
		f.entries.add()
		f.write(t)
	}

	if got := f.verify(t); !got.Valid || got.Checked != older {
		t.Fatalf("first verify: got %+v, want all %d checkpoints", got, older)
	}

	f.entries.add()
	f.write(t)

	f.entries.reads = 0

	got := f.verify(t)
	if !got.Valid || got.Checked != 1+audit.CheckpointSample || f.entries.reads != 1+audit.CheckpointSample {
		t.Fatalf("second verify: got %+v with %d entry reads, want the new one and a sample of %d",
			got, f.entries.reads, audit.CheckpointSample)
	}
}

func TestCheckpointsVerifyKeys(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 2)
	f.write(t)

	// The administrator replaces the signing key.
	replaced := f.with(newSigner(t, 8))
	if got := verifyWith(t, replaced); got.Valid {
		t.Fatalf("verify with the new key only: got %+v, want invalid", got)
	}

	signer := newSigner(t, 8)
	if err := signer.AddVerifyKey(newSigner(t, 7).PublicKey()); err != nil {
		t.Fatalf("add verify key: %v", err)
	}

	if got := verifyWith(t, f.with(signer)); !got.Valid || got.Checked != 1 {
		t.Fatalf("verify with the old key as a verify key: got %+v, want 1 valid checkpoint", got)
	}

	if err := signer.AddVerifyKey([]byte("short")); !errors.Is(err, audit.ErrInvalidVerifyKey) {
		t.Fatalf("add a short verify key: got %v, want ErrInvalidVerifyKey", err)
	}
}

func TestCheckpointsEpochs(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)
	f.write(t)

	if epoch, err := f.cps.Epoch(t.Context()); err != nil || epoch != 0 {
		t.Fatalf("epoch of a new store: got %d, %v, want 0", epoch, err)
	}

	// A restore loses entry 3, which the checkpoint of epoch 0 signs.
	f.entries.entries = f.entries.entries[:2]

	if got := f.verify(t); got.Valid {
		t.Fatalf("verify after the restore: got %+v, want invalid", got)
	}

	epoch, err := f.cps.NewEpoch(t.Context(), time.Unix(1_700_000_100, 0))
	if err != nil || epoch != 1 {
		t.Fatalf("new epoch: got %d, %v, want 1", epoch, err)
	}

	if got := f.verify(t); !got.Valid || got.Checked != 0 {
		t.Fatalf("verify in the new epoch: got %+v, want valid with nothing to read", got)
	}

	if !f.write(t) {
		t.Fatal("write in the new epoch: no checkpoint written")
	}

	if _, err := f.blobs.Get(t.Context(), f.key(t, 1, 2)); err != nil {
		t.Fatalf("checkpoint in epoch 1: %v", err)
	}
}

func TestCheckpointKey(t *testing.T) {
	t.Parallel()

	attempt := strings.Repeat("0a", 16)

	cases := []struct {
		key  func() (blob.Key, error)
		name string
		want string
	}{
		{
			name: "a copy",
			key:  func() (blob.Key, error) { return audit.CheckpointKey(3, audit.ServerScope, 42, attempt) },
			want: "audit-checkpoints/00000003/40736572766572/00000000000000000042-" + attempt + ".json",
		},
		{
			name: "an epoch copy",
			key:  func() (blob.Key, error) { return audit.EpochKey(4, attempt) },
			want: "audit-checkpoints/epochs/00000004-" + attempt + ".json",
		},
		{
			name: "a scope with capital letters",
			key:  func() (blob.Key, error) { return audit.CheckpointKey(0, "Acme_Corp", 1, attempt) },
			want: "audit-checkpoints/00000000/41636d655f436f7270/00000000000000000001-" + attempt + ".json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			k, err := tc.key()
			if err != nil || k.String() != tc.want {
				t.Fatalf("key: got %q, %v, want %q", k, err, tc.want)
			}

			if !audit.IsCheckpointKey(k) {
				t.Fatalf("IsCheckpointKey(%q): got false, want true", k)
			}
		})
	}
}

func TestCheckpointKeyRefusesABadAttempt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		attempt string
	}{
		{name: "empty", attempt: ""},
		{name: "short", attempt: strings.Repeat("a", 31)},
		{name: "long", attempt: strings.Repeat("a", 33)},
		{name: "capital letters", attempt: strings.Repeat("A", 32)},
		{name: "not hexadecimal", attempt: strings.Repeat("g", 32)},
		{name: "a path", attempt: "../" + strings.Repeat("a", 29)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if k, err := audit.CheckpointKey(0, "acme", 1, tc.attempt); !errors.Is(err, blob.ErrInvalidKey) {
				t.Fatalf("checkpoint key: got %q, %v, want ErrInvalidKey", k, err)
			}

			if k, err := audit.EpochKey(1, tc.attempt); !errors.Is(err, blob.ErrInvalidKey) {
				t.Fatalf("epoch key: got %q, %v, want ErrInvalidKey", k, err)
			}
		})
	}
}

func TestNewAttempt(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for range 64 {
		attempt, err := audit.NewAttempt()
		if err != nil {
			t.Fatalf("new attempt: %v", err)
		}

		if _, err := audit.CheckpointKey(0, "acme", 1, attempt); err != nil {
			t.Fatalf("key of attempt %q: %v", attempt, err)
		}

		if seen[attempt] {
			t.Fatalf("new attempt: %q came twice", attempt)
		}

		seen[attempt] = true
	}
}

func TestIsCheckpointKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		key  blob.Key
		want bool
	}{
		{key: "audit-checkpoints/epochs/00000001.json", want: true},
		{key: "audit-checkpoints/x", want: true},
		{key: "audit-checkpoints", want: true},
		{key: "audit-checkpointsx/a", want: false},
		{key: "namespaces/audit-checkpoints/a", want: false},
		{key: "restore-tmp/a", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.key.String(), func(t *testing.T) {
			t.Parallel()

			if got := audit.IsCheckpointKey(tc.key); got != tc.want {
				t.Fatalf("IsCheckpointKey(%q): got %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

func TestParseVerifyKey(t *testing.T) {
	t.Parallel()

	pub := newSigner(t, 7).PublicKey()

	got, err := audit.ParseVerifyKey(" " + base64.StdEncoding.EncodeToString(pub) + "\n")
	if err != nil || !bytes.Equal(got, pub) {
		t.Fatalf("parse a valid key: got %x, %v", got, err)
	}

	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := audit.ParseVerifyKey(bad); !errors.Is(err, audit.ErrInvalidVerifyKey) {
			t.Fatalf("parse %q: got %v, want ErrInvalidVerifyKey", bad, err)
		}
	}
}

func TestSigner(t *testing.T) {
	t.Parallel()

	if _, err := audit.NewSigner([]byte("short")); !errors.Is(err, audit.ErrInvalidSigningKey) {
		t.Fatalf("short seed: got %v, want ErrInvalidSigningKey", err)
	}

	signer, err := audit.NewSigner(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	if len(signer.KeyID()) != 16 || signer.KeyID() != audit.KeyID(signer.PublicKey()) {
		t.Fatalf("key ID: got %q", signer.KeyID())
	}

	cp := signer.Sign(audit.Checkpoint{
		At:        time.Unix(1, 999_999_999),
		Scope:     "acme",
		Hash:      "abc",
		KeyID:     "",
		Signature: nil,
		Sequence:  1,
		Epoch:     2,
	})

	raw, err := audit.MarshalCheckpoint(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	back, err := audit.UnmarshalCheckpoint(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if err := signer.Verify(back); err != nil {
		t.Fatalf("verify after a round trip: %v", err)
	}

	back.At = back.At.Add(time.Millisecond)
	if err := signer.Verify(back); !errors.Is(err, audit.ErrCheckpointMismatch) {
		t.Fatalf("verify a changed time: got %v, want ErrCheckpointMismatch", err)
	}
}

// epochKey returns the key of a copy of epoch.
func epochKey(t *testing.T, epoch int64) blob.Key {
	t.Helper()

	k, err := audit.EpochKey(epoch, strings.Repeat("e", 32))
	if err != nil {
		t.Fatalf("epoch key: %v", err)
	}

	return k
}

// checkpointObjects counts the stored checkpoint objects, without the epoch
// objects.
func checkpointObjects(t *testing.T, blobs blob.Store) int {
	t.Helper()

	objects, err := blobs.List(t.Context(), audit.CheckpointPrefix+"/")
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}

	var n int

	for _, obj := range objects {
		if !strings.Contains(obj.Key.String(), "/epochs/") {
			n++
		}
	}

	return n
}

func TestCheckpointsRefuseForgedEpochObjects(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 15, 12, 0, 0, 123000000, time.UTC)

	signedBy := func(t *testing.T, seed byte, epoch int64) []byte {
		t.Helper()

		rec := newSigner(t, seed).SignEpoch(audit.EpochRecord{
			At:        at,
			KeyID:     "",
			Signature: nil,
			Epoch:     epoch,
		})

		raw, err := audit.MarshalEpoch(rec)
		if err != nil {
			t.Fatalf("marshal epoch: %v", err)
		}

		return raw
	}

	realID := newSigner(t, 7).KeyID()

	missingSig := []byte(fmt.Sprintf(`{"at":"2026-10-15T12:00:00.123Z","keyID":%q,"epoch":1}`, realID))

	zeroRec := audit.EpochRecord{
		At:        at,
		KeyID:     realID,
		Signature: make([]byte, 64),
		Epoch:     1,
	}

	zeroRaw, err := audit.MarshalEpoch(zeroRec)
	if err != nil {
		t.Fatalf("marshal zero signature epoch: %v", err)
	}

	signedTwo := newSigner(t, 7).SignEpoch(audit.EpochRecord{
		At:        at,
		KeyID:     "",
		Signature: nil,
		Epoch:     2,
	})

	wrongRec := audit.EpochRecord{
		At:        at,
		KeyID:     realID,
		Signature: signedTwo.Signature,
		Epoch:     1,
	}

	wrongRaw, err := audit.MarshalEpoch(wrongRec)
	if err != nil {
		t.Fatalf("marshal wrong signature epoch: %v", err)
	}

	cases := []struct {
		raw  []byte
		name string
	}{
		{name: "unsigned body", raw: []byte(`{"at":"2026-10-15T12:00:00.123Z"}`)},
		{name: "another key", raw: signedBy(t, 9, 1)},
		{name: "number differs from key", raw: signedBy(t, 7, 2)},
		{name: "missing signature", raw: missingSig},
		{name: "zero signature", raw: zeroRaw},
		{name: "signature of another message", raw: wrongRaw},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkForgedEpoch(t, tc.raw)
		})
	}
}

// checkForgedEpoch stores raw as epoch 1 and proves that the checkpoints
// refuse it: Epoch names the key in an error that wraps
// ErrCheckpointMismatch, Verify answers invalid without an error, and
// Write signs nothing.
func checkForgedEpoch(t *testing.T, raw []byte) {
	t.Helper()

	f := newCheckpointFixture(t, 3)
	want := epochKey(t, 1)

	if err := f.blobs.Put(t.Context(), want, bytes.NewReader(raw)); err != nil {
		t.Fatalf("put forged epoch: %v", err)
	}

	if _, err := f.cps.Epoch(t.Context()); !errors.Is(err, audit.ErrCheckpointMismatch) ||
		!strings.Contains(err.Error(), want.String()) {
		t.Fatalf("epoch: got %v, want ErrCheckpointMismatch naming %s", err, want)
	}

	got, err := f.cps.Verify(t.Context(), "acme")
	if err != nil {
		t.Fatalf("verify: got error %v, want invalid without an error", err)
	}

	if got.Valid || got.BadSequence != 0 {
		t.Fatalf("verify: got %+v, want invalid at 0", got)
	}

	wrote, err := f.cps.Write(t.Context(), "acme", time.Unix(1_700_000_100, 0))
	if !errors.Is(err, audit.ErrCheckpointMismatch) {
		t.Fatalf("write: got %v, %v, want ErrCheckpointMismatch", wrote, err)
	}

	if wrote {
		t.Fatal("write with a forged epoch: wrote a checkpoint")
	}

	if n := checkpointObjects(t, f.blobs); n != 0 {
		t.Fatalf("checkpoints with a forged epoch: got %d objects, want none", n)
	}
}

func TestCheckpointsFindACopiedCheckpoint(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)
	f.write(t)

	epoch, err := f.cps.NewEpoch(t.Context(), time.Unix(1_700_000_100, 0))
	if err != nil || epoch != 1 {
		t.Fatalf("new epoch: got %d, %v, want 1", epoch, err)
	}

	body, err := f.blobs.Get(t.Context(), f.key(t, 0, 3))
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}

	raw, err := io.ReadAll(body)
	if closeErr := body.Close(); closeErr != nil {
		t.Fatalf("close checkpoint: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}

	if err := f.blobs.Put(t.Context(), copyKey(t, 1, 3, "1"), bytes.NewReader(raw)); err != nil {
		t.Fatalf("copy checkpoint to epoch 1: %v", err)
	}

	if got := f.verify(t); got.Valid || got.BadSequence != 3 {
		t.Fatalf("verify: got %+v, want invalid at 3", got)
	}
}

func TestCheckpointsFindARehashedCheckpoint(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)
	f.write(t)

	// An attacker changes entry 3, computes its hash again and writes the
	// new hash into the stored checkpoint. The old signature does not
	// cover it.
	e := f.entries.entries[2]
	e.Subject = "mallory"
	e.Hash = audit.ComputeHash(e, e.PrevHash)
	f.entries.entries[2] = e

	k := f.key(t, 0, 3)
	cp := readCheckpoint(t, f.blobs, k)
	cp.Hash = e.Hash

	raw, err := audit.MarshalCheckpoint(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	f.replace(t, k, raw)

	if got := f.verify(t); got.Valid || got.BadSequence != 3 {
		t.Fatalf("verify: got %+v, want invalid at 3", got)
	}
}

func TestCheckpointMessagePinsBytes(t *testing.T) {
	t.Parallel()

	cp := audit.Checkpoint{
		At:        time.Date(2026, time.October, 15, 12, 0, 0, 123000000, time.UTC),
		Scope:     "acme",
		Hash:      "9f2c41ab",
		KeyID:     "0123456789abcdef",
		Signature: nil,
		Sequence:  3,
		Epoch:     2,
	}

	want := "opendustpan audit checkpoint v1\nacme\n3\n2\n9f2c41ab\n1792065600123\n0123456789abcdef"

	if got := string(cp.Message()); got != want {
		t.Fatalf("message: got %q, want %q", got, want)
	}
}

func TestEpochMessagePinsBytes(t *testing.T) {
	t.Parallel()

	e := audit.EpochRecord{
		At:        time.Date(2026, time.October, 15, 12, 0, 0, 123000000, time.UTC),
		KeyID:     "0123456789abcdef",
		Signature: nil,
		Epoch:     4,
	}

	want := "opendustpan audit checkpoint epoch v1\n4\n1792065600123\n0123456789abcdef"

	if got := string(e.Message()); got != want {
		t.Fatalf("message: got %q, want %q", got, want)
	}
}

func TestSignerVerifyFailsOnAnyFieldChange(t *testing.T) {
	t.Parallel()

	signer := newSigner(t, 7)

	base := signer.Sign(audit.Checkpoint{
		At:        time.Date(2026, time.October, 15, 12, 0, 0, 123000000, time.UTC),
		Scope:     "acme",
		Hash:      "9f2c41ab",
		KeyID:     "",
		Signature: nil,
		Sequence:  3,
		Epoch:     2,
	})

	cases := []struct {
		change func(c audit.Checkpoint) audit.Checkpoint
		name   string
	}{
		{name: "scope", change: func(c audit.Checkpoint) audit.Checkpoint {
			c.Scope = "other"

			return c
		}},
		{name: "sequence", change: func(c audit.Checkpoint) audit.Checkpoint {
			c.Sequence = 4

			return c
		}},
		{name: "epoch", change: func(c audit.Checkpoint) audit.Checkpoint {
			c.Epoch = 3

			return c
		}},
		{name: "hash", change: func(c audit.Checkpoint) audit.Checkpoint {
			c.Hash = "00000000"

			return c
		}},
		{name: "time", change: func(c audit.Checkpoint) audit.Checkpoint {
			c.At = c.At.Add(time.Millisecond)

			return c
		}},
		{name: "key", change: func(c audit.Checkpoint) audit.Checkpoint {
			c.KeyID = strings.Repeat("f", 16)

			return c
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := signer.Verify(tc.change(base)); !errors.Is(err, audit.ErrCheckpointMismatch) {
				t.Fatalf("verify: got %v, want ErrCheckpointMismatch", err)
			}
		})
	}
}

// epochSwapBlobs wraps a blob store and starts epoch 1 after the first
// list of the epoch prefix, so the epoch changes between the two reads of
// one Write.
type epochSwapBlobs struct {
	inner  blob.Store
	signer *audit.Signer
	lists  int
}

func (s *epochSwapBlobs) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	return s.inner.Put(ctx, key, r)
}

func (s *epochSwapBlobs) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	return s.inner.Get(ctx, key)
}

func (s *epochSwapBlobs) Delete(ctx context.Context, key blob.Key) error {
	return s.inner.Delete(ctx, key)
}

// List answers the objects under prefix. After the first list of the epoch
// prefix, it stores epoch 1, so the next epoch read finds a new epoch.
func (s *epochSwapBlobs) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	out, err := s.inner.List(ctx, prefix)
	if err != nil || prefix != "audit-checkpoints/epochs/" || s.lists != 0 {
		return out, err
	}

	s.lists++

	rec := s.signer.SignEpoch(audit.EpochRecord{
		At:        time.Unix(1_700_000_100, 0),
		KeyID:     "",
		Signature: nil,
		Epoch:     1,
	})

	raw, marshalErr := audit.MarshalEpoch(rec)
	if marshalErr != nil {
		return out, marshalErr
	}

	key, keyErr := audit.EpochKey(1, strings.Repeat("e", 32))
	if keyErr != nil {
		return out, keyErr
	}

	if putErr := s.inner.Put(ctx, key, bytes.NewReader(raw)); putErr != nil {
		return out, putErr
	}

	return out, nil
}

func (s *epochSwapBlobs) Ping(ctx context.Context) error {
	return s.inner.Ping(ctx)
}

// TestCheckpointsWriteSkipsAnEpochChange proves that Write stores nothing
// when the epoch changes between its two reads.
func TestCheckpointsWriteSkipsAnEpochChange(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)
	signer := newSigner(t, 7)
	swap := &epochSwapBlobs{inner: f.blobs, signer: signer, lists: 0}
	cps := audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs: swap, Entries: f.entries, Signer: signer, Marks: nil, Now: nil,
	})

	wrote, err := cps.Write(t.Context(), "acme", time.Unix(1_700_000_100, 0))
	if err != nil {
		t.Fatalf("write across an epoch change: got error %v, want no error", err)
	}

	if wrote {
		t.Fatal("write across an epoch change: wrote a checkpoint")
	}

	if n := checkpointObjects(t, f.blobs); n != 0 {
		t.Fatalf("checkpoints across an epoch change: got %d objects, want none", n)
	}
}

// readCheckpoint reads and decodes the checkpoint under key.
func readCheckpoint(t *testing.T, blobs blob.Store, key blob.Key) audit.Checkpoint {
	t.Helper()

	body, err := blobs.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}

	raw, err := io.ReadAll(body)
	if closeErr := body.Close(); closeErr != nil {
		t.Fatalf("close %s: %v", key, closeErr)
	}

	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}

	cp, err := audit.UnmarshalCheckpoint(raw)
	if err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}

	return cp
}
