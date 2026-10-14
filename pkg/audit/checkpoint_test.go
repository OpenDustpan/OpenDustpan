package audit_test

import (
	"bytes"
	"context"
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

// listStore is an in-memory audit.Store of one scope. List answers newest
// first, as the SQL stores do. When onList is set, List calls it first.
type listStore struct {
	onList  func()
	entries []audit.Entry
}

func (s *listStore) Append(context.Context, namespace.Fence, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errors.ErrUnsupported
}

func (s *listStore) List(_ context.Context, _ namespace.Name, page audit.Page) (audit.Listing, error) {
	if s.onList != nil {
		s.onList()
	}

	p := page.Normalized()
	total := int64(len(s.entries))
	out := audit.Listing{Entries: nil, Total: total}

	for i := p.Offset(); i < total && len(out.Entries) < p.Size; i++ {
		out.Entries = append(out.Entries, s.entries[total-1-i])
	}

	return out, nil
}

func (s *listStore) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

// add appends one chained entry.
func (s *listStore) add() {
	e := walkEntry(int64(len(s.entries)) + 1)
	if len(s.entries) > 0 {
		e.PrevHash = s.entries[len(s.entries)-1].Hash
	}

	e.Hash = audit.ComputeHash(e, e.PrevHash)
	s.entries = append(s.entries, e)
}

// checkpointFixture holds a signer, a blob store and a store of n chained
// entries of acme.
type checkpointFixture struct {
	blobs blob.Store
	store *listStore
	cps   audit.Checkpoints
}

func newCheckpointFixture(t *testing.T, n int) checkpointFixture {
	t.Helper()

	signer, err := audit.NewSigner(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	store := &listStore{onList: nil, entries: nil}
	for range n {
		store.add()
	}

	return checkpointFixture{
		blobs: blobs,
		store: store,
		cps:   audit.Checkpoints{Blobs: blobs, Entries: store, Signer: signer},
	}
}

func (f checkpointFixture) write(t *testing.T) bool {
	t.Helper()

	wrote, err := f.cps.Write(t.Context(), "acme", time.Unix(1_700_000_000, 123_456_789))
	if err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}

	return wrote
}

func (f checkpointFixture) verify(t *testing.T) audit.CheckpointResult {
	t.Helper()

	got, err := f.cps.Verify(t.Context(), "acme")
	if err != nil {
		t.Fatalf("verify checkpoints: %v", err)
	}

	return got
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

func TestCheckpointsWriteAndVerify(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)

	if !f.write(t) {
		t.Fatal("first write: no checkpoint written")
	}

	if f.write(t) {
		t.Fatal("second write at the same sequence: wrote again, want once")
	}

	f.store.add()
	f.store.add()

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

	f.store.entries = append(f.store.entries, walkEntry(1))
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
	e := f.store.entries[2]
	e.Subject = "mallory"
	e.Hash = audit.ComputeHash(e, e.PrevHash)
	f.store.entries[2] = e

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

			key, err := audit.CheckpointKey("acme", 3)
			if err != nil {
				t.Fatalf("key: %v", err)
			}

			cp := readCheckpoint(t, f.blobs, key)

			raw, err := audit.MarshalCheckpoint(tc.change(cp))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			f.replace(t, key, raw)

			if got := f.verify(t); got.Valid || got.BadSequence != 3 {
				t.Fatalf("verify: got %+v, want invalid at 3", got)
			}
		})
	}
}

func TestCheckpointsVerifyWhileEntriesArrive(t *testing.T) {
	t.Parallel()

	f := newCheckpointFixture(t, 3)
	f.write(t)

	// An entry arrives after the first read of the store, so the page that
	// held sequence 3 moves.
	reads := 0
	f.store.onList = func() {
		reads++
		if reads == 2 {
			f.store.add()
		}
	}

	if got := f.verify(t); !got.Valid || got.Checked != 1 {
		t.Fatalf("verify while entries arrive: got %+v, want 1 valid checkpoint", got)
	}
}

func TestCheckpointKey(t *testing.T) {
	t.Parallel()

	key, err := audit.CheckpointKey(audit.ServerScope, 42)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	if want := "audit-checkpoints/40736572766572/00000000000000000042.json"; key.String() != want {
		t.Fatalf("key: got %q, want %q", key, want)
	}

	if _, err := audit.CheckpointKey("Acme_Corp", 1); err != nil {
		t.Fatalf("key of a scope with capital letters: %v", err)
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
