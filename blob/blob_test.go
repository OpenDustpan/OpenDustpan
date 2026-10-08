package blob_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/blob"
)

func TestParseKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in string
		ok bool
	}{
		{"stacks/acme/web/prod/1.json", true},
		{"a", true},
		{"", false},
		{"/abs", false},
		{"a//b", false},
		{"a/../b", false},
		{"./a", false},
		{"Upper", false},
		{"a\\b", false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			_, err := blob.ParseKey(tc.in)
			if tc.ok && err != nil {
				t.Fatalf("ParseKey(%q): %v", tc.in, err)
			}

			if !tc.ok && !errors.Is(err, blob.ErrInvalidKey) {
				t.Fatalf("ParseKey(%q): err = %v, want ErrInvalidKey", tc.in, err)
			}
		})
	}
}
