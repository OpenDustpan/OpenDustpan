package blocklist

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"

	"opendustpan.org/opendustpan/pkg/localuser"
)

// TestChangedListFailsClosed is the fail-closed layer: a list that does not
// load refuses each check with ErrUnavailable, so the service refuses the
// password and never lets it through.
func TestChangedListFailsClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		load func() ([]byte, error)
		name string
	}{
		{name: "a read error", load: func() ([]byte, error) { return nil, errBadList }},
		{name: "a changed byte", load: func() ([]byte, error) {
			raw, err := files.ReadFile(listPath)
			if err != nil {
				return nil, fmt.Errorf("read the embedded list: %w", err)
			}

			changed := append([]byte(nil), raw...)
			changed[0] ^= 1

			return changed, nil
		}},
		{name: "an empty file", load: func() ([]byte, error) { return []byte{}, nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := newList(tc.load)

			for range 2 {
				got, err := l.Blocked(t.Context(), "a long random password vq3k8mzt")
				if !errors.Is(err, localuser.ErrUnavailable) || got {
					t.Fatalf("blocked = %t, %v; want false, ErrUnavailable", got, err)
				}
			}
		})
	}
}

// TestParseLinesRefusesHostileLines is the boundary layer of the list: each
// line must be a plain password.
func TestParseLinesRefusesHostileLines(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want int
	}{
		{name: "no final line feed", raw: "a\nb", want: 2},
		{name: "a line count that is not the pinned count", raw: "a\nb\n", want: 3},
		{name: "a CR at the end of a line", raw: "a\r\nb\n", want: 2},
		{name: "a NUL", raw: "a\x00\nb\n", want: 2},
		{name: "a byte that is not UTF-8", raw: "a\xff\nb\n", want: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := parseLines([]byte(tc.raw), tc.want); !errors.Is(err, errBadList) {
				t.Fatalf("parse %q: err = %v, want errBadList", tc.raw, err)
			}
		})
	}
}

// TestFoldedCompare is the encode layer: the list and the candidate meet in
// the form of Fold, so the case and the normalization form of a candidate
// do not matter.
func TestFoldedCompare(t *testing.T) {
	t.Parallel()

	l := newList(func() ([]byte, error) { return []byte("Café\n\nhunter2\n"), nil })
	l.once.Do(func() { l.entries, l.err = parseLines([]byte("Café\n\nhunter2\n"), 3) })

	for _, candidate := range []string{"café", "CAFÉ", norm.NFD.String("café"), norm.NFD.String("CAFÉ"), "HUNTER2"} {
		got, err := l.Blocked(t.Context(), candidate)
		if err != nil || !got {
			t.Errorf("blocked %q = %t, %v; want true", candidate, got, err)
		}
	}

	for _, candidate := range []string{"", "cafe", "hunter", strings.Repeat("x", localuser.MaxBytes)} {
		got, err := l.Blocked(t.Context(), candidate)
		if err != nil || got {
			t.Errorf("blocked %q = %t, %v; want false", candidate, got, err)
		}
	}
}
