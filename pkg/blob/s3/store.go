package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

const (
	// defaultMaxAttempts is the number of tries for one operation.
	defaultMaxAttempts = 5
	// defaultMaxObjectSize is the largest object Put sends: 5 GiB.
	defaultMaxObjectSize = 5 << 30
	// maxErrorBody caps how much of an error response the adapter reads.
	maxErrorBody = 32 << 10
	// slowDownCode is the retryable S3 error code for an overloaded store.
	slowDownCode = "SlowDown"
	// conditionalConflictCode marks a 409 from a failed conditional write.
	conditionalConflictCode = "ConditionalRequestConflict"
	// noSuchKeyCode marks a missing object.
	noSuchKeyCode = "NoSuchKey"
	// backoffBase is the delay before the first retry.
	backoffBase = 100 * time.Millisecond
	// backoffCap caps the delay between retries.
	backoffCap = 5 * time.Second
)

// ErrTooLarge reports that an object exceeds the 5 GiB limit.
var ErrTooLarge = errors.New("object exceeds the 5 GiB limit")

// ErrNilReader reports a nil upload reader.
var ErrNilReader = errors.New("reader is nil")

// ErrPingFailed reports that the bucket did not answer.
var ErrPingFailed = errors.New("ping bucket failed")

// s3Error is the XML body of an S3 error answer.
type s3Error struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
}

// Option sets one optional Store field.
type Option func(*Store)

// WithMaxAttempts sets the number of tries for one operation.
func WithMaxAttempts(n int) Option {
	return func(s *Store) {
		if n > 0 {
			s.maxAttempts = n
		}
	}
}

// WithBackoff sets the delay before retry number attempt.
func WithBackoff(backoff func(attempt int) time.Duration) Option {
	return func(s *Store) {
		if backoff != nil {
			s.backoff = backoff
		}
	}
}

// WithSleep sets the sleep function. Tests inject a no-op sleep.
func WithSleep(sleep func(ctx context.Context, d time.Duration) error) Option {
	return func(s *Store) {
		if sleep != nil {
			s.sleep = sleep
		}
	}
}

// WithMaxObjectSize sets the largest object Put sends.
func WithMaxObjectSize(n int64) Option {
	return func(s *Store) {
		if n > 0 {
			s.maxSize = n
		}
	}
}

// Store is a blob.Store for one bucket and key prefix.
type Store struct {
	signer      *Signer
	prefix      string
	client      *http.Client
	maxAttempts int
	backoff     func(attempt int) time.Duration
	sleep       func(ctx context.Context, d time.Duration) error
	maxSize     int64
}

var _ blob.Store = (*Store)(nil)

// New returns a Store that signs with signer, prepends prefix to every
// object name and sends through client. A nil client uses http.DefaultClient.
// Prefix may be empty. The store makes no request until first use.
func New(signer *Signer, prefix string, client *http.Client, opts ...Option) (*Store, error) {
	if signer == nil {
		return nil, fmt.Errorf("s3: open store: %w", ErrNilSigner)
	}

	if signer.Bucket == "" {
		return nil, fmt.Errorf("s3: open store: %w", ErrEmptyBucket)
	}

	if signer.Region == "" {
		return nil, fmt.Errorf("s3: open store: %w", ErrEmptyRegion)
	}

	if client == nil {
		client = http.DefaultClient
	}

	s := &Store{
		signer:      signer,
		prefix:      strings.Trim(prefix, "/"),
		client:      client,
		maxAttempts: defaultMaxAttempts,
		backoff:     defaultBackoff,
		sleep:       defaultSleep,
		maxSize:     defaultMaxObjectSize,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	return s, nil
}

// Put stores the contents of r under key. It returns blob.ErrExists when the
// key already holds an object and ErrTooLarge when the object exceeds 5 GiB.
func (s *Store) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("s3: put %s: %w", key, err)
	}

	if r == nil {
		return fmt.Errorf("s3: put %s: %w", key, ErrNilReader)
	}

	staged, err := stageReader(r, s.maxSize)
	if err != nil {
		return fmt.Errorf("s3: put %s: %w", key, err)
	}

	defer func() {
		_ = os.Remove(staged.path) //nolint:errcheck // Best-effort cleanup of the temporary file.
	}()

	if err := s.putLoop(ctx, key, staged); err != nil {
		return fmt.Errorf("s3: put %s: %w", key, err)
	}

	return nil
}

// Get opens the object under key. The caller closes the reader.
func (s *Store) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("s3: get %s: %w", key, err)
	}

	var lastErr error

	for attempt := range s.maxAttempts {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("s3: get %s: %w", key, err)
		}

		body, err := s.getOnce(ctx, key)
		if err == nil {
			return body, nil
		}

		lastErr = err

		if missingErr, ok := errors.AsType[*missingError](err); ok && missingErr != nil {
			return nil, fmt.Errorf("s3: get %s: %w", key, blob.ErrNotFound)
		}

		retryErr, isRetry := errors.AsType[*retryableError](err)
		if !isRetry || retryErr == nil || attempt+1 >= s.maxAttempts {
			return nil, fmt.Errorf("s3: get %s: %w", key, lastErr)
		}

		if err := s.wait(ctx, attempt); err != nil {
			return nil, fmt.Errorf("s3: get %s: %w", key, err)
		}
	}

	return nil, fmt.Errorf("s3: get %s: %w", key, lastErr)
}

// Delete removes the object under key. A missing key is not an error.
func (s *Store) Delete(ctx context.Context, key blob.Key) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("s3: delete %s: %w", key, err)
	}

	var lastErr error

	for attempt := range s.maxAttempts {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("s3: delete %s: %w", key, err)
		}

		deleteErr := s.deleteOnce(ctx, key)
		if deleteErr == nil {
			return nil
		}

		lastErr = deleteErr

		retryErr, isRetry := errors.AsType[*retryableError](lastErr)
		if !isRetry || retryErr == nil || attempt+1 >= s.maxAttempts {
			return fmt.Errorf("s3: delete %s: %w", key, lastErr)
		}

		if err := s.wait(ctx, attempt); err != nil {
			return fmt.Errorf("s3: delete %s: %w", key, err)
		}
	}

	return fmt.Errorf("s3: delete %s: %w", key, lastErr)
}

// Ping checks that the bucket answers. The error holds no credential and no
// query string.
func (s *Store) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("s3: ping bucket: %w", err)
	}

	var lastErr error

	for attempt := range s.maxAttempts {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("s3: ping bucket: %w", err)
		}

		pingErr := s.pingOnce(ctx)
		if pingErr == nil {
			return nil
		}

		lastErr = pingErr

		retryErr, isRetry := errors.AsType[*retryableError](lastErr)
		if !isRetry || retryErr == nil || attempt+1 >= s.maxAttempts {
			return sanitizePingError(lastErr)
		}

		if err := s.wait(ctx, attempt); err != nil {
			return fmt.Errorf("s3: ping bucket: %w", err)
		}
	}

	return sanitizePingError(lastErr)
}

// putLoop sends the staged upload with retries.
func (s *Store) putLoop(ctx context.Context, key blob.Key, staged stagedUpload) error {
	var lastErr error

	for attempt := range s.maxAttempts {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("check context: %w", err)
		}

		lastErr = s.putOnce(ctx, key, staged)

		if lastErr == nil {
			return nil
		}

		if existsErr, ok := errors.AsType[*existsError](lastErr); ok && existsErr != nil {
			return blob.ErrExists
		}

		retryErr, isRetry := errors.AsType[*retryableError](lastErr)
		if !isRetry || retryErr == nil || attempt+1 >= s.maxAttempts {
			return lastErr
		}

		if err := s.wait(ctx, attempt); err != nil {
			return err
		}
	}

	return lastErr
}

// existsError marks a failed conditional write.
type existsError struct {
	status int
	code   string
}

func (e *existsError) Error() string {
	if e.code == "" {
		return fmt.Sprintf("status %d", e.status)
	}

	return fmt.Sprintf("status %d code %s", e.status, e.code)
}

// missingError marks a missing object.
type missingError struct {
	status int
	code   string
}

func (e *missingError) Error() string {
	if e.code == "" {
		return fmt.Sprintf("status %d", e.status)
	}

	return fmt.Sprintf("status %d code %s", e.status, e.code)
}

// retryableError marks a 5xx, 429 or SlowDown answer.
type retryableError struct {
	status int
	code   string
}

func (e *retryableError) Error() string {
	if e.code == "" {
		return fmt.Sprintf("status %d", e.status)
	}

	return fmt.Sprintf("status %d code %s", e.status, e.code)
}

// plainError holds a final status and code, never the body.
type plainError struct {
	status int
	code   string
}

func (e *plainError) Error() string {
	if e.code == "" {
		return fmt.Sprintf("status %d", e.status)
	}

	return fmt.Sprintf("status %d code %s", e.status, e.code)
}

// stagedUpload is a reader staged in a temporary file.
type stagedUpload struct {
	path string
	size int64
	hash string
}

// stageReader copies r to a temporary file. It removes the file and returns
// ErrTooLarge past maxSize.
func stageReader(r io.Reader, maxSize int64) (stagedUpload, error) {
	f, err := os.CreateTemp("", "opendustpan-s3-*")
	if err != nil {
		return stagedUpload{}, fmt.Errorf("stage upload: %w", err)
	}

	tmp := f.Name()
	hasher := sha256.New()

	size, err := io.Copy(f, io.LimitReader(io.TeeReader(r, hasher), maxSize+1))
	if err != nil {
		_ = f.Close()      //nolint:errcheck // Cleanup path; the copy error matters.
		_ = os.Remove(tmp) //nolint:errcheck // Best-effort cleanup of the temporary file.

		return stagedUpload{}, fmt.Errorf("stage upload: %w", err)
	}

	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(tmp) //nolint:errcheck // Best-effort cleanup of the temporary file.

		return stagedUpload{}, fmt.Errorf("stage upload: %w", closeErr)
	}

	if size > maxSize {
		_ = os.Remove(tmp) //nolint:errcheck // Best-effort cleanup of the temporary file.

		return stagedUpload{}, fmt.Errorf(
			"stage upload: object size %d exceeds the %d-byte limit: %w",
			size,
			maxSize,
			ErrTooLarge,
		)
	}

	return stagedUpload{path: tmp, size: size, hash: hex.EncodeToString(hasher.Sum(nil))}, nil
}

// putOnce sends the staged file once.
func (s *Store) putOnce(ctx context.Context, key blob.Key, staged stagedUpload) error {
	f, err := os.Open(staged.path)
	if err != nil {
		return fmt.Errorf("open staged file: %w", err)
	}

	defer func() {
		_ = f.Close() //nolint:errcheck // The request body is sent; close errors do not matter.
	}()

	objectURL, err := s.objectURL(key)
	if err != nil {
		return fmt.Errorf("object URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, objectURL.String(), f)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.ContentLength = staged.size
	req.Header.Set("If-None-Match", "*")

	if signErr := s.signer.Sign(req, staged.hash); signErr != nil {
		return fmt.Errorf("sign request: %w", signErr)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	if resp.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody)) //nolint:errcheck // Drain only.

		return nil
	}

	code := readCode(resp.Body)

	switch {
	case resp.StatusCode == http.StatusPreconditionFailed:
		return &existsError{status: resp.StatusCode, code: code}
	case resp.StatusCode == http.StatusConflict && code == conditionalConflictCode:
		return &existsError{status: resp.StatusCode, code: code}
	case shouldRetry(resp.StatusCode, code):
		return &retryableError{status: resp.StatusCode, code: code}
	default:
		return &plainError{status: resp.StatusCode, code: code}
	}
}

// getOnce sends one GET request. On success it returns the open body.
func (s *Store) getOnce(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	objectURL, err := s.objectURL(key)
	if err != nil {
		return nil, fmt.Errorf("object URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, objectURL.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if signErr := s.signer.Sign(req, emptyHash()); signErr != nil {
		return nil, fmt.Errorf("sign request: %w", signErr)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode == http.StatusOK {
		return resp.Body, nil
	}

	code := readCode(resp.Body)
	_ = resp.Body.Close() //nolint:errcheck // The body was read to completion above.

	switch {
	case resp.StatusCode == http.StatusNotFound && code == noSuchKeyCode:
		return nil, &missingError{status: resp.StatusCode, code: code}
	case shouldRetry(resp.StatusCode, code):
		return nil, &retryableError{status: resp.StatusCode, code: code}
	default:
		return nil, &plainError{status: resp.StatusCode, code: code}
	}
}

// deleteOnce sends one DELETE request. A 404 answer is success.
func (s *Store) deleteOnce(ctx context.Context, key blob.Key) error {
	objectURL, err := s.objectURL(key)
	if err != nil {
		return fmt.Errorf("object URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, objectURL.String(), http.NoBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	if signErr := s.signer.Sign(req, emptyHash()); signErr != nil {
		return fmt.Errorf("sign request: %w", signErr)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 || resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody)) //nolint:errcheck // Drain only.

		return nil
	}

	code := readCode(resp.Body)

	if shouldRetry(resp.StatusCode, code) {
		return &retryableError{status: resp.StatusCode, code: code}
	}

	return &plainError{status: resp.StatusCode, code: code}
}

// pingOnce sends one HEAD request for the bucket.
func (s *Store) pingOnce(ctx context.Context) error {
	bucketURL, err := s.bucketURL()
	if err != nil {
		return fmt.Errorf("bucket URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, bucketURL.String(), http.NoBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	if signErr := s.signer.Sign(req, emptyHash()); signErr != nil {
		return fmt.Errorf("sign request: %w", signErr)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // A HEAD answer has no body.
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	code := readCode(resp.Body)

	if shouldRetry(resp.StatusCode, code) {
		return &retryableError{status: resp.StatusCode, code: code}
	}

	return &plainError{status: resp.StatusCode, code: code}
}

// objectName prepends the prefix to key.
func (s *Store) objectName(key blob.Key) string {
	if s.prefix == "" {
		return key.String()
	}

	return s.prefix + "/" + key.String()
}

// objectURL returns the object URL for key.
func (s *Store) objectURL(key blob.Key) (*url.URL, error) {
	return s.signer.URL(s.objectName(key))
}

// bucketURL returns the HEAD URL for the bucket with no query string. It
// reuses the signer address logic through an empty key.
func (s *Store) bucketURL() (*url.URL, error) {
	bucketURL, err := s.signer.URL("")
	if err != nil {
		return nil, err
	}

	bucketURL.Path = strings.TrimSuffix(bucketURL.Path, "/")

	if bucketURL.Path == "" {
		bucketURL.Path = "/"
	}

	bucketURL.RawQuery = ""

	return bucketURL, nil
}

// wait sleeps for the backoff delay of retry attempt.
func (s *Store) wait(ctx context.Context, attempt int) error {
	return s.sleep(ctx, s.backoff(attempt))
}

// shouldRetry reports whether status and code allow another try.
func shouldRetry(status int, code string) bool {
	if code == slowDownCode {
		return true
	}

	if status == http.StatusTooManyRequests {
		return true
	}

	return status >= 500 && status <= 599
}

// readCode parses the S3 XML error code from r. It returns "" when the body
// has no code. It never returns the body itself.
func readCode(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, maxErrorBody))
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return ""
	}

	var parsed s3Error
	if err := xml.Unmarshal(data, &parsed); err != nil {
		return ""
	}

	return parsed.Code
}

// sanitizePingError wraps a ping failure without a credential, a path or a
// query. Typed answers keep only the status and the code. Other failures
// report only ErrPingFailed: the cause may hold the bucket URL (for example a
// transport error from the HTTP client), and signing errors stay out of the
// message for the same reason.
func sanitizePingError(err error) error {
	if retry, ok := errors.AsType[*retryableError](err); ok {
		if retry.code == "" {
			return fmt.Errorf("s3: ping bucket: status %d: %w", retry.status, ErrPingFailed)
		}

		return fmt.Errorf("s3: ping bucket: status %d code %s: %w", retry.status, retry.code, ErrPingFailed)
	}

	if plain, ok := errors.AsType[*plainError](err); ok {
		if plain.code == "" {
			return fmt.Errorf("s3: ping bucket: status %d: %w", plain.status, ErrPingFailed)
		}

		return fmt.Errorf("s3: ping bucket: status %d code %s: %w", plain.status, plain.code, ErrPingFailed)
	}

	return fmt.Errorf("s3: ping bucket: %w", ErrPingFailed)
}

// emptyHash returns the SHA-256 of an empty body.
func emptyHash() string {
	return PayloadHash(nil)
}

// defaultBackoff delays the base interval, doubling each retry, capped.
func defaultBackoff(attempt int) time.Duration {
	delay := backoffBase

	for range attempt {
		delay *= 2

		if delay >= backoffCap {
			return backoffCap
		}
	}

	return delay
}

// defaultSleep waits for d or returns early when ctx ends.
func defaultSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("sleep interrupted: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
