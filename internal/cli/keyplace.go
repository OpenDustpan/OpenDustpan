package cli

import (
	"path/filepath"
	"strings"
)

// KeyNextToData reports whether the key file is in the directory of the
// SQLite file or of the blobs, or under the directory of the blobs. It
// resolves symbolic links first, also the link of a file, so a link does not
// hide the place of a file. A path that it cannot resolve counts as not next
// to the data.
func KeyNextToData(keyPath, database, blobDir string) bool {
	keyDir, ok := resolvedDir(keyPath)
	if !ok {
		return false
	}

	if rest, isSQLite := strings.CutPrefix(database, "sqlite:"); isSQLite {
		if dir, found := resolvedDir(strings.TrimSpace(rest)); found && dir == keyDir {
			return true
		}
	}

	blobs, found := resolvedPath(DefaultBlobDir(database, blobDir))
	if !found {
		return false
	}

	rel, err := filepath.Rel(blobs, keyDir)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolvedDir answers the directory of the file at path after it resolves
// the links. The file may not exist yet, as the SQLite file may not: then
// only the links of its directory count.
func resolvedDir(path string) (string, bool) {
	if resolved, ok := resolvedPath(path); ok {
		return filepath.Dir(resolved), true
	}

	return resolvedPath(filepath.Dir(path))
}

// resolvedPath answers the absolute path of path after it resolves the links.
func resolvedPath(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false
	}

	return resolved, true
}

// DefaultBlobDir returns the state content directory: the --blob-dir flag
// when set, else a blobs directory next to the SQLite file, or blobs in the
// working directory for PostgreSQL.
func DefaultBlobDir(database, flagDir string) string {
	if strings.TrimSpace(flagDir) != "" {
		return strings.TrimSpace(flagDir)
	}

	if rest, ok := strings.CutPrefix(database, "sqlite:"); ok {
		path := strings.TrimSpace(rest)
		if path == "" {
			return "blobs"
		}

		return filepath.Join(filepath.Dir(path), "blobs")
	}

	return "blobs"
}
