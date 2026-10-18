package gate

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// GitSource reads a source from a Git repository on disk. It reads the
// ownership file at a commit, never from the working tree, and it lists the
// files that two commits differ in.
type GitSource struct{ Dir string }

func (g GitSource) git(ctx context.Context, verb string, args ...string) ([]byte, error) {
	// The arguments are fixed words and commits that match commitPattern.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", g.Dir, verb}, args...)...) //nolint:gosec // see above
	cmd.Env = []string{"GIT_CONFIG_NOSYSTEM=1", "HOME=" + g.Dir}

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", verb, err)
	}

	return out, nil
}

// Owners returns the content of the ownership file at commit, and nil when
// the commit has none.
func (g GitSource) Owners(ctx context.Context, commit string) ([]byte, error) {
	if !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("%w: commit %q", ErrInvalid, commit)
	}

	out, err := g.git(ctx, "show", commit+":"+OwnersFileName)
	if err != nil {
		// A commit with no ownership file is valid: no requirement.
		return nil, nil //nolint:nilerr // see above
	}

	return out, nil
}

// ChangedPaths returns the paths that differ between base and head.
func (g GitSource) ChangedPaths(ctx context.Context, base, head string) ([]string, error) {
	if !commitPattern.MatchString(base) || !commitPattern.MatchString(head) {
		return nil, fmt.Errorf("%w: commits %q %q", ErrInvalid, base, head)
	}

	out, err := g.git(ctx, "diff", "--name-only", base, head)
	if err != nil {
		return nil, err
	}

	return strings.Fields(string(out)), nil
}
