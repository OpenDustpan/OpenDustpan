package argon2id_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/argon2id"
	"opendustpan.org/opendustpan/pkg/localuser/localusertest"
)

func newHasher(t *testing.T) *argon2id.Hasher {
	t.Helper()

	b, err := argon2id.NewBudget(localuser.DefaultHashMemoryBudgetKiB)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	h, err := argon2id.New(b)
	if err != nil {
		t.Fatalf("new hasher: %v", err)
	}

	return h
}

func TestHasherConformance(t *testing.T) {
	t.Parallel()

	localusertest.RunHasher(t, func(t *testing.T) localuser.PasswordHasher {
		t.Helper()

		return newHasher(t)
	})
}

// TestDefaultHashVerifies hashes and verifies with the default parameters,
// which the conformance cases do not use because of their cost.
func TestDefaultHashVerifies(t *testing.T) {
	t.Parallel()

	h := newHasher(t)

	stored, err := h.Hash(t.Context(), "a password of the defaults", localuser.DefaultHashParams())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if !strings.HasPrefix(string(stored), "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %q does not start with the default parameters", stored)
	}

	v, err := h.Verify(t.Context(), "a password of the defaults", stored, localuser.DefaultHashParams())
	if err != nil || v.NeedsRehash {
		t.Fatalf("verify = %+v, %v; want no rehash and no error", v, err)
	}
}

// TestReferenceVector checks the key that the adapter derives against a
// vector of the reference implementation, so that the adapter computes
// Argon2id and not another function. The vector has a salt of 8 bytes, which
// the bounds refuse in a stored hash, so the test derives the key directly.
func TestReferenceVector(t *testing.T) {
	t.Parallel()

	// From the test suite of the reference implementation
	// (github.com/P-H-C/phc-winner-argon2, src/test.c): Argon2id, version
	// 19, t=2, m=65536 KiB, p=1, password "password", salt "somesalt".
	const want = "$argon2id$v=19$m=65536,t=2,p=1$c29tZXNhbHQ$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPc"

	d, err := argon2id.Decode(want)
	if !errors.Is(err, localuser.ErrInvalid) {
		// The salt "somesalt" has 8 bytes, below MinSaltBytes, so the
		// adapter refuses the vector as a stored hash. Derive it directly.
		t.Fatalf("decode a hash with an 8-byte salt: %+v, %v; want ErrInvalid", d, err)
	}

	got := deriveWithSalt(t, "password", []byte("somesalt"), localuser.HashParams{
		Algorithm: localuser.AlgorithmArgon2id, MemoryKiB: 65536, Iterations: 2, Parallelism: 1,
		SaltBytes: 8, KeyBytes: 32,
	})

	enc := string(argon2id.Encode(localuser.HashParams{
		Algorithm: localuser.AlgorithmArgon2id, MemoryKiB: 65536, Iterations: 2, Parallelism: 1,
		SaltBytes: 8, KeyBytes: 32,
	}, []byte("somesalt"), got))
	if enc != want {
		t.Fatalf("derived %q, want %q", enc, want)
	}
}

func deriveWithSalt(t *testing.T, password string, salt []byte, p localuser.HashParams) []byte {
	t.Helper()

	return argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyBytes)
}

func TestDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	p := localuser.HashParams{
		Algorithm: localuser.AlgorithmArgon2id, MemoryKiB: 19456, Iterations: 2, Parallelism: 1,
		SaltBytes: 16, KeyBytes: 32,
	}
	salt := bytes.Repeat([]byte{0xa5}, 16)
	key := bytes.Repeat([]byte{0x5a}, 32)

	d, err := argon2id.Decode(argon2id.Encode(p, salt, key))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if d.Params != p || !bytes.Equal(d.Salt, salt) || !bytes.Equal(d.Key, key) {
		t.Fatalf("decode = %+v, want %+v %x %x", d, p, salt, key)
	}
}

// TestDecodeRefusesLongInput is the boundary layer: a stored hash above
// MaxEncodedLength is refused before the parser reads its fields.
func TestDecodeRefusesLongInput(t *testing.T) {
	t.Parallel()

	long := localuser.PasswordHash("$argon2id$v=19$m=7168,t=1,p=1$" + strings.Repeat("A", argon2id.MaxEncodedLength))

	_, err := argon2id.Decode(long)
	if !errors.Is(err, localuser.ErrInvalid) || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("decode a long hash: err = %v, want ErrInvalid that names the length", err)
	}
}

// TestDecodeRefusesNewlineInBase64 checks the alphabet check: the base64
// decoder of the standard library skips CR and LF even in strict mode.
func TestDecodeRefusesNewlineInBase64(t *testing.T) {
	t.Parallel()

	salt := strings.Repeat("A", 11) + "\n" + strings.Repeat("A", 11)
	key := strings.Repeat("A", 43)

	for _, s := range []string{
		"$argon2id$v=19$m=7168,t=1,p=1$" + salt + "$" + key,
		"$argon2id$v=19$m=7168,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + key[:20] + "\r" + key[20:],
	} {
		if _, err := argon2id.Decode(localuser.PasswordHash(s)); !errors.Is(err, localuser.ErrInvalid) {
			t.Errorf("decode %q: err = %v, want ErrInvalid", s, err)
		}
	}
}

func TestNewRefusesNilBudget(t *testing.T) {
	t.Parallel()

	if _, err := argon2id.New(nil); !errors.Is(err, localuser.ErrInvalid) {
		t.Fatalf("new with no budget: err = %v, want ErrInvalid", err)
	}
}

func TestBudgetBounds(t *testing.T) {
	t.Parallel()

	if _, err := argon2id.NewBudget(localuser.MinHashMemoryBudgetKiB - 1); !errors.Is(err, localuser.ErrInvalid) {
		t.Errorf("budget below the minimum: err = %v, want ErrInvalid", err)
	}

	b, err := argon2id.NewBudget(localuser.MinHashMemoryBudgetKiB)
	if err != nil {
		t.Fatalf("budget at the minimum: %v", err)
	}

	for _, kib := range []uint32{0, localuser.MinHashMemoryBudgetKiB + 1} {
		if _, acquireErr := b.Acquire(t.Context(), kib); !errors.Is(acquireErr, localuser.ErrInvalid) {
			t.Errorf("acquire %d KiB: err = %v, want ErrInvalid", kib, acquireErr)
		}
	}

	release, err := b.Acquire(t.Context(), localuser.MaxMemoryKiB)
	if err != nil {
		t.Fatalf("acquire the largest valid hash: %v", err)
	}

	release()
}

// TestBudgetDefaultHoldsFourDefaultHashes checks the default numbers: four
// default hashes fit in the default budget, and a fifth waits.
func TestBudgetDefaultHoldsFourDefaultHashes(t *testing.T) {
	t.Parallel()

	b, err := argon2id.NewBudgetWithWait(localuser.DefaultHashMemoryBudgetKiB, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	releases := make([]func(), 0, 4)

	for i := range 4 {
		release, err := b.Acquire(t.Context(), localuser.DefaultMemoryKiB)
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}

		releases = append(releases, release)
	}

	if _, err := b.Acquire(t.Context(), localuser.DefaultMemoryKiB); !errors.Is(err, localuser.ErrHashBusy) {
		t.Fatalf("acquire 5: err = %v, want ErrHashBusy", err)
	}

	for _, release := range releases {
		release()
	}

	if got := b.InUse(); got != 0 {
		t.Fatalf("in use after each release = %d KiB, want 0", got)
	}
}

// TestBudgetWaitsForRoom checks that a hash that waits receives the memory
// that another hash gives back within the wait.
func TestBudgetWaitsForRoom(t *testing.T) {
	t.Parallel()

	b, err := argon2id.NewBudgetWithWait(localuser.MinHashMemoryBudgetKiB, 5*time.Second)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	release, err := b.Acquire(t.Context(), localuser.MaxMemoryKiB)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	var wg sync.WaitGroup

	errs := make(chan error, 1)

	wg.Go(func() {
		r, err := b.Acquire(context.Background(), localuser.MinMemoryKiB)
		if err == nil {
			r()
		}

		errs <- err
	})

	// Give the waiter time to find the budget full before the release.
	// The test passes in either order.
	select {
	case <-time.After(20 * time.Millisecond):
	case <-t.Context().Done():
	}

	release()
	wg.Wait()

	if err := <-errs; err != nil {
		t.Fatalf("acquire after a release: %v", err)
	}
}

// TestBudgetReleaseOnce checks that a second release gives back nothing.
func TestBudgetReleaseOnce(t *testing.T) {
	t.Parallel()

	b, err := argon2id.NewBudget(localuser.MinHashMemoryBudgetKiB)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	first, err := b.Acquire(t.Context(), localuser.MinMemoryKiB)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	second, err := b.Acquire(t.Context(), localuser.MinMemoryKiB)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	first()
	first()

	if got := b.InUse(); got != localuser.MinMemoryKiB {
		t.Fatalf("in use = %d KiB, want %d", got, localuser.MinMemoryKiB)
	}

	second()
}

// TestBudgetContextEnds checks that a wait ends with the context.
func TestBudgetContextEnds(t *testing.T) {
	t.Parallel()

	b, err := argon2id.NewBudget(localuser.MinHashMemoryBudgetKiB)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	release, err := b.Acquire(t.Context(), localuser.MaxMemoryKiB)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := b.Acquire(ctx, localuser.MinMemoryKiB); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire with an ended context: err = %v, want context.Canceled", err)
	}
}

// TestHashBusy is the limit layer: a hash that finds no room in the budget
// fails with ErrHashBusy and does no work, for Hash and for Verify.
func TestHashBusy(t *testing.T) {
	t.Parallel()

	b, err := argon2id.NewBudgetWithWait(localuser.MinHashMemoryBudgetKiB, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	h, err := argon2id.New(b)
	if err != nil {
		t.Fatalf("new hasher: %v", err)
	}

	p := localuser.HashParams{
		Algorithm: localuser.AlgorithmArgon2id, MemoryKiB: localuser.MinMemoryKiB, Iterations: 1, Parallelism: 1,
		SaltBytes: 16, KeyBytes: 32,
	}

	stored, err := h.Hash(t.Context(), "a password", p)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	release, err := b.Acquire(t.Context(), localuser.MaxMemoryKiB)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	if _, err := h.Hash(t.Context(), "a password", p); !errors.Is(err, localuser.ErrHashBusy) {
		t.Errorf("hash on a full budget: err = %v, want ErrHashBusy", err)
	}

	if _, err := h.Verify(t.Context(), "a password", stored, p); !errors.Is(err, localuser.ErrHashBusy) {
		t.Errorf("verify on a full budget: err = %v, want ErrHashBusy", err)
	}
}

// FuzzDecode sends arbitrary stored hashes to the parser. It must never
// panic, and each hash that it accepts must hold parameters inside the
// bounds and encode back to the same string.
func FuzzDecode(f *testing.F) {
	seeds := []string{
		"$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=65536,t=16,p=4$" + strings.Repeat("A", 86) + "$" + strings.Repeat("A", 86),
		"$argon2id$v=19$m=19456,t=2,p=0$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"$argon2i$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=4194304,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"",
		"$",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		d, err := argon2id.Decode(localuser.PasswordHash(s))
		if err != nil {
			if !errors.Is(err, localuser.ErrInvalid) {
				t.Fatalf("decode %q: err = %v, want ErrInvalid", s, err)
			}

			return
		}

		if verr := d.Params.Validate(); verr != nil {
			t.Fatalf("decode %q accepted parameters %+v outside the bounds: %v", s, d.Params, verr)
		}

		if again := argon2id.Encode(d.Params, d.Salt, d.Key); string(again) != s {
			t.Fatalf("decode %q, encode = %q; want the same string", s, again)
		}
	})
}
