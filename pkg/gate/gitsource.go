package gate

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// GitSource reads a source from a Git repository on disk. It lists the files
// that two commits differ in, as the fact change.paths.
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
