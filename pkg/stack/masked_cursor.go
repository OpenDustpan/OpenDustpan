package stack

// Boundary check of the masked read: the version, the value path and the
// cursor. This file holds the one parser of JSON Pointers of the stack
// domain, the wire format of the cursor and the limits of the read. Each
// refusal of this layer answers with ErrBadPath or ErrReadDeadline, carries
// no value of the caller, and reports to the sink of the caller.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// MaxCursorBytes is the most bytes of one cursor of a masked read. A cursor
// above the cap is refused before any state is read.
const MaxCursorBytes = 512

// cursorTTL is how long a cursor of a masked read stays valid, from the time
// the server made it.
const cursorTTL = 10 * time.Minute

// cursorMaxVersion is the largest version a masked read asks. A version
// outside 0..cursorMaxVersion is refused, so a version names a checkpointed
// update in every deployment the server serves.
const cursorMaxVersion = 1 << 40

var (
	// ErrBadPath is the refusal of a version, a value path or a cursor of a
	// masked read that fails the boundary check. It carries no value of the
	// caller.
	ErrBadPath = errors.New("state: bad path")

	// ErrReadDeadline is the refusal when the read exceeds the deadline of
	// its caller. The answer carries no bytes of the version.
	ErrReadDeadline = errors.New("state: read deadline exceeded")
)

const (
	// RefusalDenied is the reason a read reports when the caller holds no
	// grant on the unit.
	RefusalDenied = "denied"
	// RefusalBadPath is the reason a read reports when the boundary check
	// refuses the version, the value path or the cursor.
	RefusalBadPath = "bad-path"
	// RefusalTooLarge is the reason a read reports when the version record
	// names a content above the size limit of the read.
	RefusalTooLarge = "too-large"
	// RefusalDeadline is the reason a read reports when the context of its
	// caller is past its deadline.
	RefusalDeadline = "deadline"
)

// RefusalSink counts one refusal of a masked read. The object is the unit
// the read names, never a value of the version; reason is one of the
// Refusal* constants of this package. The adapter of the read binds the
// denial limiter of the server as the sink.
type RefusalSink func(p identity.Principal, obj identity.Object, reason string)

// cursorPayload is the payload of a cursor of a masked read. The payload
// binds the unit, the resolved version, the caller and the offset of the
// page, and expires ten minutes after the server made it.
type cursorPayload struct {
	Unit    string `json:"u"`
	Version string `json:"v"`
	Subject string `json:"s"`
	Offset  int    `json:"o"`
	Expiry  int64  `json:"e"`
}

// signCursor signs payload with the key of the server.
func signCursor(key, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	// A hash never fails.
	_, _ = mac.Write(payload)

	return mac.Sum(nil)
}

// mintCursor makes the cursor of the page that p names: the unit, the
// version, the caller and the offset of the page. It sets the expiry from
// now. It is unexported: only a masked read answers a cursor.
func mintCursor(key []byte, p cursorPayload, now time.Time) (string, error) {
	p.Expiry = now.Add(cursorTTL).Unix()

	payload, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("masked read: cursor payload: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signCursor(key, payload)), nil
}

// verifyCursor checks the cursor of a masked read against the unit, the
// version and the caller of want, and returns the offset of its page. The
// empty cursor is the first page. Any failure answers ErrBadPath, with no
// echo of the cursor.
func verifyCursor(key []byte, raw string, want cursorPayload, now time.Time) (int, error) {
	if raw == "" {
		return 0, nil
	}

	parsed, err := decodeCursor(key, raw)
	if err != nil {
		return 0, err
	}

	if parsed.Unit != want.Unit || parsed.Version != want.Version || parsed.Subject != want.Subject {
		return 0, ErrBadPath
	}

	if parsed.Offset < 0 || parsed.Expiry < now.Unix() {
		return 0, ErrBadPath
	}

	return parsed.Offset, nil
}

// decodeCursor checks the length, the format and the signature of a cursor
// and returns its payload. Any failure answers ErrBadPath, with no echo of
// the cursor.
func decodeCursor(key []byte, raw string) (cursorPayload, error) {
	var parsed cursorPayload

	if len(raw) > MaxCursorBytes {
		return parsed, ErrBadPath
	}

	left, right, ok := strings.Cut(raw, ".")
	if !ok || strings.Contains(right, ".") {
		return parsed, ErrBadPath
	}

	payload, err := base64.RawURLEncoding.DecodeString(left)
	if err != nil || len(payload) == 0 {
		return parsed, ErrBadPath
	}

	mac, err := base64.RawURLEncoding.DecodeString(right)
	if err != nil || len(mac) != sha256.Size {
		return parsed, ErrBadPath
	}

	if !hmac.Equal(signCursor(key, payload), mac) {
		return parsed, ErrBadPath
	}

	if err := json.Unmarshal(payload, &parsed); err != nil {
		return cursorPayload{}, ErrBadPath
	}

	return parsed, nil
}

// validateVersion checks a version of the stack: 0 names no version, so the
// range of a masked read is 1..cursorMaxVersion.
func validateVersion(v int64) error {
	if v < 1 || v > cursorMaxVersion {
		return fmt.Errorf("%w: version", ErrBadPath)
	}

	return nil
}
