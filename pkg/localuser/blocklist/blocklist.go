// Package blocklist is the adapter of localuser.Blocklist on a list of common
// and breached passwords that the binary holds. A check never calls a
// service outside the installation.
//
// The list is a copy of a file of SecLists, under the MIT license. The
// directory third_party/github.com/danielmiessler/SecLists holds the file,
// its license and the pinned commit. The package checks the digest and the
// line count of the embedded file when it loads, so a changed file refuses
// each new password instead of letting a password through.
package blocklist

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"opendustpan.org/opendustpan/pkg/localuser"
)

var _ localuser.Blocklist = (*List)(nil)

// listPath is the path of the list in the embedded files.
const listPath = "third_party/github.com/danielmiessler/SecLists/xato-net-10-million-passwords-100000.txt"

// The checks of the embedded list.
const (
	// ListLines is the count of lines of the list.
	ListLines = 100000
	// ListSHA256 is the SHA-256 digest of the list, in hexadecimal.
	ListSHA256 = "1472aafa2561df5e3293aee252aee3ca660c12b399a283cf808bb01b39be388b"
)

// errBadList reports an embedded list that fails a check of parse.
var errBadList = errors.New("the password blocklist is not the pinned file")

//go:embed third_party/github.com/danielmiessler/SecLists/xato-net-10-million-passwords-100000.txt
var files embed.FS

// List is the blocklist of the binary. The zero value is not usable; use
// Embedded.
type List struct {
	load    func() ([]byte, error)
	entries map[string]struct{}
	err     error
	once    sync.Once
}

// Embedded returns the list of the binary. It loads the list at the first
// check.
func Embedded() *List {
	return newList(func() ([]byte, error) { return files.ReadFile(listPath) })
}

// newList returns a list that loads its raw file with load.
func newList(load func() ([]byte, error)) *List {
	return &List{load: load, entries: nil, err: nil, once: sync.Once{}}
}

// Blocked implements localuser.Blocklist.
func (l *List) Blocked(ctx context.Context, candidate string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("check the password blocklist: %w", err)
	}

	// Defense in depth: the service refuses a password above MaxBytes before
	// it checks the list. This check stays so that a new caller cannot make
	// the fold run on an input of any size.
	if len(candidate) > localuser.MaxBytes {
		return false, fmt.Errorf("%w: %d bytes", localuser.ErrPasswordTooLong, len(candidate))
	}

	l.once.Do(func() {
		var raw []byte

		raw, l.err = l.load()
		if l.err == nil {
			l.entries, l.err = parse(raw)
		}
	})

	if l.err != nil {
		return false, fmt.Errorf("%w: password blocklist: %w", localuser.ErrUnavailable, l.err)
	}

	_, found := l.entries[Fold(candidate)]

	return found, nil
}

// Fold returns the form in which the list compares a password: the Unicode
// normalization form NFC, then a Unicode lowercase fold.
func Fold(s string) string {
	return strings.ToLower(norm.NFC.String(s))
}

// parse checks raw against the pinned digest and line count, and returns its
// entries in the form of Fold. It refuses a line that is empty, that is not
// valid UTF-8 or that holds a control character.
func parse(raw []byte) (map[string]struct{}, error) {
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != ListSHA256 {
		return nil, fmt.Errorf("%w: the digest is %s, want %s", errBadList, got, ListSHA256)
	}

	return parseLines(raw, ListLines)
}

// parseLines returns the entries of raw, one on each line that ends with
// LF, in the form of Fold. It refuses a list with another count of lines
// than want. An empty line counts as a line and adds no entry: the list of
// SecLists holds one, and an empty password is too short anyway.
func parseLines(raw []byte, want int) (map[string]struct{}, error) {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return nil, fmt.Errorf("%w: the list does not end with a line feed", errBadList)
	}

	lines := bytes.Split(raw[:len(raw)-1], []byte{'\n'})
	if len(lines) != want {
		return nil, fmt.Errorf("%w: the list has %d lines, want %d", errBadList, len(lines), want)
	}

	entries := make(map[string]struct{}, len(lines))

	for i, line := range lines {
		if len(line) == 0 {
			continue
		}

		if err := checkLine(line); err != nil {
			return nil, fmt.Errorf("line %d of the list: %w", i+1, err)
		}

		entries[Fold(string(line))] = struct{}{}
	}

	return entries, nil
}

// checkLine refuses a line that is not valid UTF-8 and a line that holds a
// control character (CR included).
func checkLine(line []byte) error {
	if !utf8.Valid(line) {
		return fmt.Errorf("%w: the line is not valid UTF-8", errBadList)
	}

	if bytes.ContainsFunc(line, unicode.IsControl) {
		return fmt.Errorf("%w: the line holds a control character", errBadList)
	}

	return nil
}
