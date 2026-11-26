package server_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/principal"
)

// hiddenValue returns "a", the code point r, and "b". The code point is
// written as a number so that the source holds no hidden character.
func hiddenValue(r rune) string {
	return "a" + string(r) + "b"
}

// TestCheckBreakGlassDisplayNameRefusesHiddenCharacter sends a hidden
// character to the check alone. The allowlist refuses each of them too, so the
// message is the only proof that the hidden-character branch ran.
func TestCheckBreakGlassDisplayNameRefusesHiddenCharacter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
	}{
		{"right-to-left override", hiddenValue(0x202e)},
		{"zero-width space", hiddenValue(0x200b)},
		{"soft hyphen", hiddenValue(0x00ad)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := server.CheckBreakGlassDisplayNameForTest(c.value)
			if !errors.Is(err, principal.ErrInvalid) {
				t.Fatalf("check of %q = %v, want principal.ErrInvalid", c.value, err)
			}

			if !strings.Contains(err.Error(), "control, format or separator character") {
				t.Errorf("check of %q = %v, want the hidden-character refusal", c.value, err)
			}
		})
	}
}

// TestCheckBreakGlassDisplayNameRefusesInvalidUTF8 sends a value that is not
// UTF-8 to the check alone. JSON decoding yields valid UTF-8, so no HTTP test
// reaches this case.
func TestCheckBreakGlassDisplayNameRefusesInvalidUTF8(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
	}{
		{"invalid byte at the end", "Recovery\xff"},
		{"truncated sequence", "Rec\xc3"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := server.CheckBreakGlassDisplayNameForTest(c.value)
			if !errors.Is(err, principal.ErrInvalid) {
				t.Fatalf("check of %q = %v, want principal.ErrInvalid", c.value, err)
			}

			// The message pins this branch: without it the allowlist below
			// refuses the value with its own message.
			if !strings.Contains(err.Error(), "display name is not UTF-8") {
				t.Errorf("check of %q = %v, want the UTF-8 refusal", c.value, err)
			}
		})
	}

	// The allowlist alone refuses an invalid byte too: a range loop makes it
	// U+FFFD, which is not a letter, a mark, a digit, a space or one of the
	// punctuation characters.
	if server.BreakGlassDisplayNameRuneForTest(utf8.RuneError) {
		t.Error("the allowlist accepts U+FFFD, so it does not refuse an invalid byte on its own")
	}
}
