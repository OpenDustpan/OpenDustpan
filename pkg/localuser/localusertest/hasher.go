package localusertest

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/localuser"
)

// fastParams are the cheapest parameters inside the bounds, so that the
// cases run fast.
func fastParams() localuser.HashParams {
	return localuser.HashParams{
		Algorithm:   localuser.AlgorithmArgon2id,
		MemoryKiB:   localuser.MinMemoryKiB,
		Iterations:  localuser.MinIterations,
		Parallelism: localuser.MinParallelism,
		SaltBytes:   localuser.MinSaltBytes,
		KeyBytes:    localuser.MinKeyBytes,
	}
}

// hasherPassword is a password that the hasher cases use.
const hasherPassword = "correct horse battery staple"

// workBound is the time that a hash of the bounds of the "before any work"
// cases must stay under. A hash with the parameters of those cases takes
// far longer than this, or does not end.
const workBound = 5 * time.Second

// phc returns a PHC string of argon2id version 19 with the parameter list
// params, a salt of saltBytes zero bytes and a key of keyBytes zero bytes.
func phc(params string, saltBytes, keyBytes int) localuser.PasswordHash {
	enc := base64.RawStdEncoding

	return localuser.PasswordHash("$argon2id$v=19$" + params +
		"$" + enc.EncodeToString(make([]byte, saltBytes)) +
		"$" + enc.EncodeToString(make([]byte, keyBytes)))
}

func hasherCases() []struct {
	test func(t *testing.T, h localuser.PasswordHasher)
	name string
} {
	return []struct {
		test func(t *testing.T, h localuser.PasswordHasher)
		name string
	}{
		{name: "a hash verifies with the same password", test: testHashVerifies},
		{name: "a hash does not verify with another password: ErrWrongPassword", test: testHashWrongPassword},
		{name: "two hashes of one password have different salts", test: testHashSalts},
		{name: "a hash holds its algorithm and its parameters in the PHC string", test: testHashForm},
		{name: "a hash with other parameters than the current ones verifies with NeedsRehash", test: testHashRehash},
		{
			name: "a hash with parameters out of the bounds fails with ErrInvalid before any work",
			test: testHashOutOfBounds,
		},
		{name: "parameters out of the bounds for a new hash fail with ErrInvalid", test: testHashNewOutOfBounds},
		{name: "a password above MaxBytes fails with ErrPasswordTooLong", test: testHashTooLong},
		{name: "a password that differs in its last byte does not verify", test: testHashLastByte},
		{name: "a malformed hash fails with ErrInvalid and does not panic", test: testHashMalformed},
		{name: "a hash with p=0 fails with ErrInvalid and does not panic", test: testHashZeroLanes},
		{
			name: "a hash with a memory cost above MaxMemoryKiB fails with ErrInvalid before any work",
			test: testHashMemoryAbove,
		},
		{name: "a hash of another algorithm fails with ErrInvalid", test: testHashOtherAlgorithm},
	}
}

func mustHash(
	t *testing.T,
	h localuser.PasswordHasher,
	password string,
	p localuser.HashParams,
) localuser.PasswordHash {
	t.Helper()

	got, err := h.Hash(t.Context(), password, p)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	return got
}

func testHashVerifies(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	stored := mustHash(t, h, hasherPassword, fastParams())

	v, err := h.Verify(t.Context(), hasherPassword, stored, fastParams())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	if v.NeedsRehash {
		t.Error("verify with the parameters of the hash: NeedsRehash = true, want false")
	}
}

func testHashWrongPassword(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	stored := mustHash(t, h, hasherPassword, fastParams())

	_, err := h.Verify(t.Context(), hasherPassword+"!", stored, fastParams())
	if !errors.Is(err, localuser.ErrWrongPassword) {
		t.Fatalf("verify another password: err = %v, want ErrWrongPassword", err)
	}
}

func testHashSalts(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	a := mustHash(t, h, hasherPassword, fastParams())
	b := mustHash(t, h, hasherPassword, fastParams())

	if a == b {
		t.Fatal("two hashes of one password are equal, want different salts")
	}

	saltOf := func(s localuser.PasswordHash) string {
		parts := strings.Split(string(s), "$")
		if len(parts) != 6 {
			t.Fatalf("hash %q has %d fields, want 6", s, len(parts))
		}

		return parts[4]
	}

	if saltOf(a) == saltOf(b) {
		t.Fatal("two hashes of one password have the same salt")
	}
}

func testHashForm(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	p := fastParams()
	p.Iterations = 3
	p.Parallelism = 2
	p.SaltBytes = 24
	p.KeyBytes = 40

	stored := mustHash(t, h, hasherPassword, p)

	parts := strings.Split(string(stored), "$")
	if len(parts) != 6 || parts[0] != "" {
		t.Fatalf("hash %q is not a PHC string of 5 fields", stored)
	}

	if parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=7168,t=3,p=2" {
		t.Fatalf("hash %q: want argon2id, v=19 and m=7168,t=3,p=2", stored)
	}

	enc := base64.RawStdEncoding

	if n := enc.DecodedLen(len(parts[4])); n != 24 {
		t.Errorf("salt of %d bytes, want 24", n)
	}

	if n := enc.DecodedLen(len(parts[5])); n != 40 {
		t.Errorf("key of %d bytes, want 40", n)
	}
}

func testHashRehash(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	stored := mustHash(t, h, hasherPassword, fastParams())

	changes := []func(p *localuser.HashParams){
		func(p *localuser.HashParams) { p.Iterations++ },
		func(p *localuser.HashParams) { p.MemoryKiB++ },
		func(p *localuser.HashParams) { p.Parallelism++ },
		func(p *localuser.HashParams) { p.SaltBytes++ },
		func(p *localuser.HashParams) { p.KeyBytes++ },
	}

	for i, change := range changes {
		current := fastParams()
		change(&current)

		v, err := h.Verify(t.Context(), hasherPassword, stored, current)
		if err != nil {
			t.Fatalf("change %d: verify: %v", i, err)
		}

		if !v.NeedsRehash {
			t.Errorf("change %d: NeedsRehash = false, want true", i)
		}
	}
}

// withinBound runs fn and fails the test when it takes longer than
// workBound. A hasher that does the work of the hash takes much longer.
func withinBound(t *testing.T, what string, fn func(ctx context.Context) error) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), workBound)
	defer cancel()

	start := time.Now()
	err := fn(ctx)

	if d := time.Since(start); d > workBound {
		t.Errorf("%s took %s, want an answer before any hash work", what, d)
	}

	return err
}

func testHashOutOfBounds(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	// Each hash names a cost that takes minutes or more, or a value that
	// the bounds refuse. Only a check before the work answers in time.
	hostile := []localuser.PasswordHash{
		phc("m=65536,t=99999,p=1", 16, 32),
		phc("m=65536,t=17,p=1", 16, 32),
		phc("m=65536,t=0,p=1", 16, 32),
		phc("m=7167,t=1,p=1", 16, 32),
		phc("m=65536,t=2,p=5", 16, 32),
		phc("m=65536,t=2,p=255", 16, 32),
		phc("m=19456,t=2,p=1", 15, 32),
		phc("m=19456,t=2,p=1", 65, 32),
		phc("m=19456,t=2,p=1", 16, 31),
		phc("m=19456,t=2,p=1", 16, 65),
	}

	for _, stored := range hostile {
		err := withinBound(t, string(stored), func(ctx context.Context) error {
			_, err := h.Verify(ctx, hasherPassword, stored, fastParams())

			return err
		})
		if !errors.Is(err, localuser.ErrInvalid) {
			t.Errorf("verify %q: err = %v, want ErrInvalid", stored, err)
		}
	}
}

func testHashNewOutOfBounds(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	changes := []func(p *localuser.HashParams){
		func(p *localuser.HashParams) { p.Algorithm = "argon2i" },
		func(p *localuser.HashParams) { p.Algorithm = "" },
		func(p *localuser.HashParams) { p.MemoryKiB = localuser.MinMemoryKiB - 1 },
		func(p *localuser.HashParams) { p.MemoryKiB = localuser.MaxMemoryKiB + 1 },
		func(p *localuser.HashParams) { p.Iterations = 0 },
		func(p *localuser.HashParams) { p.Iterations = localuser.MaxIterations + 1 },
		func(p *localuser.HashParams) { p.Parallelism = 0 },
		func(p *localuser.HashParams) { p.Parallelism = localuser.MaxParallelism + 1 },
		func(p *localuser.HashParams) { p.SaltBytes = localuser.MinSaltBytes - 1 },
		func(p *localuser.HashParams) { p.SaltBytes = localuser.MaxSaltBytes + 1 },
		func(p *localuser.HashParams) { p.KeyBytes = localuser.MinKeyBytes - 1 },
		func(p *localuser.HashParams) { p.KeyBytes = localuser.MaxKeyBytes + 1 },
	}

	for i, change := range changes {
		p := fastParams()
		change(&p)

		err := withinBound(t, "hash", func(ctx context.Context) error {
			_, err := h.Hash(ctx, hasherPassword, p)

			return err
		})
		if !errors.Is(err, localuser.ErrInvalid) {
			t.Errorf("change %d: hash: err = %v, want ErrInvalid", i, err)
		}

		stored := mustHash(t, h, hasherPassword, fastParams())

		_, err = h.Verify(t.Context(), hasherPassword, stored, p)
		if !errors.Is(err, localuser.ErrInvalid) {
			t.Errorf("change %d: verify with these current parameters: err = %v, want ErrInvalid", i, err)
		}
	}
}

func testHashTooLong(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	long := strings.Repeat("a", localuser.MaxBytes+1)

	if _, err := h.Hash(t.Context(), long, fastParams()); !errors.Is(err, localuser.ErrPasswordTooLong) {
		t.Errorf("hash: err = %v, want ErrPasswordTooLong", err)
	}

	stored := mustHash(t, h, strings.Repeat("a", localuser.MaxBytes), fastParams())

	if _, err := h.Verify(t.Context(), long, stored, fastParams()); !errors.Is(err, localuser.ErrPasswordTooLong) {
		t.Errorf("verify: err = %v, want ErrPasswordTooLong", err)
	}
}

func testHashLastByte(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	// A hash function that reads a prefix only (bcrypt reads 72 bytes)
	// fails this case.
	base := strings.Repeat("x", localuser.MaxBytes-1)
	stored := mustHash(t, h, base+"a", fastParams())

	if _, err := h.Verify(t.Context(), base+"b", stored, fastParams()); !errors.Is(err, localuser.ErrWrongPassword) {
		t.Fatalf("verify a password that differs in its last byte: err = %v, want ErrWrongPassword", err)
	}

	if _, err := h.Verify(t.Context(), base, stored, fastParams()); !errors.Is(err, localuser.ErrWrongPassword) {
		t.Fatalf("verify a password without its last byte: err = %v, want ErrWrongPassword", err)
	}
}

func testHashMalformed(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	good := string(mustHash(t, h, hasherPassword, fastParams()))

	hostile := []string{
		"",
		"$",
		"$$$$$",
		"$argon2id",
		"$argon2id$v=19",
		"$argon2id$v=19$m=7168,t=1,p=1",
		"$argon2id$v=19$m=7168,t=1,p=1$",
		"$argon2id$v=19$m=7168,t=1,p=1$$",
		"$argon2id$v=16$m=7168,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"$argon2id$v=19$t=1,m=7168,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=7168,t=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=7168,t=1,p=1,k=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=07168,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=+7168,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=-1,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=99999999999999999999,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=7168,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA==$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=7168,t=1,p=1$AAAAAAAAAAAAAAAAAAAA*A$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=7168,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43) + "$",
		"$argon2id$v=19$m=7168,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43) + "\n",
		" " + good,
		good + " ",
		good + "$",
		strings.ToUpper(good),
		good[:len(good)-1],
		strings.Repeat("$argon2id", 1000),
		"\x00",
		"\xff\xfe",
	}

	for _, s := range hostile {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("verify %q panics: %v", s, r)
				}
			}()

			_, err := h.Verify(t.Context(), hasherPassword, localuser.PasswordHash(s), fastParams())
			if !errors.Is(err, localuser.ErrInvalid) {
				t.Errorf("verify %q: err = %v, want ErrInvalid", s, err)
			}
		}()
	}
}

func testHashZeroLanes(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("verify a hash with p=0 panics: %v", r)
		}
	}()

	_, err := h.Verify(t.Context(), hasherPassword, phc("m=19456,t=2,p=0", 16, 32), fastParams())
	if !errors.Is(err, localuser.ErrInvalid) {
		t.Fatalf("verify a hash with p=0: err = %v, want ErrInvalid", err)
	}
}

func testHashMemoryAbove(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	for _, params := range []string{"m=65537,t=1,p=1", "m=99999,t=1,p=1", "m=4194304,t=1,p=1"} {
		stored := phc(params, 16, 32)

		err := withinBound(t, string(stored), func(ctx context.Context) error {
			_, err := h.Verify(ctx, hasherPassword, stored, fastParams())

			return err
		})
		if !errors.Is(err, localuser.ErrInvalid) {
			t.Errorf("verify %q: err = %v, want ErrInvalid", stored, err)
		}
	}
}

func testHashOtherAlgorithm(t *testing.T, h localuser.PasswordHasher) {
	t.Helper()

	enc := base64.RawStdEncoding
	tail := "$" + enc.EncodeToString(make([]byte, 16)) + "$" + enc.EncodeToString(make([]byte, 32))

	hostile := []localuser.PasswordHash{
		localuser.PasswordHash("$argon2i$v=19$m=19456,t=2,p=1" + tail),
		localuser.PasswordHash("$argon2d$v=19$m=19456,t=2,p=1" + tail),
		localuser.PasswordHash("$scrypt$ln=16,r=8,p=1" + tail),
		"$2b$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234",
		"$pbkdf2-sha512$i=210000" + localuser.PasswordHash(tail),
		"plain text password",
	}

	for _, stored := range hostile {
		_, err := h.Verify(t.Context(), hasherPassword, stored, fastParams())
		if !errors.Is(err, localuser.ErrInvalid) {
			t.Errorf("verify %q: err = %v, want ErrInvalid", stored, err)
		}
	}
}
