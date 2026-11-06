package launcher_test

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher"
)

// maskAll writes each part to a new masker with secrets, closes it and
// returns what it wrote.
func maskAll(t *testing.T, secrets []string, parts ...string) string {
	t.Helper()

	var out bytes.Buffer

	m := launcher.NewMasker(emitTo(&out))
	for _, s := range secrets {
		if err := m.Add([]byte(s)); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	for _, p := range parts {
		if err := m.Mask([]byte(p)); err != nil {
			t.Fatalf("Mask(%q) = %v", p, err)
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

	m := launcher.NewMasker(emitTo(&out))
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}

	if err := m.Mask([]byte("x")); !errors.Is(err, launcher.ErrMaskerClosed) {
		t.Fatalf("Mask after Close = %v, want ErrMaskerClosed", err)
	}
}

// TestMaskerBounds checks the limits of the masker at their bounds: a value
// of MaxMaskLen bytes and MaxMaskValues values fit, one byte or one value
// more is refused, and the masker holds nothing of a refused value.
func TestMaskerBounds(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	m := launcher.NewMasker(emitTo(&out))

	if err := m.Add(bytes.Repeat([]byte("a"), launcher.MaxMaskLen)); err != nil {
		t.Fatalf("Add of MaxMaskLen bytes = %v, want nil", err)
	}

	if err := m.Add(bytes.Repeat([]byte("b"), launcher.MaxMaskLen+1)); !errors.Is(err, launcher.ErrMaskerFull) {
		t.Fatalf("Add of MaxMaskLen+1 bytes = %v, want ErrMaskerFull", err)
	}

	for i := 1; i < launcher.MaxMaskValues; i++ {
		if err := m.Add([]byte("value-" + strconv.Itoa(i))); err != nil {
			t.Fatalf("Add of value %d = %v, want nil", i+1, err)
		}
	}

	if err := m.Add([]byte("one-value-too-many")); !errors.Is(err, launcher.ErrMaskerFull) {
		t.Fatalf("Add above MaxMaskValues = %v, want ErrMaskerFull", err)
	}

	// A value that is already there is no new value.
	if err := m.Add([]byte("value-1")); err != nil {
		t.Fatalf("Add of a known value = %v, want nil", err)
	}

	if err := m.Mask([]byte("value-7 one-value-too-many")); err != nil {
		t.Fatalf("Mask: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := out.String(); got != "[masked] one-value-too-many" {
		t.Fatalf("masked = %q", got)
	}
}

// TestMaskerLargeWrite masks one write far larger than the pieces of the
// masker, with a value across each border of a piece.
func TestMaskerLargeWrite(t *testing.T) {
	t.Parallel()

	const value = "dph_value-across-the-pieces"

	var text strings.Builder

	for range 5 {
		text.WriteString(strings.Repeat("x", 64<<10-10))
		text.WriteString(value)
	}

	got := maskAll(t, []string{value}, text.String())
	if strings.Contains(got, value) || strings.Count(got, launcher.MaskText) != 5 {
		t.Fatalf("masked %d values, want 5, and no secret", strings.Count(got, launcher.MaskText))
	}
}

// emitTo returns an Emit that appends the masked stream to out.
func emitTo(out *bytes.Buffer) launcher.Emit {
	return func(masked []byte) error {
		if _, err := out.Write(masked); err != nil {
			return fmt.Errorf("append: %w", err)
		}

		return nil
	}
}
