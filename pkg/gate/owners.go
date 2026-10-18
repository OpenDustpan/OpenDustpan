package gate

import (
	"bufio"
	"bytes"
	"fmt"
	"path"
	"strconv"
	"strings"
)

// OwnersFileName is the name of the ownership file at the root of a source.
const OwnersFileName = "OWNERS.dustpan"

// OwnerSection is one section of the ownership file. Its form is
//
//	[name] @group:<group> <count> <path glob>...
//
// A glob has "/" separated segments; "**" matches any number of segments.
type OwnerSection struct {
	Name  string
	Group string
	Count int
	Globs []string
}

// ParseOwners reads the ownership file. A blank line and a line that starts
// with "#" are ignored. It returns ErrInvalid with the line number for a
// line that does not have the form.
func ParseOwners(raw []byte) ([]OwnerSection, error) {
	var out []OwnerSection

	sc := bufio.NewScanner(bytes.NewReader(raw))
	line := 0

	for sc.Scan() {
		line++

		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		sec, err := parseOwnerLine(text)
		if err != nil {
			return nil, fmt.Errorf("%w: %s line %d: %w", ErrInvalid, OwnersFileName, line, err)
		}

		out = append(out, sec)
	}

	err := sc.Err()
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %w", ErrInvalid, OwnersFileName, err)
	}

	return out, nil
}

// minOwnerFields is the field count of the shortest section: name, approver, count and one glob.
const minOwnerFields = 4

func parseOwnerLine(text string) (OwnerSection, error) {
	fields := strings.Fields(text)
	if len(fields) < minOwnerFields {
		return OwnerSection{}, fmt.Errorf("%w: want [name] @group:<group> <count> <glob>..., got %q", ErrInvalid, text)
	}

	name, ok := strings.CutSuffix(strings.TrimPrefix(fields[0], "["), "]")
	if !ok || !strings.HasPrefix(fields[0], "[") || name == "" {
		return OwnerSection{}, fmt.Errorf("%w: section name %q is not [name]", ErrInvalid, fields[0])
	}

	group, ok := strings.CutPrefix(fields[1], "@group:")
	if !ok || group == "" {
		return OwnerSection{}, fmt.Errorf("%w: approver %q is not @group:<group>", ErrInvalid, fields[1])
	}

	count, err := strconv.Atoi(fields[2])
	if err != nil || count < 1 {
		return OwnerSection{}, fmt.Errorf("%w: count %q is not a positive number", ErrInvalid, fields[2])
	}

	return OwnerSection{Name: name, Group: group, Count: count, Globs: fields[3:]}, nil
}

// Matches reports whether one of the paths is in a glob of the section.
func (s OwnerSection) Matches(paths []string) bool { return matchGlobs(s.Globs, paths) }

// matchGlobs reports whether one of the paths is in one of the globs.
func matchGlobs(globs, paths []string) bool {
	for _, p := range paths {
		for _, g := range globs {
			if globMatch(strings.Split(g, "/"), strings.Split(p, "/")) {
				return true
			}
		}
	}

	return false
}

func globMatch(glob, parts []string) bool {
	if len(glob) == 0 {
		return len(parts) == 0
	}

	if glob[0] == "**" {
		for i := 0; i <= len(parts); i++ {
			if globMatch(glob[1:], parts[i:]) {
				return true
			}
		}

		return false
	}

	if len(parts) == 0 {
		return false
	}

	ok, err := path.Match(glob[0], parts[0])
	if err != nil || !ok {
		return false
	}

	return globMatch(glob[1:], parts[1:])
}
