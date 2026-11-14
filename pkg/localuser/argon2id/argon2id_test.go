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

	waitForWaiting(t, b, 1)

	release()
	wg.Wait()

	if err := <-errs; err != nil {
		t.Fatalf("acquire after a release: %v", err)
	}
}

// TestBudgetGrantAndCancelAtOnceLeaksNoMemory checks the race where the budget
// grants a waiter its memory at the same time as the context of the waiter
// ends: the memory comes back either way, so the budget holds nothing and no
// waiter stays after each round.
func TestBudgetGrantAndCancelAtOnceLeaksNoMemory(t *testing.T) {
	t.Parallel()

	b, err := argon2id.NewBudgetWithWait(localuser.MinHashMemoryBudgetKiB, 5*time.Second)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	for round := 1; round <= 500; round++ {
		hold, err := b.Acquire(t.Context(), localuser.MaxMemoryKiB)
		if err != nil {
			t.Fatalf("round %d: acquire the hold: %v", round, err)
		}

		ctx, cancel := context.WithCancel(t.Context())

		waiter := acquireAsync(ctx, b, localuser.MinMemoryKiB)
		waitForWaiting(t, b, 1)

		// End the context first, so the waiter meets the end of the context
		// and the grant of the release together.
		cancel()
		hold()

		got := waitForAcquired(t, waiter)
		if got.err == nil {
			got.release()
		}

		if inUse := b.InUse(); inUse != 0 {
			t.Fatalf("round %d: in use = %d KiB, want 0", round, inUse)
		}

		if waiting := b.Waiting(); waiting != 0 {
			t.Fatalf("round %d: waiting = %d, want 0", round, waiting)
		}
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

// TestReserveOrderOfASignIn shows the order of a password sign-in on the
// port: the failure of the name counts after the hash has its budget and
// before the compare. With no room, Reserve fails with ErrHashBusy and the
// sign-in counts nothing. With room, the count happens while the
// reservation holds the memory, before Verify.
func TestReserveOrderOfASignIn(t *testing.T) {
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

	failures := 0
	signIn := func(password string) error {
		r, reserveErr := h.Reserve(t.Context(), stored)
		if reserveErr != nil {
			return reserveErr
		}
		defer r.Release()

		if got := b.InUse(); got != localuser.MinMemoryKiB {
			t.Errorf("in use when the failure counts = %d KiB, want %d: the hash holds its budget", got,
				localuser.MinMemoryKiB)
		}

		failures++

		_, verifyErr := r.Verify(password, p)

		return verifyErr
	}

	full, err := b.Acquire(t.Context(), localuser.MaxMemoryKiB)
	if err != nil {
		t.Fatalf("fill the budget: %v", err)
	}

	if err := signIn("a password"); !errors.Is(err, localuser.ErrHashBusy) {
		t.Fatalf("sign-in on a full budget: err = %v, want ErrHashBusy", err)
	}

	if failures != 0 {
		t.Fatalf("failures after ErrHashBusy = %d, want 0", failures)
	}

	full()

	if err := signIn("a wrong password"); !errors.Is(err, localuser.ErrWrongPassword) {
		t.Fatalf("sign-in with a wrong password: err = %v, want ErrWrongPassword", err)
	}

	if failures != 1 {
		t.Fatalf("failures after one wrong password = %d, want 1", failures)
	}

	if got := b.InUse(); got != 0 {
		t.Fatalf("in use after the sign-in = %d KiB, want 0", got)
	}
}

// TestReserveTakesNoBudgetForAMalformedHash checks that Reserve parses the
// hash before it waits for the budget.
func TestReserveTakesNoBudgetForAMalformedHash(t *testing.T) {
	t.Parallel()

	b, err := argon2id.NewBudgetWithWait(localuser.MinHashMemoryBudgetKiB, time.Hour)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	h, err := argon2id.New(b)
	if err != nil {
		t.Fatalf("new hasher: %v", err)
	}

	full, err := b.Acquire(t.Context(), localuser.MaxMemoryKiB)
	if err != nil {
		t.Fatalf("fill the budget: %v", err)
	}
	defer full()

	// With a wait of one hour, a Reserve that waited for the budget would
	// not answer within the test.
	if _, err := h.Reserve(t.Context(), "$argon2id$v=19$bad"); !errors.Is(err, localuser.ErrInvalid) {
		t.Fatalf("reserve a malformed hash on a full budget: err = %v, want ErrInvalid", err)
	}
}

// acquired is the result of one Acquire call that runs in its own goroutine.
type acquired struct {
	release func()
	err     error
}

// acquireAsync starts one goroutine that calls b.Acquire and sends the result
// on the returned channel.
func acquireAsync(ctx context.Context, b *argon2id.Budget, kib uint32) <-chan acquired {
	out := make(chan acquired, 1)

	go func() {
		release, err := b.Acquire(ctx, kib)
		out <- acquired{release: release, err: err}
	}()

	return out
}

// waitForAcquired reads the result of an acquireAsync call, and fails the
// test when the call does not return within 5 seconds.
func waitForAcquired(t *testing.T, ch <-chan acquired) acquired {
	t.Helper()

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	select {
	case got := <-ch:
		return got
	case <-timer.C:
		t.Fatal("acquire did not return within 5 seconds")

		return acquired{release: nil, err: nil}
	}
}

// waitForWaiting checks b.Waiting every millisecond until it is n. It fails
// the test after 5 seconds or when the test ends.
func waitForWaiting(t *testing.T, b *argon2id.Budget, n int) {
	t.Helper()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		if b.Waiting() == n {
			return
		}

		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("waiting = %d after 5 seconds, want %d", b.Waiting(), n)
		case <-t.Context().Done():
			t.Fatalf("test ended while waiting for %d waiters: %v", n, t.Context().Err())
		}
	}
}

func TestBudgetServesWaitersInOrder(t *testing.T) {
	t.Parallel()

	b, err := argon2id.NewBudgetWithWait(localuser.MinHashMemoryBudgetKiB, 5*time.Second)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	hold, err := b.Acquire(t.Context(), localuser.MaxMemoryKiB)
	if err != nil {
		t.Fatalf("acquire the hold: %v", err)
	}

	large := acquireAsync(t.Context(), b, localuser.MaxMemoryKiB)
	waitForWaiting(t, b, 1)

	small := acquireAsync(t.Context(), b, localuser.MinMemoryKiB)
	waitForWaiting(t, b, 2)

	hold()

	gotLarge := waitForAcquired(t, large)
	if gotLarge.err != nil {
		t.Fatalf("large waiter: %v", gotLarge.err)
	}

	if b.InUse() != localuser.MaxMemoryKiB {
		t.Errorf("in use after the large waiter got the memory = %d, want %d", b.InUse(), localuser.MaxMemoryKiB)
	}

	if b.Waiting() != 1 {
		t.Errorf("waiting after the large waiter got the memory = %d, want 1", b.Waiting())
	}

	gotLarge.release()

	gotSmall := waitForAcquired(t, small)
	if gotSmall.err != nil {
		t.Fatalf("small waiter: %v", gotSmall.err)
	}

	if b.InUse() != localuser.MinMemoryKiB {
		t.Errorf("in use after the small waiter got the memory = %d, want %d", b.InUse(), localuser.MinMemoryKiB)
	}

	gotSmall.release()

	if b.InUse() != 0 {
		t.Errorf("in use at the end = %d, want 0", b.InUse())
	}
}

func TestBudgetNewAcquireDoesNotPassAWaiter(t *testing.T) {
	t.Parallel()

	b, err := argon2id.NewBudgetWithWait(localuser.MinHashMemoryBudgetKiB, 5*time.Second)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	hold, err := b.Acquire(t.Context(), localuser.MaxMemoryKiB)
	if err != nil {
		t.Fatalf("acquire the hold: %v", err)
	}

	first := acquireAsync(t.Context(), b, localuser.DefaultMemoryKiB)
	waitForWaiting(t, b, 1)

	second := acquireAsync(t.Context(), b, localuser.DefaultMemoryKiB)
	waitForWaiting(t, b, 2)

	large := acquireAsync(t.Context(), b, localuser.MaxMemoryKiB)
	waitForWaiting(t, b, 3)

	hold()

	gotFirst := waitForAcquired(t, first)
	if gotFirst.err != nil {
		t.Fatalf("first default waiter: %v", gotFirst.err)
	}

	gotSecond := waitForAcquired(t, second)
	if gotSecond.err != nil {
		t.Fatalf("second default waiter: %v", gotSecond.err)
	}

	// The free memory is now 26624 KiB, which fits a small hash. The large
	// waiter at the head of the list still waits, so the new call must join
	// the list and must not take the memory.
	inUse := b.InUse()

	newcomer := acquireAsync(t.Context(), b, localuser.MinMemoryKiB)
	waitForWaiting(t, b, 2)

	select {
	case got := <-newcomer:
		t.Fatalf("new acquire returned while a waiter is in the list: err = %v", got.err)
	default:
	}

	if b.InUse() != inUse {
		t.Errorf("in use after the new acquire joins = %d, want %d", b.InUse(), inUse)
	}

	gotFirst.release()
	gotSecond.release()

	gotLarge := waitForAcquired(t, large)
	if gotLarge.err != nil {
		t.Fatalf("large waiter: %v", gotLarge.err)
	}

	gotLarge.release()

	gotNewcomer := waitForAcquired(t, newcomer)
	if gotNewcomer.err != nil {
		t.Fatalf("new acquire: %v", gotNewcomer.err)
	}

	gotNewcomer.release()

	if b.InUse() != 0 {
		t.Errorf("in use at the end = %d, want 0", b.InUse())
	}
}

func TestBudgetReleaseServesOnlyWhatFits(t *testing.T) {
	t.Parallel()

	b, err := argon2id.NewBudgetWithWait(localuser.MinHashMemoryBudgetKiB, 5*time.Second)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	hold, err := b.Acquire(t.Context(), localuser.MaxMemoryKiB)
	if err != nil {
		t.Fatalf("acquire the hold: %v", err)
	}

	first := acquireAsync(t.Context(), b, localuser.DefaultMemoryKiB)
	waitForWaiting(t, b, 1)

	second := acquireAsync(t.Context(), b, localuser.DefaultMemoryKiB)
	waitForWaiting(t, b, 2)

	large := acquireAsync(t.Context(), b, localuser.MaxMemoryKiB)
	waitForWaiting(t, b, 3)

	hold()

	gotFirst := waitForAcquired(t, first)
	if gotFirst.err != nil {
		t.Fatalf("first default waiter: %v", gotFirst.err)
	}

	gotSecond := waitForAcquired(t, second)
	if gotSecond.err != nil {
		t.Fatalf("second default waiter: %v", gotSecond.err)
	}

	if b.InUse() != 2*localuser.DefaultMemoryKiB {
		t.Errorf("in use = %d, want %d", b.InUse(), 2*localuser.DefaultMemoryKiB)
	}

	if b.Waiting() != 1 {
		t.Errorf("waiting = %d, want 1", b.Waiting())
	}

	gotFirst.release()
	gotSecond.release()

	gotLarge := waitForAcquired(t, large)
	if gotLarge.err != nil {
		t.Fatalf("large waiter: %v", gotLarge.err)
	}

	if b.InUse() != localuser.MaxMemoryKiB {
		t.Errorf("in use after the large waiter got the memory = %d, want %d", b.InUse(), localuser.MaxMemoryKiB)
	}

	gotLarge.release()

	if b.InUse() != 0 {
		t.Errorf("in use at the end = %d, want 0", b.InUse())
	}
}

func TestBudgetWaiterThatLeavesDoesNotBlock(t *testing.T) {
	t.Parallel()

	b, err := argon2id.NewBudgetWithWait(localuser.MinHashMemoryBudgetKiB, 5*time.Second)
	if err != nil {
		t.Fatalf("new budget: %v", err)
	}

	hold, err := b.Acquire(t.Context(), localuser.MinHashMemoryBudgetKiB-localuser.DefaultMemoryKiB)
	if err != nil {
		t.Fatalf("acquire the hold: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	large := acquireAsync(ctx, b, localuser.MaxMemoryKiB)
	waitForWaiting(t, b, 1)

	small := acquireAsync(t.Context(), b, localuser.DefaultMemoryKiB)
	waitForWaiting(t, b, 2)

	cancel()

	gotLarge := waitForAcquired(t, large)
	if !errors.Is(gotLarge.err, context.Canceled) {
		t.Fatalf("large waiter after cancel: err = %v, want context.Canceled", gotLarge.err)
	}

	gotSmall := waitForAcquired(t, small)
	if gotSmall.err != nil {
		t.Fatalf("small waiter behind the large one: %v", gotSmall.err)
	}

	if b.InUse() != localuser.MinHashMemoryBudgetKiB {
		t.Errorf("in use = %d, want %d", b.InUse(), localuser.MinHashMemoryBudgetKiB)
	}

	if b.Waiting() != 0 {
		t.Errorf("waiting = %d, want 0", b.Waiting())
	}

	gotSmall.release()
	hold()

	if b.InUse() != 0 {
		t.Errorf("in use at the end = %d, want 0", b.InUse())
	}
}
