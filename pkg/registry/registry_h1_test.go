package registry_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// The H1 digests of the fixtures, in the stored form: "h1:" and the base64 of
// 32 bytes.
const (
	storedH1  = "h1:ASNFZ4mrze8BI0VniavN7wEjRWeJq83vASNFZ4mrze8="
	changedH1 = "h1:/u3+3P7t/tz+7f7c/u3+3P7t/tz+7f7c/u3+3P7t/tw="
)

// TestSameContent_countsH1 is hostile: a republish with different bytes must
// not compare equal to the stored release. It fails when SameContent drops H1.
func TestSameContent_countsH1(t *testing.T) {
	t.Parallel()

	base := provider()
	base.Archives[0].H1 = storedH1

	same := provider()
	same.Archives[0].H1 = storedH1
	if !base.SameContent(same) {
		t.Fatal("identical releases do not compare equal")
	}

	changed := provider()
	changed.Archives[0].H1 = changedH1
	if base.SameContent(changed) {
		t.Fatal("releases with different H1 compare equal")
	}

	empty := provider()
	empty.Archives[0].H1 = ""
	if base.SameContent(empty) {
		t.Fatal("release with H1 compares equal to one without H1")
	}
}

// TestReleaseValidate_H1 is hostile: Validate accepts only an empty H1 or the
// stored form. It fails when the H1 check of validateArchive is removed.
func TestReleaseValidate_H1(t *testing.T) {
	t.Parallel()

	body := strings.TrimPrefix(storedH1, "h1:")

	tests := []struct {
		name string
		h1   string
		ok   bool
	}{
		{name: "stored form", h1: storedH1, ok: true},
		{name: "other stored form", h1: changedH1, ok: true},
		{name: "empty", h1: "", ok: true},
		{name: "no prefix", h1: body, ok: false},
		{name: "upper prefix", h1: "H1:" + body, ok: false},
		{name: "other prefix", h1: "h2:" + body, ok: false},
		{name: "short", h1: "h1:stored-digest-value", ok: false},
		{name: "long", h1: "h1:A" + body, ok: false},
		{name: "no padding", h1: strings.TrimSuffix(storedH1, "="), ok: false},
		{name: "url alphabet", h1: "h1:-_" + body[2:], ok: false},
		{name: "trailing newline", h1: storedH1 + "\n", ok: false},
		{name: "leading space", h1: " " + storedH1, ok: false},
		{name: "two values", h1: storedH1 + "," + storedH1, ok: false},
		{name: "control character", h1: "h1:\x00" + body[1:], ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := provider()
			r.Archives = append([]registry.Archive(nil), r.Archives...)
			r.Archives[1].H1 = tc.h1

			err := r.Validate()
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, registry.ErrInvalid)) {
				t.Fatalf("Validate with H1 %q = %v, want ok=%v", tc.h1, err, tc.ok)
			}

			if got := registry.ValidH1(tc.h1); got != tc.ok {
				t.Fatalf("ValidH1(%q) = %v, want %v", tc.h1, got, tc.ok)
			}
		})
	}
}
