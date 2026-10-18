package event

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Headers of a webhook delivery. A receiver checks the signature over the
// event ID, the timestamp and the body, refuses a timestamp older than
// SignatureTolerance, and drops a second delivery of one event ID.
const (
	HeaderEventID   = "Dustpan-Event-Id"
	HeaderTimestamp = "Dustpan-Timestamp"
	HeaderSignature = "Dustpan-Signature"
)

// Headers holds the values of the three headers of a delivery:
// HeaderEventID, HeaderTimestamp and HeaderSignature.
type Headers struct {
	EventID   string
	Timestamp string
	Signature string
}

// SignatureTolerance is the largest difference between the timestamp of a
// delivery and the clock of the receiver that Verify accepts.
const SignatureTolerance = 5 * time.Minute

// signaturePrefix names the version of the signature scheme.
const signaturePrefix = "v1="

// Errors of Verify.
var (
	// ErrSignature reports a signature that does not match.
	ErrSignature = errors.New("webhook signature does not match")
	// ErrSignatureExpired reports a timestamp outside SignatureTolerance.
	ErrSignatureExpired = errors.New("webhook timestamp outside the tolerance")
)

// Sign returns the value of HeaderSignature for the event id, sent at the
// Unix second timestamp with body: "v1=" and the hex HMAC-SHA256, keyed
// with secret, over id, ".", timestamp, "." and body.
func Sign(secret []byte, id string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(id))
	mac.Write([]byte{'.'})
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte{'.'})
	mac.Write(body)

	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks the headers h of a delivery with body, as a receiver
// does. It returns ErrSignatureExpired for a timestamp that is not a
// number or is outside SignatureTolerance of now, and ErrSignature for a
// signature that does not match. It compares in constant time.
func Verify(secret []byte, h Headers, body []byte, now time.Time) error {
	ts, err := strconv.ParseInt(h.Timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %q is not a Unix time", ErrSignatureExpired, h.Timestamp)
	}

	if d := now.Sub(time.Unix(ts, 0)); d > SignatureTolerance || d < -SignatureTolerance {
		return ErrSignatureExpired
	}

	if !strings.HasPrefix(h.Signature, signaturePrefix) {
		return ErrSignature
	}

	if !hmac.Equal([]byte(Sign(secret, h.EventID, ts, body)), []byte(h.Signature)) {
		return ErrSignature
	}

	return nil
}
