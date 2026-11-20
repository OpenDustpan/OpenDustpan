package s3_test

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob/s3"
)

// authorizationPattern is the form of each Authorization header that Sign
// writes with the published example key, date and region.
var authorizationPattern = regexp.MustCompile(
	`^AWS4-HMAC-SHA256 Credential=` + vectorAccessKeyID + `/20130524/us-east-1/s3/aws4_request,` +
		`SignedHeaders=host(;x-amz-[a-z0-9-]+)+,Signature=[0-9a-f]{64}$`,
)

// FuzzSignCanonical checks the canonicalization of Sign on any path, query
// and header value:
//   - Sign fails only with ErrInvalidQuery, and the error holds no secret;
//   - the Authorization header has the SigV4 form and holds no secret;
//   - the signature does not change with the order of the query pairs, with
//     "+" in place of "%20" in the query, or with runs of spaces in a signed
//     header value, because the canonical request sorts, decodes and
//     collapses them.
func FuzzSignCanonical(f *testing.F) {
	f.Add("/test.txt", "", "value")
	f.Add("/", "lifecycle=", "")
	f.Add("/a b/ü/%2F", "max-keys=2&prefix=J", "  a   b  ")
	f.Add("/photos/2006/February/sample.jpg", "list-type=2&prefix=a+b&delimiter=%2F", "x\ty")
	f.Add("", "a=1&a=0&&=&b", "\x00\r\n")
	f.Add("/k", "bad=%zz", "v")

	f.Fuzz(func(t *testing.T, path, query, value string) {
		signer := vectorSigner("us-east-1", "", "")

		first, err := signFuzzRequest(t, signer, path, query, value)
		if err != nil {
			checkSignError(t, path, query, err)

			return
		}

		if !authorizationPattern.MatchString(first) {
			t.Fatalf("Sign(%q, %q) wrote Authorization %q, want the SigV4 form", path, query, first)
		}

		if strings.Contains(first, vectorSecretAccessKey) {
			t.Fatalf("Sign(%q, %q): the Authorization header holds the secret access key", path, query)
		}

		checkSignVariants(t, path, query, value, first)
	})
}

// FuzzSignerURL checks that URL either fails or answers an http or https URL
// with a host whose path ends with the bucket and the key.
func FuzzSignerURL(f *testing.F) {
	f.Add("", "a/b")
	f.Add("http://localhost:9000", "tofu/ws-1/sv-1/a1")
	f.Add("https://s3.example.test/base/", "k")
	f.Add("ftp://example.test", "k")
	f.Add("http://", "k")
	f.Add("http://[::1]:9000/%2e%2e/", "../x")

	f.Fuzz(func(t *testing.T, endpoint, key string) {
		signer := vectorSigner("us-east-1", endpoint, "")

		u, err := signer.URL(key)
		if err != nil {
			return
		}

		if u.Scheme != "http" && u.Scheme != "https" {
			t.Fatalf("URL(%q) with endpoint %q has the scheme %q", key, endpoint, u.Scheme)
		}

		if u.Host == "" {
			t.Fatalf("URL(%q) with endpoint %q has no host", key, endpoint)
		}

		if !strings.HasSuffix(u.Path, "/"+key) {
			t.Fatalf("URL(%q) with endpoint %q has the path %q, want the key at its end", key, endpoint, u.Path)
		}

		if endpoint != "" && !strings.HasSuffix(u.Path, "/examplebucket/"+key) {
			t.Fatalf("URL(%q) with endpoint %q has the path %q, want the bucket before the key", key, endpoint, u.Path)
		}
	})
}

// checkSignError fails t unless err of Sign wraps ErrInvalidQuery and holds
// no secret.
func checkSignError(t *testing.T, path, query string, err error) {
	t.Helper()

	if !errors.Is(err, s3.ErrInvalidQuery) {
		t.Fatalf("Sign(%q, %q) = %v, want ErrInvalidQuery or no error", path, query, err)
	}

	if strings.Contains(err.Error(), vectorSecretAccessKey) {
		t.Fatalf("Sign(%q, %q): the error holds the secret access key", path, query)
	}
}

// checkSignVariants fails t unless each request that has the same canonical
// form as the first one gets the Authorization header first.
func checkSignVariants(t *testing.T, path, query, value, first string) {
	t.Helper()

	signer := vectorSigner("us-east-1", "", "")

	for _, variant := range []struct {
		name, query, value string
	}{
		{name: "the same request", query: query, value: value},
		{name: "reversed query pairs", query: reversePairs(query), value: value},
		{name: "%20 in place of +", query: strings.ReplaceAll(query, "+", "%20"), value: value},
		{name: "collapsed header spaces", query: query, value: strings.Join(strings.Fields(value), " ")},
	} {
		again, err := signFuzzRequest(t, signer, path, variant.query, variant.value)
		if err != nil || again != first {
			t.Fatalf(
				"Sign with %s (%q, %q) = %q, %v, want %q",
				variant.name, variant.query, variant.value, again, err, first,
			)
		}
	}
}

// signFuzzRequest signs a new GET request with path, raw query and one
// x-amz-meta-fuzz header value, and returns its Authorization header.
func signFuzzRequest(t *testing.T, signer *s3.Signer, path, query, value string) (string, error) {
	t.Helper()

	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: "https", Host: "examplebucket.s3.amazonaws.com", Path: path, RawQuery: query},
		Header: http.Header{"X-Amz-Meta-Fuzz": []string{value}},
	}

	if err := signer.Sign(req, vectorEmptyHash); err != nil {
		return "", err
	}

	return req.Header.Get("Authorization"), nil
}

// reversePairs returns the pairs of a raw query in reverse order.
func reversePairs(query string) string {
	pairs := strings.Split(query, "&")
	slices.Reverse(pairs)

	return strings.Join(pairs, "&")
}
