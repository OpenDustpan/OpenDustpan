package pagetoken_test

import (
	"encoding/base64"
	"testing"

	"opendustpan.org/opendustpan/internal/pagetoken"
)

// TestEachDecoderGuardRefusesHostileText proves that each guard of Decode
// refuses its hostile form alone: the alphabet check a carriage return and a
// line feed, the strict decode unused bits in the final character, and the
// canonical-text check a text that decodes but is not the encoder form.
func TestEachDecoderGuardRefusesHostileText(t *testing.T) {
	t.Parallel()

	b64 := base64.RawURLEncoding.EncodeToString

	t.Run("alphabet check refuses a carriage return and a line feed", func(t *testing.T) {
		t.Parallel()

		token := b64([]byte(`{"a":"x"}`))
		token = token[:4] + "\r\n" + token[4:]

		if pagetoken.HasCanonicalAlphabet(token) {
			t.Fatalf("the alphabet check accepted %q", token)
		}
	})

	t.Run("strict decode refuses unused bits in the final character", func(t *testing.T) {
		t.Parallel()

		if _, err := pagetoken.DecodeStrict("e31"); err == nil {
			t.Fatal("the strict decode accepted unused bits")
		}
	})

	t.Run("canonical text refuses a text that is not the encoder form", func(t *testing.T) {
		t.Parallel()

		raw, err := pagetoken.DecodeStrict("e30")
		if err != nil {
			t.Fatalf("strict decode of e30: %v", err)
		}

		if pagetoken.AcceptsCanonicalText(raw, "e30\r\n") {
			t.Fatal("the canonical-text check accepted a text with a line break")
		}
	})
}
