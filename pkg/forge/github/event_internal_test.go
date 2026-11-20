package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

const (
	internalSecretA = "internal-secret-a-0123456789abcd"
	internalSecretB = "internal-secret-b-0123456789abcd"
	internalShort   = "internal-short-0123456789abcd"
	internalBody    = `{"zen":"x","sender":{"id":1,"login":"octo"}}`
)

// countingEqual returns a compare function and the list of the lengths of the
// digests that it compared. It decides like hmac.Equal.
func countingEqual() (func(a, b []byte) bool, *[]int) {
	var seen []int

	return func(a, b []byte) bool {
		seen = append(seen, len(a), len(b))

		return hmac.Equal(a, b)
	}, &seen
}

// internalDigits returns the 64 hex digits of the HMAC-SHA256 of body.
func internalDigits(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(body))

	return hex.EncodeToString(m.Sum(nil))
}

func internalRequest(secret, body string) forge.EventRequest {
	return forge.EventRequest{
		Header: map[string][]string{
			"X-Hub-Signature-256": {"sha256=" + internalDigits(secret, body)},
			"X-Github-Event":      {"ping"},
			"X-Github-Delivery":   {"0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"},
		},
		Body: []byte(body),
	}
}

// TestParseEventComparesEachUsableSecret checks the compare of the digests: one
// call of the compare function for each usable secret, also after a match, with
// digests of 32 bytes, and no call for a secret that the verifier ignores.
func TestParseEventComparesEachUsableSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		secrets   [][]byte
		wantCalls int
		wantMatch bool
	}{
		{"match on the first of two", [][]byte{[]byte(internalSecretA), []byte(internalSecretB)}, 2, true},
		{"match on the second of two", [][]byte{[]byte(internalSecretB), []byte(internalSecretA)}, 2, true},
		{"no match", [][]byte{[]byte(internalSecretB), []byte(internalSecretB)}, 2, false},
		{
			"a short and an empty secret are ignored",
			[][]byte{[]byte(internalShort), nil, {}, []byte(internalSecretA)},
			1,
			true,
		},
		{"only ignored secrets", [][]byte{[]byte(internalShort), nil}, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			equal, seen := countingEqual()

			_, err := parseEvent(internalRequest(internalSecretA, internalBody), tc.secrets, equal)

			if got := len(*seen) / 2; got != tc.wantCalls {
				t.Fatalf("the compare ran %d times, want %d", got, tc.wantCalls)
			}

			if !slices.Equal(slices.Compact(slices.Clone(*seen)), []int{sha256.Size}) && tc.wantCalls > 0 {
				t.Fatalf("the compare got values of the lengths %v, want %d", *seen, sha256.Size)
			}

			if (err == nil) != tc.wantMatch {
				t.Fatalf("parseEvent = %v, want a match = %v", err, tc.wantMatch)
			}
		})
	}
}

// TestParseEventUsesTheCompareFunction checks that the verdict is the answer of
// the compare function, so that a replacement of hmac.Equal by another compare
// shows. A function that is always true must still not make a short secret
// valid.
func TestParseEventUsesTheCompareFunction(t *testing.T) {
	t.Parallel()

	req := internalRequest(internalSecretA, internalBody)

	never := func(_, _ []byte) bool { return false }
	always := func(_, _ []byte) bool { return true }

	if _, err := parseEvent(req, [][]byte{[]byte(internalSecretA)}, never); !errors.Is(err, forge.ErrSignature) {
		t.Fatalf("parseEvent with a compare that never matches = %v, want ErrSignature", err)
	}

	if _, err := parseEvent(req, [][]byte{[]byte(internalSecretB)}, always); err != nil {
		t.Fatalf("parseEvent with a compare that always matches = %v, want the event", err)
	}

	// A secret that the verifier ignores never reaches the compare.
	for _, ignored := range [][]byte{[]byte(internalShort), nil, {}} {
		if _, err := parseEvent(req, [][]byte{ignored}, always); !errors.Is(err, forge.ErrSignature) {
			t.Fatalf("parseEvent with the ignored secret %q = %v, want ErrSignature", ignored, err)
		}
	}
}

// TestParseEventNearMissSignatures checks that a signature that matches 0, 16
// or 31 of the 32 bytes gets the same error and the same single compare as any
// other wrong signature.
func TestParseEventNearMissSignatures(t *testing.T) {
	t.Parallel()

	good := internalRequest(internalSecretA, internalBody)
	digits := internalDigits(internalSecretA, internalBody)

	for _, matching := range []int{0, 16, 31} {
		bad := []byte(digits)
		// Change each hex digit after the first matching bytes.
		for i := matching * 2; i < len(bad); i++ {
			bad[i] = flip(bad[i])
		}

		req := forge.EventRequest{
			Header: map[string][]string{
				"X-Hub-Signature-256": {"sha256=" + string(bad)}, "X-Github-Event": {"ping"},
				"X-Github-Delivery": {"0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"},
			},
			Body: good.Body,
		}

		equal, seen := countingEqual()

		_, err := parseEvent(req, [][]byte{[]byte(internalSecretA)}, equal)
		if !errors.Is(err, forge.ErrSignature) {
			t.Fatalf("a signature that matches %d bytes: parseEvent = %v, want ErrSignature", matching, err)
		}

		if len(*seen) != 2 {
			t.Fatalf("a signature that matches %d bytes: %d compares, want 1", matching, len(*seen)/2)
		}
	}
}

func flip(c byte) byte {
	if c == '0' {
		return '1'
	}

	return '0'
}
