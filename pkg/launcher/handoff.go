package launcher

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"opendustpan.org/opendustpan/pkg/runner"
)

// ErrHandoff reports a handoff that gave no valid handoff token.
var ErrHandoff = errors.New("no valid handoff token")

// HandoffFD is the file descriptor of the pipe of runner.HandoffPipe.
const HandoffFD = 3

// MaxHandoff is the most bytes that the launcher reads from a handoff.
const MaxHandoff = 512

// ReadHandoff reads the handoff token from f, which must be a pipe, and
// closes f. It never reads a regular file, a terminal or a socket: a token
// in a file on a disk could stay there. The token is HandoffTokenPrefix and
// then letters, digits, '-' and '_', with an optional line feed at the end.
// It returns an error that wraps ErrHandoff for any other input. The error
// never holds what it read.
func ReadHandoff(f *os.File) (string, error) {
	if f == nil {
		return "", fmt.Errorf("%w: no handoff file", ErrHandoff)
	}

	handoff, err := readPipe(f)

	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = fmt.Errorf("%w: close the handoff: %w", ErrHandoff, closeErr)
	}

	if err != nil {
		return "", err
	}

	return handoff, nil
}

// readPipe checks that f is a pipe and reads the token from it.
func readPipe(f *os.File) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("%w: the handoff is not readable", ErrHandoff)
	}

	if info.Mode()&os.ModeNamedPipe == 0 {
		return "", fmt.Errorf("%w: the handoff is not a pipe", ErrHandoff)
	}

	raw, err := io.ReadAll(io.LimitReader(f, MaxHandoff+1))
	if err != nil {
		return "", fmt.Errorf("%w: the handoff is not readable", ErrHandoff)
	}

	if len(raw) > MaxHandoff {
		return "", fmt.Errorf("%w: the handoff has more than %d bytes", ErrHandoff, MaxHandoff)
	}

	handoff := strings.TrimSuffix(string(raw), "\n")
	if !ValidHandoffToken(handoff) {
		return "", fmt.Errorf("%w: the handoff does not hold a handoff token", ErrHandoff)
	}

	return handoff, nil
}

// ValidHandoffToken reports whether s has the form of a handoff token:
// runner.HandoffTokenPrefix, then 1 or more ASCII letters, digits, '-' and
// '_'.
func ValidHandoffToken(s string) bool {
	rest, ok := strings.CutPrefix(s, runner.HandoffTokenPrefix)
	if !ok || rest == "" || len(s) > MaxHandoff {
		return false
	}

	return !strings.ContainsFunc(rest, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_'
	})
}
