package cli

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// TestFingerprintNeverPrints proves fmt prints no part of a fingerprint:
// not with the fingerprint alone, and not inside a Resolved with %v, %+v
// and %#v (the review of ADR 63 part 2, item 6).
func TestFingerprintNeverPrints(t *testing.T) {
	t.Parallel()

	fp := fingerprint("value")
	hexText := hex.EncodeToString(fp[:])
	rawText := fmt.Sprint([32]byte(*fp))

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%X", "%q", "%d"} {
		if got := fmt.Sprintf(verb, fp); got != "" {
			t.Errorf("Sprintf(%q, fingerprint) = %q, want empty", verb, got)
		}
	}

	if got := fp.String(); got != "" {
		t.Errorf("String() = %q, want empty", got)
	}

	resolved := []Resolved{{Setting: nil, Source: SourceFile, Value: "set", Layers: nil, fingerprint: fp}}

	for _, verb := range []string{"%v", "%+v", "%#v"} {
		text := fmt.Sprintf(verb, resolved)
		if strings.Contains(text, hexText) || strings.Contains(text, rawText) ||
			strings.Contains(text, hexText[len(hexText)-8:]) {
			t.Errorf("Sprintf(%q, resolved) holds the fingerprint: %s", verb, text)
		}
	}
}
