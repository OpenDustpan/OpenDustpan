package s3_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/blob/s3"
)

// Published vectors come from the S3 Developer Guide, "Signature Calculations
// for the Authorization Header: Transferring Payload in a Single Chunk (AWS
// Signature Version 4)", which gives the canonical request, the string to
// sign and the signature for each example:
// https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sig-v4-header-based-auth.html
// The examples use the key AKIAIOSFODNN7EXAMPLE, the date 20130524T000000Z,
// the bucket examplebucket and the region us-east-1.

const (
	vectorAccessKeyID     = "AKIAIOSFODNN7EXAMPLE"                     //nolint:gosec // Published AWS documentation example, not a real key.
	vectorSecretAccessKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" //nolint:gosec // Published AWS documentation example.
	vectorAmzDate         = "20130524T000000Z"
	vectorEmptyHash       = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	vectorBodyHash        = "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072"
)

// vectorSigner returns a signer with the published example credentials and a
// fixed clock at the published example date. The bucket is always
// examplebucket from the published examples.
func vectorSigner(region, endpoint, sessionToken string) *s3.Signer {
	return &s3.Signer{
		Bucket:   "examplebucket",
		Region:   region,
		Endpoint: endpoint,
		Credentials: s3.Credentials{
			AccessKeyID:     vectorAccessKeyID,
			SecretAccessKey: vectorSecretAccessKey,
			SessionToken:    sessionToken,
		},
		Now: func() time.Time {
			return time.Date(2013, time.May, 24, 0, 0, 0, 0, time.UTC)
		},
	}
}

func TestSignPublishedGetVector(t *testing.T) {
	t.Parallel()

	signer := vectorSigner("us-east-1", "", "")

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"https://examplebucket.s3.amazonaws.com/?lifecycle=",
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	if err := signer.Sign(req, vectorEmptyHash); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if got := req.Header.Get("X-Amz-Date"); got != vectorAmzDate {
		t.Errorf("X-Amz-Date = %q, want %q", got, vectorAmzDate)
	}

	if got := req.Header.Get("X-Amz-Content-Sha256"); got != vectorEmptyHash {
		t.Errorf("X-Amz-Content-Sha256 = %q, want %q", got, vectorEmptyHash)
	}

	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request," +
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date," +
		"Signature=fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543"

	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

func TestSignPublishedQueryVector(t *testing.T) {
	t.Parallel()

	// The published ListObjects example signs max-keys=2&prefix=J. Both
	// orders below must give the published signature, which checks that the
	// canonical query string sorts parameters after encoding.
	queries := []string{"max-keys=2&prefix=J", "prefix=J&max-keys=2"}

	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			signer := vectorSigner("us-east-1", "", "")

			req, err := http.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				"https://examplebucket.s3.amazonaws.com/?"+query,
				http.NoBody,
			)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}

			if err := signer.Sign(req, vectorEmptyHash); err != nil {
				t.Fatalf("Sign: %v", err)
			}

			want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request," +
				"SignedHeaders=host;x-amz-content-sha256;x-amz-date," +
				"Signature=34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"

			if got := req.Header.Get("Authorization"); got != want {
				t.Errorf("Authorization = %q, want %q", got, want)
			}
		})
	}
}

func TestSignPutWithBody(t *testing.T) {
	t.Parallel()

	// The body and its hash come from the published PUT example, which signs
	// the payload hash 44ce7dd6... for the body "Welcome to Amazon S3.".
	body := "Welcome to Amazon S3."

	if got := s3.PayloadHash([]byte(body)); got != vectorBodyHash {
		t.Fatalf("PayloadHash = %q, want %q", got, vectorBodyHash)
	}

	signer := vectorSigner("us-east-1", "", "")

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"https://examplebucket.s3.amazonaws.com/test.txt",
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	if err := signer.Sign(req, vectorBodyHash); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if got := req.Header.Get("X-Amz-Content-Sha256"); got != vectorBodyHash {
		t.Errorf("X-Amz-Content-Sha256 = %q, want %q", got, vectorBodyHash)
	}

	// The signature below is not published: the published PUT example signs
	// extra headers that the signer does not set. It was checked against an
	// independent Python implementation of the same documented steps.
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request," +
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date," +
		"Signature=e8a654937e21054a5da5e95e82cff2d3ee352d5aa6e0d966385c61adda282699"

	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

func TestSignEncodesEachSegmentOnce(t *testing.T) {
	t.Parallel()

	// The key test$file.text must enter the canonical URI as /test%24file.text,
	// as in the published PUT example. The signature below was checked against
	// an independent Python implementation of the same documented steps.
	signer := vectorSigner("us-east-1", "", "")

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"https://examplebucket.s3.amazonaws.com/test$file.text",
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	if err := signer.Sign(req, vectorBodyHash); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request," +
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date," +
		"Signature=1e15d288c066d54cc20d8f664ade3fa96e6744b2861304671f6eb633045a2d4a"

	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

func TestSignSessionToken(t *testing.T) {
	t.Parallel()

	const token = "example-session-token"

	signer := vectorSigner("us-east-1", "", token)

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"https://examplebucket.s3.amazonaws.com/test.txt",
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	if err := signer.Sign(req, vectorEmptyHash); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if got := req.Header.Get("X-Amz-Security-Token"); got != token {
		t.Errorf("X-Amz-Security-Token = %q, want %q", got, token)
	}

	// The signature below was checked against an independent Python
	// implementation of the same documented steps.
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request," +
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date;x-amz-security-token," +
		"Signature=1c807d954263192cee6fe476c135ea9036ec81b7080159405dcf177df9b563ba"

	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

func TestURLAddressing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		bucket   string
		region   string
		endpoint string
		key      string
		want     string
		wantPath string
	}{
		{
			name:     "virtual host us-east-1",
			bucket:   "examplebucket",
			region:   "us-east-1",
			endpoint: "",
			key:      "tofu/ws1/abc",
			want:     "https://examplebucket.s3.amazonaws.com/tofu/ws1/abc",
			wantPath: "",
		},
		{
			name:     "virtual host other region",
			bucket:   "examplebucket",
			region:   "eu-west-1",
			endpoint: "",
			key:      "tofu/ws1/abc",
			want:     "https://examplebucket.s3.eu-west-1.amazonaws.com/tofu/ws1/abc",
			wantPath: "",
		},
		{
			name:     "path style custom endpoint",
			bucket:   "examplebucket",
			region:   "us-east-1",
			endpoint: "http://localhost:9000",
			key:      "tofu/ws1/abc",
			want:     "http://localhost:9000/examplebucket/tofu/ws1/abc",
			wantPath: "",
		},
		{
			name:     "path style endpoint with trailing slash",
			bucket:   "examplebucket",
			region:   "us-east-1",
			endpoint: "http://localhost:9000/",
			key:      "tofu/ws1/abc",
			want:     "http://localhost:9000/examplebucket/tofu/ws1/abc",
			wantPath: "",
		},
		{
			name:     "key stays raw in path so nothing encodes twice",
			bucket:   "examplebucket",
			region:   "us-east-1",
			endpoint: "",
			key:      "test$file.text",
			want:     "https://examplebucket.s3.amazonaws.com/test$file.text",
			wantPath: "/test$file.text",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			signer := &s3.Signer{
				Bucket:   tc.bucket,
				Region:   tc.region,
				Endpoint: tc.endpoint,
				Credentials: s3.Credentials{
					AccessKeyID:     vectorAccessKeyID,
					SecretAccessKey: vectorSecretAccessKey,
					SessionToken:    "",
				},
				Now: nil,
			}

			got, err := signer.URL(tc.key)
			if err != nil {
				t.Fatalf("URL: %v", err)
			}

			if got.String() != tc.want {
				t.Errorf("URL = %q, want %q", got.String(), tc.want)
			}

			if tc.wantPath != "" && got.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", got.Path, tc.wantPath)
			}
		})
	}
}

func TestSignPathStyleEndpoint(t *testing.T) {
	t.Parallel()

	signer := vectorSigner("us-east-1", "http://localhost:9000", "")

	objectURL, err := signer.URL("test.txt")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}

	if got, want := objectURL.String(), "http://localhost:9000/examplebucket/test.txt"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, objectURL.String(), http.NoBody)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	if err := signer.Sign(req, vectorBodyHash); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// The signature below was checked against an independent Python
	// implementation of the same documented steps.
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request," +
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date," +
		"Signature=93e523711c2c48d25221ccd603ff4fbc6adb0a227b67a2ad0b17f94f86224ba1"

	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

func lookupWith(vars map[string]string) s3.LookupEnv {
	return func(name string) (string, bool) {
		value, ok := vars[name]

		return value, ok
	}
}

func TestLoadCredentialsSuccess(t *testing.T) {
	t.Parallel()

	t.Run("all variables", func(t *testing.T) {
		t.Parallel()

		got, err := s3.LoadCredentials(lookupWith(map[string]string{
			"AWS_ACCESS_KEY_ID":     "key-id",
			"AWS_SECRET_ACCESS_KEY": "secret",
			"AWS_SESSION_TOKEN":     "token",
		}))
		if err != nil {
			t.Fatalf("LoadCredentials: %v", err)
		}

		want := s3.Credentials{AccessKeyID: "key-id", SecretAccessKey: "secret", SessionToken: "token"}
		if got != want {
			t.Errorf("credentials = %+v, want %+v", got, want)
		}
	})

	t.Run("no session token", func(t *testing.T) {
		t.Parallel()

		got, err := s3.LoadCredentials(lookupWith(map[string]string{
			"AWS_ACCESS_KEY_ID":     "key-id",
			"AWS_SECRET_ACCESS_KEY": "secret",
		}))
		if err != nil {
			t.Fatalf("LoadCredentials: %v", err)
		}

		if got.SessionToken != "" {
			t.Errorf("session token = %q, want empty", got.SessionToken)
		}
	})
}

func TestLoadCredentialsMissing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		vars map[string]string
		want string
	}{
		{
			name: "missing access key ID",
			vars: map[string]string{
				"AWS_SECRET_ACCESS_KEY": "secret",
				"AWS_SESSION_TOKEN":     "token",
			},
			want: "AWS_ACCESS_KEY_ID",
		},
		{
			name: "missing secret access key",
			vars: map[string]string{
				"AWS_ACCESS_KEY_ID": "key-id",
				"AWS_SESSION_TOKEN": "token",
			},
			want: "AWS_SECRET_ACCESS_KEY",
		},
		{
			name: "empty access key ID",
			vars: map[string]string{
				"AWS_ACCESS_KEY_ID":     "",
				"AWS_SECRET_ACCESS_KEY": "secret",
			},
			want: "AWS_ACCESS_KEY_ID",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := s3.LoadCredentials(lookupWith(tc.vars))
			if err == nil {
				t.Fatalf("LoadCredentials succeeded, want error naming %s", tc.want)
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %s", err.Error(), tc.want)
			}

			assertRedacted(t, err)
		})
	}
}

func TestLoadCredentialsNilLookup(t *testing.T) {
	t.Parallel()

	_, err := s3.LoadCredentials(nil)
	if err == nil {
		t.Fatal("LoadCredentials succeeded, want error")
	}

	assertRedacted(t, err)
}

// newSignTestRequest builds a GET request for the signer error tests.
func newSignTestRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	return req
}

// requireSignError fails when signing succeeds. It checks that the error
// carries no credential values.
func requireSignError(t *testing.T, signer *s3.Signer, req *http.Request, payloadHash string) {
	t.Helper()

	if err := signer.Sign(req, payloadHash); err == nil {
		t.Fatal("Sign succeeded, want error")
	} else {
		assertRedacted(t, err)
	}
}

// emptySecretSigner returns a signer without a secret access key.
func emptySecretSigner() *s3.Signer {
	return &s3.Signer{
		Bucket: "examplebucket",
		Region: "us-east-1",
		Credentials: s3.Credentials{
			AccessKeyID:     vectorAccessKeyID,
			SecretAccessKey: "",
			SessionToken:    "",
		},
		Endpoint: "",
		Now:      nil,
	}
}

func TestSignErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		signer   *s3.Signer
		url      string
		hash     string
		clearURL bool
	}{
		{
			name:     "request without URL",
			signer:   vectorSigner("us-east-1", "", "example-session-token"),
			url:      "https://examplebucket.s3.amazonaws.com/test.txt",
			hash:     vectorEmptyHash,
			clearURL: true,
		},
		{
			name:     "bad payload hash",
			signer:   vectorSigner("us-east-1", "", "example-session-token"),
			url:      "https://examplebucket.s3.amazonaws.com/test.txt",
			hash:     "not-a-hash",
			clearURL: false,
		},
		{
			name:     "missing region",
			signer:   vectorSigner("", "", ""),
			url:      "https://examplebucket.s3.amazonaws.com/test.txt",
			hash:     vectorEmptyHash,
			clearURL: false,
		},
		{
			name:     "missing secret access key",
			signer:   emptySecretSigner(),
			url:      "https://examplebucket.s3.amazonaws.com/test.txt",
			hash:     vectorEmptyHash,
			clearURL: false,
		},
		{
			name:     "invalid query",
			signer:   vectorSigner("us-east-1", "", "example-session-token"),
			url:      "https://examplebucket.s3.amazonaws.com/?bad=%zz",
			hash:     vectorEmptyHash,
			clearURL: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := newSignTestRequest(t, tc.url)
			if tc.clearURL {
				req.URL = nil
			}

			requireSignError(t, tc.signer, req, tc.hash)
		})
	}
}

func TestURLErrors(t *testing.T) {
	t.Parallel()

	bad := vectorSigner("us-east-1", "://no-host", "")

	if _, err := bad.URL("test.txt"); err == nil {
		t.Fatal("URL succeeded, want error")
	} else {
		assertRedacted(t, err)
	}
}

func TestSignedRequestCarriesNoSecret(t *testing.T) {
	t.Parallel()

	signer := vectorSigner("us-east-1", "", "example-session-token")

	req := newSignTestRequest(t, "https://examplebucket.s3.amazonaws.com/test.txt")

	if err := signer.Sign(req, vectorEmptyHash); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	auth := req.Header.Get("Authorization")

	if strings.Contains(auth, vectorSecretAccessKey) {
		t.Error("Authorization header contains the secret access key")
	}

	if strings.Contains(auth, "example-session-token") {
		t.Error("Authorization header contains the session token")
	}
}

// assertRedacted fails when err carries any credential value. The signer
// takes no logger, so errors are the only text it produces.
func assertRedacted(t *testing.T, err error) {
	t.Helper()

	for _, secret := range []string{vectorAccessKeyID, vectorSecretAccessKey, "example-session-token"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q contains credential material", err.Error())
		}
	}
}
