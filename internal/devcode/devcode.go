// Package devcode holds the one-time sign-in code of opendustpan dev.
//
// The code is 32 random bytes from crypto/rand. The server keeps only the
// SHA-256 hash of the code, in memory, and compares it in constant time. The
// code works one time, and it stops working a fixed time after it was made.
// A restart makes a new code, because nothing is stored.
package devcode

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// Lifetime is the time that a code works after Make.
const Lifetime = 10 * time.Minute

// codeBytes is the size of a code before the encoding.
const codeBytes = 32

// Code is a one-time sign-in code. Its zero value is not usable: Make
// returns one.
type Code struct {
	now     func() time.Time
	expires time.Time
	hash    [sha256.Size]byte
	mu      sync.Mutex
	used    bool
}

// Make returns a new code and its text. The text is the only copy of the
// code: the Code keeps its hash. A nil now means time.Now.
func Make(now func() time.Time) (*Code, string, error) {
	if now == nil {
		now = time.Now
	}

	raw := make([]byte, codeBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("make sign-in code: %w", err)
	}

	text := base64.RawURLEncoding.EncodeToString(raw)

	return &Code{
		now:     now,
		expires: now().Add(Lifetime),
		hash:    sha256.Sum256([]byte(text)),
		mu:      sync.Mutex{},
		used:    false,
	}, text, nil
}

// Redeem reports whether candidate is the code, and spends the code when it
// is. It answers false for a wrong candidate, for a code that was spent, and
// for a code past its lifetime. A wrong candidate does not spend the code.
func (c *Code) Redeem(candidate string) bool {
	sum := sha256.Sum256([]byte(candidate))
	match := subtle.ConstantTimeCompare(sum[:], c.hash[:]) == 1

	c.mu.Lock()
	defer c.mu.Unlock()

	if !match || c.used || !c.now().Before(c.expires) {
		return false
	}

	c.used = true

	return true
}
