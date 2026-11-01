package stack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// NewItemKeyFunc creates the item key of a new update in the transaction tx
// of the store, under fence. secret.ItemSealer.NewItemKey has this form. The
// store calls it in the write transaction that stores the update, so the
// record of the key commits with the update row.
type NewItemKeyFunc func(
	ctx context.Context, tx secret.ItemKeyTx, ref secret.ItemRef, fence namespace.Fence,
) (secret.ItemKey, error)

// UpdateKey is the item key that a new update gets: the reference of the key
// and the function that creates it. Every update has one, a preview too. It
// seals the payload of each event of the update.
type UpdateKey struct {
	Ref    secret.ItemRef
	NewKey NewItemKeyFunc
}

// ErrInvalidUpdateKey reports an UpdateKey that does not name the update that
// a store creates: a missing function, or a reference of another namespace,
// another stack, another kind or another update.
var ErrInvalidUpdateKey = errors.New("the item key does not name the update")

// UpdateItem returns the reference of the item key of the update id of the
// stack with the stable ID stackID in the namespace ns. The kind is
// secret.ItemKindState, and the item is the update ID.
func UpdateItem(ns namespace.Name, stackID ID, id UpdateID) (secret.ItemRef, error) {
	scope, err := secret.NewScope(ns, string(stackID))
	if err != nil {
		return secret.ItemRef{}, err
	}

	return secret.NewItemRef(scope, secret.ItemKindState, id.String())
}

// CheckUpdateKey returns ErrInvalidUpdateKey when k does not name the update
// id of the stack stackID in the namespace ns, or has no NewKey function. A
// store calls it in the transaction that creates the update, after it
// resolves the stack.
//
// Defense in depth: Updates.Create builds the reference with UpdateItem. This
// check stays so that a new caller of a store cannot store an update under the
// key of another item.
func CheckUpdateKey(k UpdateKey, ns namespace.Name, stackID ID, id UpdateID) error {
	want, err := UpdateItem(ns, stackID, id)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidUpdateKey, err)
	}

	if k.NewKey == nil || k.Ref != want {
		return ErrInvalidUpdateKey
	}

	return nil
}

// eventBindingLabel starts the binding of a sealed event payload.
const eventBindingLabel = "opendustpan/pulumi/update-event/v1"

// eventBinding binds a sealed event payload to its update and its sequence,
// so a payload does not open as another event of the same update.
func eventBinding(id UpdateID, sequence int64) []byte {
	return []byte(eventBindingLabel + "\x00" + id.String() + "\x00" + strconv.FormatInt(sequence, 10))
}

// sealEvents returns a copy of events with each payload sealed under the
// item key ref. It never changes events.
func sealEvents(
	ctx context.Context, sealer secret.ItemSealer, ref secret.ItemRef, id UpdateID, events []Event,
) ([]Event, error) {
	out := make([]Event, 0, len(events))

	for _, e := range events {
		r, err := sealer.SealContent(ctx, ref, eventBinding(id, e.Sequence), bytes.NewReader(e.Payload))
		if err != nil {
			return nil, fmt.Errorf("seal event %d: %w", e.Sequence, err)
		}

		sealed, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("seal event %d: %w", e.Sequence, err)
		}

		out = append(out, Event{Timestamp: e.Timestamp, Payload: sealed, Sequence: e.Sequence})
	}

	return out, nil
}

// openEvents opens the payload of each stored event in place. It fails on
// the first payload that does not open, so a read never answers a payload that
// the item key did not seal.
func openEvents(
	ctx context.Context, sealer secret.ItemSealer, ref secret.ItemRef, id UpdateID, events []Event,
) error {
	for i := range events {
		e := &events[i]

		r, err := sealer.OpenContent(ctx, ref, eventBinding(id, e.Sequence), bytes.NewReader(e.Payload))
		if err != nil {
			return fmt.Errorf("open event %d: %w", e.Sequence, err)
		}

		plain, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("open event %d: %w", e.Sequence, err)
		}

		e.Payload = plain
	}

	return nil
}
