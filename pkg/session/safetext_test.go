package session_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
)

// spoofText returns values that make a name look like another name or
// break a line: bidirectional overrides, embeddings, isolates and marks,
// the zero-width space, the byte order mark, the line and paragraph
// separators, control characters, a joiner with no letter on one side, and
// a value that is not UTF-8. Both rules refuse each of them.
func spoofText() map[string]string {
	return map[string]string{
		"right-to-left override":  "alice\u202egnp.exe",
		"left-to-right embedding": "alice\u202a",
		"pop directional":         "alice\u202c",
		"first strong isolate":    "alice\u2068",
		"pop isolate":             "alice\u2069",
		"right-to-left mark":      "alice\u200f",
		"arabic letter mark":      "alice\u061c",
		"zero-width space":        "ali\u200bce",
		"word joiner":             "ali\u2060ce",
		"byte order mark":         "\ufeffalice",
		"line separator":          "alice\u2028admin",
		"paragraph separator":     "alice\u2029admin",
		"tab":                     "alice\x09smith",
		"C1 control":              "alice\u0085",
		"joiner at the start":     "\u200dalice",
		"joiner at the end":       "alice\u200c",
		"joiner between spaces":   "alice \u200d admin",
		"not UTF-8":               "alice\xff",
	}
}

// displayOnly returns real names that a display text holds and that a
// subject or a group does not: a Persian name with a zero-width
// non-joiner, Devanagari with a zero-width joiner, a no-break space, a soft
// hyphen and an emoji sequence.
func displayOnly() map[string]string {
	return map[string]string{
		"Persian with a non-joiner":  "\u0639\u0644\u06cc\u200c\u0631\u0636\u0627",
		"Devanagari with a joiner":   "\u0915\u094d\u200d\u0937",
		"no-break space":             "Jean\u00a0Dupont",
		"soft hyphen":                "Mar\u00adtin",
		"emoji sequence with joiner": "\U0001f469\u200d\U0001f4bb Ana",
		"ideographic space":          "\u5c71\u7530\u3000\u592a\u90ce",
	}
}

func TestIsSafeTextRefusesEachCharacterThatIsNotGraphic(t *testing.T) {
	t.Parallel()

	for name, v := range spoofText() {
		if session.IsSafeText(v) {
			t.Fatalf("%s: IsSafeText(%q) = true, want false", name, v)
		}
	}

	for name, v := range displayOnly() {
		if session.IsSafeText(v) {
			t.Fatalf("%s: IsSafeText(%q) = true, want false", name, v)
		}
	}

	for _, v := range []string{"", "Alice Example", "alice@example.com", "a-b_c.d+e:f", "Zo\u00eb"} {
		if !session.IsSafeText(v) {
			t.Fatalf("IsSafeText(%q) = false, want true", v)
		}
	}
}

func TestIsDisplayTextKeepsRealNamesAndRefusesSpoofing(t *testing.T) {
	t.Parallel()

	for name, v := range spoofText() {
		if session.IsDisplayText(v) {
			t.Fatalf("%s: IsDisplayText(%q) = true, want false", name, v)
		}
	}

	for name, v := range displayOnly() {
		if !session.IsDisplayText(v) {
			t.Fatalf("%s: IsDisplayText(%q) = false, want true", name, v)
		}
	}
}

// Defense in depth: CheckAssertion refuses each spoofing value in each text
// field, as if the method had not checked it.
func TestCheckAssertionRefusesSpoofingText(t *testing.T) {
	t.Parallel()

	for field, set := range textFields() {
		for name, v := range spoofText() {
			a := oidcAssertion()
			set(&a, v)

			if err := session.CheckAssertion(a, checkNow()); !errors.Is(err, session.ErrInvalidAssertion) {
				t.Fatalf("%s with %s: CheckAssertion() = %v, want %v", field, name, err, session.ErrInvalidAssertion)
			}
		}
	}
}

// CheckAssertion keeps the real names of displayOnly in a name, a user name
// and an email, and refuses them in a subject and a group.
func TestCheckAssertionKeepsRealDisplayNames(t *testing.T) {
	t.Parallel()

	for field, set := range textFields() {
		strict := field == "subject" || field == "group"

		for name, v := range displayOnly() {
			a := oidcAssertion()
			set(&a, v)

			err := session.CheckAssertion(a, checkNow())
			if strict && !errors.Is(err, session.ErrInvalidAssertion) {
				t.Fatalf("%s with %s: CheckAssertion() = %v, want %v", field, name, err, session.ErrInvalidAssertion)
			}

			if !strict && err != nil {
				t.Fatalf("%s with %s: CheckAssertion() = %v, want nil", field, name, err)
			}
		}
	}
}

// textFields returns a setter for each text field of an Assertion.
func textFields() map[string]func(a *session.Assertion, v string) {
	return map[string]func(a *session.Assertion, v string){
		"subject":   func(a *session.Assertion, v string) { a.Subject = v },
		"user name": func(a *session.Assertion, v string) { a.UserName = v },
		"email":     func(a *session.Assertion, v string) { a.Email = v },
		"name":      func(a *session.Assertion, v string) { a.Names = []string{v} },
		"group":     func(a *session.Assertion, v string) { a.Groups, a.GroupsPresent = []string{v}, true },
	}
}
