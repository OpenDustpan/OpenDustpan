// Command pinfrozen refuses a branch that changes the description pin, or
// info.version of the description, before the first tag (make pin-frozen). It
// does nothing after the first tag, when the pin moves with each pull request
// that changes the description.
//
// Usage: go run ./cmd/pinfrozen <base ref>
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"opendustpan.org/opendustpan/internal/pinfrozen"
	"opendustpan.org/opendustpan/internal/releaserecord"
)

// The exit codes of the command.
const (
	exitPass  = 0
	exitFail  = 1
	exitUsage = 2
)

// refPattern allows a branch, a tag or a commit with a relative suffix. It
// starts with a letter or a digit, so that a ref cannot read as a Git option.
var refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/~^-]*$`)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// run performs the check and returns the exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || !refPattern.MatchString(args[0]) {
		return report(stderr, exitUsage, "usage: pinfrozen <base ref>")
	}

	base := args[0]

	// A base ref that is missing fails the check before the release check, so
	// that a CI job with no fetched ref cannot pass by skipping the rule.
	if _, err := git(ctx, "rev-parse", "--verify", "--quiet", base+"^{commit}"); err != nil {
		return report(stderr, exitFail, "pin-frozen: base ref %s does not exist; fetch it first", base)
	}

	rec, err := releaserecord.Current()
	if err != nil {
		return report(stderr, exitFail, "pin-frozen: read the release record: %v", err)
	}

	if rec.Released() {
		return report(stdout, exitPass,
			"pin-frozen: a tag shipped; the pin moves with each pull request that changes the description")
	}

	out, err := git(ctx, "diff", "--name-only", base+"...HEAD")
	if err != nil {
		return report(stderr, exitFail, "pin-frozen: list the changed files against %s: %v", base, err)
	}

	changed := strings.Fields(string(out))

	readBase := func(path string) ([]byte, error) { return gitShow(ctx, base, path) }
	readHead := func(path string) ([]byte, error) { return gitShow(ctx, "HEAD", path) }

	if err := pinfrozen.Check(changed, readBase, readHead); err != nil {
		return report(stderr, exitFail, "pin-frozen: %v", err)
	}

	return report(stdout, exitPass,
		"pin-frozen: this branch does not change the pin or info.version before the first tag")
}

// report writes one line and returns code. A line that cannot be written fails
// the check, so that a run with no readable output never reads as a pass.
func report(w io.Writer, code int, format string, args ...any) int {
	if _, err := fmt.Fprintf(w, format+"\n", args...); err != nil {
		return exitFail
	}

	return code
}

// gitShow returns a file at one revision. A file that the revision does not
// hold wraps fs.ErrNotExist.
func gitShow(ctx context.Context, rev, path string) ([]byte, error) {
	spec := rev + ":" + path

	if _, err := git(ctx, "cat-file", "-e", spec); err != nil {
		return nil, fmt.Errorf("%s: %w", spec, fs.ErrNotExist)
	}

	out, err := git(ctx, "show", spec)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", spec, err)
	}

	return out, nil
}

// git runs one git command with an argument list, never through a shell. The
// command name is fixed, and the only input is a ref that refPattern allowed.
func git(ctx context.Context, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer

	//nolint:gosec // The command name is fixed and the ref was checked by refPattern; no shell runs.
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}
