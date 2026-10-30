package audit_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
)

// replacingBlobs is an in-memory blob store that ignores conditional
// writes: a Put on a key that holds an object replaces it with no error,
// like a store that accepts If-None-Match and ignores it.
type replacingBlobs struct {
	objects map[blob.Key][]byte
	puts    int
	mu      sync.Mutex
}

func newReplacingBlobs() *replacingBlobs {
	return &replacingBlobs{objects: map[blob.Key][]byte{}, puts: 0, mu: sync.Mutex{}}
}

func (s *replacingBlobs) Put(_ context.Context, key blob.Key, r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read %s: %w", key, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.objects[key] = raw
	s.puts++

	return nil
}

func (s *replacingBlobs) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("%s: %w", key, blob.ErrNotFound)
	}

	return io.NopCloser(bytes.NewReader(raw)), nil
}

func (s *replacingBlobs) Delete(_ context.Context, key blob.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.objects, key)

	return nil
}

// List answers the objects under the key directory prefix, ordered by key.
func (s *replacingBlobs) List(_ context.Context, prefix string) ([]blob.Object, error) {
	dir := strings.TrimSuffix(prefix, "/") + "/"

	s.mu.Lock()
	defer s.mu.Unlock()

	var out []blob.Object

	for key := range s.objects {
		if strings.HasPrefix(key.String(), dir) {
			out = append(out, blob.Object{ModifiedAt: time.Time{}, Key: key})
		}
	}

	slices.SortFunc(out, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return out, nil
}

func (s *replacingBlobs) Ping(context.Context) error { return nil }

// raw answers the bytes under key.
func (s *replacingBlobs) raw(t *testing.T, key blob.Key) []byte {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	raw, ok := s.objects[key]
	if !ok {
		t.Fatalf("no object under %s", key)
	}

	return append([]byte(nil), raw...)
}

// store stores raw under key.
func (s *replacingBlobs) store(t *testing.T, key blob.Key, raw []byte) {
	t.Helper()

	if err := s.Put(t.Context(), key, bytes.NewReader(raw)); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

// replacingFixture is checkpoints over a store that ignores conditional
// writes and a log of n chained entries of acme.
type replacingFixture struct {
	blobs   *replacingBlobs
	entries *memEntries
	signer  *audit.Signer
}

func newReplacingFixture(t *testing.T, n int) replacingFixture {
	t.Helper()

	entries := &memEntries{entries: nil, reads: 0}
	for range n {
		entries.add()
	}

	return replacingFixture{blobs: newReplacingBlobs(), entries: entries, signer: newSigner(t, 7)}
}

// checkpoints answers a new instance of the checkpoints over the fixture.
func (f replacingFixture) checkpoints() *audit.Checkpoints {
	return audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs: f.blobs, Entries: f.entries, Signer: f.signer, Marks: nil, Now: nil,
	})
}

// rechain changes the subject of entry seq and computes the hash of it and
// of every later entry again, as an attacker who changes the database does.
func (f replacingFixture) rechain(seq int) {
	for i := seq - 1; i < len(f.entries.entries); i++ {
		e := f.entries.entries[i]
		if i == seq-1 {
			e.Subject = "mallory"
		}

		if i > 0 {
			e.PrevHash = f.entries.entries[i-1].Hash
		}

		e.Hash = audit.ComputeHash(e, e.PrevHash)
		f.entries.entries[i] = e
	}
}

// signed answers the stored form of a checkpoint of acme at 3 in epoch 0
// that the signer of the fixture signs over hash.
func (f replacingFixture) signed(t *testing.T, hash string) []byte {
	t.Helper()

	raw, err := audit.MarshalCheckpoint(f.signer.Sign(audit.Checkpoint{
		At:        time.Unix(1_700_000_200, 0),
		Scope:     "acme",
		Hash:      hash,
		KeyID:     "",
		Signature: nil,
		Sequence:  3,
		Epoch:     0,
	}))
	if err != nil {
		t.Fatalf("marshal checkpoint: %v", err)
	}

	return raw
}

// copyKey answers the key of a new copy of the checkpoint of acme at seq in
// epoch, with attempt b repeated as its attempt part.
func copyKey(t *testing.T, epoch, seq int64, b string) blob.Key {
	t.Helper()

	k, err := audit.CheckpointKey(epoch, "acme", seq, strings.Repeat(b, 32))
	if err != nil {
		t.Fatalf("checkpoint key: %v", err)
	}

	return k
}

func writeAcme(t *testing.T, cps *audit.Checkpoints) (bool, error) {
	t.Helper()

	return cps.Write(t.Context(), "acme", time.Unix(1_700_000_100, 0))
}

// TestCheckpointsFindATamperedRebuildOnAStoreThatReplaces proves that on a
// store that ignores conditional writes, a log that an attacker changes and
// chains again at a sequence that a checkpoint signs is found. The next
// signer run finds the stored copy, signs nothing and keeps the good copy,
// and the check fails at that sequence.
func TestCheckpointsFindATamperedRebuildOnAStoreThatReplaces(t *testing.T) {
	t.Parallel()

	f := newReplacingFixture(t, 3)
	cps := f.checkpoints()

	if wrote, err := writeAcme(t, cps); err != nil || !wrote {
		t.Fatalf("first write: got %v, %v, want a checkpoint", wrote, err)
	}

	good := storedCopies(t, f.blobs, 0, 3)
	if len(good) != 1 {
		t.Fatalf("copies after the first write: got %d, want 1", len(good))
	}

	before := f.blobs.raw(t, good[0])

	f.rechain(3)

	// A new instance knows nothing of the first one: only the store holds
	// the evidence.
	signer := f.checkpoints()

	wrote, err := writeAcme(t, signer)
	if !errors.Is(err, audit.ErrCheckpointMismatch) || wrote {
		t.Fatalf("write over the rebuilt log: got %v, %v, want ErrCheckpointMismatch", wrote, err)
	}

	if after := storedCopies(t, f.blobs, 0, 3); !slices.Equal(after, good) {
		t.Fatalf("copies after the write over the rebuilt log: got %v, want %v", after, good)
	}

	if !bytes.Equal(f.blobs.raw(t, good[0]), before) {
		t.Fatal("the good copy changed")
	}

	if got := verifyWith(t, f.checkpoints()); got.Valid || got.BadSequence != 3 {
		t.Fatalf("verify: got %+v, want invalid at 3", got)
	}
}

// TestCheckpointsFindCopiesThatSignDifferentHashes proves that the check
// reads every copy of a sequence, and that two copies with a valid signature
// that sign different hashes fail the scope, also when the database matches
// one of them. The database never decides which copy wins.
func TestCheckpointsFindCopiesThatSignDifferentHashes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// rebuilt is the attempt part of the copy over the rebuilt log. The
		// good copy has the attempt part of "5": the case orders the copies.
		rebuilt string
		// firstLayout stores the copy over the rebuilt log under the key of
		// the first layout.
		firstLayout bool
	}{
		{name: "the rebuilt copy lists first", rebuilt: "0", firstLayout: false},
		{name: "the rebuilt copy lists last", rebuilt: "f", firstLayout: false},
		{name: "the rebuilt copy has the key of the first layout", rebuilt: "", firstLayout: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newReplacingFixture(t, 3)
			f.blobs.store(t, copyKey(t, 0, 3, "5"), f.signed(t, f.entries.entries[2].Hash))

			f.rechain(3)

			rebuilt := firstLayoutKey(t, 0, 3)
			if !tc.firstLayout {
				rebuilt = copyKey(t, 0, 3, tc.rebuilt)
			}

			f.blobs.store(t, rebuilt, f.signed(t, f.entries.entries[2].Hash))

			got := verifyWith(t, f.checkpoints())
			if got.Valid || got.BadSequence != 3 || !strings.Contains(got.Reason, "different hashes") {
				t.Fatalf("verify: got %+v, want invalid at 3 for different hashes", got)
			}

			if wrote, err := writeAcme(t, f.checkpoints()); !errors.Is(err, audit.ErrCheckpointMismatch) || wrote {
				t.Fatalf("write: got %v, %v, want ErrCheckpointMismatch", wrote, err)
			}
		})
	}
}

// barrierBlobs holds each Put under the checkpoint prefix until n of them
// wait, so n writers list the copies before any of them stores one.
type barrierBlobs struct {
	*replacingBlobs

	ready chan struct{}
	n     int
	mu    sync.Mutex
}

func (s *barrierBlobs) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	s.mu.Lock()
	s.n--

	if s.n == 0 {
		close(s.ready)
	}
	s.mu.Unlock()

	select {
	case <-s.ready:
	case <-ctx.Done():
		return fmt.Errorf("wait for the other writers: %w", ctx.Err())
	}

	return s.replacingBlobs.Put(ctx, key, r)
}

// TestCheckpointsTwoHonestWritersRaiseNoAlarm proves that two instances
// that sign the same sequence at the same moment store two copies of it,
// and that the check reads both and finds no mismatch. A later write signs
// nothing more.
func TestCheckpointsTwoHonestWritersRaiseNoAlarm(t *testing.T) {
	t.Parallel()

	const writers = 2

	f := newReplacingFixture(t, 3)
	race := &barrierBlobs{replacingBlobs: f.blobs, ready: make(chan struct{}), n: writers, mu: sync.Mutex{}}

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	type outcome struct {
		err   error
		wrote bool
	}

	results := make(chan outcome, writers)

	for i := range writers {
		cps := audit.NewCheckpoints(audit.CheckpointsConfig{
			Blobs: race, Entries: f.entries, Signer: f.signer, Marks: nil, Now: nil,
		})

		go func() {
			wrote, err := cps.Write(ctx, "acme", time.Unix(1_700_000_100+int64(i), 0))
			results <- outcome{err: err, wrote: wrote}
		}()
	}

	for range writers {
		if r := <-results; r.err != nil || !r.wrote {
			t.Fatalf("racing write: got %v, %v, want a checkpoint", r.wrote, r.err)
		}
	}

	if n := len(storedCopies(t, f.blobs, 0, 3)); n != writers {
		t.Fatalf("copies after the race: got %d, want %d", n, writers)
	}

	got := verifyWith(t, f.checkpoints())
	if !got.Valid || got.Checked != writers {
		t.Fatalf("verify: got %+v, want valid with %d copies read", got, writers)
	}

	if wrote, err := writeAcme(t, f.checkpoints()); err != nil || wrote {
		t.Fatalf("write after the race: got %v, %v, want nothing written", wrote, err)
	}

	if n := len(storedCopies(t, f.blobs, 0, 3)); n != writers {
		t.Fatalf("copies after a later write: got %d, want %d", n, writers)
	}
}

// TestCheckpointsReadTheFirstLayout proves that a checkpoint under a key of
// the first layout counts as a copy: the check reads it, and a write at its
// sequence finds it and signs nothing more.
func TestCheckpointsReadTheFirstLayout(t *testing.T) {
	t.Parallel()

	f := newReplacingFixture(t, 3)
	f.blobs.store(t, firstLayoutKey(t, 0, 3), f.signed(t, f.entries.entries[2].Hash))

	if got := verifyWith(t, f.checkpoints()); !got.Valid || got.Checked != 1 {
		t.Fatalf("verify: got %+v, want valid with 1 copy read", got)
	}

	if wrote, err := writeAcme(t, f.checkpoints()); err != nil || wrote {
		t.Fatalf("write: got %v, %v, want nothing written", wrote, err)
	}

	f.rechain(3)

	if got := verifyWith(t, f.checkpoints()); got.Valid || got.BadSequence != 3 {
		t.Fatalf("verify over the rebuilt log: got %+v, want invalid at 3", got)
	}
}

// TestCheckpointsRefuseBadCopyDirectories proves that the check and the
// writer fail a scope whose directory holds a key that the server does not
// write, or more copies of one sequence than MaxCheckpointCopies, and that
// they read no copy of it.
func TestCheckpointsRefuseBadCopyDirectories(t *testing.T) {
	t.Parallel()

	cases := []struct {
		fill    func(t *testing.T, f replacingFixture)
		name    string
		wantSeq int64
	}{
		{name: "a key with no sequence", wantSeq: 0, fill: func(t *testing.T, f replacingFixture) {
			t.Helper()
			f.blobs.store(
				t,
				blob.Key(strings.Replace(firstLayoutKey(t, 0, 3).String(), "0003.json", "0003.txt", 1)),
				nil,
			)
		}},
		{name: "a short attempt part", wantSeq: 0, fill: func(t *testing.T, f replacingFixture) {
			t.Helper()
			f.blobs.store(t, blob.Key(strings.Replace(firstLayoutKey(t, 0, 3).String(), ".json", "-abc.json", 1)), nil)
		}},
		{name: "a key in a subdirectory", wantSeq: 0, fill: func(t *testing.T, f replacingFixture) {
			t.Helper()
			k := firstLayoutKey(t, 0, 3).String()
			i := strings.LastIndexByte(k, '/')
			f.blobs.store(t, blob.Key(k[:i]+"/x"+k[i:]), nil)
		}},
		{name: "sequence 0", wantSeq: 0, fill: func(t *testing.T, f replacingFixture) {
			t.Helper()
			f.blobs.store(t, firstLayoutKey(t, 0, 0), nil)
		}},
		{name: "too many copies", wantSeq: 3, fill: func(t *testing.T, f replacingFixture) {
			t.Helper()

			raw := f.signed(t, f.entries.entries[2].Hash)

			for i := range audit.MaxCheckpointCopies + 1 {
				k, err := audit.CheckpointKey(0, "acme", 3, fmt.Sprintf("%032x", i))
				if err != nil {
					t.Fatalf("checkpoint key: %v", err)
				}

				f.blobs.store(t, k, raw)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newReplacingFixture(t, 3)
			tc.fill(t, f)
			checkBadDirectory(t, f, tc.wantSeq)
		})
	}
}

// checkBadDirectory proves that the check fails the scope at wantSeq with
// no copy read, and that the writer signs and stores nothing.
func checkBadDirectory(t *testing.T, f replacingFixture, wantSeq int64) {
	t.Helper()

	got := verifyWith(t, f.checkpoints())
	if got.Valid || got.BadSequence != wantSeq || got.Checked != 0 {
		t.Fatalf("verify: got %+v, want invalid at %d with no copy read", got, wantSeq)
	}

	puts := f.blobs.puts

	if wrote, err := writeAcme(t, f.checkpoints()); !errors.Is(err, audit.ErrCheckpointMismatch) || wrote {
		t.Fatalf("write: got %v, %v, want ErrCheckpointMismatch", wrote, err)
	}

	if f.blobs.puts != puts {
		t.Fatal("write into a bad directory stored an object")
	}
}

// TestCheckpointsFindAMissingCopyOfTheLastGoodSequence proves that the
// check fails when no copy of the last sequence that matched is stored, also
// when a newer sequence has a copy.
func TestCheckpointsFindAMissingCopyOfTheLastGoodSequence(t *testing.T) {
	t.Parallel()

	f := newReplacingFixture(t, 3)
	cps := f.checkpoints()

	if wrote, err := writeAcme(t, cps); err != nil || !wrote {
		t.Fatalf("write at 3: got %v, %v", wrote, err)
	}

	if got := verifyWith(t, cps); !got.Valid {
		t.Fatalf("verify at 3: got %+v, want valid", got)
	}

	f.entries.add()

	if wrote, err := writeAcme(t, cps); err != nil || !wrote {
		t.Fatalf("write at 4: got %v, %v", wrote, err)
	}

	for _, k := range storedCopies(t, f.blobs, 0, 3) {
		if err := f.blobs.Delete(t.Context(), k); err != nil {
			t.Fatalf("delete %s: %v", k, err)
		}
	}

	if got := verifyWith(t, cps); got.Valid || got.BadSequence != 3 {
		t.Fatalf("verify: got %+v, want invalid at 3", got)
	}
}

// epochCopy answers the stored form of epoch signed by the signer of seed.
func epochCopy(t *testing.T, seed byte, epoch int64) []byte {
	t.Helper()

	raw, err := audit.MarshalEpoch(newSigner(t, seed).SignEpoch(audit.EpochRecord{
		At:        time.Unix(1_700_000_300, 0),
		KeyID:     "",
		Signature: nil,
		Epoch:     epoch,
	}))
	if err != nil {
		t.Fatalf("marshal epoch: %v", err)
	}

	return raw
}

// epochCopyKey answers the key of a copy of epoch with attempt b repeated
// as its attempt part.
func epochCopyKey(t *testing.T, epoch int64, b string) blob.Key {
	t.Helper()

	k, err := audit.EpochKey(epoch, strings.Repeat(b, 32))
	if err != nil {
		t.Fatalf("epoch key: %v", err)
	}

	return k
}

// TestCheckpointsEpochSelection proves that the current epoch is the
// highest epoch with a valid signature, over the copies of both layouts, and
// that a bad copy never counts: it fails the read.
func TestCheckpointsEpochSelection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		fill    func(t *testing.T, s *replacingBlobs)
		wantErr error
		name    string
		want    int64
	}{
		{name: "no epoch", want: 0, wantErr: nil, fill: func(*testing.T, *replacingBlobs) {}},
		{name: "one copy", want: 1, wantErr: nil, fill: func(t *testing.T, s *replacingBlobs) {
			t.Helper()
			s.store(t, epochCopyKey(t, 1, "a"), epochCopy(t, 7, 1))
		}},
		{
			name:    "the first layout and a newer copy",
			want:    3,
			wantErr: nil,
			fill: func(t *testing.T, s *replacingBlobs) {
				t.Helper()
				s.store(t, epochKey(t, 2), epochCopy(t, 7, 2))
				s.store(t, epochCopyKey(t, 3, "a"), epochCopy(t, 7, 3))
			},
		},
		{
			name:    "a newer epoch of the first layout",
			want:    2,
			wantErr: nil,
			fill: func(t *testing.T, s *replacingBlobs) {
				t.Helper()
				s.store(t, epochCopyKey(t, 1, "a"), epochCopy(t, 7, 1))
				s.store(t, epochKey(t, 2), epochCopy(t, 7, 2))
			},
		},
		{name: "two copies of one epoch", want: 2, wantErr: nil, fill: func(t *testing.T, s *replacingBlobs) {
			t.Helper()
			s.store(t, epochCopyKey(t, 1, "a"), epochCopy(t, 7, 1))
			s.store(t, epochCopyKey(t, 2, "a"), epochCopy(t, 7, 2))
			s.store(t, epochCopyKey(t, 2, "b"), epochCopy(t, 7, 2))
		}},
		{
			name:    "a higher epoch of another key",
			want:    0,
			wantErr: audit.ErrCheckpointMismatch,
			fill: func(t *testing.T, s *replacingBlobs) {
				t.Helper()
				s.store(t, epochCopyKey(t, 1, "a"), epochCopy(t, 7, 1))
				s.store(t, epochCopyKey(t, 9, "a"), epochCopy(t, 9, 9))
			},
		},
		{
			name:    "a bad copy beside a good copy of one epoch",
			want:    0,
			wantErr: audit.ErrCheckpointMismatch,
			fill: func(t *testing.T, s *replacingBlobs) {
				t.Helper()
				s.store(t, epochCopyKey(t, 2, "a"), epochCopy(t, 7, 2))
				s.store(t, epochCopyKey(t, 2, "b"), epochCopy(t, 9, 2))
			},
		},
		{
			name:    "a copy that holds another epoch",
			want:    0,
			wantErr: audit.ErrCheckpointMismatch,
			fill: func(t *testing.T, s *replacingBlobs) {
				t.Helper()
				s.store(t, epochCopyKey(t, 4, "a"), epochCopy(t, 7, 3))
			},
		},
		{
			name:    "a key that the server does not write",
			want:    0,
			wantErr: audit.ErrCheckpointMismatch,
			fill: func(t *testing.T, s *replacingBlobs) {
				t.Helper()
				s.store(t, epochCopyKey(t, 1, "a"), epochCopy(t, 7, 1))
				s.store(t, "audit-checkpoints/epochs/latest.json", epochCopy(t, 7, 5))
			},
		},
		{name: "epoch 0", want: 0, wantErr: audit.ErrCheckpointMismatch, fill: func(t *testing.T, s *replacingBlobs) {
			t.Helper()
			s.store(t, epochKey(t, 0), epochCopy(t, 7, 0))
		}},
		{
			name:    "too many copies of one epoch",
			want:    0,
			wantErr: audit.ErrCheckpointMismatch,
			fill: func(t *testing.T, s *replacingBlobs) {
				t.Helper()

				for i := range audit.MaxCheckpointCopies + 1 {
					k, err := audit.EpochKey(1, fmt.Sprintf("%032x", i))
					if err != nil {
						t.Fatalf("epoch key: %v", err)
					}

					s.store(t, k, epochCopy(t, 7, 1))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newReplacingFixture(t, 0)
			tc.fill(t, f.blobs)
			checkEpoch(t, f.checkpoints(), tc.want, tc.wantErr)
		})
	}
}

// checkEpoch proves that the epoch read of cps answers want, or an error
// that wraps wantErr when wantErr is not nil.
func checkEpoch(t *testing.T, cps *audit.Checkpoints, want int64, wantErr error) {
	t.Helper()

	got, err := cps.Epoch(t.Context())
	if wantErr != nil {
		if !errors.Is(err, wantErr) {
			t.Fatalf("epoch: got %d, %v, want %v", got, err, wantErr)
		}

		return
	}

	if err != nil || got != want {
		t.Fatalf("epoch: got %d, %v, want %d", got, err, want)
	}
}

// TestCheckpointsNewEpochOnTwoInstances proves that two instances that
// start the next epoch at the same moment each store a copy, both answer the
// same epoch, and the epoch read finds no mismatch.
func TestCheckpointsNewEpochOnTwoInstances(t *testing.T) {
	t.Parallel()

	const starters = 2

	f := newReplacingFixture(t, 0)
	race := &barrierBlobs{replacingBlobs: f.blobs, ready: make(chan struct{}), n: starters, mu: sync.Mutex{}}

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	type outcome struct {
		err   error
		epoch int64
	}

	results := make(chan outcome, starters)

	for range starters {
		cps := audit.NewCheckpoints(audit.CheckpointsConfig{
			Blobs: race, Entries: f.entries, Signer: f.signer, Marks: nil, Now: nil,
		})

		go func() {
			epoch, err := cps.NewEpoch(ctx, time.Unix(1_700_000_400, 0))
			results <- outcome{err: err, epoch: epoch}
		}()
	}

	for range starters {
		if r := <-results; r.err != nil || r.epoch != 1 {
			t.Fatalf("racing new epoch: got %d, %v, want 1", r.epoch, r.err)
		}
	}

	objects, err := f.blobs.List(t.Context(), audit.CheckpointPrefix+"/epochs/")
	if err != nil || len(objects) != starters {
		t.Fatalf("epoch copies: got %d, %v, want %d", len(objects), err, starters)
	}

	if epoch, err := f.checkpoints().Epoch(t.Context()); err != nil || epoch != 1 {
		t.Fatalf("epoch: got %d, %v, want 1", epoch, err)
	}

	if epoch, err := f.checkpoints().NewEpoch(t.Context(), time.Unix(1_700_000_500, 0)); err != nil || epoch != 2 {
		t.Fatalf("next epoch: got %d, %v, want 2", epoch, err)
	}
}
