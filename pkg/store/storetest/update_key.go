package storetest

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
)

// StackGetter resolves a stack reference to its stack.
type StackGetter interface {
	GetStack(ctx context.Context, ref stack.Ref) (stack.Stack, error)
}

// missingStackID is the stack ID of the key of an update of a stack that the
// store does not hold. The store refuses the update before it checks the key.
const missingStackID stack.ID = "missing-stack"

// UpdateKeyFor returns the item key of the new update u: the reference that
// stack.UpdateItem gives for the stack of u in s, and RecordingKey(nil). For a
// stack that s does not hold, the reference names the stack ID
// "missing-stack".
func UpdateKeyFor(ctx context.Context, s StackGetter, u stack.Update) stack.UpdateKey {
	id := missingStackID
	if st, err := s.GetStack(ctx, u.Ref); err == nil {
		id = st.ID
	}

	ref, err := stack.UpdateItem(u.Ref.Namespace, id, u.ID)
	if err != nil {
		return stack.UpdateKey{Ref: emptyItemRef(), NewKey: RecordingKey(err)}
	}

	return stack.UpdateKey{Ref: ref, NewKey: RecordingKey(nil)}
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
			Blob:      secret.ItemKeyBlobInfo{MD5: [16]byte{1}, Size: recordingKeyBlobSize, Version: 1},
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

// emptyItemRef returns the item reference with no field set.
func emptyItemRef() secret.ItemRef {
	return secret.ItemRef{Scope: secret.Scope{Namespace: "", ID: ""}, Kind: "", Item: ""}
}
