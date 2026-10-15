package collection_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
)

func TestParseName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "plain name", input: "payments", wantErr: false},
		{name: "dash and underscore", input: "core-api_v2", wantErr: false},
		{name: "inner space", input: "Core Team", wantErr: false},
		{name: "minimum length", input: "api", wantErr: false},
		{name: "maximum length", input: "abcdefghijklmnopqrstuvwxyz0123456789abcd", wantErr: false},
		{name: "too short", input: "ab", wantErr: true},
		{name: "too long", input: "abcdefghijklmnopqrstuvwxyz0123456789abcde", wantErr: true},
		{name: "leading dash", input: "-api", wantErr: true},
		{name: "trailing space", input: "api ", wantErr: true},
		{name: "dot is not allowed", input: "pay.ments", wantErr: true},
		{name: "slash is not allowed", input: "pay/ments", wantErr: true},
		{name: "non-ASCII letter", input: "pagosñ", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := collection.ParseName(tc.input)
			if tc.wantErr {
				if !errors.Is(err, collection.ErrInvalidName) {
					t.Fatalf("ParseName(%q) error = %v, want ErrInvalidName", tc.input, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseName(%q) error = %v", tc.input, err)
			}

			if got.String() != tc.input {
				t.Errorf("ParseName(%q) = %q", tc.input, got)
			}
		})
	}
}

func TestNameKeyIgnoresCase(t *testing.T) {
	t.Parallel()

	a, err := collection.ParseName("Payments")
	if err != nil {
		t.Fatalf("ParseName: %v", err)
	}

	b, err := collection.ParseName("PAYMENTS")
	if err != nil {
		t.Fatalf("ParseName: %v", err)
	}

	if a.Key() != b.Key() {
		t.Errorf("keys %q and %q differ", a.Key(), b.Key())
	}
}
