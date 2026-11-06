package ldapauth_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/ldapauth"
)

// TestGUIDText checks the text form of an objectGUID, with the vector of
// the decision of the subject.
func TestGUIDText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want string
		in   []byte
		ok   bool
	}{
		{
			name: "the vector", want: "04030201-0605-0807-090a-0b0c0d0e0f10", ok: true,
			in: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		},
		{
			name: "lowercase", want: "ffeeddcc-bbaa-9988-7766-554433221100", ok: true,
			in: []byte{0xcc, 0xdd, 0xee, 0xff, 0xaa, 0xbb, 0x88, 0x99, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00},
		},
		{name: "15 bytes", want: "", ok: false, in: make([]byte, 15)},
		{name: "17 bytes", want: "", ok: false, in: make([]byte, 17)},
		{name: "UUID text", want: "", ok: false, in: []byte("6ba7b810-9dad-11d1-80b4-00c04fd430c8")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ldapauth.GUIDText(tc.in)
			if (err == nil) != tc.ok || got != tc.want {
				t.Fatalf("GUIDText() = %q, %v, want %q (ok %v)", got, err, tc.want, tc.ok)
			}
		})
	}
}

// TestUUIDText checks the text form of an entryUUID.
func TestUUIDText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
		ok       bool
	}{
		{"6BA7B810-9DAD-11D1-80B4-00C04FD430C8", "6ba7b810-9dad-11d1-80b4-00c04fd430c8", true},
		{"6ba7b810-9dad-11d1-80b4-00c04fd430c8", "6ba7b810-9dad-11d1-80b4-00c04fd430c8", true},
		{"6ba7b8109dad11d180b400c04fd430c8", "", false},
		{"6ba7b810-9dad-11d1-80b4-00c04fd430cg", "", false},
		{"6ba7b810-9dad-11d1-80b4-00c04fd430c8 ", "", false},
		{"6ba7b810+9dad-11d1-80b4-00c04fd430c8", "", false},
		{"", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			got, err := ldapauth.UUIDText([]byte(tc.in))
			if (err == nil) != tc.ok || got != tc.want {
				t.Fatalf("UUIDText(%q) = %q, %v, want %q (ok %v)", tc.in, got, err, tc.want, tc.ok)
			}
		})
	}
}
