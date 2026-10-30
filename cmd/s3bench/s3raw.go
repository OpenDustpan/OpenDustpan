package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/s3"
)

// maxRawAnswer caps how much of an S3 answer the raw client reads.
const maxRawAnswer = 64 << 10

// errS3Status reports an S3 answer with an unexpected status.
var errS3Status = errors.New("S3 answered an unexpected status")

// s3Target is one S3-compatible store of the run, reached through its proxy.
// raw requests and the adapter share one client, so both reuse the same
// connections.
type s3Target struct {
	name    string
	signer  *s3.Signer
	client  *http.Client
	adapter *s3.Store
}

// newS3Target answers the target name at endpoint with the credentials in
// creds. The adapter opens without the conditional-write probe, so a store
// that ignores If-None-Match serves the run too.
func newS3Target(name, endpoint, bucket string, creds s3.Credentials) (*s3Target, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("%w: the default transport is not an *http.Transport", errBadFlag)
	}

	tuned := transport.Clone()
	tuned.MaxIdleConnsPerHost = maxParallel

	signer := &s3.Signer{Bucket: bucket, Region: s3Region, Endpoint: endpoint, Credentials: creds, Now: nil}
	client := &http.Client{Transport: tuned}

	adapter, err := s3.New(signer, "", client)
	if err != nil {
		return nil, fmt.Errorf("open %s adapter: %w", name, err)
	}

	return &s3Target{name: name, signer: signer, client: client, adapter: adapter}, nil
}

// rawRequest is one signed S3 request of the raw client.
type rawRequest struct {
	method string
	url    string
	body   []byte
	hash   string
	header http.Header
}

// send signs and sends req and answers the status. The answer body is read
// and dropped.
func (t *s3Target) send(ctx context.Context, r rawRequest) (int, error) {
	req, err := http.NewRequestWithContext(ctx, r.method, r.url, bytes.NewReader(r.body))
	if err != nil {
		return 0, fmt.Errorf("build %s request: %w", r.method, err)
	}

	req.ContentLength = int64(len(r.body))

	for name, values := range r.header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}

	if signErr := t.signer.Sign(req, r.hash); signErr != nil {
		return 0, fmt.Errorf("sign %s request: %w", r.method, signErr)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("send %s request: %w", r.method, err)
	}

	_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, maxRawAnswer))
	if closeErr := errors.Join(readErr, resp.Body.Close()); closeErr != nil {
		return 0, fmt.Errorf("read %s answer: %w", r.method, closeErr)
	}

	return resp.StatusCode, nil
}

// conditionalHeader answers the header of a create-only PUT.
func conditionalHeader() http.Header {
	h := http.Header{}
	h.Set("If-None-Match", "*")

	return h
}

// rawPut sends one PUT of p under key with the headers hdr: the
// conditional header for a create-only write, or none for a plain write
// under a unique key.
func (t *s3Target) rawPut(ctx context.Context, key string, p payload, hdr http.Header) (int, error) {
	target, err := t.signer.URL(key)
	if err != nil {
		return 0, fmt.Errorf("object URL: %w", err)
	}

	return t.send(
		ctx,
		rawRequest{method: http.MethodPut, url: target.String(), body: p.body, hash: p.hash, header: hdr},
	)
}

// createBucket creates the bucket of the target. A bucket that the same
// credentials already own is not an error.
func (t *s3Target) createBucket(ctx context.Context) error {
	target, err := t.signer.URL("")
	if err != nil {
		return fmt.Errorf("bucket URL: %w", err)
	}

	bucketURL := strings.TrimSuffix(target.String(), "/")

	status, err := t.send(ctx, rawRequest{
		method: http.MethodPut, url: bucketURL, body: nil, hash: s3.PayloadHash(nil), header: http.Header{},
	})
	if err != nil {
		return fmt.Errorf("create bucket on %s: %w", t.name, err)
	}

	if status != http.StatusOK && status != http.StatusConflict {
		return fmt.Errorf("create bucket on %s: status %d: %w", t.name, status, errS3Status)
	}

	return nil
}

// conditionalCheck is the answer of the If-None-Match check of a store.
type conditionalCheck struct {
	FirstStatus  int  `json:"firstStatus"`
	SecondStatus int  `json:"secondStatus"`
	Enforced     bool `json:"enforced"`
}

// checkConditional writes one key twice with If-None-Match: * and reports
// whether the store refused the second write with 412 or 409.
func (t *s3Target) checkConditional(ctx context.Context, key string) (conditionalCheck, error) {
	body := []byte("opendustpan-bench-conditional-check")
	p := payload{body: body, hash: s3.PayloadHash(body)}

	first, err := t.rawPut(ctx, key, p, conditionalHeader())
	if err != nil {
		return conditionalCheck{}, err
	}

	second, err := t.rawPut(ctx, key, p, conditionalHeader())
	if err != nil {
		return conditionalCheck{}, err
	}

	parsed, err := blob.ParseKey(key)
	if err != nil {
		return conditionalCheck{}, fmt.Errorf("conditional check key: %w", err)
	}

	if deleteErr := t.adapter.Delete(ctx, parsed); deleteErr != nil {
		return conditionalCheck{}, fmt.Errorf("delete conditional check key: %w", deleteErr)
	}

	enforced := second == http.StatusPreconditionFailed || second == http.StatusConflict

	return conditionalCheck{FirstStatus: first, SecondStatus: second, Enforced: enforced}, nil
}
