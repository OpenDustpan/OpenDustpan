package pagetoken_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/pagetoken"
)

// wire is the cursor of the tests, in the shape of an adapter's wire struct.
type wire struct {
	After       string `json:"a"`
	Fingerprint string `json:"f"`
}

// TestRoundTrip proves that a token decodes to the cursor that made it.
func TestRoundTrip(t *testing.T) {
	t.Parallel()

	want := wire{After: "web/prod", Fingerprint: pagetoken.Fingerprint("ns=acme")}

	token, err := pagetoken.Encode(want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var got wire
	if err := pagetoken.Decode(token, &got); err != nil || got != want {
		t.Fatalf("decode = %+v, %v, want %+v", got, err, want)
	}
}

// TestDecodeRefusesHostileTokens proves that a token that is too long, that is
// not base64url, or that does not hold one JSON object of the wire shape gives
// ErrInvalid.
func TestDecodeRefusesHostileTokens(t *testing.T) {
	t.Parallel()

	b64 := base64.RawURLEncoding.EncodeToString

	for _, tc := range []struct{ name, token string }{
		{name: "empty", token: ""},
		{name: "not base64url", token: "%%"},
		{name: "standard base64 with padding", token: base64.StdEncoding.EncodeToString([]byte(`{"a":"xy"}`))},
		{name: "not JSON", token: b64([]byte("x"))},
		{name: "a JSON array", token: b64([]byte(`["x"]`))},
		{name: "a JSON string", token: b64([]byte(`"x"`))},
		{name: "a member of another type", token: b64([]byte(`{"a":1}`))},
		{name: "data after the object", token: b64([]byte(`{"a":"x"} {}`))},
	} {
		var got wire

		if err := pagetoken.Decode(tc.token, &got); !errors.Is(err, pagetoken.ErrInvalid) {
			t.Errorf("%s: Decode = %v, want ErrInvalid", tc.name, err)
		}
	}
}

// TestSameFingerprint proves that two fingerprints match only when they are
// equal, also for a prefix and for an empty value.
func TestSameFingerprint(t *testing.T) {
	t.Parallel()

	fp := pagetoken.Fingerprint("organization=acme\nproject=web")

	for _, tc := range []struct {
		got  string
		want bool
	}{
		{got: fp, want: true},
		{got: fp[:len(fp)-1], want: false},
		{got: fp + "0", want: false},
		{got: "", want: false},
		{got: strings.ToUpper(fp), want: false},
	} {
		if same := pagetoken.SameFingerprint(tc.got, fp); same != tc.want {
			t.Errorf("SameFingerprint(%q, %q) = %v, want %v", tc.got, fp, same, tc.want)
		}
	}
}

// TestFingerprint proves that a fingerprint is the first 8 bytes of the
// SHA-256 of its input in hex, so the tokens that the adapters made before
// this package keep their fingerprint.
func TestFingerprint(t *testing.T) {
	t.Parallel()

	// The SHA-256 of "abc" starts with ba7816bf8f01cfea.
	if got := pagetoken.Fingerprint("abc"); got != "ba7816bf8f01cfea" {
		t.Fatalf("Fingerprint(abc) = %q, want ba7816bf8f01cfea", got)
	}
}

// tokenOfLength returns a valid token of exactly n characters: the base64url
// of a JSON object of the wire shape, with a padding string in the After
// member. n must not be 1 modulo 4, because no unpadded base64url string has
// that length.
func tokenOfLength(t *testing.T, n int) string {
	t.Helper()

	for pad := 0; ; pad++ {
		raw := []byte(`{"a":"` + strings.Repeat("x", pad) + `"}`)
		token := base64.RawURLEncoding.EncodeToString(raw)

		if len(token) == n {
			return token
		}

		if len(token) > n {
			t.Fatalf("no valid token of %d characters", n)
		}
	}
}

// TestDecodeCapsTheLength proves the length cap of Decode with valid tokens on
// both sides of MaxLen. A valid token one step above the cap is refused by the
// cap alone; a valid token one step below it is not refused by the cap.
func TestDecodeCapsTheLength(t *testing.T) {
	t.Parallel()

	// 4098 characters is the first valid length above MaxLen, because 4097
	// is 1 modulo 4. 4095 is the last valid length below it.
	above := tokenOfLength(t, pagetoken.MaxLen+2)
	below := tokenOfLength(t, pagetoken.MaxLen-1)

	var got wire

	if err := pagetoken.Decode(above, &got); !errors.Is(err, pagetoken.ErrInvalid) {
		t.Errorf("a token of %d characters: Decode = %v, want ErrInvalid", len(above), err)
	}

	if err := pagetoken.Decode(below, &got); err != nil {
		t.Errorf("a token of %d characters: Decode = %v, want no error", len(below), err)
	}
}
