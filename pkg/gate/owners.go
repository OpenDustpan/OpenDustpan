package gate

import (
	"path"
	"strings"
)

// matchGlobs reports whether one of the paths is in one of the globs. A glob
// has "/" separated segments; "**" matches any number of segments.
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
