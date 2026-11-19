package runner_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/runner"
)

// TestNewProxySettings is the hostile table of the pool proxy settings. Each
// row is a value that a save must refuse, and the constructor is the only
// layer of this check that the store and the handler rely on.
func TestNewProxySettings(t *testing.T) {
	t.Parallel()

	manyRanges := make([]string, 65)
	for i := range manyRanges {
		manyRanges[i] = fmt.Sprintf("10.%d.0.0/16", i)
	}

	tests := []struct {
		name   string
		ranges []string
		url    string
	}{
		{"loopback range", []string{"127.0.0.0/8"}, ""},
		{"link-local range", []string{"169.254.0.0/16"}, ""},
		{"default route", []string{"0.0.0.0/0"}, ""},
		{"IPv6 loopback", []string{"::1/128"}, ""},
		{"IPv4 prefix below the minimum", []string{"10.0.0.0/4"}, ""},
		{"range with a host bit", []string{"10.0.0.1/8"}, ""},
		{"65 ranges", manyRanges, ""},
		{"URL with ftp scheme", nil, "ftp://proxy.example:21"},
		{"URL with javascript scheme", nil, "javascript:alert(1)"},
		{"URL with user information", nil, "http://user:pw@proxy.example:3128"},
		{"URL with a fragment", nil, "http://proxy.example:3128#x"},
		{"URL with no host", nil, "http://"},
		{"URL that names loopback", nil, "http://127.0.0.1:3128"},
		{"URL that names the metadata address", nil, "http://169.254.169.254:80"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := runner.NewProxySettings(tc.ranges, tc.url)
			if !errors.Is(err, runner.ErrInvalid) {
				t.Fatalf(
					"NewProxySettings(%d ranges, %q) = %v, want %v",
					len(tc.ranges),
					tc.url,
					err,
					runner.ErrInvalid,
				)
			}

			if tc.url != "" && strings.Contains(err.Error(), tc.url) {
				t.Fatalf("error text %q repeats the upstream URL", err)
			}
		})
	}
}

// TestNewProxySettingsValid checks the values that a save may keep, and that
// the constructor returns them as they were given.
func TestNewProxySettingsValid(t *testing.T) {
	t.Parallel()

	s, err := runner.NewProxySettings([]string{"10.0.0.0/8", "fd00::/48"}, "https://proxy.example:3128")
	if err != nil {
		t.Fatalf("NewProxySettings: %v", err)
	}

	if got := s.AllowedRanges(); len(got) != 2 || got[0] != "10.0.0.0/8" || got[1] != "fd00::/48" {
		t.Fatalf("AllowedRanges = %v", got)
	}

	if got := s.UpstreamURL(); got != "https://proxy.example:3128" {
		t.Fatalf("UpstreamURL = %q", got)
	}

	none, err := runner.NewProxySettings(nil, "")
	if err != nil || len(none.AllowedRanges()) != 0 || none.UpstreamURL() != "" {
		t.Fatalf("NewProxySettings(nil, \"\") = %+v, %v; want the default", none, err)
	}
}

// TestNewProxySettingsNamesTheField checks that each refusal names the field
// and holds no text of the input.
func TestNewProxySettingsNamesTheField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ranges []string
		url    string
		field  string
	}{
		{"range text", []string{"ZZ-marker"}, "", "allowedRanges"},
		{"range with a host bit", []string{"10.0.0.1/8"}, "", "allowedRanges"},
		{"upstream scheme", nil, "ZZ-marker://x", "upstreamProxy"},
		{"upstream user information", nil, "http://ZZ-marker:pw@proxy.example:3128", "upstreamProxy"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := runner.NewProxySettings(tc.ranges, tc.url)

			fe, ok := errors.AsType[*runner.FieldError](err)
			if !ok || fe.Field() != tc.field {
				t.Fatalf("NewProxySettings error = %v, want a field error of %s", err, tc.field)
			}

			if strings.Contains(err.Error(), "ZZ-marker") || strings.Contains(err.Error(), "10.0.0.1") {
				t.Errorf("error text %q repeats the input", err)
			}
		})
	}
}
