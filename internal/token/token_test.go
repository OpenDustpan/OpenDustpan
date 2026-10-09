package token_test

import (
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/token"
)

func TestNewTokensAreUniqueAndParse(t *testing.T) {
	t.Parallel()

	a, err := token.New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	b, err := token.New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if a.Reveal() == b.Reveal() {
		t.Fatal("two tokens are equal")
	}

	parsed, err := token.Parse(a.Reveal())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !a.Digest().Matches(parsed) {
		t.Error("parsed token does not match its digest")
	}

	if a.Digest().Matches(b) {
		t.Error("digest matches another token")
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"", "short", strings.Repeat("!", 43), strings.Repeat("a", 44)} {
		if _, err := token.Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded", s)
		}
	}
}

func TestTokenIsRedactedWhenFormatted(t *testing.T) {
	t.Parallel()

	tok, err := token.New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	for _, verb := range []string{"%s", "%v", "%+v", "%#v"} {
		if out := fmt.Sprintf(verb, tok); strings.Contains(out, tok.Reveal()) {
			t.Errorf("%s leaks the token: %s", verb, out)
		}
	}
}

func TestZeroDigestMatchesNothing(t *testing.T) {
	t.Parallel()

	var zero token.Digest

	if zero.Matches(token.Token{}) {
		t.Error("zero digest matches the zero token")
	}
}
