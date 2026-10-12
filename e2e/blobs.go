//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/s3"
)

const (
	// blobsEnv names the blob store selector: "fs" opens the state content
	// directory, "s3" opens S3-compatible storage. Empty means "fs", so runs
	// with E2E_BLOBS unset are unchanged.
	blobsEnv = "E2E_BLOBS"
	// blobFS is the directory blob store every run uses by default.
	blobFS = "fs"
	// blobS3 is the S3-compatible blob store a run uses with E2E_BLOBS=s3.
	blobS3 = "s3"
	// s3Region signs live-server requests. S3-compatible servers accept any
	// fixed region.
	s3Region = "us-east-1"
	// s3PrefixBytes is the random suffix length of one run prefix in bytes.
	s3PrefixBytes = 8
)

// Live-server variables for S3 runs. They reuse the names of the blob/s3
// conformance suite, so one setup serves both.
const (
	s3EndpointEnv = "DUSTPAN_TEST_S3_ENDPOINT"
	s3BucketEnv   = "DUSTPAN_TEST_S3_BUCKET"
	s3AccessEnv   = "DUSTPAN_TEST_S3_ACCESS_KEY"
	s3SecretEnv   = "DUSTPAN_TEST_S3_SECRET_KEY" //nolint:gosec // Environment variable name, not a credential value.
)

// ErrUnknownBlobStore reports an E2E_BLOBS value that names no blob store.
var ErrUnknownBlobStore = errors.New("unknown blob store")

// BlobStoreName returns the blob store every scenario run uses: E2E_BLOBS as
// "fs" or "s3", or "fs" when it is unset. An unknown value is an error that
// names the allowed values.
func BlobStoreName() (string, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(blobsEnv)))

	switch raw {
	case "", blobFS:
		return blobFS, nil
	case blobS3:
		return blobS3, nil
	default:
		return "", fmt.Errorf("%w: %q (want fs or s3)", ErrUnknownBlobStore, strings.TrimSpace(os.Getenv(blobsEnv)))
	}
}

// UseS3Blobs reports whether the suite runs with S3 live blobs. Unknown
// values fail the test, so a typo never silently runs on the directory store.
func UseS3Blobs(tb testing.TB) bool {
	tb.Helper()

	name, err := BlobStoreName()
	if err != nil {
		tb.Fatalf("blob store: %v", err)
	}

	return name == blobS3
}

// S3Config holds the live-server settings of one S3 run.
type S3Config struct {
	endpoint string
	bucket   string
	access   string
	secret   string
}

// Endpoint returns the S3 endpoint URL of the run.
func (c S3Config) Endpoint() string {
	return c.endpoint
}

// Bucket returns the S3 bucket of the run.
func (c S3Config) Bucket() string {
	return c.bucket
}

// LookupS3Config returns the live-server settings with whether all four
// variables hold values. The access values never reach a log or the report:
// the harness maps them into the child environment only.
func LookupS3Config() (S3Config, bool) {
	cfg := S3Config{
		endpoint: os.Getenv(s3EndpointEnv),
		bucket:   os.Getenv(s3BucketEnv),
		access:   os.Getenv(s3AccessEnv),
		secret:   os.Getenv(s3SecretEnv),
	}

	if cfg.endpoint == "" || cfg.bucket == "" || cfg.access == "" || cfg.secret == "" {
		return S3Config{endpoint: "", bucket: "", access: "", secret: ""}, false
	}

	return cfg, true
}

// S3SkipReason names the four variables an S3 run needs, the same way the
// blob/s3 conformance suite does.
func S3SkipReason() string {
	return fmt.Sprintf(
		"set %s, %s, %s and %s to run the S3 blob suite",
		s3EndpointEnv,
		s3BucketEnv,
		s3AccessEnv,
		s3SecretEnv,
	)
}

// RequireS3Config returns the live-server settings, skipping the test with
// the four variable names when any of them is unset.
func RequireS3Config(tb testing.TB) S3Config {
	tb.Helper()

	if cfg, ok := LookupS3Config(); ok {
		return cfg
	}

	tb.Skip(S3SkipReason())

	return S3Config{endpoint: "", bucket: "", access: "", secret: ""}
}

// newS3RunPrefix returns a unique key prefix for one environment, so parallel
// runs share a bucket without sharing keys.
func newS3RunPrefix(tb testing.TB) string {
	tb.Helper()

	raw := make([]byte, s3PrefixBytes)
	if _, err := rand.Read(raw); err != nil {
		tb.Fatalf("generate S3 prefix: %v", err)
	}

	return "e2e-" + hex.EncodeToString(raw)
}

// openS3Store opens one S3 store under prefix and registers a cleanup that
// deletes every key under it, so no run object stays on the server. Each
// store gets its own transport, so its keep-alive connections close with it.
//
//nolint:ireturn // The S3 adapter is chosen at runtime, so the result must be an interface.
func openS3Store(ctx context.Context, tb testing.TB, cfg S3Config, prefix string) blob.Store {
	tb.Helper()

	signer := &s3.Signer{
		Bucket:   cfg.bucket,
		Region:   s3Region,
		Endpoint: cfg.endpoint,
		Credentials: s3.Credentials{
			AccessKeyID:     cfg.access,
			SecretAccessKey: cfg.secret,
			SessionToken:    "",
		},
		Now: nil,
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		tb.Fatal("default transport is not *http.Transport")
	}

	transport = transport.Clone()
	tb.Cleanup(transport.CloseIdleConnections)

	client := &http.Client{Transport: transport}

	store, err := s3.Open(ctx, signer, prefix, client)
	if err != nil {
		tb.Fatalf("open S3 store: %v", err)
	}

	tb.Cleanup(func() {
		cleanupS3Prefix(ctx, tb, store)
	})

	return store
}

// cleanupS3Prefix deletes every key under the prefix of store. It lists the
// store instead of tracking writes, so keys the server binary wrote are
// removed too. It reports each failure and keeps deleting the rest.
func cleanupS3Prefix(ctx context.Context, tb testing.TB, store blob.Store) {
	tb.Helper()

	ctx = context.WithoutCancel(ctx)

	objects, err := store.List(ctx, "")
	if err != nil {
		tb.Errorf("list S3 keys for cleanup: %v", err)

		return
	}

	for _, obj := range objects {
		if err := store.Delete(ctx, obj.Key); err != nil {
			tb.Errorf("delete S3 key %s: %v", obj.Key, err)
		}
	}
}

// OpenS3TargetStore opens the S3 backup target in target
// ("s3://<bucket>[/<prefix>]") for tests that read backup content outside
// the server, like the manifest tamper step. It registers a cleanup that
// removes the keys under the prefix, so copied objects never stay behind.
//
//nolint:ireturn // The S3 adapter is chosen at runtime, so the result must be an interface.
func OpenS3TargetStore(ctx context.Context, tb testing.TB, target string) blob.Store {
	tb.Helper()

	cfg := RequireS3Config(tb)

	rest, ok := strings.CutPrefix(strings.TrimSpace(target), "s3://")
	if !ok || strings.TrimSpace(rest) == "" {
		tb.Fatalf("backup target = %q, want s3://<bucket>[/<prefix>]", target)
	}

	bucket, prefix, _ := strings.Cut(strings.TrimSpace(rest), "/")
	bucket = strings.TrimSpace(bucket)
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")

	if bucket == "" {
		tb.Fatalf("backup target = %q, want s3://<bucket>[/<prefix>]", target)
	}

	return openS3Store(ctx, tb, S3Config{
		endpoint: cfg.endpoint,
		bucket:   bucket,
		access:   cfg.access,
		secret:   cfg.secret,
	}, prefix)
}

// blobNameOf returns the live blob store of one environment: "s3" with
// E2E_BLOBS=s3, else "fs".
func blobNameOf(tb testing.TB) string {
	tb.Helper()

	if UseS3Blobs(tb) {
		return blobS3
	}

	return blobFS
}

// s3EndpointOf returns the S3 endpoint URL of one environment, or "" on the
// directory store.
func s3EndpointOf(tb testing.TB) string {
	tb.Helper()

	if !UseS3Blobs(tb) {
		return ""
	}

	return RequireS3Config(tb).Endpoint()
}

// BlobName returns the live blob store of the environment: "fs" or "s3". The
// report names it, so a summary shows what was driven.
func (e *Env) BlobName() string {
	return e.blobName
}

// S3Endpoint returns the S3 endpoint URL of the environment, or "" on the
// directory store. Tests pass it to `backup check` and `restore` as
// --s3-endpoint when the target or the live blobs are S3. It holds no
// credential.
func (e *Env) S3Endpoint() string {
	return e.s3Endpoint
}

// BlobStoreValue returns the --blob-store value of the environment for
// `restore`: s3://<bucket>/<prefix> on the S3 store, or "" on the directory
// store, where --blob-dir applies instead. It holds no credential.
func (e *Env) BlobStoreValue() string {
	if e.blobName != blobS3 {
		return ""
	}

	return e.backendCfg.blobStore
}

// S3Args returns the S3 flags a client command needs on the S3 store:
// --s3-endpoint with the endpoint, or nothing on the directory store.
func (e *Env) S3Args() []string {
	if e.blobName != blobS3 {
		return nil
	}

	return []string{"--s3-endpoint", e.s3Endpoint}
}

// blobDirGone reports whether the directory blob store of the environment has
// no blob directory. A missing directory counts as empty. It always reports
// false on the S3 store.
func (e *Env) blobDirGone(tb testing.TB) bool {
	tb.Helper()

	if e.blobName == blobS3 {
		return false
	}

	if _, err := os.Stat(e.BlobDir(tb)); err != nil {
		if os.IsNotExist(err) {
			return true
		}

		tb.Fatalf("stat blob directory: %v", err)
	}

	return false
}

// listLiveKeys returns every object of the live blob store of the
// environment. A missing blob directory counts as empty, so the empty check
// after a wipe reports empty instead of failing.
func (e *Env) listLiveKeys(ctx context.Context, tb testing.TB) []blob.Object {
	tb.Helper()

	if e.blobDirGone(tb) {
		return nil
	}

	objects, err := e.blobs.List(ctx, "")
	if err != nil {
		if e.blobDirGone(tb) {
			return nil
		}

		tb.Fatalf("list live blobs: %v", err)
	}

	return objects
}

// ClearLiveBlobs empties the live blob store of the environment, so a restore
// loads into an empty destination. On both stores it deletes every key, so
// the open store handle stays valid: removing the blob directory itself would
// leave the handle on a deleted directory. A missing blob directory counts as
// empty.
func (e *Env) ClearLiveBlobs(ctx context.Context, tb testing.TB) {
	tb.Helper()

	for _, obj := range e.listLiveKeys(ctx, tb) {
		if err := e.blobs.Delete(ctx, obj.Key); err != nil {
			tb.Fatalf("delete live blob %s: %v", obj.Key, err)
		}
	}
}

// CountLiveBlobs returns the number of objects in the live blob store of the
// environment. A missing blob directory counts as zero objects, so the empty
// check after a wipe reports empty instead of failing.
func (e *Env) CountLiveBlobs(ctx context.Context, tb testing.TB) int {
	tb.Helper()

	return len(e.listLiveKeys(ctx, tb))
}
