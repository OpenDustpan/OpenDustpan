package jobtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// errKeyFunc is the error of the item key functions of the cases.
var errKeyFunc = errors.New("item key function failed")

// keyBlobSize is the size in the records that the cases make. The cases write
// no blob.
const keyBlobSize = 60

// emptySHA256 is the SHA-256 of no content.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// keyTxCases returns the cases of the item key record in the transaction of
// the item.
func keyTxCases() []recorderCase {
	return []recorderCase{
		{"keys: the record of a plan key commits with the reserved plan", testKeyCommitsWithPlan},
		{"keys: a failed reserve of a plan leaves no key record and no plan", testKeyRollsBackWithPlan},
		{"keys: the record of a log key commits with the log version", testKeyCommitsWithLog},
		{"keys: a failed reserve of chunk 0 leaves no key record and no log version", testKeyRollsBackWithLog},
		{"keys: a reserve with no key function or a reference of another item is ErrInvalid", testKeyRefusals},
	}
}

// recordingKey returns an item key function that stores a record of ref through
// the transaction of the store, and then returns after (nil for success). It
// writes no blob.
func recordingKey(after error) job.NewItemKeyFunc {
	return func(ctx context.Context, tx secret.ItemKeyTx, ref secret.ItemRef, _ namespace.Fence) (secret.ItemKey, error) {
		k, err := tx.CreateItemKey(ctx, secret.ItemKey{
			CreatedAt:  time.Now().UTC(),
			ShreddedAt: time.Time{},
			Ref:        ref,
			ID:         secret.NewItemKeyID(),
			WrapperID:  "kek-1",
			Blob: secret.ItemKeyBlobInfo{
				MD5:     [16]byte{1},
				Size:    keyBlobSize,
				Version: 1,
				Attempt: "abcdefghij0123456789",
			},
		})
		if err != nil {
			return secret.ItemKey{}, err
		}

		return k, after
	}
}

// newPlanFor returns a reserve of a new plan of the job of a.
func newPlanFor(t *testing.T, h RecorderHarness, a *job.Assignment, mk job.NewItemKeyFunc) *job.NewPlan {
	t.Helper()

	j := jobOf(t, h.Harness, a.Claim.Job)
	id := job.PlanIDPrefix + "keytx"
	now := time.Now().UTC()

	return &job.NewPlan{
		Plan: job.PlanRef{
			ID: id, ItemKey: "", StateVersion: "", Parts: nil, CreatedAt: now,
			ExpiresAt: history.ExpiresAt(now, h.Retention.Plan), RedactedAt: time.Time{},
		},
		Ref:    itemKeyOf(t, &j, secret.ItemKindPlan, id),
		NewKey: mk,
	}
}

// newLogFor returns the new log version that chunk 0 of the step creates.
func newLogFor(t *testing.T, h RecorderHarness, a *job.Assignment, mk job.NewItemKeyFunc) *job.NewLog {
	t.Helper()

	j := jobOf(t, h.Harness, a.Claim.Job)
	id := job.LogVersionIDPrefix + "keytx"
	now := time.Now().UTC()

	return &job.NewLog{
		Log: job.LogRef{
			ID: id, ItemKey: "", Version: 1, Chunks: 0, SHA256: "", Size: 0, CreatedAt: now,
			ClosedAt: time.Time{}, ExpiresAt: history.ExpiresAt(now, h.Retention.JobLog), RedactedAt: time.Time{},
			ReplacedBy: "", Matches: 0,
		},
		Ref:    itemKeyOf(t, &j, secret.ItemKindJobLog, id),
		NewKey: mk,
	}
}

func testKeyCommitsWithPlan(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	np := newPlanFor(t, h, a, recordingKey(nil))

	p, err := h.Records.ReservePlan(t.Context(), a.Claim, np)
	if err != nil {
		t.Fatalf("ReservePlan = %v", err)
	}

	k, err := h.ItemKeys.GetItemKey(t.Context(), np.Ref)
	if err != nil || k.ID != p.ItemKey || p.ItemKey == "" {
		t.Fatalf("record = %+v, %v; want the record that the plan names (%q)", k, err, p.ItemKey)
	}

	if _, readErr := h.Records.Plan(t.Context(), f.fence.Namespace, p.ID); !errors.Is(readErr, job.ErrNotFound) {
		t.Errorf("Plan of a reserve = %v, want ErrNotFound: it has no part", readErr)
	}
}

func testKeyRollsBackWithPlan(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	np := newPlanFor(t, h, a, recordingKey(errKeyFunc))

	if _, err := h.Records.ReservePlan(t.Context(), a.Claim, np); !errors.Is(err, errKeyFunc) {
		t.Fatalf("ReservePlan = %v, want the error of the key function", err)
	}

	if k, err := h.ItemKeys.GetItemKey(t.Context(), np.Ref); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("record after the failed reserve = %+v, %v; want ErrKeyNotFound", k, err)
	}

	if _, err := h.Records.Plan(t.Context(), f.fence.Namespace, np.Plan.ID); !errors.Is(err, job.ErrNotFound) {
		t.Errorf("Plan after the failed reserve = %v, want ErrNotFound", err)
	}

	// The failure leaves the phase as it was: a plan still stores.
	putPlan(t, h, a)
}

func testKeyCommitsWithLog(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openApply(t, h, f, "unit-0")
	mustBegin(t, h, a, stepPrepare)

	nl := newLogFor(t, h, a, recordingKey(nil))
	reserve := job.ChunkReserve{Seq: 0, SHA256: emptySHA256, Size: 0}

	plan, err := h.Records.ReserveLogChunk(t.Context(), a.Claim, stepPrepare, reserve, nl)
	if err != nil || plan.Version != nl.Log.ID {
		t.Fatalf("ReserveLogChunk = %+v, %v; want the new version", plan, err)
	}

	rec, err := h.Records.Log(t.Context(), f.fence.Namespace, nl.Log.ID)
	if err != nil {
		t.Fatalf("Log = %v", err)
	}

	k, err := h.ItemKeys.GetItemKey(t.Context(), nl.Ref)
	if err != nil || k.ID != rec.Log.ItemKey || k.ID == "" {
		t.Fatalf("record = %+v, %v; want the record that the log version names (%q)", k, err, rec.Log.ItemKey)
	}
}

func testKeyRollsBackWithLog(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openApply(t, h, f, "unit-0")
	mustBegin(t, h, a, stepPrepare)

	nl := newLogFor(t, h, a, recordingKey(errKeyFunc))
	reserve := job.ChunkReserve{Seq: 0, SHA256: emptySHA256, Size: 0}

	_, err := h.Records.ReserveLogChunk(t.Context(), a.Claim, stepPrepare, reserve, nl)
	if !errors.Is(err, errKeyFunc) {
		t.Fatalf("ReserveLogChunk = %v, want the error of the key function", err)
	}

	if k, err := h.ItemKeys.GetItemKey(t.Context(), nl.Ref); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("record after the failed reserve = %+v, %v; want ErrKeyNotFound", k, err)
	}

	if _, err := h.Records.Log(t.Context(), f.fence.Namespace, nl.Log.ID); !errors.Is(err, job.ErrNotFound) {
		t.Errorf("Log after the failed reserve = %v, want ErrNotFound", err)
	}

	// The step has no log version, so the recorder still creates one.
	mustAppend(t, h, a, 0, "after the failure\n")
}

// testKeyRefusals checks the layer of the store: whatever the caller of the
// store passes, the record of a key never lands for another item than the one
// that commits with it.
func testKeyRefusals(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	j := jobOf(t, h.Harness, a.Claim.Job)

	noKey := newPlanFor(t, h, a, nil)
	otherItem := newPlanFor(t, h, a, recordingKey(nil))
	otherItem.Ref = itemKeyOf(t, &j, secret.ItemKindPlan, job.PlanIDPrefix+"another")
	otherKind := newPlanFor(t, h, a, recordingKey(nil))
	otherKind.Ref = itemKeyOf(t, &j, secret.ItemKindJobLog, otherKind.Plan.ID)

	for name, np := range map[string]*job.NewPlan{
		"no key function": noKey, "a reference of another item": otherItem, "a reference of another kind": otherKind,
	} {
		if _, err := h.Records.ReservePlan(t.Context(), a.Claim, np); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("ReservePlan with %s = %v, want ErrInvalid", name, err)
		}

		if k, err := h.ItemKeys.GetItemKey(t.Context(), np.Ref); err == nil {
			t.Errorf("record after ReservePlan with %s = %+v, want none", name, k)
		}
	}
}
