package backup

import (
	"errors"
	"fmt"
	"slices"
)

// BlobsNotBackedText starts the text of a BlobsNotBackedError. A restore row
// stores the text, and the answer of the API derives the error code from it.
const BlobsNotBackedText = "backup is incomplete: the data names "

// ErrBlobsNotBacked reports a backup whose SQL data names blobs that the
// backup does not hold: the rows name a plan, a log chunk or a registry file
// with no size and MD5 of its stored bytes, and the manifest does not list the
// blob as not copied. A restore of such a backup would bring back rows with no
// content, so it refuses. It matches ErrCorrupt.
var ErrBlobsNotBacked = errors.New("data names blobs that the backup does not hold")

// BlobsNotBackedError carries the facts of ErrBlobsNotBacked: how many blobs
// the data names that the backup does not hold, and the first key in order.
// Count is at least 1.
type BlobsNotBackedError struct {
	Count int
	First string
}

// Error returns "backup is incomplete: the data names <count> blobs that the
// backup does not hold, first <key>: backup data is corrupt".
func (e *BlobsNotBackedError) Error() string {
	return fmt.Sprintf(
		"%s%d blobs that the backup does not hold, first %q: %s",
		BlobsNotBackedText, e.Count, e.First, ErrCorrupt,
	)
}

// Is reports whether target is ErrBlobsNotBacked or ErrCorrupt.
func (e *BlobsNotBackedError) Is(target error) bool {
	return target == ErrBlobsNotBacked || target == ErrCorrupt
}

// CheckUnverified compares the blobs that the staged data names without a
// check value with the list of the manifest. The manifest lists the blobs that
// the backup knowingly did not copy; every key the staged data names must be
// in that list. A key the staged data names and the manifest does not list is a
// blob that the backup lost, for example a backup that an earlier version of
// the server made. It returns *BlobsNotBackedError for such a key and nil
// otherwise. Both lists must be sorted.
func CheckUnverified(staged, listed []string) error {
	var (
		count int
		first string
	)

	for _, key := range staged {
		if _, found := slices.BinarySearch(listed, key); found {
			continue
		}

		if count == 0 {
			first = key
		}

		count++
	}

	if count == 0 {
		return nil
	}

	return &BlobsNotBackedError{Count: count, First: first}
}
