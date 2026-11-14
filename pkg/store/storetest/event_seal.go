package storetest

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// EventRows reads and changes the stored rows of update_events with direct
// SQL, so a case can see the bytes at rest and damage them.
type EventRows interface {
	// EventPayloads returns the stored payload of every event row.
	EventPayloads(ctx context.Context) ([][]byte, error)
	// SwapEventPayloads swaps the stored payloads of the event a of update
	// au and the event b of update bu.
	SwapEventPayloads(ctx context.Context, au stack.UpdateID, a int64, bu stack.UpdateID, b int64) error
}

// EventSealStore is a store for the sealing cases of the update events: the
// update ports, the item key index, and direct SQL on the event rows.
type EventSealStore interface {
	UpdatesStore
	secret.ItemKeyIndex
	EventRows
}

// EventSealFactory returns a fresh, empty store for one case.
type EventSealFactory func(t *testing.T) EventSealStore

// sealInstallation is the installation of the namespace of the cases.
const sealInstallation = "local"

// eventCanary is a secret value that a program printed. It must never be in
// a stored row.
const eventCanary = "s3cr3t-event-canary-7f1c"

// RunEventSealing runs the cases of the sealing of update event payloads with
// the item key of each update, over a real secret.ItemKeySealer:
//
//	storetest.RunEventSealing(t, func(t *testing.T) storetest.EventSealStore { return newStore(t) })
func RunEventSealing(t *testing.T, newStore EventSealFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, r *sealRig)
		name string
	}{
		{name: "a stored event payload holds no plain text", test: testEventsStoredSealed},
		{name: "the item key record commits with the update", test: testUpdateKeyCommits},
		{name: "a preview gets its own item key", test: testPreviewKey},
		{name: "a failed key write stores no update", test: testUpdateKeyRollsBack},
		{name: "a key with no function stores no update", test: testUpdateKeyRefused},
		{name: "the key names the update and the stack that the store resolved", test: testUpdateKeyNamesTheStack},
		{name: "a payload moved to another sequence does not open", test: testEventSwapSequence},
		{name: "a payload moved to another update does not open", test: testEventSwapUpdate},
		{name: "a payload moved to an update of another stack does not open", test: testEventSwapStack},
		{name: "a shredded key refuses the write and the read of events", test: testEventsShredded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newSealRig(t, newStore(t)))
		})
	}
}

// sealRig is the store of one case, a stack in it, and the update service
// over a real item key sealer.
type sealRig struct {
	store   EventSealStore
	updates *stack.Updates
	ref     stack.Ref
	stackID stack.ID
	fence   namespace.Fence
}

// newSealRig creates the namespace "acme" and the stack "acme/web/prod" in
// store, and builds the update service.
func newSealRig(t *testing.T, store EventSealStore) *sealRig {
	t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: now(), Name: "acme", PrimaryInstallation: sealInstallation,
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := store.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	st := newStack(ns.Name, "web", "prod")
	if err := store.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	nsSvc, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: sealInstallation})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer: realItemSealer(t, store), BlobGuard: blobguardtest.None{}, ManualLocks: nil, Updates: store,
		Tokens: nil, Checkpoints: store, Events: store, Namespaces: nsSvc, Stacks: store,
		Blobs: openCaseBlobsIn(t, t.TempDir()), Refusals: stacktest.NewRefusals(), Now: nil, Logger: nil,
		MaxCheckpointBytes: 0, Journal: nil, Replayer: nil,
	})
	if err != nil {
		t.Fatalf("update service: %v", err)
	}

	return &sealRig{store: store, updates: updates, ref: st.Ref, stackID: st.ID, fence: ns.Fence()}
}

// realItemSealer returns a secret.ItemKeySealer over the item key records of
// store, wrapped key blobs in a fresh directory and a random key-encryption
// key.
func realItemSealer(t *testing.T, store EventSealStore) *secret.ItemKeySealer {
	t.Helper()

	dir, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open key blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := dir.Close(); closeErr != nil {
			t.Errorf("close key blob store: %v", closeErr)
		}
	})

	blobs, err := secret.NewItemKeyBlobs(dir, stacktest.NewRefusals(), itemKeyNoRefusals{})
	if err != nil {
		t.Fatalf("item key blobs: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	sealer, err := secret.NewItemKeySealer(secret.ItemSealerConfig{
		Index: store, Blobs: blobs, Wrapper: wrapper, Namespaces: store, Now: nil,
	})
	if err != nil {
		t.Fatalf("item key sealer: %v", err)
	}

	return sealer
}

// start creates and starts an update of the rig with plan.
func (r *sealRig) start(t *testing.T, plan stack.Plan) stack.Started {
	t.Helper()

	u, err := r.updates.Create(t.Context(), r.ref, plan)
	if err != nil {
		t.Fatalf("create update: %v", err)
	}

	started, err := r.updates.Start(t.Context(), r.ref, u.ID)
	if err != nil {
		t.Fatalf("start update: %v", err)
	}

	return started
}

// diagnostic returns the payload of a diagnostic event with message, as the
// Pulumi CLI sends it for a line that the program printed.
func diagnostic(seq int, message string) []byte {
	return []byte(`{"sequence":` + strconv.Itoa(seq) + `,"timestamp":1,"diagnosticEvent":{"message":"` +
		message + `\n","color":"raw","severity":"info"}}`)
}

// appendDiagnostics appends one diagnostic event for each message, with the
// sequences 0, 1, and so on.
func (r *sealRig) appendDiagnostics(t *testing.T, s stack.Started, messages ...string) []stack.Event {
	t.Helper()

	events := make([]stack.Event, 0, len(messages))
	for i, m := range messages {
		events = append(events, stack.Event{
			Timestamp: time.UnixMilli(int64(1000 + i)).UTC(), Payload: diagnostic(i, m), Sequence: int64(i),
		})
	}

	if err := r.updates.AppendEvents(t.Context(), r.ref, s.Update.ID, s.Token, events); err != nil {
		t.Fatalf("append events: %v", err)
	}

	return events
}

// itemOf returns the item key reference of the update id of the rig.
func (r *sealRig) itemOf(t *testing.T, id stack.UpdateID) secret.ItemRef {
	t.Helper()

	ref, err := stack.UpdateItem(r.ref.Namespace, r.stackID, id)
	if err != nil {
		t.Fatalf("item of update %s: %v", id, err)
	}

	return ref
}

func testEventsStoredSealed(t *testing.T, r *sealRig) {
	t.Helper()

	s := r.start(t, mutating())
	sent := r.appendDiagnostics(t, s, "stdout-literal "+eventCanary, "plain line")

	rows, err := r.store.EventPayloads(t.Context())
	if err != nil {
		t.Fatalf("read event rows: %v", err)
	}

	if len(rows) != len(sent) {
		t.Fatalf("event rows = %d, want %d", len(rows), len(sent))
	}

	for i, row := range rows {
		if bytes.Contains(row, []byte(eventCanary)) || bytes.Contains(row, []byte("plain line")) ||
			bytes.Contains(row, []byte("diagnosticEvent")) {
			t.Fatalf("event row %d holds plain text", i)
		}
	}

	got, err := r.updates.Events(t.Context(), r.ref, s.Update.ID, -1, 10)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}

	if len(got) != len(sent) {
		t.Fatalf("events = %d, want %d", len(got), len(sent))
	}

	for i := range sent {
		if !bytes.Equal(got[i].Payload, sent[i].Payload) || got[i].Sequence != sent[i].Sequence {
			t.Fatalf("event %d = %q, want %q", i, got[i].Payload, sent[i].Payload)
		}
	}
}

func testUpdateKeyCommits(t *testing.T, r *sealRig) {
	t.Helper()

	u, err := r.updates.Create(t.Context(), r.ref, mutating())
	if err != nil {
		t.Fatalf("create update: %v", err)
	}

	k, err := r.store.GetItemKey(t.Context(), r.itemOf(t, u.ID))
	if err != nil {
		t.Fatalf("item key of the update: %v", err)
	}

	if k.Ref.Kind != secret.ItemKindState || k.Ref.Item != u.ID.String() || k.Ref.Scope.ID != string(r.stackID) {
		t.Fatalf("item key names %s, want the update %s of stack %s", k.Ref, u.ID, r.stackID)
	}
}

func testPreviewKey(t *testing.T, r *sealRig) {
	t.Helper()

	s := r.start(t, preview())
	r.appendDiagnostics(t, s, "preview "+eventCanary)

	if _, err := r.store.GetItemKey(t.Context(), r.itemOf(t, s.Update.ID)); err != nil {
		t.Fatalf("item key of the preview: %v", err)
	}

	rows, err := r.store.EventPayloads(t.Context())
	if err != nil {
		t.Fatalf("read event rows: %v", err)
	}

	for i, row := range rows {
		if bytes.Contains(row, []byte(eventCanary)) {
			t.Fatalf("event row %d of the preview holds plain text", i)
		}
	}
}

var errKeyWrite = errors.New("key write failed")

func testUpdateKeyRollsBack(t *testing.T, r *sealRig) {
	t.Helper()

	u := newUpdate(r.ref, "rolled", mutating(), "", 0)
	key := stack.UpdateKey{NewKey: RecordingKey(errKeyWrite)}

	if err := r.store.CreateUpdate(t.Context(), u, key, r.fence); !errors.Is(err, errKeyWrite) {
		t.Fatalf("create update with a failed key: err = %v, want the key error", err)
	}

	if _, err := r.store.GetUpdate(t.Context(), r.ref, u.ID); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("update after a failed key: err = %v, want ErrUpdateNotFound", err)
	}

	if _, err := r.store.GetItemKey(t.Context(), r.itemOf(t, u.ID)); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Fatalf("key record after a failed create: err = %v, want ErrKeyNotFound", err)
	}
}

func testUpdateKeyRefused(t *testing.T, r *sealRig) {
	t.Helper()

	u := newUpdate(r.ref, "refused", mutating(), "", 0)

	err := r.store.CreateUpdate(t.Context(), u, stack.UpdateKey{NewKey: nil}, r.fence)
	if !errors.Is(err, stack.ErrInvalidUpdateKey) {
		t.Fatalf("create update with no key function: err = %v, want ErrInvalidUpdateKey", err)
	}

	if _, err := r.store.GetUpdate(t.Context(), r.ref, u.ID); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("update with no key function: err = %v, want ErrUpdateNotFound", err)
	}
}

// testUpdateKeyNamesTheStack checks that the store gives the key function the
// reference of the update and of the stack that it resolved.
func testUpdateKeyNamesTheStack(t *testing.T, r *sealRig) {
	t.Helper()

	var got secret.ItemRef

	capture := func(
		ctx context.Context, tx secret.ItemKeyTx, ref secret.ItemRef, fence namespace.Fence,
	) (secret.ItemKey, error) {
		got = ref

		return RecordingKey(nil)(ctx, tx, ref, fence)
	}

	u := newUpdate(r.ref, "named", mutating(), "", 0)
	if err := r.store.CreateUpdate(t.Context(), u, stack.UpdateKey{NewKey: capture}, r.fence); err != nil {
		t.Fatalf("create update: %v", err)
	}

	if want := r.itemOf(t, u.ID); got != want {
		t.Fatalf("key reference = %s, want %s", got, want)
	}

	if _, err := r.store.GetItemKey(t.Context(), got); err != nil {
		t.Fatalf("key record of the update: %v", err)
	}
}

func testEventSwapSequence(t *testing.T, r *sealRig) {
	t.Helper()

	s := r.start(t, mutating())
	r.appendDiagnostics(t, s, "first", "second")

	if err := r.store.SwapEventPayloads(t.Context(), s.Update.ID, 0, s.Update.ID, 1); err != nil {
		t.Fatalf("swap event rows: %v", err)
	}

	if _, err := r.updates.Events(t.Context(), r.ref, s.Update.ID, -1, 10); !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("read swapped events: err = %v, want ErrDecrypt", err)
	}
}

func testEventSwapUpdate(t *testing.T, r *sealRig) {
	t.Helper()

	first := r.start(t, preview())
	r.appendDiagnostics(t, first, "of the preview")

	second := r.start(t, preview())
	r.appendDiagnostics(t, second, "of the second preview")

	if err := r.store.SwapEventPayloads(t.Context(), first.Update.ID, 0, second.Update.ID, 0); err != nil {
		t.Fatalf("swap event rows: %v", err)
	}

	for _, id := range []stack.UpdateID{first.Update.ID, second.Update.ID} {
		if _, err := r.updates.Events(t.Context(), r.ref, id, -1, 10); !errors.Is(err, secret.ErrDecrypt) {
			t.Fatalf("read moved event of %s: err = %v, want ErrDecrypt", id, err)
		}
	}
}

// startOn creates and starts an update with plan on the stack ref of the rig.
func (r *sealRig) startOn(t *testing.T, ref stack.Ref, plan stack.Plan) stack.Started {
	t.Helper()

	u, err := r.updates.Create(t.Context(), ref, plan)
	if err != nil {
		t.Fatalf("create update of %s: %v", ref, err)
	}

	started, err := r.updates.Start(t.Context(), ref, u.ID)
	if err != nil {
		t.Fatalf("start update of %s: %v", ref, err)
	}

	return started
}

func testEventSwapStack(t *testing.T, r *sealRig) {
	t.Helper()

	other := newStack(r.ref.Namespace, "web", "staging")
	if err := r.store.CreateStack(t.Context(), other, r.fence); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	first := r.start(t, preview())
	r.appendDiagnostics(t, first, "of this stack")

	second := r.startOn(t, other.Ref, preview())

	events := []stack.Event{
		{Timestamp: time.UnixMilli(1000).UTC(), Payload: diagnostic(0, "of the other stack"), Sequence: 0},
	}
	if err := r.updates.AppendEvents(t.Context(), other.Ref, second.Update.ID, second.Token, events); err != nil {
		t.Fatalf("append events: %v", err)
	}

	if err := r.store.SwapEventPayloads(t.Context(), first.Update.ID, 0, second.Update.ID, 0); err != nil {
		t.Fatalf("swap event rows: %v", err)
	}

	if _, err := r.updates.Events(t.Context(), r.ref, first.Update.ID, -1, 10); !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("read a payload of another stack: err = %v, want ErrDecrypt", err)
	}

	_, err := r.updates.Events(t.Context(), other.Ref, second.Update.ID, -1, 10)
	if !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("read a payload of this stack on the other stack: err = %v, want ErrDecrypt", err)
	}
}

func testEventsShredded(t *testing.T, r *sealRig) {
	t.Helper()

	s := r.start(t, mutating())
	r.appendDiagnostics(t, s, "before the shred "+eventCanary)

	if _, err := r.store.ShredItemKey(t.Context(), r.itemOf(t, s.Update.ID), now(), r.fence); err != nil {
		t.Fatalf("shred: %v", err)
	}

	more := []stack.Event{
		{Timestamp: time.UnixMilli(2000).UTC(), Payload: diagnostic(1, "after the shred"), Sequence: 1},
	}

	err := r.updates.AppendEvents(t.Context(), r.ref, s.Update.ID, s.Token, more)
	if !errors.Is(err, stack.ErrItemKeyUnavailable) || !errors.Is(err, secret.ErrShredded) {
		t.Fatalf("append after a shred: err = %v, want ErrItemKeyUnavailable and ErrShredded", err)
	}

	got, err := r.updates.Events(t.Context(), r.ref, s.Update.ID, -1, 10)
	if !errors.Is(err, stack.ErrItemKeyUnavailable) || !errors.Is(err, secret.ErrShredded) || got != nil {
		t.Fatalf("read after a shred = %d events, %v; want none, ErrItemKeyUnavailable and ErrShredded", len(got), err)
	}

	rows, err := r.store.EventPayloads(t.Context())
	if err != nil {
		t.Fatalf("read event rows: %v", err)
	}

	if len(rows) != 1 || bytes.Contains(rows[0], []byte(eventCanary)) {
		t.Fatalf("event rows after a shred = %d, want the one sealed row of before the shred", len(rows))
	}
}
