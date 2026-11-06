package launcher_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher"
)

// maskAll writes each part to a new masker with secrets, closes it and
// returns what it wrote.
func maskAll(t *testing.T, secrets []string, parts ...string) string {
	t.Helper()

	var out bytes.Buffer

	m := launcher.NewMasker(&out)
	for _, s := range secrets {
		m.Add([]byte(s))
	}

	for _, p := range parts {
		if n, err := m.Write([]byte(p)); err != nil || n != len(p) {
			t.Fatalf("Write(%q) = %d, %v", p, n, err)
		}
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	return out.String()
}

func TestMaskerHidesSecrets(t *testing.T) {
	t.Parallel()

	const secret = "s3cret-value"

	cases := []struct {
		name    string
		secrets []string
		parts   []string
		want    string
	}{
		{"one write", []string{secret}, []string{"a " + secret + " b\n"}, "a [masked] b\n"},
		{"two values", []string{secret, "other-one"}, []string{"other-one " + secret}, "[masked] [masked]"},
		{"value twice", []string{secret}, []string{secret + secret}, "[masked][masked]"},
		{"start of a value at the end", []string{secret}, []string{"x s3cr"}, "x s3cr"},
		{"short value is not masked", []string{"abc"}, []string{"abc, abc"}, "abc, abc"},
		{"longest value first", []string{"token", "token-long"}, []string{"token-long token"}, "[masked] [masked]"},
		{"no value", nil, []string{"plain text"}, "plain text"},
		{"split in three writes", []string{secret}, []string{"a s3", "cret-va", "lue b"}, "a [masked] b"},
		{"a near miss ends", []string{secret}, []string{"s3cret-", "nothing"}, "s3cret-nothing"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := maskAll(t, tc.secrets, tc.parts...); got != tc.want {
				t.Fatalf("masked = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMaskerHidesAValueAtEachSplit writes a log in two parts at each byte
// of a secret value, as the reads of a pipe can split it.
func TestMaskerHidesAValueAtEachSplit(t *testing.T) {
	t.Parallel()

	const secret = "dph_0123456789abcdef"

	text := "before " + secret + " after\n"

	for i := 1; i < len(text); i++ {
		got := maskAll(t, []string{secret}, text[:i], text[i:])
		if strings.Contains(got, secret) || got != "before [masked] after\n" {
			t.Fatalf("split at %d: masked = %q", i, got)
		}
	}
}

func TestMaskerRefusesAWriteAfterClose(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	m := launcher.NewMasker(&out)
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}

	if _, err := m.Write([]byte("x")); !errors.Is(err, launcher.ErrMaskerClosed) {
		t.Fatalf("Write after Close = %v, want ErrMaskerClosed", err)
	}
}
