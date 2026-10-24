package inventory_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// asciiLabelValues are label values that the label grammar refuses: each
// holds a character outside letters, digits and . _ : / -. The key is the
// name of the case.
func asciiLabelValues() map[string]string {
	return map[string]string{
		"non ASCII letter":       "équipe",
		"space":                  "payments team",
		"right-to-left override": "pay\u202ements",
		"zero width space":       "pay\u200bments",
		"line separator":         "pay\u2028ments",
		"equals sign":            "a=b",
		"comma":                  "a,b",
		"asterisk":               "a*",
	}
}

// TestUnitListRefusesALabelValueOutsideASCII proves that the label filter of
// the unit list answers 400 request.invalid-query for a value with a
// character outside the label grammar, and that the text names the rule.
func TestUnitListRefusesALabelValueOutsideASCII(t *testing.T) {
	t.Parallel()

	for name, value := range asciiLabelValues() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler := newFilterHandler(t, selectiveAuthorizer{deny: nil}, "")

			query := "label=" + percentEncode("team="+value)

			rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units?"+query, "test-token")

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, rec.Body.Bytes(),
				errcodetest.Want{Code: errcode.RequestInvalidQuery, Args: nil, RequestBody: nil})

			if !strings.Contains(rec.Body.String(), "letters, digits and . _ : / -") {
				t.Fatalf("answer = %s, want the rule of a label value", rec.Body.String())
			}
		})
	}
}

// TestUnitListAcceptsALabelValueInTheGrammar proves that a value of the
// grammar still filters: every allowed character, the empty value and the
// longest value.
func TestUnitListAcceptsALabelValueInTheGrammar(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"payments", "a.b_c:d/e-F9", "", strings.Repeat("v", 256)} {
		handler := newFilterHandler(t, selectiveAuthorizer{deny: nil}, "")

		rec := authorizedGet(t, handler,
			"/api/dustpan/v1/namespaces/acme/units?label="+percentEncode("team="+value), "test-token")
		if rec.Code != http.StatusOK {
			t.Fatalf("label value %q: status = %d, want 200: %s", value, rec.Code, rec.Body.String())
		}
	}
}

// TestSavedViewRefusesALabelValueOutsideASCII proves that a saved view with a
// label value outside the grammar answers 400 view.invalid and stores no
// view.
func TestSavedViewRefusesALabelValueOutsideASCII(t *testing.T) {
	t.Parallel()

	for name, value := range asciiLabelValues() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newViewFixture(t)

			label, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("encode value: %v", err)
			}

			body := `{"name":"Payments","query":{"labels":[{"key":"team","value":` + string(label) +
				`}],"references":[]}}`

			res := f.do(t, http.MethodPost, viewsPath, tokenAna, body)

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, []byte(res.body),
				errcodetest.Want{Code: errcode.ViewInvalid, Args: nil, RequestBody: []byte(body)})

			list := f.do(t, http.MethodGet, viewsPath, tokenAna, "")
			if list.status != http.StatusOK || strings.Contains(list.body, `"Payments"`) {
				t.Fatalf("list = %d %s, want 200 with no view", list.status, list.body)
			}
		})
	}
}

// TestUnitListShowsALabelValueOutsideASCII proves that a unit whose stored
// label value is outside the grammar, such as a Pulumi stack tag, still lists
// with that label. The grammar limits the filter and the writes, not the read.
func TestUnitListShowsALabelValueOutsideASCII(t *testing.T) {
	t.Parallel()

	labels := map[string]string{"team": "équipe paiements", "owner": "pay\u202ements"}

	handler := newOutsideGrammarHandler(t, labels)

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got labelsPage

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.Units) != 1 || got.Units[0].Labels["team"] != labels["team"] ||
		got.Units[0].Labels["owner"] != labels["owner"] {
		t.Fatalf("units = %+v, want one unit with its two labels", got.Units)
	}
}

// percentEncode encodes every byte of s that is not an ASCII letter or digit,
// so that the query holds the exact bytes of the value.
func percentEncode(s string) string {
	const hex = "0123456789ABCDEF"

	var b strings.Builder

	for i := range len(s) {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			b.WriteByte(c)

			continue
		}

		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}

	return b.String()
}
