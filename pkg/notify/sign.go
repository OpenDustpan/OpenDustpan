package notify

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

// Headers of a webhook delivery. A receiver checks a signature over the
// notification ID, the timestamp and the body, refuses a timestamp older
// than SignatureTolerance, and drops a second delivery of one notification
// ID.
const (
	HeaderNotificationID = "Dustpan-Notification-Id"
	HeaderTimestamp      = "Dustpan-Timestamp"
	HeaderSignature      = "Dustpan-Signature"
)

// Headers holds the values of the three headers of a delivery:
// HeaderNotificationID, HeaderTimestamp and HeaderSignature.
type Headers struct {
	NotificationID string
	Timestamp      string
	Signature      string
}

// SignatureTolerance is the largest difference between the timestamp of a
// delivery and the clock of the receiver that Verify accepts.
const SignatureTolerance = 5 * time.Minute

// signaturePrefix names the version of the signature scheme.
const signaturePrefix = "v1="

// Errors of Sign and Verify.
var (
	// ErrSignature reports a signature that does not match, or a header
	// that does not parse.
	ErrSignature = errors.New("webhook signature does not match")
	// ErrSignatureExpired reports a timestamp outside SignatureTolerance.
	ErrSignatureExpired = errors.New("webhook timestamp outside the tolerance")
	// ErrSecrets reports no secret, more than MaxSigningSecrets secrets,
	// or an empty secret.
	ErrSecrets = errors.New("webhook needs one or two signing secrets")
)

// Sign returns the value of HeaderSignature for the notification id, sent
// at the Unix second timestamp with body: one "v1=" signature for each
// secret, in the order of secrets, joined with ",". A signature is the hex
// HMAC-SHA256, keyed with the secret, over id, ".", timestamp, "." and
// body. It returns ErrSecrets for no secret, an empty secret, or more than
// MaxSigningSecrets secrets.
func Sign(secrets [][]byte, id string, timestamp int64, body []byte) (string, error) {
	if err := checkSecrets(secrets); err != nil {
		return "", err
	}

	sigs := make([]string, 0, len(secrets))
	for _, s := range secrets {
		sigs = append(sigs, signaturePrefix+hex.EncodeToString(mac(s, id, timestamp, body)))
	}

	return strings.Join(sigs, ","), nil
}

// Verify checks the headers h of a delivery with body against the secrets
// of the receiver, as a receiver does. It accepts the delivery when one
// signature of the header matches one secret. It returns ErrSecrets for a
// bad list of secrets, ErrSignatureExpired for a timestamp that is not a
// number or is outside SignatureTolerance of now, and ErrSignature for a
// header with no signature or more than MaxSigningSecrets signatures, or
// with no signature that matches. It compares every pair in constant time
// with hmac.Equal and does not stop at the first match.
func Verify(secrets [][]byte, h Headers, body []byte, now time.Time) error {
	if err := checkSecrets(secrets); err != nil {
		return err
	}

	ts, err := strconv.ParseInt(h.Timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %q is not a Unix time", ErrSignatureExpired, h.Timestamp)
	}

	if d := now.Sub(time.Unix(ts, 0)); d > SignatureTolerance || d < -SignatureTolerance {
		return ErrSignatureExpired
	}

	got, err := parseSignatures(h.Signature)
	if err != nil {
		return err
	}

	match := false

	for _, s := range secrets {
		want := mac(s, h.NotificationID, ts, body)
		for _, g := range got {
			match = hmac.Equal(want, g) || match
		}
	}

	if !match {
		return ErrSignature
	}

	return nil
}

// parseSignatures returns the decoded signatures of the value of
// HeaderSignature, and ErrSignature for more than MaxSigningSecrets
// signatures or for a signature that does not parse.
func parseSignatures(header string) ([][]byte, error) {
	parts := strings.Split(header, ",")
	if len(parts) > MaxSigningSecrets {
		return nil, fmt.Errorf("%w: more than %d signatures", ErrSignature, MaxSigningSecrets)
	}

	got := make([][]byte, 0, len(parts))

	for _, p := range parts {
		hexSig, ok := strings.CutPrefix(strings.TrimSpace(p), signaturePrefix)
		if !ok {
			return nil, ErrSignature
		}

		b, err := hex.DecodeString(hexSig)
		if err != nil {
			return nil, ErrSignature
		}

		got = append(got, b)
	}

	return got, nil
}

func checkSecrets(secrets [][]byte) error {
	if len(secrets) == 0 || len(secrets) > MaxSigningSecrets {
		return ErrSecrets
	}

	for _, s := range secrets {
		if len(s) == 0 {
			return ErrSecrets
		}
	}

	return nil
}

func mac(secret []byte, id string, timestamp int64, body []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(id))
	m.Write([]byte{'.'})
	m.Write([]byte(strconv.FormatInt(timestamp, 10)))
	m.Write([]byte{'.'})
	m.Write(body)

	return m.Sum(nil)
}
