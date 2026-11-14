package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// LateKeyStore is what the late item key cases need of a store: a namespace,
// a stack, the updates of the stack, and the read of an item key record.
type LateKeyStore interface {
	stack.UpdateStore

	CreateNamespace(ctx context.Context, ns namespace.Namespace) error
	CreateStack(ctx context.Context, st stack.Stack, fence namespace.Fence) error
	GetItemKey(ctx context.Context, ref secret.ItemRef) (secret.ItemKey, error)
}

// LateKeyRig is one store of the late item key cases.
type LateKeyRig struct {
	// Store is the store under test.
	Store LateKeyStore
	// Jobs runs the transactions of the job stores of Store.
	Jobs jobstore.Runner
	// SetAttemptDeadline makes the attempt deadline of each transaction that
	// starts after the call d after its start.
	SetAttemptDeadline func(d time.Duration)
}

// LateKeyFactory returns a fresh rig for one case.
type LateKeyFactory func(t *testing.T) LateKeyRig

// lateDeadline is the short attempt deadline of the cases, and lateWait is
// longer than it.
const (
	lateDeadline = 200 * time.Millisecond
	lateWait     = 600 * time.Millisecond
)

// RunLateItemKeyCommits checks that every write path that can write an item
// key record ends at the attempt deadline: the update path (CreateUpdate) and
// the job path (the job runner). A record write after the deadline, and a
// commit after the deadline of a record written in time, both fail with
// secret.ErrItemKeyAttemptExpired and leave no record, so the reaper never
// deletes the blob of a record that commits. The context of each call has no
// deadline: the store must give the transaction its own.
func RunLateItemKeyCommits(t *testing.T, newRig LateKeyFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, r LateKeyRig)
		name string
	}{
		{name: "an update that writes its item key record in time commits", test: lateUpdateInTime},
		{name: "an update that writes its item key record after the deadline fails", test: lateUpdateWrite},
		{name: "an update whose commit comes after the deadline fails", test: lateUpdateCommit},
		{name: "a job write that writes an item key record after the deadline fails", test: lateJobWrite},
		{name: "a job write whose commit comes after the deadline fails", test: lateJobCommit},
		{name: "a job read transaction writes no item key record", test: lateJobRead},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newRig(t))
		})
	}
}

// lateFixture is the namespace and the stack of one case.
type lateFixture struct {
	fence namespace.Fence
	ref   stack.Ref
}

func newLateFixture(t *testing.T, r LateKeyRig) lateFixture {
	t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: now(), Name: "acme", PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := r.Store.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	st := newStack(ns.Name, "web", "prod")
	if err := r.Store.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	return lateFixture{fence: ns.Fence(), ref: st.Ref}
}

// lateRecord returns a record of ref, as RecordingKey makes it.
func lateRecord(ref secret.ItemRef) secret.ItemKey {
	return secret.ItemKey{
		CreatedAt: time.Now().UTC(), ShreddedAt: time.Time{}, Ref: ref, ID: secret.NewItemKeyID(),
		WrapperID: "kek-1",
		Blob: secret.ItemKeyBlobInfo{
			MD5: [16]byte{1}, Size: recordingKeyBlobSize, Version: 1, Attempt: recordingKeyAttempt,
		},
	}
}

// lateRef returns the reference of an item key of a job plan in the namespace
// of fence.
func lateRef(t *testing.T, fence namespace.Fence, item string) secret.ItemRef {
	t.Helper()

	scope, err := secret.NewScope(fence.Namespace, "job-1")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	ref, err := secret.NewItemRef(scope, secret.ItemKindPlan, item)
	if err != nil {
		t.Fatalf("item ref: %v", err)
	}

	return ref
}

// sleepFor waits for d.
func sleepFor(d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	<-timer.C
}

// keyFunc returns an item key function that waits for before, writes the
// record of its reference through the transaction, waits for after and
// returns. It keeps the reference in got.
func keyFunc(before, after time.Duration, got *secret.ItemRef) stack.NewItemKeyFunc {
	return func(ctx context.Context, tx secret.ItemKeyTx, ref secret.ItemRef, _ namespace.Fence) (secret.ItemKey, error) {
		*got = ref

		sleepFor(before)

		k, err := tx.CreateItemKey(ctx, lateRecord(ref))
		if err != nil {
			return secret.ItemKey{}, err
		}

		sleepFor(after)

		return k, nil
	}
}

// createLateUpdate runs CreateUpdate of one update with the item key function
// fn, and returns the error and the reference that fn got.
func createLateUpdate(t *testing.T, r LateKeyRig, before, after time.Duration) (secret.ItemRef, lateFixture, error) {
	t.Helper()

	f := newLateFixture(t, r)
	r.SetAttemptDeadline(lateDeadline)

	var ref secret.ItemRef

	u := newUpdate(f.ref, "u-late", stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0},
		"", 0)
	err := r.Store.CreateUpdate(t.Context(), u, stack.UpdateKey{NewKey: keyFunc(before, after, &ref)}, f.fence)

	return ref, f, err
}

// wantNoLateRecord fails the case when err is not ErrItemKeyAttemptExpired or
// when the record of ref exists.
func wantNoLateRecord(t *testing.T, r LateKeyRig, err error, ref secret.ItemRef) {
	t.Helper()

	if !errors.Is(err, secret.ErrItemKeyAttemptExpired) {
		t.Fatalf("late write err = %v, want ErrItemKeyAttemptExpired", err)
	}

	if _, getErr := r.Store.GetItemKey(t.Context(), ref); !errors.Is(getErr, secret.ErrKeyNotFound) {
		t.Errorf("a late write left a record: err = %v", getErr)
	}
}

func lateUpdateInTime(t *testing.T, r LateKeyRig) {
	t.Helper()

	ref, f, err := createLateUpdate(t, r, 0, 0)
	if err != nil {
		t.Fatalf("create update in time: %v", err)
	}

	if _, getErr := r.Store.GetItemKey(t.Context(), ref); getErr != nil {
		t.Errorf("record of the update: %v", getErr)
	}

	if _, getErr := r.Store.GetUpdate(t.Context(), f.ref, "u-late"); getErr != nil {
		t.Errorf("update: %v", getErr)
	}
}

func lateUpdateWrite(t *testing.T, r LateKeyRig) {
	t.Helper()

	ref, f, err := createLateUpdate(t, r, lateWait, 0)
	wantNoLateRecord(t, r, err, ref)

	if _, getErr := r.Store.GetUpdate(t.Context(), f.ref, "u-late"); getErr == nil {
		t.Error("a late update left its row")
	}
}

func lateUpdateCommit(t *testing.T, r LateKeyRig) {
	t.Helper()

	ref, f, err := createLateUpdate(t, r, 0, lateWait)
	wantNoLateRecord(t, r, err, ref)

	if _, getErr := r.Store.GetUpdate(t.Context(), f.ref, "u-late"); getErr == nil {
		t.Error("a late update left its row")
	}
}

// lateJob runs one job write that waits for before, writes a record of a job
// plan, and waits for after.
func lateJob(t *testing.T, r LateKeyRig, before, after time.Duration) (secret.ItemRef, error) {
	t.Helper()

	f := newLateFixture(t, r)
	ref := lateRef(t, f.fence, "pln-late")
	r.SetAttemptDeadline(lateDeadline)

	err := r.Jobs.Write(t.Context(), f.fence, func(tx jobstore.Tx) error {
		sleepFor(before)

		if _, err := tx.ItemKeys(f.fence).CreateItemKey(t.Context(), lateRecord(ref)); err != nil {
			return err
		}

		sleepFor(after)

		return nil
	})

	return ref, err
}

func lateJobWrite(t *testing.T, r LateKeyRig) {
	t.Helper()

	ref, err := lateJob(t, r, lateWait, 0)
	wantNoLateRecord(t, r, err, ref)
}

func lateJobCommit(t *testing.T, r LateKeyRig) {
	t.Helper()

	ref, err := lateJob(t, r, 0, lateWait)
	wantNoLateRecord(t, r, err, ref)
}

func lateJobRead(t *testing.T, r LateKeyRig) {
	t.Helper()

	f := newLateFixture(t, r)
	ref := lateRef(t, f.fence, "pln-read")

	err := r.Jobs.Read(t.Context(), func(tx jobstore.Tx) error {
		_, err := tx.ItemKeys(f.fence).CreateItemKey(t.Context(), lateRecord(ref))

		return err
	})
	wantNoLateRecord(t, r, err, ref)
}
