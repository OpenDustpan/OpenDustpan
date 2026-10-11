// Package s3 stores blobs in S3-compatible object storage. It signs requests
// with AWS Signature Version 4 from the standard library only. It sends each
// object with one PUT and If-None-Match: *. One PUT holds 5 GiB at most.
//
// To run the conformance suite, start a local S3-compatible server that
// supports conditional writes. Create one bucket. Set
// DUSTPAN_TEST_S3_ENDPOINT, DUSTPAN_TEST_S3_BUCKET, DUSTPAN_TEST_S3_ACCESS_KEY
// and DUSTPAN_TEST_S3_SECRET_KEY. Run make test-s3. Without them the suite
// skips. Any S3-compatible server works. No specific server is required.
package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	// algorithm is the SigV4 algorithm name in the string to sign and the header.
	algorithm = "AWS4-HMAC-SHA256"
	// service is the SigV4 service name for S3-compatible storage.
	service = "s3"
	// requestKind terminates the credential scope.
	requestKind = "aws4_request"
	// amzDateFormat is the ISO 8601 basic format of the x-amz-date header.
	amzDateFormat = "20060102T150405Z"
	// dateStampFormat is the YYYYMMDD format of the credential scope and the key.
	dateStampFormat = "20060102"
	// payloadHashLength is the hex length of a SHA-256 digest.
	payloadHashLength = 64
)

const (
	envAccessKeyID     = "AWS_ACCESS_KEY_ID"
	envSecretAccessKey = "AWS_SECRET_ACCESS_KEY" //nolint:gosec // Environment variable name, not a credential value.
	envSessionToken    = "AWS_SESSION_TOKEN"     //nolint:gosec // Environment variable name, not a credential value.
)

// Sentinel errors returned by the signer. Callers match them with errors.Is.
// The messages carry no credential values.
var (
	// ErrNilLookup reports a nil environment lookup function.
	ErrNilLookup = errors.New("lookup function is nil")
	// ErrMissingCredential reports a missing access key ID or secret access key.
	ErrMissingCredential = errors.New("missing credential")
	// ErrNilSigner reports a nil signer.
	ErrNilSigner = errors.New("signer is nil")
	// ErrNilRequest reports a nil HTTP request.
	ErrNilRequest = errors.New("request is nil")
	// ErrNoRequestURL reports a request without a URL.
	ErrNoRequestURL = errors.New("request has no URL")
	// ErrEmptyRegion reports an empty region.
	ErrEmptyRegion = errors.New("region is empty")
	// ErrEmptyBucket reports an empty bucket.
	ErrEmptyBucket = errors.New("bucket is empty")
	// ErrEmptyAccessKeyID reports empty credentials access key ID.
	ErrEmptyAccessKeyID = errors.New("access key ID is empty")
	// ErrEmptySecretAccessKey reports an empty credentials secret access key.
	ErrEmptySecretAccessKey = errors.New("secret access key is empty")
	// ErrInvalidPayloadHash reports a payload hash that is not 64 hex characters.
	ErrInvalidPayloadHash = errors.New("payload hash is not a 64-character hex string")
	// ErrInvalidEndpoint reports an endpoint that is not an http(s) URL with a host.
	ErrInvalidEndpoint = errors.New("invalid endpoint")
	// ErrInvalidQuery reports a query string that does not parse.
	ErrInvalidQuery = errors.New("invalid query string")
)

// Credentials holds static S3 credentials. The access key ID travels in the
// Authorization header by design. The secret access key and the session token
// never appear in an error or a log line.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// LookupEnv returns the value of an environment variable, as os.LookupEnv
// does. The signer takes it as an argument so tests can inject values.
type LookupEnv func(name string) (string, bool)

// LoadCredentials reads the access key ID, the secret access key and the
// optional session token through lookup. No credential comes from a flag. A
// missing variable is an error that names the variable.
func LoadCredentials(lookup LookupEnv) (Credentials, error) {
	if lookup == nil {
		return Credentials{}, fmt.Errorf("s3: load credentials: %w", ErrNilLookup)
	}

	id, ok := lookup(envAccessKeyID)
	if !ok || id == "" {
		return Credentials{}, fmt.Errorf("s3: load credentials: missing %s: %w", envAccessKeyID, ErrMissingCredential)
	}

	secret, ok := lookup(envSecretAccessKey)
	if !ok || secret == "" {
		return Credentials{}, fmt.Errorf(
			"s3: load credentials: missing %s: %w",
			envSecretAccessKey,
			ErrMissingCredential,
		)
	}

	token, _ := lookup(envSessionToken)

	return Credentials{AccessKeyID: id, SecretAccessKey: secret, SessionToken: token}, nil
}

// Signer signs HTTP requests for one bucket. The zero value signs nothing:
// URL fails until Bucket holds a value, and Sign and URL fail until Region
// and Credentials hold values. Now is
// an optional clock for tests. It defaults to time.Now when nil.
type Signer struct {
	// Bucket is the S3 bucket name.
	Bucket string
	// Region is the region name in the credential scope, for example us-east-1.
	Region string
	// Endpoint is an optional custom endpoint URL for S3-compatible servers,
	// for example http://localhost:9000. Empty selects AWS.
	Endpoint string
	// Credentials holds the static credentials the signer signs with.
	Credentials Credentials
	// Now returns the current time. Tests set a fixed clock.
	Now func() time.Time
}

// PayloadHash returns the lowercase hex SHA-256 of body. The signer sends it
// as x-amz-content-sha256. Hashing an empty body gives the empty hash.
func PayloadHash(body []byte) string {
	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:])
}

// URL returns the object URL for key. With no endpoint it uses virtual-host
// addressing on AWS. With an endpoint it uses path-style addressing. The key
// is a validated blob key. The URL encodes each path segment once.
func (s *Signer) URL(key string) (*url.URL, error) {
	if s == nil {
		return nil, fmt.Errorf("s3: object URL: %w", ErrNilSigner)
	}

	if s.Bucket == "" {
		return nil, fmt.Errorf("s3: object URL: %w", ErrEmptyBucket)
	}

	if s.Endpoint == "" {
		if s.Region == "" {
			return nil, fmt.Errorf("s3: object URL: %w", ErrEmptyRegion)
		}

		host := s.Bucket + ".s3." + s.Region + ".amazonaws.com"
		if s.Region == "us-east-1" {
			host = s.Bucket + ".s3.amazonaws.com"
		}

		return &url.URL{Scheme: "https", Host: host, Path: "/" + key}, nil
	}

	endpoint, err := url.Parse(s.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("s3: object URL: invalid endpoint: %w", err)
	}

	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("s3: object URL: endpoint scheme must be http or https: %w", ErrInvalidEndpoint)
	}

	if endpoint.Host == "" {
		return nil, fmt.Errorf("s3: object URL: endpoint has no host: %w", ErrInvalidEndpoint)
	}

	path := strings.TrimSuffix(endpoint.Path, "/") + "/" + s.Bucket + "/" + key

	return &url.URL{Scheme: endpoint.Scheme, Host: endpoint.Host, Path: path}, nil
}

// Sign signs req for the body with hex SHA-256 payloadHash (see PayloadHash).
// It sets x-amz-content-sha256, x-amz-date, x-amz-security-token when the
// credentials carry a session token, and the Authorization header. It signs
// the host header and every x-amz-* header on the request. Errors never carry
// credential values.
func (s *Signer) Sign(req *http.Request, payloadHash string) error {
	if s == nil {
		return fmt.Errorf("s3: sign request: %w", ErrNilSigner)
	}

	if req == nil {
		return fmt.Errorf("s3: sign request: %w", ErrNilRequest)
	}

	if req.URL == nil {
		return fmt.Errorf("s3: sign request: %w", ErrNoRequestURL)
	}

	if s.Region == "" {
		return fmt.Errorf("s3: sign request: %w", ErrEmptyRegion)
	}

	if s.Credentials.AccessKeyID == "" {
		return fmt.Errorf("s3: sign request: %w", ErrEmptyAccessKeyID)
	}

	if s.Credentials.SecretAccessKey == "" {
		return fmt.Errorf("s3: sign request: %w", ErrEmptySecretAccessKey)
	}

	if !validPayloadHash(payloadHash) {
		return fmt.Errorf("s3: sign request: %w", ErrInvalidPayloadHash)
	}

	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}

	amzDate := now.Format(amzDateFormat)
	dateStamp := now.Format(dateStampFormat)

	setSignHeaders(req, payloadHash, amzDate, s.Credentials.SessionToken)

	signedHeaders, canonicalHeaders := signedHeaderStrings(req)

	canonicalRequest, err := canonicalRequestString(req, canonicalHeaders, signedHeaders, payloadHash)
	if err != nil {
		return err
	}

	stringToSign := algorithm + "\n" + amzDate + "\n" + dateStamp + "/" + s.Region + "/" + service + "/" +
		requestKind + "\n" + sha256Hex(canonicalRequest)

	signingKey, err := deriveSigningKey(s.Credentials.SecretAccessKey, dateStamp, s.Region)
	if err != nil {
		return err
	}

	signature, err := hmacSHA256(signingKey, stringToSign)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", algorithm+" Credential="+s.Credentials.AccessKeyID+"/"+dateStamp+"/"+
		s.Region+"/"+service+"/"+requestKind+",SignedHeaders="+signedHeaders+",Signature="+hex.EncodeToString(signature))

	return nil
}

// setSignHeaders sets the signature headers on req. The session token header
// stays absent when the token is empty.
func setSignHeaders(req *http.Request, payloadHash, amzDate, sessionToken string) {
	if req.Header == nil {
		req.Header = make(http.Header)
	}

	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("X-Amz-Date", amzDate)

	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}
}

// signedHeaderStrings collects the host header and every x-amz-* header on
// the request into canonical form. It returns the signed header names and
// the canonical header block. The two strings stay unnamed, as nonamedreturns
// requires; the first is the names, the second is the block.
//
//nolint:revive // Unnamed two-string result per nonamedreturns; order is documented.
func signedHeaderStrings(req *http.Request) (string, string) {
	headers := make(map[string]string)

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	headers["host"] = collapseSpaces(host)

	for name, values := range req.Header {
		lower := strings.ToLower(name)
		if lower == "authorization" || !strings.HasPrefix(lower, "x-amz-") {
			continue
		}

		headers[lower] = collapseSpaces(strings.Join(values, ","))
	}

	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}

	slices.Sort(names)

	var block strings.Builder

	for _, name := range names {
		block.WriteString(name)
		block.WriteByte(':')
		block.WriteString(headers[name])
		block.WriteByte('\n')
	}

	return strings.Join(names, ";"), block.String()
}

// canonicalRequestString builds the canonical request for req.
func canonicalRequestString(
	req *http.Request,
	canonicalHeaders, signedHeaders, payloadHash string,
) (string, error) {
	path := req.URL.Path
	if path == "" {
		path = "/"
	}

	query, err := canonicalQueryString(req.URL.RawQuery)
	if err != nil {
		return "", err
	}

	var request strings.Builder

	request.WriteString(req.Method)
	request.WriteByte('\n')
	request.WriteString(encodePath(path))
	request.WriteByte('\n')
	request.WriteString(query)
	request.WriteByte('\n')
	request.WriteString(canonicalHeaders)
	request.WriteByte('\n')
	request.WriteString(signedHeaders)
	request.WriteByte('\n')
	request.WriteString(payloadHash)

	return request.String(), nil
}

// canonicalQueryString sorts and encodes the raw query string. An empty query
// stays empty.
func canonicalQueryString(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}

	type pair struct {
		name  string
		value string
	}

	var pairs []pair

	for part := range strings.SplitSeq(raw, "&") {
		if part == "" {
			continue
		}

		name, value, _ := strings.Cut(part, "=")

		decodedName, err := decodeQueryComponent(name)
		if err != nil {
			return "", err
		}

		decodedValue, err := decodeQueryComponent(value)
		if err != nil {
			return "", err
		}

		pairs = append(pairs, pair{name: encodeSegment(decodedName), value: encodeSegment(decodedValue)})
	}

	slices.SortFunc(pairs, func(a, b pair) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}

		return strings.Compare(a.value, b.value)
	})

	encoded := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		encoded = append(encoded, pair.name+"="+pair.value)
	}

	return strings.Join(encoded, "&"), nil
}

// decodeQueryComponent decodes one query name or value. A plus sign means a
// space, as in a URL query.
func decodeQueryComponent(s string) (string, error) {
	decoded, err := url.QueryUnescape(strings.ReplaceAll(s, "+", "%20"))
	if err != nil {
		return "", fmt.Errorf("s3: sign request: %w: %w", ErrInvalidQuery, err)
	}

	return decoded, nil
}

// encodePath encodes each segment of p once and keeps slashes as separators.
func encodePath(p string) string {
	segments := strings.Split(p, "/")

	for i := range segments {
		segments[i] = encodeSegment(segments[i])
	}

	return strings.Join(segments, "/")
}

// encodeSegment percent-encodes s, leaving unreserved characters as is and
// using uppercase hex digits.
func encodeSegment(s string) string {
	const hexDigits = "0123456789ABCDEF"

	var encoded strings.Builder

	for i := range len(s) {
		c := s[i]

		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			encoded.WriteByte(c)
		default:
			encoded.WriteByte('%')
			encoded.WriteByte(hexDigits[c>>4])
			encoded.WriteByte(hexDigits[c&0x0F])
		}
	}

	return encoded.String()
}

// collapseSpaces trims s and reduces each run of whitespace to one space.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// sha256Hex returns the lowercase hex SHA-256 of s.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

// hmacSHA256 returns the HMAC-SHA256 of data under key.
func hmacSHA256(key []byte, data string) ([]byte, error) {
	mac := hmac.New(sha256.New, key)

	if _, err := mac.Write([]byte(data)); err != nil {
		return nil, fmt.Errorf("s3: sign request: calculate HMAC: %w", err)
	}

	return mac.Sum(nil), nil
}

// deriveSigningKey derives the SigV4 signing key for the date stamp, the
// region and the S3 service from the secret access key.
func deriveSigningKey(secret, dateStamp, region string) ([]byte, error) {
	dateKey, err := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	if err != nil {
		return nil, err
	}

	regionKey, err := hmacSHA256(dateKey, region)
	if err != nil {
		return nil, err
	}

	serviceKey, err := hmacSHA256(regionKey, service)
	if err != nil {
		return nil, err
	}

	return hmacSHA256(serviceKey, requestKind)
}

// validPayloadHash reports whether h is a 64-character hex string.
func validPayloadHash(h string) bool {
	if len(h) != payloadHashLength {
		return false
	}

	for i := range len(h) {
		c := h[i]

		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			continue
		}

		return false
	}

	return true
}
