package session_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
)

// hostileText returns values that look like another name or break a line:
// bidirectional overrides and isolates, zero-width characters, the line and
// paragraph separators, a space that is not the ASCII space, a control
// character, a private-use code point and a value that is not UTF-8.
func hostileText() map[string]string {
	return map[string]string{
		"right-to-left override":  "alice\u202egnp.exe",
		"left-to-right embedding": "alice\u202a",
		"pop directional":         "alice\u202c",
		"first strong isolate":    "alice\u2068",
		"pop isolate":             "alice\u2069",
		"zero-width space":        "ali\u200bce",
		"zero-width joiner":       "ali\u200dce",
		"right-to-left mark":      "alice\u200f",
		"byte order mark":         "\ufeffalice",
		"line separator":          "alice\u2028admin",
		"paragraph separator":     "alice\u2029admin",
		"no-break space":          "alice\u00a0smith",
		"ideographic space":       "alice\u3000smith",
		"tab":                     "alice\tsmith",
		"C1 control":              "alice\u0085",
		"private use":             "alice\ue000",
		"not UTF-8":               "alice\xff",
	}
}

func TestSafeTextRefusesFormatAndSeparatorCharacters(t *testing.T) {
	t.Parallel()

	for name, v := range hostileText() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if session.SafeText(v) {
				t.Fatalf("SafeText(%q) = true, want false", v)
			}
		})
	}

	for _, v := range []string{"", "Alice Example", "Zo\u00eb \u00c5ngstr\u00f6m", "\u5c71\u7530 \u592a\u90ce", "alice@example.com", "a-b_c.d+e:f"} {
		if !session.SafeText(v) {
			t.Fatalf("SafeText(%q) = false, want true", v)
		}
	}
}

// Defense in depth: CheckAssertion refuses each hostile value in each text
// field, as if the method had not checked it.
func TestCheckAssertionRefusesFormatAndSeparatorCharacters(t *testing.T) {
	t.Parallel()

	fields := map[string]func(a *session.Assertion, v string){
		"subject":   func(a *session.Assertion, v string) { a.Subject = v },
		"user name": func(a *session.Assertion, v string) { a.UserName = v },
		"email":     func(a *session.Assertion, v string) { a.Email = v },
		"name":      func(a *session.Assertion, v string) { a.Names = []string{v} },
		"group":     func(a *session.Assertion, v string) { a.Groups, a.GroupsPresent = []string{v}, true },
	}

	for field, set := range fields {
		for name, v := range hostileText() {
			a := oidcAssertion()
			set(&a, v)

			if err := session.CheckAssertion(a, checkNow()); !errors.Is(err, session.ErrInvalidAssertion) {
				t.Fatalf("%s with %s: CheckAssertion() = %v, want %v", field, name, err, session.ErrInvalidAssertion)
			}
		}
	}
}
