package codec

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"opendustpan.org/opendustpan/pkg/idempotency"
)

// idempotencyKeyBytes is the size of the server key of the fingerprints and
// the size of a token, in bytes.
const (
	idempotencyServerKeyBytes = 32
	idempotencyTokenBytes     = 16
)

// IdempotencyRow is the column representation of a replay record. Both
// dialects generate the same fields.
type IdempotencyRow struct {
	Fingerprint []byte
	Token       string
	Status      int64
	Headers     string
	Body        []byte
	CreatedAtMs int64
}

// NewIdempotencyToken returns a random token for a pending record.
func NewIdempotencyToken() (string, error) {
	raw := make([]byte, idempotencyTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("make idempotency token: %w", err)
	}

	return hex.EncodeToString(raw), nil
}

// NewIdempotencyServerKey returns a random key for the fingerprints.
func NewIdempotencyServerKey() ([]byte, error) {
	raw := make([]byte, idempotencyServerKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("make idempotency server key: %w", err)
	}

	return raw, nil
}

// EncodeIdempotencyHeaders returns the stored form of the headers of an answer.
func EncodeIdempotencyHeaders(h map[string]string) (string, error) {
	if len(h) == 0 {
		return "", nil
	}

	raw, err := json.Marshal(h)
	if err != nil {
		return "", fmt.Errorf("encode idempotency headers: %w", err)
	}

	return string(raw), nil
}

// IdempotencyOutcome returns the result of Begin that holds no answer and no
// token.
func IdempotencyOutcome(outcome idempotency.Outcome) idempotency.Result {
	var none idempotency.Answer

	return idempotency.Result{Answer: none, Token: "", Outcome: outcome}
}

// ClassifyIdempotency returns what a request with the fingerprint fp finds in
// the live record row.
func ClassifyIdempotency(row IdempotencyRow, fp []byte) (idempotency.Result, error) {
	switch {
	case !bytes.Equal(row.Fingerprint, fp):
		return IdempotencyOutcome(idempotency.Reused), nil
	case row.Status == 0:
		return IdempotencyOutcome(idempotency.InProgress), nil
	}

	headers := map[string]string{}

	if row.Headers != "" {
		if err := json.Unmarshal([]byte(row.Headers), &headers); err != nil {
			return idempotency.Result{}, fmt.Errorf("decode idempotency headers: %w", err)
		}
	}

	return idempotency.Result{
		Answer:  idempotency.Answer{Headers: headers, Body: row.Body, Status: int(row.Status)},
		Token:   "",
		Outcome: idempotency.Replayed,
	}, nil
}
