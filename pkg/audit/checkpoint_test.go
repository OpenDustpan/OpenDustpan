package audit_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
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
		cps:     audit.NewCheckpoints(audit.CheckpointsConfig{Blobs: blobs, Entries: entries, Signer: newSigner(t, 7)}),
	}
}

// with returns checkpoints over the same blobs and entries with signer.
func (f checkpointFixture) with(signer *audit.Signer) *audit.Checkpoints {
	return audit.NewCheckpoints(audit.CheckpointsConfig{Blobs: f.blobs, Entries: f.entries, Signer: signer})
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

// key returns the checkpoint key of acme at seq in epoch.
func key(t *testing.T, epoch, seq int64) blob.Key {
	t.Helper()

	k, err := audit.CheckpointKey(epoch, "acme", seq)
	if err != nil {
		t.Fatalf("checkpoint key: %v", err)
	}

	return k
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

			k := key(t, 0, 3)
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

	if _, err := f.blobs.Get(t.Context(), key(t, 1, 2)); err != nil {
		t.Fatalf("checkpoint in epoch 1: %v", err)
	}
}

func TestCheckpointKey(t *testing.T) {
	t.Parallel()

	k, err := audit.CheckpointKey(3, audit.ServerScope, 42)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	if want := "audit-checkpoints/00000003/40736572766572/00000000000000000042.json"; k.String() != want {
		t.Fatalf("key: got %q, want %q", k, want)
	}

	if _, err := audit.CheckpointKey(0, "Acme_Corp", 1); err != nil {
		t.Fatalf("key of a scope with capital letters: %v", err)
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
