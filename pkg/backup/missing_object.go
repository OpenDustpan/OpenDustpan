package backup

import (
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/blob"
)

// MissingObjectError reports an object that the manifest of a backup names
// and that the target does not hold. It matches ErrCorrupt. Key is the key of
// the object in the target, for example blobs/<key> or backups/<id>/<file>.
type MissingObjectError struct {
	Key blob.Key
}

// Error returns "backup is incomplete: object "<key>" is missing: backup data
// is corrupt".
func (e *MissingObjectError) Error() string {
	return fmt.Sprintf("backup is incomplete: object %q is missing: %s", e.Key, ErrCorrupt)
}

// Is reports whether target is ErrCorrupt.
func (e *MissingObjectError) Is(target error) bool {
	return target == ErrCorrupt
}

// missingObjectText returns the text that a restore row stores for err when
// err holds a *MissingObjectError: the text of that error alone, with no
// wrapping prefix. It reports false when err holds none, or when the key is
// not a valid key of a backup object: a blob (blobs/) or a file of a backup
// (backups/).
//
// Defense in depth: every place that makes a MissingObjectError has the key
// from blob.ParseKey already. This check stays so that a new caller cannot
// put text that is not a key of the target into the row that GET shows.
func missingObjectText(err error) (string, bool) {
	missing, ok := errors.AsType[*MissingObjectError](err)
	if !ok || missing == nil {
		return "", false
	}

	key, parseErr := blob.ParseKey(missing.Key.String())
	if parseErr != nil {
		return "", false
	}

	if !strings.HasPrefix(key.String(), "blobs/") && !strings.HasPrefix(key.String(), "backups/") {
		return "", false
	}

	return (&MissingObjectError{Key: key}).Error(), true
}
