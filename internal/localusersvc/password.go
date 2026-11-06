package localusersvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
)

// productNames returns the words of the product that no password can hold.
func productNames() []string {
	return []string{"opendustpan", "dustpan"}
}

// minSequence is the shortest text that has a step between two code points.
// A shorter text is a repeat of one character.
const minSequence = 2

// minContextWord is the shortest context word, in code points, that the
// password check refuses inside a password. A shorter word, for example a
// user name of two letters, would refuse too many good passwords.
const minContextWord = 3

// checkForm checks the form of a password that comes from outside the
// process: valid UTF-8, at most MaxBytes before and after NFC, and no
// control character. It returns the NFC form. It never changes a password to
// make it valid, and it never puts the password in an error.
func checkForm(password string) (string, error) {
	if len(password) > localuser.MaxBytes {
		return "", fmt.Errorf("%w: more than %d bytes", localuser.ErrPasswordTooLong, localuser.MaxBytes)
	}

	if !utf8.ValidString(password) {
		return "", fmt.Errorf("%w: the password is not valid UTF-8", localuser.ErrPasswordForm)
	}

	if strings.ContainsFunc(password, unicode.IsControl) {
		return "", fmt.Errorf("%w: the password holds a control character", localuser.ErrPasswordForm)
	}

	nfc := norm.NFC.String(password)

	// Defense in depth: the length check above runs on the raw text. NFC can
	// make a text longer, so this check runs on the form that the server
	// hashes, and does not rely on the first one.
	if len(nfc) > localuser.MaxBytes {
		return "", fmt.Errorf("%w: more than %d bytes", localuser.ErrPasswordTooLong, localuser.MaxBytes)
	}

	return nfc, nil
}

// checkNew checks a new password against the rules of p and the context
// words, and returns its NFC form. It refuses a password with the first rule
// that fails, in this order: form, length, letters and digits, repeat or
// sequence, context words, blocklist. A blocklist that cannot answer refuses
// the password with ErrUnavailable.
func (s *Service) checkNew(ctx context.Context, password string, p Policy, words []string) (string, error) {
	return checkNewPassword(ctx, s.blocklist, password, p, words)
}

// checkNewPassword is checkNew with the blocklist as an argument, so the demo
// seed applies the same rules without a Service.
func checkNewPassword(
	ctx context.Context, list localuser.Blocklist, password string, p Policy, words []string,
) (string, error) {
	nfc, err := checkForm(password)
	if err != nil {
		return "", err
	}

	n := utf8.RuneCountInString(nfc)

	switch {
	case n > localuser.MaxLength:
		return "", fmt.Errorf("%w: more than %d characters", localuser.ErrPasswordTooLong, localuser.MaxLength)
	case n < p.MinLength:
		return "", &ShortError{MinLength: p.MinLength}
	case p.RequireLettersAndDigits && !lettersAndDigits(nfc):
		return "", localuser.ErrPasswordLettersAndDigits
	case repeatOrSequence(nfc):
		return "", fmt.Errorf("%w: one character again and again, or a plain sequence", localuser.ErrPasswordBlocked)
	case holdsWord(nfc, words):
		return "", fmt.Errorf("%w: the password holds a name of the person or of the product",
			localuser.ErrPasswordBlocked)
	}

	blocked, err := list.Blocked(ctx, nfc)
	if err != nil {
		// Fail closed: a list that cannot answer refuses each new password.
		return "", fmt.Errorf("%w: the blocklist cannot answer", localuser.ErrUnavailable)
	}

	if blocked {
		return "", fmt.Errorf("%w: the password is on the list of common passwords", localuser.ErrPasswordBlocked)
	}

	return nfc, nil
}

// ShortError is ErrPasswordTooShort with the minimum length that applies.
type ShortError struct {
	MinLength int
}

// Error implements error.
func (e *ShortError) Error() string {
	return fmt.Sprintf("%s: use %d characters or more", localuser.ErrPasswordTooShort, e.MinLength)
}

// Unwrap returns localuser.ErrPasswordTooShort.
func (*ShortError) Unwrap() error { return localuser.ErrPasswordTooShort }

// MinLengthOf returns the minimum length that err names, and false when err
// is not a ShortError.
func MinLengthOf(err error) (int, bool) {
	if short, ok := errors.AsType[*ShortError](err); ok {
		return short.MinLength, true
	}

	return 0, false
}

// lettersAndDigits reports whether s holds a letter (Unicode category L) and
// a decimal digit (Unicode category Nd).
func lettersAndDigits(s string) bool {
	return strings.ContainsFunc(s, unicode.IsLetter) && strings.ContainsFunc(s, func(r rune) bool {
		return unicode.Is(unicode.Nd, r)
	})
}

// repeatOrSequence reports whether s is one character again and again, or a
// plain sequence of code points that go up or down by one, for example
// "abcdef" or "987654". The case of a letter does not count.
func repeatOrSequence(s string) bool {
	runes := []rune(blocklist.Fold(s))
	if len(runes) < minSequence {
		return true
	}

	step := runes[1] - runes[0]
	if step < -1 || step > 1 {
		return false
	}

	for i := 2; i < len(runes); i++ {
		if runes[i]-runes[i-1] != step {
			return false
		}
	}

	return true
}

// holdsWord reports whether s holds a product name, or one of words that has
// at least minContextWord code points, after NFC and a lowercase fold.
func holdsWord(s string, words []string) bool {
	folded := blocklist.Fold(s)

	for _, w := range append(productNames(), words...) {
		fw := blocklist.Fold(w)
		if utf8.RuneCountInString(fw) < minContextWord {
			continue
		}

		if strings.Contains(folded, fw) {
			return true
		}
	}

	return false
}
