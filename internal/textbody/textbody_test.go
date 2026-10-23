package textbody_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/textbody"
)

// TestRefused proves that Refused reports the bodies whose text a JSON decode
// would change: bytes that are not valid UTF-8, and a lone surrogate escape.
// It does not report a body that is well formed apart from that fault, so the
// decode of such a body keeps its own answer.
func TestRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, body string
		want       bool
	}{
		{name: "valid text", body: `{"summary":"plain"}`, want: false},
		{name: "a surrogate pair escape", body: `{"summary":"pager 📟"}`, want: false},
		{name: "an escape of a letter", body: `{"summary":"A"}`, want: false},
		{name: "a raw byte ff", body: "{\"summary\":\"\xff\"}", want: true},
		{name: "a cut sequence", body: "{\"owners\":[\"team-\xc3\"]}", want: true},
		{name: "a raw byte ff with a syntax fault", body: "{\"summary\":\"\xff\",}", want: true},
		{name: "a lone high surrogate escape", body: `{"summary":"lone \ud800"}`, want: true},
		{name: "a lone low surrogate escape", body: `{"runbook":"lone \udc00"}`, want: true},
		{name: "a high escape followed by a letter", body: `{"owners":["team-\ud83dx"]}`, want: true},
		{name: "a lone surrogate with a syntax fault", body: `{"summary":"\ud800",}`, want: false},
		{name: "a syntax fault alone", body: `{"summary":"a",}`, want: false},
		{name: "an empty body", body: ``, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := textbody.Refused([]byte(tc.body)); got != tc.want {
				t.Errorf("Refused(%q) = %t, want %t", tc.body, got, tc.want)
			}
		})
	}
}
