package policy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The headers of a call of the webhook adapter. SignatureHeader holds the
// value of Sign. DeliveryHeader holds a new ID for each call, so that a
// receiver can find a call in its log.
const (
	SignatureHeader = "Dustpan-Signature"
	DeliveryHeader  = "Dustpan-Delivery"
)

// SignatureTolerance is the largest difference between the time of a
// signature and the clock of the receiver that Verify accepts.
const SignatureTolerance = 5 * time.Minute

// MinSecretBytes is the shortest shared secret of a webhook.
const MinSecretBytes = 32

// Limits of a signature header that Verify reads. A header from the network is
// untrusted, so Verify refuses one above them before it parses it.
const (
	// MaxSignatureHeaderBytes bounds the header.
	MaxSignatureHeaderBytes = 1 << 10
	// MaxSignatures bounds the v1 values of one header. Two are active during
	// a rotation.
	MaxSignatures = 4
)

var (
	// ErrSignature reports a signature header that does not parse, or no
	// signature that matches the body with a secret.
	ErrSignature = errors.New("webhook signature does not match")
	// ErrSignatureTime reports a signature whose time is more than
	// SignatureTolerance from the clock of the receiver.
	ErrSignatureTime = errors.New("webhook signature is too old or too new")
	// ErrSecret reports a shared secret shorter than MinSecretBytes.
	ErrSecret = errors.New("webhook secret is too short")
)

// WebhookCall is the body that the webhook adapter sends. Document is the
// input document as JSON, as it is: a receiver reads an object, never a
// base64 string.
type WebhookCall struct {
	Kind          Kind            `json:"kind"`
	Document      json.RawMessage `json:"document"`
	SchemaVersion int             `json:"schemaVersion"`
}

// Sign returns the value of SignatureHeader for body at the time at:
// "t=<unix seconds>,v1=<hex>" with one v1 for each secret, in the order of
// secrets. Each hex is the lowercase hex of the HMAC-SHA256 of
// "<unix seconds>.<body>" with that secret. Two secrets are active during a
// rotation, newest first. It returns ErrSecret for no secret or a secret
// shorter than MinSecretBytes.
func Sign(body []byte, at time.Time, secrets ...[]byte) (string, error) {
	if len(secrets) == 0 {
		return "", fmt.Errorf("%w: no secret", ErrSecret)
	}

	ts := strconv.FormatInt(at.Unix(), 10)
	parts := make([]string, 0, len(secrets)+1)
	parts = append(parts, "t="+ts)

	for _, s := range secrets {
		if len(s) < MinSecretBytes {
			return "", fmt.Errorf("%w: %d bytes, the minimum is %d", ErrSecret, len(s), MinSecretBytes)
		}

		parts = append(parts, "v1="+hex.EncodeToString(mac(s, ts, body)))
	}

	return strings.Join(parts, ","), nil
}

// Verify checks header, the value of SignatureHeader, against body with
// secret at the time now of the receiver. It returns nil when one v1 of the
// header matches, ErrSignatureTime when the time of the header is more than
// SignatureTolerance from now, and ErrSignature for a header that does not
// parse or no match. The compare takes the same time for each byte.
//
// Verify fails closed. It returns ErrSecret for a secret shorter than
// MinSecretBytes, as Sign does, because a receiver with an empty or short
// secret would accept a signature that anyone can make. It returns
// ErrSignature for a header above MaxSignatureHeaderBytes, with more than
// MaxSignatures values of v1, with no v1, with a repeated t, or with a time
// that is not in the form that Sign writes. The errors never quote the
// header.
func Verify(header string, body, secret []byte, now time.Time) error {
	// Defense in depth: the caller reads the secret from its settings and
	// checks its length too; this check stays so that a new caller cannot
	// skip it.
	if len(secret) < MinSecretBytes {
		return fmt.Errorf("%w: %d bytes, the minimum is %d", ErrSecret, len(secret), MinSecretBytes)
	}

	if len(header) > MaxSignatureHeaderBytes {
		return fmt.Errorf("%w: the header has %d bytes, the limit is %d", ErrSignature, len(header),
			MaxSignatureHeaderBytes)
	}

	ts, sigs, err := parseSignature(header)
	if err != nil {
		return err
	}

	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || strconv.FormatInt(unix, 10) != ts {
		return fmt.Errorf("%w: no valid time", ErrSignature)
	}

	if d := now.Sub(time.Unix(unix, 0)).Abs(); d > SignatureTolerance {
		return fmt.Errorf("%w: %v from the clock of the receiver", ErrSignatureTime, d)
	}

	want := mac(secret, ts, body)
	for _, sig := range sigs {
		if hmac.Equal(sig, want) {
			return nil
		}
	}

	return ErrSignature
}

// parseSignature reads the time and the v1 values of a signature header.
func parseSignature(header string) (string, [][]byte, error) {
	var (
		ts   string
		seen bool
		sigs [][]byte
	)

	for part := range strings.SplitSeq(header, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return "", nil, fmt.Errorf("%w: a part of the header has no '='", ErrSignature)
		}

		switch key {
		case "t":
			if seen {
				return "", nil, fmt.Errorf("%w: t is given two times", ErrSignature)
			}

			ts, seen = value, true
		case "v1":
			if len(sigs) == MaxSignatures {
				return "", nil, fmt.Errorf("%w: more than %d signatures", ErrSignature, MaxSignatures)
			}

			sig, err := hex.DecodeString(value)
			if err != nil {
				return "", nil, fmt.Errorf("%w: v1 is not hex", ErrSignature)
			}

			sigs = append(sigs, sig)
		default:
			// A later scheme adds its own key. Keys that the receiver
			// does not know do not count.
		}
	}

	if len(sigs) == 0 {
		return "", nil, fmt.Errorf("%w: no v1", ErrSignature)
	}

	return ts, sigs, nil
}

func mac(secret []byte, ts string, body []byte) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(ts))
	h.Write([]byte("."))
	h.Write(body)

	return h.Sum(nil)
}
