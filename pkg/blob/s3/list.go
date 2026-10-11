package s3

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// maxListBody bounds one ListObjectsV2 answer. A page holds at most 1000
// keys of at most 1024 bytes each, so 8 MiB leaves room for the XML.
const maxListBody = 8 << 20

// ErrListTooLarge reports a ListObjectsV2 answer above 8 MiB.
var ErrListTooLarge = errors.New("list answer exceeds 8 MiB")

// listResult is the part of a ListObjectsV2 answer that List reads.
type listResult struct {
	NextContinuationToken string        `xml:"NextContinuationToken"`
	Contents              []listContent `xml:"Contents"`
	IsTruncated           bool          `xml:"IsTruncated"`
}

// listContent is one object of a ListObjectsV2 answer.
type listContent struct {
	LastModified time.Time `xml:"LastModified"`
	Key          string    `xml:"Key"`
}

// List returns the objects under prefix, ordered by key. Prefix names a key
// directory with or without a trailing slash. It sends ListObjectsV2
// requests and follows the continuation token until the last page. An
// object name that is not a valid blob key, for example one that another
// tool wrote under the store prefix, is not listed.
func (s *Store) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("s3: list %s: %w", prefix, err)
	}

	listPrefix := s.listPrefix(prefix)

	var (
		out   []blob.Object
		token string
	)

	for {
		page, err := s.listPage(ctx, listPrefix, token)
		if err != nil {
			return nil, fmt.Errorf("s3: list %s: %w", prefix, err)
		}

		out = append(out, s.pageObjects(page)...)

		if !page.IsTruncated || page.NextContinuationToken == "" {
			break
		}

		token = page.NextContinuationToken
	}

	slices.SortFunc(out, func(a, b blob.Object) int {
		return strings.Compare(a.Key.String(), b.Key.String())
	})

	return out, nil
}

// listPrefix returns the object name prefix that List sends for prefix:
// the store prefix and the key directory, with one trailing slash, or ""
// for the whole bucket.
func (s *Store) listPrefix(prefix string) string {
	name := strings.Trim(prefix, "/")

	if s.prefix != "" {
		name = strings.TrimSuffix(s.prefix+"/"+name, "/")
	}

	if name == "" {
		return ""
	}

	return name + "/"
}

// pageObjects converts the objects of one page into blob objects. It strips
// the store prefix and skips names that are not valid blob keys.
func (s *Store) pageObjects(page listResult) []blob.Object {
	out := make([]blob.Object, 0, len(page.Contents))

	for _, content := range page.Contents {
		name := content.Key

		if s.prefix != "" {
			trimmed, ok := strings.CutPrefix(name, s.prefix+"/")
			if !ok {
				continue
			}

			name = trimmed
		}

		key, err := blob.ParseKey(name)
		if err != nil {
			continue
		}

		out = append(out, blob.Object{ModifiedAt: content.LastModified.UTC(), Key: key})
	}

	return out
}

// listPage fetches one page with retries.
func (s *Store) listPage(ctx context.Context, listPrefix, token string) (listResult, error) {
	var lastErr error

	for attempt := range s.maxAttempts {
		if err := ctx.Err(); err != nil {
			return listResult{}, fmt.Errorf("list page: %w", err)
		}

		page, err := s.listOnce(ctx, listPrefix, token)
		if err == nil {
			return page, nil
		}

		lastErr = err

		retryErr, isRetry := errors.AsType[*retryableError](lastErr)
		if !isRetry || retryErr == nil || attempt+1 >= s.maxAttempts {
			return listResult{}, lastErr
		}

		if waitErr := s.wait(ctx, attempt); waitErr != nil {
			return listResult{}, waitErr
		}
	}

	return listResult{}, lastErr
}

// listOnce sends one ListObjectsV2 request.
func (s *Store) listOnce(ctx context.Context, listPrefix, token string) (listResult, error) {
	listURL, err := s.bucketURL()
	if err != nil {
		return listResult{}, fmt.Errorf("bucket URL: %w", err)
	}

	query := url.Values{}
	query.Set("list-type", "2")
	query.Set("prefix", listPrefix)

	if token != "" {
		query.Set("continuation-token", token)
	}

	listURL.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL.String(), http.NoBody)
	if err != nil {
		return listResult{}, fmt.Errorf("build request: %w", err)
	}

	if signErr := s.signer.Sign(req, emptyHash()); signErr != nil {
		return listResult{}, fmt.Errorf("sign request: %w", signErr)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return listResult{}, fmt.Errorf("send request: %w", err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read below.
	}()

	if resp.StatusCode != http.StatusOK {
		code := readCode(resp.Body)

		if shouldRetry(resp.StatusCode, code) {
			return listResult{}, &retryableError{status: resp.StatusCode, code: code}
		}

		return listResult{}, &plainError{status: resp.StatusCode, code: code}
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxListBody+1))
	if err != nil {
		return listResult{}, fmt.Errorf("read list answer: %w", err)
	}

	if len(data) > maxListBody {
		return listResult{}, fmt.Errorf("read list answer: %w", ErrListTooLarge)
	}

	var page listResult
	if err := xml.Unmarshal(data, &page); err != nil {
		return listResult{}, fmt.Errorf("parse list answer: %w", err)
	}

	return page, nil
}
