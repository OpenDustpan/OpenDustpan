package s3_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/blob/s3"
)

// Live-server variables for the S3 conformance suite. The suite runs when all
// four hold values and skips otherwise, as the PostgreSQL suite does for
// DUSTPAN_TEST_POSTGRES_URL.
const (
	envS3Endpoint = "DUSTPAN_TEST_S3_ENDPOINT"
	envS3Bucket   = "DUSTPAN_TEST_S3_BUCKET"
	envS3Access   = "DUSTPAN_TEST_S3_ACCESS_KEY"
	envS3Secret   = "DUSTPAN_TEST_S3_SECRET_KEY" //nolint:gosec // Environment variable name, not a credential value.
)

// liveRegion is the region name in the credential scope of live-server
// requests. S3-compatible servers accept any fixed region.
const liveRegion = "us-east-1"

// TestConformance runs blobtest.Run against a live S3-compatible server. Each
// subtest opens its own store under a unique key prefix and deletes the keys
// it wrote, so no conformance object stays on the server.
func TestConformance(t *testing.T) {
	t.Parallel()

	cfg := liveConfig{
		endpoint: os.Getenv(envS3Endpoint),
		bucket:   os.Getenv(envS3Bucket),
		access:   os.Getenv(envS3Access),
		secret:   os.Getenv(envS3Secret),
	}

	if cfg.endpoint == "" || cfg.bucket == "" || cfg.access == "" || cfg.secret == "" {
		t.Skipf(
			"set %s, %s, %s and %s to run the S3 conformance suite",
			envS3Endpoint,
			envS3Bucket,
			envS3Access,
			envS3Secret,
		)
	}

	blobtest.Run(t, func(t *testing.T) blob.Store {
		t.Helper()

		return openLiveStore(t, cfg)
	})
}

// liveConfig holds the live-server settings of one TestConformance run.
type liveConfig struct {
	endpoint string
	bucket   string
	access   string
	secret   string
}

// openLiveStore opens one store under a unique prefix through Open, so the
// probe proves conditional writes on the live server too. It registers a
// cleanup that deletes every key the subtest wrote.
func openLiveStore(t *testing.T, cfg liveConfig) *cleaningStore {
	t.Helper()

	var prefix [8]byte

	if _, err := rand.Read(prefix[:]); err != nil {
		t.Fatalf("generate key prefix: %v", err)
	}

	signer := &s3.Signer{
		Bucket:   cfg.bucket,
		Region:   liveRegion,
		Endpoint: cfg.endpoint,
		Credentials: s3.Credentials{
			AccessKeyID:     cfg.access,
			SecretAccessKey: cfg.secret,
			SessionToken:    "",
		},
		Now: nil,
	}

	// Each subtest gets its own transport, so its keep-alive connections
	// close with it and the leak check finds no idle goroutine.
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("default transport is not *http.Transport")
	}

	transport = transport.Clone()
	t.Cleanup(transport.CloseIdleConnections)

	client := &http.Client{Transport: transport}

	inner, err := s3.Open(t.Context(), signer, "conformance-"+hex.EncodeToString(prefix[:]), client)
	if err != nil {
		t.Fatalf("open live store: %v", err)
	}

	s := &cleaningStore{inner: inner, mu: sync.Mutex{}, keys: map[blob.Key]struct{}{}}

	t.Cleanup(func() {
		s.cleanup(t)
	})

	return s
}

// cleaningStore wraps a blob.Store and records the keys Put stored. Cleanup
// deletes them, so no conformance object stays on the server.
type cleaningStore struct {
	inner *s3.Store
	mu    sync.Mutex
	keys  map[blob.Key]struct{}
}

var _ blob.Store = (*cleaningStore)(nil)

// Put stores r under key and records key for cleanup on success.
func (s *cleaningStore) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if err := s.inner.Put(ctx, key, r); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.keys == nil {
		s.keys = map[blob.Key]struct{}{}
	}

	s.keys[key] = struct{}{}

	return nil
}

// Get opens the object under key.
func (s *cleaningStore) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	return s.inner.Get(ctx, key)
}

// Delete removes the object under key and drops key from the cleanup set on
// success. A failed delete keeps the key, so cleanup retries it.
func (s *cleaningStore) Delete(ctx context.Context, key blob.Key) error {
	err := s.inner.Delete(ctx, key)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.keys, key)

	return nil
}

// List returns the objects under prefix.
func (s *cleaningStore) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	return s.inner.List(ctx, prefix)
}

// Ping checks that the bucket answers.
func (s *cleaningStore) Ping(ctx context.Context) error {
	return s.inner.Ping(ctx)
}

// cleanup deletes every key the subtest wrote and still holds. It reports
// each failure and keeps deleting the rest.
func (s *cleaningStore) cleanup(t *testing.T) {
	t.Helper()

	s.mu.Lock()
	keys := make([]blob.Key, 0, len(s.keys))

	for key := range s.keys {
		keys = append(keys, key)
	}

	s.mu.Unlock()

	ctx := context.WithoutCancel(t.Context())

	for _, key := range keys {
		if err := s.inner.Delete(ctx, key); err != nil {
			t.Errorf("delete live key %s: %v", key, err)
		}
	}
}
