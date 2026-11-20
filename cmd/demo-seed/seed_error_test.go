package main

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/ident"
)

// seedError is an error whose text is the test value.
type seedError string

func (e seedError) Error() string { return string(e) }

// TestWriteSeedErrorEscapesHiddenRunes is the guard on the error line of
// demo-seed: an error text with a line break gives one line with a visible
// escape, and a backslash is written as two.
func TestWriteSeedErrorEscapesHiddenRunes(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"a\r\nX":                     `demo-seed: a\r\nX`,
		"ok\nlevel=ERROR msg=forged": `demo-seed: ok\nlevel=ERROR msg=forged`,
		"a\x1b\u0085":                `demo-seed: a\x1b\x85`,
		`path C:\new`:                `demo-seed: path C:\\new`,
	} {
		var b strings.Builder

		writeSeedError(&b, seedError(in))

		out := b.String()
		if r, found := ident.FirstHidden(strings.TrimSuffix(out, "\n")); found {
			t.Errorf("output holds hidden rune %U: %q", r, out)
		}

		if out != want+"\n" {
			t.Errorf("output = %q, want %q", out, want+"\n")
		}
	}
}
