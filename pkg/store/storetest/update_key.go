package storetest

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
)

// UnrecordedKey returns the item key of a new update with a key function
// that writes no record, so the suites that list the rows or the blobs of a
// store see no item key. RunEventSealing checks the records.
func UnrecordedKey() stack.UpdateKey {
	return stack.UpdateKey{NewKey: writeNoRecord}
}

// writeNoRecord is an item key function that writes no record and no blob.
func writeNoRecord(
	ctx context.Context, _ secret.ItemKeyTx, ref secret.ItemRef, fence namespace.Fence,
) (secret.ItemKey, error) {
	return RecordingKey(nil)(ctx, nil, ref, fence)
}

// RecordingKey returns an item key function that stores a record of ref
// through the transaction of the store, and then returns after (nil for
// success). It writes no blob, so the suites of the stores need no sealer. A
// fake store with no transaction passes a nil tx: the function then stores no
// record and returns the record that it made.
func RecordingKey(after error) stack.NewItemKeyFunc {
	return func(ctx context.Context, tx secret.ItemKeyTx, ref secret.ItemRef, _ namespace.Fence) (secret.ItemKey, error) {
		rec := secret.ItemKey{
			CreatedAt: time.Now().UTC(), ShreddedAt: time.Time{}, Ref: ref, ID: secret.NewItemKeyID(),
			WrapperID: "kek-1",
			Blob: secret.ItemKeyBlobInfo{
				MD5: [16]byte{1}, Size: recordingKeyBlobSize, Version: 1, Attempt: recordingKeyAttempt,
			},
		}
		if tx == nil {
			return rec, after
		}

		k, err := tx.CreateItemKey(ctx, rec)
		if err != nil {
			return secret.ItemKey{}, err
		}

		return k, after
	}
}

// recordingKeyBlobSize is the size of the key blob that a record of
// RecordingKey names. No blob exists.
const recordingKeyBlobSize = 60

// recordingKeyAttempt is the attempt part of the key blob that a record of
// RecordingKey names: 20 lowercase letters and digits, as the store requires.
// No blob exists.
const recordingKeyAttempt = "recordingkeyattempt0"
