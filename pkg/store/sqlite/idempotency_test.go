package sqlite_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
)

func TestIdempotencyConformance(t *testing.T) {
	t.Parallel()

	idempotencytest.Run(t, func(t *testing.T) idempotency.Store {
		t.Helper()

		return open(t).Idempotency()
	})
}

// TestIdempotencyKeepsNoRequestBytes proves the database file holds the
// fingerprint of a request and never the request: the file and its log hold
// no text of the request bytes.
func TestIdempotencyKeepsNoRequestBytes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s := openPath(t, path)
	material := []byte("a-secret-in-the-request-body-0123456789")
	ref := idempotency.Ref{Principal: "alice", Method: "POST", Path: "/api/dustpan/v1/p", Key: "k1"}

	res, err := s.Idempotency().Begin(t.Context(), ref, material, time.Now())
	if err != nil || res.Outcome != idempotency.Started {
		t.Fatalf("begin: outcome = %d, err = %v", res.Outcome, err)
	}

	answer := idempotency.Answer{Status: 201, Headers: nil, Body: []byte(`{"id":"p1"}`)}
	if err = s.Idempotency().Complete(t.Context(), ref, res.Token, answer); err != nil {
		t.Fatalf("complete: %v", err)
	}

	for _, name := range []string{path, path + "-wal"} {
		data, readErr := os.ReadFile(name) //nolint:gosec // The test reads its own temporary database file.
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}

		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if bytes.Contains(data, material) {
			t.Errorf("%s holds the request bytes", name)
		}
	}
}
