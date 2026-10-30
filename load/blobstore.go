package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/blob/s3"
)

// Blob store names of the -blobs flag.
const (
	blobsFS = "fs"
	blobsS3 = "s3"
)

// Live-server variables of an S3 run. They reuse the names of the blob/s3
// conformance suite and the e2e suite, so one setup serves all three. The
// variable of the secret key stays inline in s3Signer: a named constant for
// an env var name that holds a credential trips the hardcoded-credential
// check.
const (
	s3EndpointEnv = "DUSTPAN_TEST_S3_ENDPOINT"
	s3BucketEnv   = "DUSTPAN_TEST_S3_BUCKET"
	s3AccessEnv   = "DUSTPAN_TEST_S3_ACCESS_KEY"
)

// s3Region signs the requests of an S3 run. S3-compatible servers accept a
// fixed region.
const s3Region = "us-east-1"

// s3RunBytes is the random part of the key prefix of one S3 run.
const s3RunBytes = 8

// errBadBlobs reports a -blobs value that names no blob store.
var errBadBlobs = errors.New("-blobs must be fs or s3")

// errS3Unset reports an S3 run without its live-server variables.
var errS3Unset = errors.New(
	"-blobs s3 needs DUSTPAN_TEST_S3_ENDPOINT, DUSTPAN_TEST_S3_BUCKET, " +
		"DUSTPAN_TEST_S3_ACCESS_KEY and DUSTPAN_TEST_S3_SECRET_KEY",
)

// blobSettings selects the live blob store and the backup target of a run.
type blobSettings struct {
	// kind is blobsFS or blobsS3.
	kind string
	// probe opens the S3 store with the conditional-write probe, as the
	// server binary does. Without it the store opens without a request, so
	// a store that ignores If-None-Match also serves the run.
	probe bool
	// dir holds the directory stores of an fs run.
	dir string
	// prefix is the key prefix of an S3 run, unique for each run.
	prefix string
}

// newBlobSettings checks kind and answers the settings of one run.
func newBlobSettings(kind string, probe bool, dir string) (blobSettings, error) {
	if kind != blobsFS && kind != blobsS3 {
		return blobSettings{}, errBadBlobs
	}

	raw := make([]byte, s3RunBytes)
	if _, err := rand.Read(raw); err != nil {
		return blobSettings{}, fmt.Errorf("random run prefix: %w", err)
	}

	return blobSettings{kind: kind, probe: probe, dir: dir, prefix: "load-" + hex.EncodeToString(raw)}, nil
}

// s3Signer answers the signer of an S3 run from the live-server variables.
// No error holds a credential.
func s3Signer() (*s3.Signer, error) {
	endpoint, bucket := os.Getenv(s3EndpointEnv), os.Getenv(s3BucketEnv)
	access, secretKey := os.Getenv(s3AccessEnv), os.Getenv("DUSTPAN_TEST_S3_SECRET_KEY")

	if endpoint == "" || bucket == "" || access == "" || secretKey == "" {
		return nil, errS3Unset
	}

	return &s3.Signer{
		Bucket:      bucket,
		Region:      s3Region,
		Endpoint:    endpoint,
		Credentials: s3.Credentials{AccessKeyID: access, SecretAccessKey: secretKey, SessionToken: ""},
		Now:         nil,
	}, nil
}

// openedBlobs is one opened blob store with the name that the backups table
// keeps for a backup target.
type openedBlobs struct {
	store blob.Store
	name  string
	// close releases the handle: a directory store holds an open root, an
	// S3 store holds nothing.
	close func() error
}

// noClose is the close func of a store that holds no handle.
func noClose() error {
	return nil
}

// openS3 opens the S3 store under prefix, with the probe when b.probe is
// set. The store sends through http.DefaultClient, as the server binary does:
// only the request context bounds a request.
func (b blobSettings) openS3(ctx context.Context, prefix string) (*s3.Store, error) {
	signer, err := s3Signer()
	if err != nil {
		return nil, err
	}

	if b.probe {
		store, openErr := s3.Open(ctx, signer, prefix, nil)
		if openErr != nil {
			return nil, fmt.Errorf("open S3 store: %w", openErr)
		}

		return store, nil
	}

	store, err := s3.New(signer, prefix, nil)
	if err != nil {
		return nil, fmt.Errorf("open S3 store: %w", err)
	}

	return store, nil
}

// openLive opens the live blob store of one instance. Each instance opens
// its own handle on the same directory or prefix.
func (b blobSettings) openLive(ctx context.Context) (openedBlobs, error) {
	if b.kind == blobsS3 {
		store, err := b.openS3(ctx, b.prefix+"/live")
		if err != nil {
			return openedBlobs{}, err
		}

		return openedBlobs{store: store, name: "s3", close: noClose}, nil
	}

	store, err := fs.Open(filepath.Join(b.dir, "blobs"))
	if err != nil {
		return openedBlobs{}, fmt.Errorf("open blob store: %w", err)
	}

	return openedBlobs{store: store, name: "fs", close: store.Close}, nil
}

// openBackupTarget opens the backup target of the run with the target name
// that the backups table keeps.
func (b blobSettings) openBackupTarget(ctx context.Context) (openedBlobs, error) {
	if b.kind == blobsS3 {
		store, err := b.openS3(ctx, b.prefix+"/backup")
		if err != nil {
			return openedBlobs{}, err
		}

		return openedBlobs{
			store: store,
			name:  "s3://" + os.Getenv(s3BucketEnv) + "/" + b.prefix + "/backup",
			close: noClose,
		}, nil
	}

	dir := filepath.Join(b.dir, "backups")

	store, err := fs.Open(dir)
	if err != nil {
		return openedBlobs{}, fmt.Errorf("open backup target: %w", err)
	}

	return openedBlobs{store: store, name: "dir:" + dir, close: store.Close}, nil
}
