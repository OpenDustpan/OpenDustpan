package selfservice

import (
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// The git layout of a request: GitRoot/<namespace>/<blueprint>/<name>/RequestFile.
const (
	// GitRoot is the directory of the requests in a repository.
	GitRoot = "self-service"
	// RequestFile is the name of the file of a request.
	RequestFile = "request.json"
	// RenderDir is the directory below the directory of a request where the
	// render writes its files. The repository ignores it.
	RenderDir = ".render"
)

// GitDir returns the directory of the request ref in a repository. It is
// the code location of the unit for the write-back job and for the team's
// CI.
func GitDir(ref RequestRef) string {
	return GitRoot + "/" + ref.Namespace.String() + "/" + ref.Blueprint + "/" + ref.Name
}

// GitPath returns the path of the request file of ref in a repository.
func GitPath(ref RequestRef) string {
	return GitDir(ref) + "/" + RequestFile
}

// ParseGitPath returns the request that the path of a request file names.
// It returns ErrInvalid for a path that is not of the layout, or that names
// a namespace, a blueprint or a name that is not valid.
func ParseGitPath(path string) (RequestRef, error) {
	parts := strings.Split(path, "/")
	if len(parts) != 5 || parts[0] != GitRoot || parts[4] != RequestFile {
		return RequestRef{}, fmt.Errorf("%w: %q is not of the form %s", ErrInvalid, path,
			GitRoot+"/<namespace>/<blueprint>/<name>/"+RequestFile)
	}

	ns, err := namespace.ParseName(parts[1])
	if err != nil || !ValidName(parts[2]) || !ValidName(parts[3]) {
		return RequestRef{}, fmt.Errorf("%w: %q names a namespace, a blueprint or a name that is not valid",
			ErrInvalid, path)
	}

	return RequestRef{Namespace: ns, Blueprint: parts[2], Name: parts[3]}, nil
}

// CheckPath returns ErrPathMismatch when the request file at path holds a
// document of another request than the path names.
func CheckPath(path string, d *Document) error {
	ref, err := ParseGitPath(path)
	if err != nil {
		return err
	}

	if ref != d.Ref() {
		return fmt.Errorf("%w: the path names %s and the document names %s", ErrPathMismatch, ref, d.Ref())
	}

	return nil
}
