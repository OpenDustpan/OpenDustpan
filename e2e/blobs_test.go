//go:build e2e

package e2e_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

func TestBlobStoreName(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "unset means fs", value: "", want: "fs"},
		{name: "fs", value: "fs", want: "fs"},
		{name: "s3", value: "s3", want: "s3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("E2E_BLOBS", tc.value)

			got, err := e2e.BlobStoreName()
			if err != nil {
				t.Fatalf("BlobStoreName with %q: %v", tc.value, err)
			}

			if got != tc.want {
				t.Fatalf("BlobStoreName with %q = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestBlobStoreNameUnknown(t *testing.T) {
	t.Setenv("E2E_BLOBS", "bogus")

	if _, err := e2e.BlobStoreName(); err == nil || !strings.Contains(err.Error(), "fs") ||
		!strings.Contains(err.Error(), "s3") {
		t.Fatalf("BlobStoreName with bogus: err = %v, want it to name fs and s3", err)
	}
}

func TestS3SkipReason(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		"DUSTPAN_TEST_S3_ENDPOINT",
		"DUSTPAN_TEST_S3_BUCKET",
		"DUSTPAN_TEST_S3_ACCESS_KEY",
		"DUSTPAN_TEST_S3_SECRET_KEY",
	} {
		if !strings.Contains(e2e.S3SkipReason(), want) {
			t.Fatalf("skip reason %q lacks %q", e2e.S3SkipReason(), want)
		}
	}
}

func TestLookupS3Config(t *testing.T) {
	t.Setenv("DUSTPAN_TEST_S3_ENDPOINT", "")
	t.Setenv("DUSTPAN_TEST_S3_BUCKET", "")
	t.Setenv("DUSTPAN_TEST_S3_ACCESS_KEY", "")
	t.Setenv("DUSTPAN_TEST_S3_SECRET_KEY", "")

	if _, ok := e2e.LookupS3Config(); ok {
		t.Fatal("LookupS3Config with no variables = true, want false")
	}

	t.Setenv("DUSTPAN_TEST_S3_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("DUSTPAN_TEST_S3_BUCKET", "dustpan-e2e")
	t.Setenv("DUSTPAN_TEST_S3_ACCESS_KEY", "dustpan")
	t.Setenv("DUSTPAN_TEST_S3_SECRET_KEY", "dustpansecret")

	cfg, ok := e2e.LookupS3Config()
	if !ok {
		t.Fatal("LookupS3Config with all variables = false, want true")
	}

	if cfg.Endpoint() == "" || cfg.Bucket() == "" {
		t.Fatalf("S3 config holds no endpoint or bucket: %+v", cfg)
	}
}
