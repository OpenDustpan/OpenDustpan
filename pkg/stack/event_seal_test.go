package stack_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
)

// eventSecret is a secret value that a program printed. No stored event may
// hold it.
const eventSecret = "s3cr3t-printed-by-the-program"

// printed returns the payload of a diagnostic event that holds eventSecret,
// as the Pulumi CLI sends a line that the program printed.
func printed() []byte {
	return []byte(`{"sequence":0,"diagnosticEvent":{"message":"stdout ` + eventSecret + `\n","severity":"info"}}`)
}

// itemOf returns the item key reference of the update id of the fixture.
func (f updatesFixture) itemOf(t *testing.T, id stack.UpdateID) secret.ItemRef {
	t.Helper()

	ref, err := stack.UpdateItem(f.ref.Namespace, "stk-test", id)
	if err != nil {
		t.Fatalf("item of update %s: %v", id, err)
	}

	return ref
}

// TestEventsAreSealed proves that AppendEvents stores each payload sealed
// under the item key of the update, and that Events opens it.
func TestEventsAreSealed(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()

	for _, kind := range []stack.UpdateKind{stack.KindUpdate, stack.KindPreview} {
		s := f.started(t, kind)

		if !f.sealer.Has(f.itemOf(t, s.Update.ID)) {
			t.Fatalf("%s %s has no item key", kind, s.Update.ID)
		}

		sent := stack.Event{Timestamp: f.clock.Now(), Payload: printed(), Sequence: 0}
		if err := f.svc.AppendEvents(ctx, f.ref, s.Update.ID, s.Token, []stack.Event{sent}); err != nil {
			t.Fatalf("append events: %v", err)
		}

		stored, err := f.store.ListEvents(ctx, f.ref, s.Update.ID, -1, 10)
		if err != nil || len(stored) != 1 {
			t.Fatalf("stored events = %d, %v; want 1", len(stored), err)
		}

		if bytes.Contains(stored[0].Payload, []byte(eventSecret)) || bytes.Equal(stored[0].Payload, sent.Payload) {
			t.Fatalf("the stored payload of the %s holds the plain text", kind)
		}

		got, err := f.svc.Events(ctx, f.ref, s.Update.ID, -1, 10)
		if err != nil || len(got) != 1 || !bytes.Equal(got[0].Payload, sent.Payload) {
			t.Fatalf("events = %v, %v; want the sent payload", got, err)
		}
	}
}

// TestEventsAppendDoesNotChangeTheBatch proves that the seal works on a copy:
// the batch of the caller keeps its plain payloads.
func TestEventsAppendDoesNotChangeTheBatch(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	s := f.started(t, stack.KindUpdate)

	batch := []stack.Event{{Timestamp: f.clock.Now(), Payload: printed(), Sequence: 0}}
	if err := f.svc.AppendEvents(t.Context(), f.ref, s.Update.ID, s.Token, batch); err != nil {
		t.Fatalf("append events: %v", err)
	}

	if !bytes.Equal(batch[0].Payload, printed()) {
		t.Fatal("AppendEvents changed the batch of the caller")
	}
}

// TestEventsFailClosedWithoutTheKey proves that a shredded or a missing item
// key fails the write and the read, and that no payload is stored or answered
// as the CLI sent it.
func TestEventsFailClosedWithoutTheKey(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	sent := []stack.Event{{Timestamp: f.clock.Now(), Payload: printed(), Sequence: 0}}
	if err := f.svc.AppendEvents(ctx, f.ref, s.Update.ID, s.Token, sent); err != nil {
		t.Fatalf("append events: %v", err)
	}

	f.sealer.Shred(f.itemOf(t, s.Update.ID))

	more := []stack.Event{{Timestamp: f.clock.Now(), Payload: printed(), Sequence: 1}}
	if err := f.svc.AppendEvents(ctx, f.ref, s.Update.ID, s.Token, more); !errors.Is(err, secret.ErrShredded) {
		t.Fatalf("append after a shred: err = %v, want ErrShredded", err)
	}

	if got, err := f.svc.Events(ctx, f.ref, s.Update.ID, -1, 10); !errors.Is(err, secret.ErrShredded) || got != nil {
		t.Fatalf("events after a shred = %v, %v; want ErrShredded and none", got, err)
	}

	stored, err := f.store.ListEvents(ctx, f.ref, s.Update.ID, -1, 10)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored events after a shred = %d, %v; want the first one only", len(stored), err)
	}
}

// TestEventsOfAnUpdateWithNoKey proves that an update whose key the sealer
// never made, for example a row that another path stored, takes no event and
// answers none.
func TestEventsOfAnUpdateWithNoKey(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	// The row exists with no item key: the fake store takes it directly.
	other := s.Update
	other.ID = "u-without-key"

	f.store.mu.Lock()
	f.store.updates[other.ID] = other
	f.store.events[other.ID] = []stack.Event{{Timestamp: f.clock.Now(), Payload: printed(), Sequence: 0}}
	f.store.mu.Unlock()

	sent := []stack.Event{{Timestamp: f.clock.Now(), Payload: printed(), Sequence: 1}}
	if err := f.svc.AppendEvents(ctx, f.ref, other.ID, s.Token, sent); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Fatalf("append to an update with no key: err = %v, want ErrKeyNotFound", err)
	}

	if _, err := f.svc.Events(ctx, f.ref, other.ID, -1, 10); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Fatalf("events of an update with no key: err = %v, want ErrKeyNotFound", err)
	}
}

// TestEventsOpenOnlyAtTheirSequence proves that a stored payload opens only
// for its own update and sequence.
func TestEventsOpenOnlyAtTheirSequence(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	first := f.started(t, stack.KindPreview)
	second := f.started(t, stack.KindPreview)

	for _, s := range []stack.Started{first, second} {
		batch := []stack.Event{
			{Timestamp: f.clock.Now(), Payload: []byte(`{"a":0}`), Sequence: 0},
			{Timestamp: f.clock.Now(), Payload: []byte(`{"a":1}`), Sequence: 1},
		}
		if err := f.svc.AppendEvents(ctx, f.ref, s.Update.ID, s.Token, batch); err != nil {
			t.Fatalf("append events: %v", err)
		}
	}

	f.store.mu.Lock()
	ev, ev2 := f.store.events[first.Update.ID], f.store.events[second.Update.ID]

	if len(ev) != 2 || len(ev2) != 2 {
		f.store.mu.Unlock()
		t.Fatalf("stored events = %d and %d, want 2 each", len(ev), len(ev2))
	}

	ev[0].Payload, ev[1].Payload = ev[1].Payload, ev[0].Payload
	ev2[0].Payload = ev[1].Payload
	f.store.mu.Unlock()

	for _, id := range []stack.UpdateID{first.Update.ID, second.Update.ID} {
		if _, err := f.svc.Events(ctx, f.ref, id, -1, 10); !errors.Is(err, secret.ErrDecrypt) {
			t.Fatalf("events of %s with a moved payload: err = %v, want ErrDecrypt", id, err)
		}
	}
}

// TestNewUpdatesNeedsASealer proves that the service never starts without a
// sealer, so no payload is stored as the CLI sent it.
func TestNewUpdatesNeedsASealer(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)

	cfg := updatesConfigFor(f, f.blobs, f.refusals)
	cfg.Sealer = nil

	if _, err := stack.NewUpdates(cfg); !errors.Is(err, stack.ErrMissingDependency) {
		t.Fatalf("NewUpdates with no sealer: err = %v, want ErrMissingDependency", err)
	}
}

// TestUpdateKeyRefFor sends keys and parts that are not valid to the
// reference that the stores build.
func TestUpdateKeyRefFor(t *testing.T) {
	t.Parallel()

	mk := stack.NewItemKeyFunc(func(
		context.Context, secret.ItemKeyTx, secret.ItemRef, namespace.Fence,
	) (secret.ItemKey, error) {
		return secret.ItemKey{
			CreatedAt: time.Time{}, ShreddedAt: time.Time{},
			Ref: secret.ItemRef{Scope: secret.Scope{Namespace: "", ID: ""}, Kind: "", Item: ""}, ID: "",
			WrapperID: "", Blob: secret.ItemKeyBlobInfo{MD5: [16]byte{}, Size: 0, Version: 0, Attempt: ""},
		}, nil
	})

	want, err := stack.UpdateItem("acme", "stk-1", "u1")
	if err != nil {
		t.Fatalf("item: %v", err)
	}

	if got, err := (stack.UpdateKey{NewKey: mk}).RefFor("acme", "stk-1", "u1"); err != nil || got != want {
		t.Fatalf("RefFor = %s, %v; want %s", got, err, want)
	}

	if want.Kind != secret.ItemKindState || want.Item != "u1" || want.Scope.ID != "stk-1" {
		t.Fatalf("reference = %s, want the state item u1 of stk-1", want)
	}

	for name, tc := range map[string]struct {
		key stack.UpdateKey
		ns  namespace.Name
		id  stack.ID
		upd stack.UpdateID
	}{
		"no key function": {stack.UpdateKey{NewKey: nil}, "acme", "stk-1", "u1"},
		"a bad namespace": {stack.UpdateKey{NewKey: mk}, "", "stk-1", "u1"},
		"a bad stack ID":  {stack.UpdateKey{NewKey: mk}, "acme", "stk 1", "u1"},
		"an empty stack":  {stack.UpdateKey{NewKey: mk}, "acme", "", "u1"},
		"a bad update ID": {stack.UpdateKey{NewKey: mk}, "acme", "stk-1", "u/1"},
		"an empty update": {stack.UpdateKey{NewKey: mk}, "acme", "stk-1", ""},
	} {
		if _, err := tc.key.RefFor(tc.ns, tc.id, tc.upd); !errors.Is(err, stack.ErrInvalidUpdateKey) {
			t.Errorf("RefFor with %s = %v, want ErrInvalidUpdateKey", name, err)
		}
	}
}
