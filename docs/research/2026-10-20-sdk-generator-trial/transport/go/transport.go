// Package transport is a trial sketch of the hand-written transport layer of
// the Go SDK. It is an http.RoundTripper, so each candidate generator can use
// it through its own http.Client option. It uses the standard library only.
//
// It adds the bearer token, an Idempotency-Key on each mutating request that
// has none, and retries a request that is safe to retry. The caller reads the
// ETag of an answer and sends If-Match through the generated call, because
// If-Match is a parameter of the operation in the description.
package transport

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"time"
)

// Transport adds authentication, idempotency keys and retries.
type Transport struct {
	// Base sends the request. Nil means http.DefaultTransport.
	Base http.RoundTripper
	// Token is the bearer token. Empty means no Authorization header.
	Token string
	// MaxAttempts is the number of tries for a retryable request. Zero means 3.
	MaxAttempts int
	// Backoff is the wait before the second try. It doubles for each try.
	Backoff time.Duration
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}

	return http.DefaultTransport
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if t.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.Token)
	}

	if mutating(req.Method) && req.Header.Get("Idempotency-Key") == "" {
		req.Header.Set("Idempotency-Key", newKey())
	}

	// A retry must send the body again, so read it once.
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}

	attempts := t.MaxAttempts
	if attempts == 0 {
		attempts = 3
	}

	wait := t.Backoff
	for try := 1; ; try++ {
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
		}

		resp, err := t.base().RoundTrip(req)
		if try >= attempts || !retryable(resp, err) {
			return resp, err
		}

		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
}

// mutating reports whether the method changes data. Every mutating request
// carries an Idempotency-Key, so a retry of it is safe.
func mutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// retryable reports whether the transport tries again: a network error, 429,
// 502, 503 or 504. The sketch does not retry the 409 of a key whose first
// request is in progress, because only errorCode in the body tells it apart
// from other conflicts. The real transport reads the error body for it.
func retryable(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}
