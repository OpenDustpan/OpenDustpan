package stack

import (
	"context"
	"errors"
	"fmt"
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

// UpdateKey is the item key that a new update gets: the function that
// creates it. Every update has one, a preview too. It seals the payload of
// each event of the update. The store builds the reference of the key with
// RefFor, from the stack ID that it resolves in the transaction that creates
// the update, so the key always names the stack of the row.
type UpdateKey struct {
	NewKey NewItemKeyFunc
}

// ErrInvalidUpdateKey reports an UpdateKey with no NewKey function, or a
// reference that RefFor cannot build from the parts of the update.
var ErrInvalidUpdateKey = errors.New("the item key of the update is not valid")

// UpdateItem returns the reference of the item key of the update id of the
// stack with the stable ID stackID in the namespace ns. The kind is
// secret.ItemKindState, and the item is the update ID.
func UpdateItem(ns namespace.Name, stackID ID, id UpdateID) (secret.ItemRef, error) {
	scope, err := secret.NewScope(ns, string(stackID))
	if err != nil {
		return secret.ItemRef{Scope: secret.Scope{Namespace: "", ID: ""}, Kind: "", Item: ""}, err
	}

	return secret.NewItemRef(scope, secret.ItemKindState, id.String())
}

// RefFor returns the reference of the item key of the update id of the stack
// stackID in the namespace ns, as UpdateItem builds it. It returns
// ErrInvalidUpdateKey when k has no NewKey function or a part is not valid. A
// store calls it in the transaction that creates the update, after it resolves
// the stack.
func (k UpdateKey) RefFor(ns namespace.Name, stackID ID, id UpdateID) (secret.ItemRef, error) {
	if k.NewKey == nil {
		return secret.ItemRef{Scope: secret.Scope{Namespace: "", ID: ""}, Kind: "", Item: ""}, ErrInvalidUpdateKey
	}

	ref, err := UpdateItem(ns, stackID, id)
	if err != nil {
		return ref, fmt.Errorf("%w: %w", ErrInvalidUpdateKey, err)
	}

	return ref, nil
}

// ErrItemKeyUnavailable reports an update whose item key is shredded or
// missing, so the server cannot seal or open the payloads of its events. It
// wraps secret.ErrShredded or secret.ErrKeyNotFound. The server fails closed:
// it stores no event and answers none.
var ErrItemKeyUnavailable = errors.New("the item key of the update is shredded or missing")

// keyUnavailable wraps err in ErrItemKeyUnavailable when the item key is
// shredded or missing, and returns err as it is otherwise.
func keyUnavailable(err error) error {
	if errors.Is(err, secret.ErrShredded) || errors.Is(err, secret.ErrKeyNotFound) {
		return fmt.Errorf("%w: %w", ErrItemKeyUnavailable, err)
	}

	return err
}

// eventBindingLabel starts the binding of a sealed event payload.
const eventBindingLabel = "opendustpan/pulumi/update-event/v1"

// eventBinding binds a sealed event payload to its update and its sequence,
// so a payload does not open as another event of the same update.
func eventBinding(id UpdateID, sequence int64) []byte {
	return []byte(eventBindingLabel + "\x00" + id.String() + "\x00" + strconv.FormatInt(sequence, 10))
}

// eventParts returns the content parts of events, with the payloads and
// the bindings of the events.
func eventParts(id UpdateID, events []Event) []secret.ContentPart {
	parts := make([]secret.ContentPart, 0, len(events))
	for i := range events {
		parts = append(parts, secret.ContentPart{
			Binding: eventBinding(id, events[i].Sequence), Data: events[i].Payload,
		})
	}

	return parts
}

// sealEvents returns a copy of events with each payload sealed under the
// item key ref. It opens the key one time for the batch, and never changes
// events.
func sealEvents(
	ctx context.Context, sealer secret.ItemSealer, ref secret.ItemRef, id UpdateID, events []Event,
) ([]Event, error) {
	sealed, err := sealer.SealContents(ctx, ref, eventParts(id, events))
	if err != nil {
		return nil, fmt.Errorf("seal events: %w", keyUnavailable(err))
	}

	if len(sealed) != len(events) {
		return nil, fmt.Errorf("%w: %d sealed payloads for %d events", secret.ErrDecrypt, len(sealed), len(events))
	}

	out := make([]Event, 0, len(events))
	for i := range events {
		out = append(out, Event{Timestamp: events[i].Timestamp, Payload: sealed[i], Sequence: events[i].Sequence})
	}

	return out, nil
}

// openEvents opens the payload of each stored event in place. It opens the
// key one time for the batch, and fails when a payload does not open, so a
// read never answers a payload that the item key did not seal.
func openEvents(
	ctx context.Context, sealer secret.ItemSealer, ref secret.ItemRef, id UpdateID, events []Event,
) error {
	plain, err := sealer.OpenContents(ctx, ref, eventParts(id, events))
	if err != nil {
		return fmt.Errorf("open events: %w", keyUnavailable(err))
	}

	if len(plain) != len(events) {
		return fmt.Errorf("%w: %d opened payloads for %d events", secret.ErrDecrypt, len(plain), len(events))
	}

	for i := range events {
		events[i].Payload = plain[i]
	}

	return nil
}
