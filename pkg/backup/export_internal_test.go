package backup

import "crypto"

// CheckObjectBytes runs the object check of one manifest entry on data: the
// hash function h, the hex sum and the size that the manifest records. The
// restore paths and backup check run the same check.
func CheckObjectBytes(h crypto.Hash, sum string, size int64, data []byte) error {
	check, err := newObjectCheck("test-object", objectWant{size: size, hash: h, sum: sum})
	if err != nil {
		return err
	}

	if _, err := check.Write(data); err != nil {
		return err
	}

	return check.verify("test-object")
}
