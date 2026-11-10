package storetest

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // Test digests of stored bytes, for integrity, not security.
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// UpdatesStore is every port of the update cycle, plus the namespace and
// stack ports its fixtures need.
type UpdatesStore interface {
	Store
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
	stack.JournalStore
	stack.BuiltCheckpointStore
}

// KeyEraser is an optional UpdatesStore capability for the keyless row case:
// it tries to set the blob key of every checkpoint row to NULL with a direct
// SQL update. The port refuses such a write, and the schema refuses the SQL
// update too.
type KeyEraser interface {
	// EraseCheckpointKeys sets the blob key of every checkpoint row to NULL.
	EraseCheckpointKeys(ctx context.Context) error
}

// UpdatesFactory returns a fresh, empty store for one subtest.
type UpdatesFactory func(t *testing.T) UpdatesStore

// RunUpdates executes the conformance suite of the update-cycle ports
// (stack.UpdateStore, stack.CheckpointStore, stack.EventStore) against stores
// produced by newStore.
func RunUpdates(t *testing.T, newStore UpdatesFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, f fixture)
		name string
	}{
		{name: "update round trip", test: testUpdateRoundTrip},
		{name: "update keeps the access token that started it", test: testUpdateTokenIDRoundTrip},
		{name: "a delete appends its change entry in the same transaction", test: testDeleteAppendsChangeEntry},
		{name: "a delete with a refused audit entry changes nothing", test: testDeleteRefusedEntryChangesNothing},
		{name: "a delete whose audit append fails changes nothing", test: testDeleteFailedAppendChangesNothing},
		{name: "the stored name of a deleted stack finds nothing", test: testTombstoneNameFindsNothing},
		{name: "a restore brings a deleted stack back under its name", test: testRestoreStackUnderItsName},
		{name: "a restore under a taken name fails and under a new name works", test: testRestoreStackUnderANewName},
		{name: "a restore or purge of an unknown or live stack is not found", test: testRestoreOrPurgeUnknownStack},
		{name: "a purge removes a deleted stack for good", test: testPurgeStack},
		{name: "a sweep purge checks the retention in its transaction", test: testSweepPurgeChecksTheRetention},
		{name: "a purge ends the grants on the stack", test: testPurgeStackRevokesItsGrants},
		{name: "a purge removes the context of the stack", test: testPurgeStackDropsItsContext},
		{name: "an audit entry on a stack gets its stable ID", test: testAppendFillsStackID},
		{
			name: "a restore or a purge with the fence of another namespace changes nothing",
			test: testRestoreOrPurgeStackInAnotherNamespace,
		},
		{name: "a restore and a purge at the same time: one wins", test: testRestoreRacesPurge},
		{
			name: "a restore or purge whose audit append fails changes nothing",
			test: testRestoreFailedAppendChangesNothing,
		},
		{name: "a journal batch replay under another key keeps the first key", test: testJournalReplayOtherKey},
		{name: "journal batches give the key, the SHA-256 and the size", test: testJournalBatches},
		{name: "concurrent writes of one journal batch: one winner", test: testJournalConcurrentWinner},
		{name: "journal version round trip", test: testJournalVersionRoundTrip},
		{name: "journal version of a new update is zero", test: testJournalVersionDefaultsToZero},
		{name: "count of running journaled updates", test: testCountRunningJournaledUpdates},
		{name: "missing update is ErrUpdateNotFound", test: testMissingUpdate},
		{name: "update of missing stack is ErrNotFound", test: testUpdateMissingStack},
		{name: "stale fence is ErrFenced on every write", test: testUpdateWritesFenced},
		{name: "start assigns versions and ending moves the stack", test: testStartVersions},
		{name: "one mutating update runs per stack", test: testOneRunning},
		{name: "concurrent starts admit exactly one", test: testConcurrentStarts},
		{name: "stale revision is ErrConcurrentModification", test: testStaleRevision},
		{name: "concurrent transitions admit exactly one", test: testConcurrentSaves},
		{name: "active update is the running mutating one", test: testActiveUpdate},
		{name: "history is started mutating updates, newest first", test: testListUpdates},
		{name: "checkpoint round trip keeps bytes", test: testCheckpointRoundTrip},
		{name: "checkpoint blob metadata round trip", test: testCheckpointBlobMetadata},
		{name: "checkpoint reads carry the namespace and stack IDs", test: testCheckpointReadsCarryIDs},
		{name: "checkpoint key must belong to the update", test: testCheckpointKeyBelongsToUpdate},
		{name: "checkpoint by update returns only that update", test: testCheckpointByUpdate},
		{name: "history reports the stored size for blob rows", test: testHistoryBlobSize},
		{name: "referenced blob keys list every checkpoint key of the stack", test: testReferencedBlobKeys},
		{name: "referenced blob keys list the journal keys of the stack", test: testReferencedBlobKeysJournal},
		{name: "referenced blob keys follow the stack ID and refuse a missing stack", test: testReferencedBlobKeysByID},
		{name: "checkpoint needs the running update's lease", test: testCheckpointLease},
		{name: "checkpoint sequence numbers are idempotent", test: testCheckpointSequence},
		{name: "a checkpoint replay under another key keeps the first key", test: testCheckpointReplayOtherKey},
		{name: "a checkpoint conflict under another key keeps the first key", test: testCheckpointConflictOtherKey},
		{name: "a checkpoint of an attempt outside its time window is refused", test: testCheckpointAttemptWindow},
		{name: "a checkpoint replay after the attempt deadline is refused", test: testCheckpointReplayAfterDeadline},
		{name: "a built checkpoint after the attempt deadline is refused", test: testBuiltCheckpointAttemptWindow},
		{name: "a journal row of an attempt outside its time window is refused", test: testJournalAttemptWindow},
		{
			name: "a checkpoint that a lock wait holds past its deadline is refused",
			test: testCheckpointLockWaitPastDeadline,
		},
		{name: "the store reads the clock of the database for the sweeps", test: testDatabaseClock},
		{name: "an unsequenced checkpoint replaces the row and its key", test: testCheckpointUnsequencedKeys},
		{name: "concurrent checkpoints of one sequence: one winner", test: testCheckpointConcurrentWinner},
		{name: "latest checkpoint spans updates", test: testCheckpointAcrossUpdates},
		{name: "latest checkpoint is the highest version", test: testCheckpointLatestIsHighest},
		{name: "checkpoint by version returns only that version", test: testCheckpointByVersion},
		{name: "checkpoint of a preview is rejected", test: testCheckpointPreviewRejected},
		{name: "checkpoint without a blob key is refused", test: testCheckpointWithoutKeyRefused},
		{name: "the schema refuses a checkpoint row without a blob key", test: testKeylessRowRefused},
		{name: "canceled or failed update keeps its checkpoint", test: testCheckpointKeepsAfterEnd},
		{name: "children of a deleted stack are not reachable by name", test: testDeletedStackChildrenUnreachable},
		{name: "delete during a running update is ErrUpdateConflict", test: testDeleteDuringRunningUpdate},
		{name: "rename during a running update is ErrUpdateConflict", test: testRenameDuringRunningUpdate},
		{name: "rename races a new update and exactly one wins", test: testRenameRaceWithNewUpdate},
		{name: "delete without force refuses a stack with resources", test: testDeleteRefusesResources},
		{name: "delete with force removes a stack with resources", test: testDeleteWithForceRemovesResources},
		{name: "delete without force follows the latest checkpoint", test: testDeleteFollowsLatestCheckpoint},
		{name: "delete with a blob checkpoint uses the domain count", test: testDeleteWithBlobCheckpoint},
		{name: "delete races a finishing update without dropping resources", test: testDeleteRaceWithFinishingUpdate},
		{name: "delete races a new update without dropping resources", test: testDeleteRaceWithNewUpdate},
		{name: "events append, dedupe and page", test: testEvents},
		{name: "events need the running update's lease", test: testEventsLease},
		{name: "journal rows get arrivals and retries write nothing", test: testJournalPut},
		{name: "journal refuses leases, previews, stacks, fences and foreign keys", test: testJournalRefusals},
		{name: "journal refuses a batch for an update with version 0", test: testJournalRefusesVersionZero},
		{name: "journal refuses batches after a cancel", test: testJournalAfterCancel},
		{name: "journal end stops on an unstored refusal", test: testJournalEndStopsOnUnstoredRefusal},
		{name: "journal end keeps a refusal whose sequences are stored", test: testJournalEndKeepsStoredRefusal},
		{name: "journal stores two batches of one update that arrive at once", test: testJournalConcurrentRows},
		{name: "journal stores one copy of a batch sent twice at once", test: testJournalConcurrentRetry},
		{name: "journal check sees the stored operations and refusals store nothing", test: testJournalVerify},
		{name: "journal refuses a row with no entry check", test: testJournalRefusesNilVerify},
		{name: "journal refuses a row with bad sequence IDs", test: testJournalRefusesBadSequenceIDs},
		{name: "journal refuses a write after a stored change", test: testJournalRefusesWriteAfterStoredChange},
		{name: "journal refuses a reference to a begun-only operation", test: testJournalBegunRefusal},
		{
			name: "journal refuses a reference before a stored rebuild",
			test: testJournalRefusesReferenceAfterStoredRebuild,
		},
		{name: "build removes the journal and returns its blob keys", test: testJournalBuild},
		{name: "build races a batch without losing its key", test: testJournalBuildRace},
		{name: "build leaves other updates and stacks journaled", test: testJournalBuildIsolation},
		{name: "build refuses a preview and a stale sequence and keeps the journal", test: testJournalBuildRefusals},
		{name: "journal is sweepable only when closed or no longer running", test: testJournalSweepable},
		{name: "journal keys follow a stack rename", test: testJournalKeysFollowRename},
		{name: "journal refusals read back in arrival order", test: testJournalRefusalsRoundTrip},
		{name: "journal refusals of a missing update record nothing", test: testJournalRefusalsMissingUpdate},
		{name: "journal refusals of an ended update record nothing", test: testJournalRefusalsOnlyRunning},
		{name: "journal refusal rows are gone after cancel or failure", test: testJournalEndDropsRefusals},
		{name: "journal refusal with a stale fence is fenced", test: testJournalRefusalStaleFence},
		{name: "journal sequences read back in order", test: testJournalSequences},
		{name: "build removes the refusals with the journal", test: testJournalBuildRemovesRefusals},
		{name: "rename keeps history, checkpoint and events", test: testRenameKeepsHistory},
		{name: "inventory follows an update finish", test: testInventoryAfterUpdateFinish},
		{name: "inventory holds the lock of a running update", test: testInventoryHoldsRunningLock},
		{name: "the change log follows the writes of a stack", test: testStackChangeLog},
		{name: "the change log follows the end of an update", test: testStackChangeLogUpdates},
		{name: "a refused write of a stack appends no change log row", test: testStackChangeLogFence},
		{name: "inventory shows no lock for a preview or a dry run", test: testInventoryIgnoresPreviewLock},
		{name: "inventory keeps the lock after the lease expires", test: testInventoryKeepsLockAfterLeaseExpiry},
		{name: "a stale fence on start and end writes no lock", test: testUpdateInventoryLockFenced},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newFixture(t, newStore(t)))
		})
	}
}

// HistoryStore is the port surface the history cases need: namespaces and
// stacks for their fixtures, and the update cycle for the history itself.
type HistoryStore interface {
	namespace.Store
	stack.UpdateStore
	stack.CheckpointStore
	CreateStack(ctx context.Context, st stack.Stack, fence namespace.Fence) error
	GetStack(ctx context.Context, ref stack.Ref) (stack.Stack, error)
}

// HistoryFactory returns a fresh, empty store for one history subtest.
type HistoryFactory func(t *testing.T) HistoryStore

// RunUpdateHistory runs the history cases of ADR 28 decision 4: the creator
// and the restore source of each update, the checkpoint size in history, and
// the import and restore kinds. The SQL adapters grow the matching columns in
// issues #135 and #136, which move these cases into RunUpdates. Until then
// the stack package runs them on its in-memory store.
func RunUpdateHistory(t *testing.T, newStore HistoryFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, f historyFixture)
		name string
	}{
		{name: "creator and restore source survive create, start, save and list", test: testHistoryCreator},
		{name: "history reports the checkpoint size, zero without a checkpoint", test: testHistoryCheckpointSize},
		{name: "a later checkpoint write changes the history size", test: testHistorySizeFollowsRewrite},
		{name: "import and restore updates appear in history with their kinds", test: testHistoryKinds},
		{name: "a fenced create writes nothing", test: testHistoryCreateFenced},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newHistoryFixture(t, newStore(t)))
		})
	}
}

// fixture is a store holding namespace "acme" and stack "acme/web/prod".
type fixture struct {
	s     UpdatesStore
	ref   stack.Ref
	fence namespace.Fence
}

func newFixture(t *testing.T, s UpdatesStore) fixture {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	return fixture{s: s, ref: st.Ref, fence: ns.Fence()}
}

// other creates stack "acme/web/dev".
func (f fixture) other(t *testing.T) stack.Ref {
	t.Helper()

	st := newStack(f.ref.Namespace, f.ref.Project.String(), "dev")
	if err := f.s.CreateStack(t.Context(), st, f.fence); err != nil {
		t.Fatalf("create stack %s: %v", st.Ref, err)
	}

	return st.Ref
}

// create persists a new, not-started update of ref.
func (f fixture) create(t *testing.T, ref stack.Ref, id string, plan stack.Plan) stack.Update {
	t.Helper()

	u := newUpdate(ref, id, plan, plan.CreatedBy, plan.RestoredFrom)

	if err := f.s.CreateUpdate(t.Context(), u, UnrecordedKey(), f.fence); err != nil {
		t.Fatalf("create update %s: %v", id, err)
	}

	return u
}

// newUpdate returns a new, not-started update of ref with the given creator
// and restore source, not yet persisted.
func newUpdate(ref stack.Ref, id string, plan stack.Plan, createdBy string, restoredFrom int64) stack.Update {
	return stack.Update{
		TokenID:         "",
		JournalVersion:  0,
		CreatedAt:       now(),
		StartedAt:       time.Time{},
		EndedAt:         time.Time{},
		Lease:           stack.Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}},
		ID:              stack.UpdateID(id),
		Kind:            plan.Kind,
		Status:          stack.StatusNotStarted,
		Ref:             ref,
		DryRun:          plan.DryRun,
		Version:         0,
		Revision:        1,
		CreatedBy:       createdBy,
		RestoredFrom:    restoredFrom,
		CheckpointBytes: 0,
		JobID:           "",
		GitHead:         "",
	}
}

// started returns u moved to running under a fresh lease, not yet persisted.
func started(t *testing.T, u stack.Update) stack.Update {
	t.Helper()

	tok, err := token.New()
	if err != nil {
		t.Fatalf("new lease token: %v", err)
	}

	u.Status = stack.StatusRunning
	u.StartedAt = now().Add(time.Second)
	u.Lease = stack.Lease{ExpiresAt: now().Add(stack.DefaultLease), Digest: tok.Digest()}
	u.Revision++

	return u
}

// start persists u as started and returns it with its version.
func (f fixture) start(t *testing.T, u stack.Update) stack.Update {
	t.Helper()

	got, err := f.s.StartUpdate(t.Context(), started(t, u), f.fence)
	if err != nil {
		t.Fatalf("start update %s: %v", u.ID, err)
	}

	return got
}

// run creates and starts an update of ref.
func (f fixture) run(t *testing.T, ref stack.Ref, id string, plan stack.Plan) stack.Update {
	t.Helper()

	return f.start(t, f.create(t, ref, id, plan))
}

// ended returns u ended with status, not yet persisted.
func ended(u stack.Update, status stack.UpdateStatus) stack.Update {
	u.Status = status
	u.EndedAt = now().Add(time.Minute)
	u.Lease = stack.Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}}
	u.Revision++

	return u
}

// end persists u as ended with status.
func (f fixture) end(t *testing.T, u stack.Update, status stack.UpdateStatus) stack.Update {
	t.Helper()

	u = ended(u, status)
	if err := f.s.SaveUpdate(t.Context(), u, f.fence); err != nil {
		t.Fatalf("end update %s: %v", u.ID, err)
	}

	return u
}

func (f fixture) stackVersion(t *testing.T, ref stack.Ref) int64 {
	t.Helper()

	st, err := f.s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack %s: %v", ref, err)
	}

	return st.Version
}

func mutating() stack.Plan {
	return stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}
}

func preview() stack.Plan {
	return stack.Plan{Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}
}

// dryRun is the preview phase of `pulumi up`: the real kind, not mutating.
func dryRun() stack.Plan {
	return stack.Plan{Kind: stack.KindUpdate, DryRun: true, CreatedBy: "", RestoredFrom: 0, GitHead: ""}
}

// historyFixture is a store holding namespace "acme" and stack
// "acme/web/prod", for the staged history cases of RunUpdateHistory.
type historyFixture struct {
	s     HistoryStore
	ref   stack.Ref
	fence namespace.Fence
}

func newHistoryFixture(t *testing.T, s HistoryStore) historyFixture {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	return historyFixture{s: s, ref: st.Ref, fence: ns.Fence()}
}

// create persists a new, not-started update with the given creator and
// restore source.
func (f historyFixture) create(
	t *testing.T, id string, plan stack.Plan, createdBy string, restoredFrom int64,
) stack.Update {
	t.Helper()

	u := newUpdate(f.ref, id, plan, createdBy, restoredFrom)

	if err := f.s.CreateUpdate(t.Context(), u, UnrecordedKey(), f.fence); err != nil {
		t.Fatalf("create update %s: %v", id, err)
	}

	return u
}

// start persists u as started and returns it with its version.
func (f historyFixture) start(t *testing.T, u stack.Update) stack.Update {
	t.Helper()

	got, err := f.s.StartUpdate(t.Context(), started(t, u), f.fence)
	if err != nil {
		t.Fatalf("start update %s: %v", u.ID, err)
	}

	return got
}

// run creates and starts an update with the given creator and restore source.
func (f historyFixture) run(
	t *testing.T, id string, plan stack.Plan, createdBy string, restoredFrom int64,
) stack.Update {
	t.Helper()

	return f.start(t, f.create(t, id, plan, createdBy, restoredFrom))
}

// end persists u as ended with status.
func (f historyFixture) end(t *testing.T, u stack.Update, status stack.UpdateStatus) stack.Update {
	t.Helper()

	u = ended(u, status)
	if err := f.s.SaveUpdate(t.Context(), u, f.fence); err != nil {
		t.Fatalf("end update %s: %v", u.ID, err)
	}

	return u
}

// putCheckpoint saves body as the checkpoint of u.
func (f historyFixture) putCheckpoint(t *testing.T, u stack.Update, body string) {
	t.Helper()

	cp := newCheckpoint(u, 0, body)
	cp.BlobKey = checkpointKey(t, f.s, u, 0, defaultAttempt)

	if _, err := f.s.PutCheckpoint(t.Context(), cp, f.fence); err != nil {
		t.Fatalf("put checkpoint of %s: %v", u.ID, err)
	}
}

// history returns the stack history, newest first.
func (f historyFixture) history(t *testing.T) []stack.Update {
	t.Helper()

	list, err := f.s.ListUpdates(t.Context(), f.ref, stack.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list updates: %v", err)
	}

	return list
}

// assertUpdate checks that the store holds want.
func (f historyFixture) assertUpdate(t *testing.T, want stack.Update) {
	t.Helper()

	got, err := f.s.GetUpdate(t.Context(), want.Ref, want.ID)
	if err != nil {
		t.Fatalf("get update %s: %v", want.ID, err)
	}

	if !sameUpdate(got, want) {
		t.Fatalf("update %s = %+v, want %+v", want.ID, got, want)
	}
}

// historySize returns the CheckpointBytes history reports for id.
func (f historyFixture) historySize(t *testing.T, id stack.UpdateID) int64 {
	t.Helper()

	history := f.history(t)
	for i := range history {
		if history[i].ID == id {
			return history[i].CheckpointBytes
		}
	}

	t.Fatalf("update %s is missing from history", id)

	return 0
}

// testJournalEndStopsOnUnstoredRefusal shows that the end transition of a
// journaled update that succeeds stops on a refusal of a batch that is still
// unstored, and that the update keeps running.
func testJournalEndStopsOnUnstoredRefusal(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, u, 1, 1), f.fence); err != nil {
		t.Fatalf("store batch: %v", err)
	}

	recordRefusal(t, f, journalRefusal(u, []int64{3}, 400, "unknown kind"))

	err := f.s.SaveUpdate(t.Context(), ended(u, stack.StatusSucceeded), f.fence)
	if !errors.Is(err, stack.ErrJournalIncomplete) {
		t.Fatalf("end with an unstored refusal: err %v, want ErrJournalIncomplete", err)
	}

	got, err := f.s.GetUpdate(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("get update: %v", err)
	}

	if got.Status != stack.StatusRunning {
		t.Fatalf("status after the refused end = %q, want %q", got.Status, stack.StatusRunning)
	}

	// A stack runs one update at a time, so the update ends here.
	f.end(t, u, stack.StatusCanceled)
}

// testJournalEndKeepsStoredRefusal shows that a refusal whose sequence IDs are
// all stored, because a retry stored the batch, does not stop the end.
func testJournalEndKeepsStoredRefusal(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u2")

	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, u, 1, 1), f.fence); err != nil {
		t.Fatalf("store batch: %v", err)
	}

	recordRefusal(t, f, journalRefusal(u, []int64{1}, 400, "unknown kind"))

	if err := f.s.SaveUpdate(t.Context(), ended(u, stack.StatusSucceeded), f.fence); err != nil {
		t.Fatalf("end with stored sequences: %v", err)
	}

	got, err := f.s.GetUpdate(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("get update: %v", err)
	}

	if got.Status != stack.StatusSucceeded {
		t.Fatalf("status after the end = %q, want %q", got.Status, stack.StatusSucceeded)
	}
}

func testUpdateRoundTrip(t *testing.T, f fixture) {
	t.Helper()

	want := f.create(
		t,
		f.ref,
		"u1",
		stack.Plan{Kind: stack.KindRefresh, DryRun: true, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	assertUpdate(t, f, want)

	want = f.start(t, want)
	assertUpdate(t, f, want)

	want = f.end(t, want, stack.StatusSucceeded)
	assertUpdate(t, f, want)
}

// testUpdateTokenIDRoundTrip checks that the access token ID that a start
// stores comes back from every read, that a save keeps it, and that an update
// started with no token keeps an empty ID (ADR 53).
func testUpdateTokenIDRoundTrip(t *testing.T, f fixture) {
	t.Helper()

	u := started(t, f.create(t, f.ref, "u1", mutating()))
	u.TokenID = "tok-1"

	got, err := f.s.StartUpdate(t.Context(), u, f.fence)
	if err != nil {
		t.Fatalf("start update with a token: %v", err)
	}

	if got.TokenID != "tok-1" {
		t.Fatalf("start answers token %q, want %q", got.TokenID, "tok-1")
	}

	assertUpdate(t, f, got)

	done := f.end(t, got, stack.StatusSucceeded)
	assertUpdate(t, f, done)

	bare := f.run(t, f.ref, "u2", preview())
	assertUpdate(t, f, bare)

	if bare.TokenID != "" {
		t.Fatalf("update without a token answers token %q, want none", bare.TokenID)
	}
}

// testJournalVersionRoundTrip checks that the journal version that a start
// stores comes back from every read, and that a later save keeps it.
func testJournalVersionRoundTrip(t *testing.T, f fixture) {
	t.Helper()

	u := started(t, f.create(t, f.ref, "u1", mutating()))
	u.JournalVersion = 2

	got, err := f.s.StartUpdate(t.Context(), u, f.fence)
	if err != nil {
		t.Fatalf("start update with journal version 2: %v", err)
	}

	if got.JournalVersion != 2 {
		t.Fatalf("start returned journal version %d, want 2", got.JournalVersion)
	}

	assertUpdate(t, f, got)

	active, err := f.s.ActiveUpdate(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("active update: %v", err)
	}

	if active.JournalVersion != 2 {
		t.Fatalf("active update journal version = %d, want 2", active.JournalVersion)
	}

	list, err := f.s.ListUpdates(t.Context(), f.ref, stack.Page{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list updates: %v", err)
	}

	if len(list) != 1 || list[0].JournalVersion != 2 {
		t.Fatalf("list updates = %+v, want one update with journal version 2", list)
	}

	got = f.end(t, got, stack.StatusSucceeded)
	assertUpdate(t, f, got)
}

// testJournalVersionDefaultsToZero checks that an update that never started
// reads back as journal version 0, which means checkpoints.
func testJournalVersionDefaultsToZero(t *testing.T, f fixture) {
	t.Helper()

	want := f.create(t, f.ref, "u1", mutating())
	assertUpdate(t, f, want)

	if want.JournalVersion != 0 {
		t.Fatalf("new update journal version = %d, want 0", want.JournalVersion)
	}

	got := f.start(t, want)
	if got.JournalVersion != 0 {
		t.Fatalf("started update journal version = %d, want 0", got.JournalVersion)
	}
}

// testCountRunningJournaledUpdates checks the count of running updates with
// journal version above 0: an empty store counts 0, each running journaled
// update counts once, a running update without journaling changes nothing,
// and an ended journaled update leaves the count.
func testCountRunningJournaledUpdates(t *testing.T, f fixture) {
	t.Helper()

	counter, ok := f.s.(interface {
		CountRunningJournaledUpdates(ctx context.Context) (int64, error)
	})
	if !ok {
		t.Fatal("store does not count running journaled updates")
	}

	count := func() int64 {
		t.Helper()

		got, err := counter.CountRunningJournaledUpdates(t.Context())
		if err != nil {
			t.Fatalf("count running journaled updates: %v", err)
		}

		return got
	}

	startJournaled := func(ref stack.Ref, id string, version int64) stack.Update {
		t.Helper()

		u := started(t, f.create(t, ref, id, mutating()))
		u.JournalVersion = version

		got, err := f.s.StartUpdate(t.Context(), u, f.fence)
		if err != nil {
			t.Fatalf("start journaled update %s: %v", id, err)
		}

		return got
	}

	if got := count(); got != 0 {
		t.Fatalf("empty count = %d, want 0", got)
	}

	first := startJournaled(f.ref, "u1", 1)
	if got := count(); got != 1 {
		t.Fatalf("count with one running journaled update = %d, want 1", got)
	}

	other := f.other(t)
	second := startJournaled(other, "u2", 2)
	if got := count(); got != 2 {
		t.Fatalf("count with two running journaled updates = %d, want 2", got)
	}

	f.end(t, first, stack.StatusSucceeded)
	if got := count(); got != 1 {
		t.Fatalf("count after one journaled update ended = %d, want 1", got)
	}

	plain := f.run(t, f.ref, "u3", mutating())
	if got := count(); got != 1 {
		t.Fatalf("count with a running update without journaling = %d, want 1", got)
	}

	f.end(t, second, stack.StatusSucceeded)
	f.end(t, plain, stack.StatusSucceeded)

	if got := count(); got != 0 {
		t.Fatalf("count after every update ended = %d, want 0", got)
	}
}

func testMissingUpdate(t *testing.T, f fixture) {
	t.Helper()

	if _, err := f.s.GetUpdate(t.Context(), f.ref, "nope"); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("get missing update: err = %v, want ErrUpdateNotFound", err)
	}

	f.create(t, f.ref, "u1", mutating())
	other := f.other(t)

	if _, err := f.s.GetUpdate(t.Context(), other, "u1"); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("get update through another stack: err = %v, want ErrUpdateNotFound", err)
	}

	if _, err := f.s.ListEvents(t.Context(), f.ref, "nope", 0, 10); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("list events of missing update: err = %v, want ErrUpdateNotFound", err)
	}
}

func testUpdateMissingStack(t *testing.T, f fixture) {
	t.Helper()

	ghost := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "ghost"}
	u := newUpdate(ghost, "u1", mutating(), "", 0)

	if err := f.s.CreateUpdate(t.Context(), u, UnrecordedKey(), f.fence); !errors.Is(
		err, stack.ErrNotFound,
	) {
		t.Fatalf("create update of missing stack: err = %v, want stack.ErrNotFound", err)
	}
}

func testUpdateWritesFenced(t *testing.T, f fixture) {
	t.Helper()

	stale := namespace.Fence{Namespace: f.fence.Namespace, FailoverVersion: f.fence.FailoverVersion + 1}
	running := f.run(t, f.ref, "running", mutating())
	waiting := f.create(t, f.other(t), "waiting", mutating())

	writes := map[string]func() error{
		"CreateUpdate": func() error {
			u := waiting
			u.ID = "fenced"

			return f.s.CreateUpdate(t.Context(), u, UnrecordedKey(), stale)
		},
		"StartUpdate": func() error {
			_, err := f.s.StartUpdate(t.Context(), started(t, waiting), stale)

			return err
		},
		"SaveUpdate": func() error {
			return f.s.SaveUpdate(t.Context(), ended(running, stack.StatusSucceeded), stale)
		},
		"PutCheckpoint": func() error {
			_, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, running, 0, `{}`), stale)

			return err
		},
		"AppendEvents": func() error {
			return f.s.AppendEvents(t.Context(), newEvents(running, 0), stale)
		},
	}

	for name, write := range writes {
		if err := write(); !errors.Is(err, namespace.ErrFenced) {
			t.Errorf("%s with stale fence: err = %v, want ErrFenced", name, err)
		}
	}

	if _, err := f.s.GetUpdate(t.Context(), f.ref, "fenced"); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Errorf("fenced update was created: err = %v, want ErrUpdateNotFound", err)
	}

	assertUpdate(t, f, running)
	assertUpdate(t, f, waiting)

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("fenced checkpoint was saved: err = %v, want ErrNoState", err)
	}

	assertEvents(t, f, running)
}

func testStartVersions(t *testing.T, f fixture) {
	t.Helper()

	first := f.run(t, f.ref, "u1", mutating())
	if first.Version != 1 {
		t.Fatalf("first update version = %d, want 1", first.Version)
	}

	if v := f.stackVersion(t, f.ref); v != 0 {
		t.Fatalf("stack version while the first update runs = %d, want 0", v)
	}

	f.end(t, first, stack.StatusFailed)

	if v := f.stackVersion(t, f.ref); v != 1 {
		t.Fatalf("stack version after a failed update = %d, want 1", v)
	}

	// A preview takes the stack's version, not a new one.
	if p := f.run(t, f.ref, "p1", preview()); p.Version != 1 {
		t.Fatalf("preview version = %d, want 1", p.Version)
	}

	second := f.run(
		t,
		f.ref,
		"u2",
		stack.Plan{Kind: stack.KindDestroy, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if second.Version != 2 {
		t.Fatalf("second update version = %d, want 2", second.Version)
	}

	// A canceled update keeps its number; the next one does not reuse it.
	f.end(t, second, stack.StatusCanceled)

	third := f.run(t, f.ref, "u3", mutating())
	if third.Version != 3 {
		t.Fatalf("third update version = %d, want 3", third.Version)
	}

	f.end(t, third, stack.StatusSucceeded)

	if v := f.stackVersion(t, f.ref); v != 3 {
		t.Fatalf("stack version after the third update = %d, want 3", v)
	}

	// Ending a preview leaves the stack alone; versions are per stack.
	f.end(t, f.run(t, f.ref, "p2", dryRun()), stack.StatusSucceeded)

	if v := f.stackVersion(t, f.ref); v != 3 {
		t.Fatalf("stack version after a preview = %d, want 3", v)
	}

	if u := f.run(t, f.other(t), "d1", mutating()); u.Version != 1 {
		t.Fatalf("first update of another stack: version = %d, want 1", u.Version)
	}
}

func testOneRunning(t *testing.T, f fixture) {
	t.Helper()

	running := f.run(t, f.ref, "u1", mutating())

	blocked := f.create(
		t,
		f.ref,
		"u2",
		stack.Plan{Kind: stack.KindRefresh, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)

	// The refusal names the update that holds the stack, so the caller needs
	// no second read to show it.
	_, err := f.s.StartUpdate(t.Context(), started(t, blocked), f.fence)
	if conflict, ok := errors.AsType[*stack.UpdateConflictError](err); !ok ||
		!errors.Is(err, stack.ErrUpdateConflict) || conflict.Active != running.ID {
		t.Fatalf("start a second mutating update: err = %v, want ErrUpdateConflict that names %s", err, running.ID)
	}

	assertUpdate(t, f, blocked)

	// Previews and dry runs never wait, and other stacks are independent.
	f.run(t, f.ref, "p1", preview())
	f.run(t, f.ref, "p2", dryRun())
	f.run(t, f.other(t), "d1", mutating())

	f.end(t, running, stack.StatusSucceeded)

	if u := f.start(t, blocked); u.Version != 2 {
		t.Fatalf("start after the first ended: version = %d, want 2", u.Version)
	}
}

func testConcurrentStarts(t *testing.T, f fixture) {
	t.Helper()

	const n = 8

	pending := make([]stack.Update, n)
	for i := range pending {
		pending[i] = started(t, f.create(t, f.ref, fmt.Sprintf("u%d", i), mutating()))
	}

	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := range pending {
		wg.Go(func() {
			_, errs[i] = f.s.StartUpdate(t.Context(), pending[i], f.fence)
		})
	}

	wg.Wait()

	var won int

	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, stack.ErrUpdateConflict):
			t.Errorf("start u%d: err = %v, want nil or ErrUpdateConflict", i, err)
		default:
		}
	}

	if won != 1 {
		t.Fatalf("%d of %d concurrent starts succeeded, want 1", won, n)
	}

	active, err := f.s.ActiveUpdate(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("active update: %v", err)
	}

	if active.Version != 1 {
		t.Fatalf("winning update version = %d, want 1", active.Version)
	}
}

func testStaleRevision(t *testing.T, f fixture) {
	t.Helper()

	u := f.create(t, f.ref, "u1", mutating())

	skipped := started(t, u)
	skipped.Revision++

	if _, err := f.s.StartUpdate(t.Context(), skipped, f.fence); !errors.Is(err, stack.ErrConcurrentModification) {
		t.Fatalf("start with a skipped revision: err = %v, want ErrConcurrentModification", err)
	}

	running := f.start(t, u)

	renewed := running
	renewed.Lease.ExpiresAt = renewed.Lease.ExpiresAt.Add(time.Minute)
	renewed.Revision++

	if err := f.s.SaveUpdate(t.Context(), renewed, f.fence); err != nil {
		t.Fatalf("renew: %v", err)
	}

	// Ending from the pre-renewal read loses the compare-and-swap.
	stale := ended(running, stack.StatusSucceeded)
	if err := f.s.SaveUpdate(t.Context(), stale, f.fence); !errors.Is(err, stack.ErrConcurrentModification) {
		t.Fatalf("save from a stale read: err = %v, want ErrConcurrentModification", err)
	}

	assertUpdate(t, f, renewed)

	if v := f.stackVersion(t, f.ref); v != 0 {
		t.Fatalf("stack version after a lost save = %d, want 0", v)
	}
}

func testConcurrentSaves(t *testing.T, f fixture) {
	t.Helper()

	running := f.run(t, f.ref, "u1", mutating())
	statuses := []stack.UpdateStatus{
		stack.StatusSucceeded, stack.StatusFailed, stack.StatusCanceled,
		stack.StatusSucceeded, stack.StatusFailed, stack.StatusCanceled,
	}
	errs := make([]error, len(statuses))

	var wg sync.WaitGroup
	for i, status := range statuses {
		wg.Go(func() {
			errs[i] = f.s.SaveUpdate(t.Context(), ended(running, status), f.fence)
		})
	}

	wg.Wait()

	var won []stack.UpdateStatus

	for i, err := range errs {
		switch {
		case err == nil:
			won = append(won, statuses[i])
		case !errors.Is(err, stack.ErrConcurrentModification):
			t.Errorf("save %d: err = %v, want nil or ErrConcurrentModification", i, err)
		default:
		}
	}

	if len(won) != 1 {
		t.Fatalf("%d of %d concurrent saves succeeded, want 1", len(won), len(statuses))
	}

	assertUpdate(t, f, ended(running, won[0]))
}

func testActiveUpdate(t *testing.T, f fixture) {
	t.Helper()

	if _, err := f.s.ActiveUpdate(t.Context(), f.ref); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("active update of an idle stack: err = %v, want ErrUpdateNotFound", err)
	}

	f.create(t, f.ref, "waiting", mutating())
	f.run(t, f.ref, "p1", preview())
	f.run(t, f.ref, "p2", dryRun())
	f.run(t, f.other(t), "other", mutating())

	if _, err := f.s.ActiveUpdate(t.Context(), f.ref); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("active update with only previews running: err = %v, want ErrUpdateNotFound", err)
	}

	want := f.run(t, f.ref, "u1", mutating())

	got, err := f.s.ActiveUpdate(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("active update: %v", err)
	}

	if !sameUpdate(got, want) {
		t.Fatalf("active update = %+v, want %+v", got, want)
	}

	f.end(t, want, stack.StatusSucceeded)

	if _, err := f.s.ActiveUpdate(t.Context(), f.ref); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("active update after it ended: err = %v, want ErrUpdateNotFound", err)
	}
}

func testListUpdates(t *testing.T, f fixture) {
	t.Helper()

	all := stack.Page{Limit: 100, Offset: 0}

	if list, err := f.s.ListUpdates(t.Context(), f.ref, all); err != nil || len(list) != 0 {
		t.Fatalf("history of a new stack = %v, %v; want empty", list, err)
	}

	ghost := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "ghost"}
	if list, err := f.s.ListUpdates(t.Context(), ghost, all); err != nil || len(list) != 0 {
		t.Fatalf("history of a missing stack = %v, %v; want empty", list, err)
	}

	f.end(t, f.run(t, f.ref, "u1", mutating()), stack.StatusSucceeded)
	f.end(t, f.run(t, f.ref, "p1", preview()), stack.StatusSucceeded)
	f.end(
		t,
		f.run(
			t,
			f.ref,
			"u2",
			stack.Plan{Kind: stack.KindRefresh, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
		),
		stack.StatusFailed,
	)
	f.end(t, f.run(t, f.ref, "p2", dryRun()), stack.StatusSucceeded)
	f.end(t, f.run(t, f.ref, "u3", mutating()), stack.StatusCanceled)
	f.create(t, f.ref, "waiting", mutating())
	f.run(
		t,
		f.ref,
		"u4",
		stack.Plan{Kind: stack.KindImport, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	f.end(t, f.run(t, f.other(t), "other", mutating()), stack.StatusSucceeded)

	for _, tc := range []struct {
		page stack.Page
		want []stack.UpdateID
	}{
		{page: all, want: []stack.UpdateID{"u4", "u3", "u2", "u1"}},
		{page: stack.Page{Limit: 2, Offset: 0}, want: []stack.UpdateID{"u4", "u3"}},
		{page: stack.Page{Limit: 2, Offset: 2}, want: []stack.UpdateID{"u2", "u1"}},
		{page: stack.Page{Limit: 2, Offset: 4}, want: []stack.UpdateID{}},
	} {
		list, err := f.s.ListUpdates(t.Context(), f.ref, tc.page)
		if err != nil {
			t.Fatalf("list updates %+v: %v", tc.page, err)
		}

		got := make([]stack.UpdateID, 0, len(list))
		for i := range list {
			got = append(got, list[i].ID)
		}

		if !slices.Equal(got, tc.want) {
			t.Errorf("list updates %+v = %v, want %v", tc.page, got, tc.want)
		}
	}
}

// testHistoryCreator checks that CreatedBy and RestoredFrom survive create,
// start, save and list.
func testHistoryCreator(t *testing.T, f historyFixture) {
	t.Helper()

	want := f.create(t, "u1", mutating(), "alice@example.com", 0)
	f.assertUpdate(t, want)

	want = f.start(t, want)
	f.assertUpdate(t, want)

	want = f.end(t, want, stack.StatusSucceeded)
	f.assertUpdate(t, want)

	// A restore records the version it copied. Each update keeps its own creator.
	restored := f.create(
		t,
		"u2",
		stack.Plan{Kind: stack.KindRestore, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
		"bob@example.com",
		want.Version,
	)
	restored = f.end(t, f.start(t, restored), stack.StatusSucceeded)
	f.assertUpdate(t, restored)

	history := f.history(t)
	if len(history) != 2 || !sameUpdate(history[0], restored) || !sameUpdate(history[1], want) {
		t.Fatalf("history = %+v, want [u2 u1] with creator and restore source", history)
	}
}

// testHistoryCheckpointSize checks that history reports the size of the
// checkpoint each update saved, and 0 for an update with no checkpoint and
// for a preview.
func testHistoryCheckpointSize(t *testing.T, f historyFixture) {
	t.Helper()

	withCheckpoint := f.run(t, "u1", mutating(), "", 0)
	body := `{"v":1}`
	f.putCheckpoint(t, withCheckpoint, body)
	withCheckpoint = f.end(t, withCheckpoint, stack.StatusSucceeded)
	withCheckpoint.CheckpointBytes = int64(len(body))

	withoutCheckpoint := f.end(t, f.run(t, "u2", mutating(), "", 0), stack.StatusSucceeded)

	// The preview ends failed to show the status does not matter: previews
	// are never history and never carry a size.
	preview := f.end(t, f.run(t, "p1", preview(), "", 0), stack.StatusFailed)

	history := f.history(t)
	if len(history) != 2 || !sameUpdate(history[0], withoutCheckpoint) || !sameUpdate(history[1], withCheckpoint) {
		t.Fatalf("history = %+v, want [u2 u1] with checkpoint sizes", history)
	}

	// GetUpdate reports the same sizes, including 0 for the preview.
	wants := []stack.Update{withCheckpoint, withoutCheckpoint, preview}
	for i := range wants {
		got, err := f.s.GetUpdate(t.Context(), f.ref, wants[i].ID)
		if err != nil {
			t.Fatalf("get update %s: %v", wants[i].ID, err)
		}

		if !sameUpdate(got, wants[i]) {
			t.Fatalf("update %s = %+v, want %+v", wants[i].ID, got, wants[i])
		}
	}
}

// testHistorySizeFollowsRewrite checks that a later checkpoint write of the
// running update changes the size history reports.
func testHistorySizeFollowsRewrite(t *testing.T, f historyFixture) {
	t.Helper()

	u := f.run(t, "u1", mutating(), "", 0)

	for _, body := range []string{`{"v":1}`, `{"v":1,"more":"a longer deployment"}`} {
		f.putCheckpoint(t, u, body)

		if got := f.historySize(t, u.ID); got != int64(len(body)) {
			t.Fatalf("history size of %s = %d, want %d", u.ID, got, len(body))
		}
	}

	done := f.end(t, u, stack.StatusSucceeded)
	done.CheckpointBytes = int64(len(`{"v":1,"more":"a longer deployment"}`))
	f.assertUpdate(t, done)
}

// testHistoryKinds checks that an import and a restore update appear in
// history with their kinds.
func testHistoryKinds(t *testing.T, f historyFixture) {
	t.Helper()

	f.end(
		t,
		f.run(
			t,
			"u1",
			stack.Plan{Kind: stack.KindImport, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
			"alice@example.com",
			0,
		),
		stack.StatusSucceeded,
	)

	restored := f.run(
		t,
		"u2",
		stack.Plan{Kind: stack.KindRestore, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
		"",
		1,
	)
	f.end(t, restored, stack.StatusSucceeded)

	history := f.history(t)
	if len(history) != 2 || history[0].ID != "u2" || history[0].Kind != stack.KindRestore ||
		history[0].RestoredFrom != 1 || history[1].ID != "u1" || history[1].Kind != stack.KindImport {
		t.Fatalf("history kinds = %+v, want [restore u2, import u1]", history)
	}
}

// testHistoryCreateFenced checks that a create with a stale fence writes
// nothing.
func testHistoryCreateFenced(t *testing.T, f historyFixture) {
	t.Helper()

	stale := namespace.Fence{Namespace: f.fence.Namespace, FailoverVersion: f.fence.FailoverVersion + 1}
	u := newUpdate(f.ref, "fenced", mutating(), "alice@example.com", 0)

	if err := f.s.CreateUpdate(t.Context(), u, UnrecordedKey(), stale); !errors.Is(
		err, namespace.ErrFenced,
	) {
		t.Fatalf("create update with stale fence: err = %v, want ErrFenced", err)
	}

	if _, err := f.s.GetUpdate(t.Context(), f.ref, "fenced"); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("fenced update was created: err = %v, want ErrUpdateNotFound", err)
	}

	if history := f.history(t); len(history) != 0 {
		t.Fatalf("history after a fenced create = %+v, want empty", history)
	}
}

// newBlobCheckpoint returns a checkpoint of u kept in blobs under key: the
// store keeps the key with the size, the digest and the sequence, and no
// content. The deployment travels for the resource count only.
func newBlobCheckpoint(u stack.Update, key blob.Key, seq int64, deployment string) stack.Checkpoint {
	cp := newCheckpoint(u, seq, deployment)
	cp.BlobKey = key
	cp.BlobSize, cp.BlobMD5 = gzipAttrs(deployment)

	return cp
}

// gzipAttrs returns the size and MD5 of deployment gzipped, as the blob store
// holds it.
func gzipAttrs(deployment string) (int64, [md5.Size]byte) {
	var buf bytes.Buffer

	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(deployment)); err != nil {
		panic(err)
	}

	if err := w.Close(); err != nil {
		panic(err)
	}

	sum := md5.Sum(buf.Bytes()) //nolint:gosec // Test digests of stored bytes, for integrity, not security.

	return int64(buf.Len()), sum
}

// stackBlobPrefix returns the prefix of the stack blobs of the namespace ns:
// namespaces/<namespace ID>/pulumi/. The key of a checkpoint or of a journal
// batch of a stack starts with it.
func stackBlobPrefix(t *testing.T, s namespace.Store, ns namespace.Name) string {
	t.Helper()

	row, err := s.GetNamespace(t.Context(), ns)
	if err != nil {
		t.Fatalf("get namespace %s: %v", ns, err)
	}

	return "namespaces/" + row.ID.String() + "/pulumi/"
}

// keyStore is the part of a store that the key of a checkpoint blob needs: the
// namespace row and the stack row.
type keyStore interface {
	namespace.Store
	GetStack(ctx context.Context, ref stack.Ref) (stack.Stack, error)
}

// defaultAttempt is the attempt ID that fixture.blobKey puts in a key.
const defaultAttempt = "attempt0000"

// checkpointKey returns the blob key of a save of u with seq and the attempt
// ID, for the stack of u:
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence>-<attempt ID>.
func checkpointKey(t *testing.T, s keyStore, u stack.Update, seq int64, attempt string) blob.Key {
	t.Helper()

	st, err := s.GetStack(t.Context(), u.Ref)
	if err != nil {
		t.Fatalf("get stack %s: %v", u.Ref, err)
	}

	prefix := stackBlobPrefix(t, s, u.Ref.Namespace)

	key, err := blob.ParseKey(
		prefix + st.ID.String() + "/" + u.ID.String() + "/" + strconv.FormatInt(seq, 10) + "-" + attempt,
	)
	if err != nil {
		t.Fatalf("parse blob key: %v", err)
	}

	return key
}

// blobKey returns the blob key of a save of u with seq, for the stack of u.
func (f fixture) blobKey(t *testing.T, u stack.Update, seq int64) blob.Key {
	t.Helper()

	return checkpointKey(t, f.s, u, seq, defaultAttempt)
}

// attemptKey returns the blob key of the save of u with seq that the attempt
// wrote.
func (f fixture) attemptKey(t *testing.T, u stack.Update, seq int64, attempt string) blob.Key {
	t.Helper()

	return checkpointKey(t, f.s, u, seq, attempt)
}

// attemptCheckpoint returns a checkpoint of u under the key of one write
// attempt, as f.checkpoint does for the key without an attempt ID.
func (f fixture) attemptCheckpoint(
	t *testing.T, u stack.Update, seq int64, deployment, attempt string,
) stack.Checkpoint {
	t.Helper()

	cp := newCheckpoint(u, seq, deployment)
	cp.BlobKey = f.attemptKey(t, u, seq, attempt)

	return cp
}

// checkpoint returns a checkpoint of u as newCheckpoint does, under the blob
// key that the service builds for the save: the store refuses any other key.
func (f fixture) checkpoint(t *testing.T, u stack.Update, seq int64, deployment string) stack.Checkpoint {
	t.Helper()

	cp := newCheckpoint(u, seq, deployment)
	cp.BlobKey = f.blobKey(t, u, seq)

	return cp
}

// newCheckpoint returns a checkpoint of u written under its current lease,
// under a placeholder blob key that the store refuses. The store never reads the
// blob store, so a key needs no object. Use fixture.checkpoint for a checkpoint
// that the store accepts.
func newCheckpoint(u stack.Update, seq int64, deployment string) stack.Checkpoint {
	size, sum := gzipAttrs(deployment)

	return stack.Checkpoint{
		NamespaceID: "", StackID: "",
		SavedAt:       now().Add(2 * time.Second),
		Features:      []string{"deltaCheckpointUploadsV2", "verbatim"},
		Deployment:    []byte(deployment),
		Update:        u.ID,
		Ref:           u.Ref,
		Lease:         u.Lease.Digest,
		SchemaVersion: 3,
		Sequence:      seq,
		Digest:        sha256.Sum256([]byte(deployment)),
		Invalid:       false,
		BlobKey:       placeholderKey(u, seq),
		BlobSize:      size,
		BlobMD5:       sum,
		// The attempt starts now, so the store accepts it in its time window.
		AttemptStartedAt: time.Now(), Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// placeholderKey returns a blob key of a save of u with seq that no object
// holds, and that no store accepts in a row: it is in no real namespace.
func placeholderKey(u stack.Update, seq int64) blob.Key {
	name := hex.EncodeToString([]byte(u.ID))
	tail := strconv.FormatInt(seq, 10)

	key, err := blob.ParseKey("namespaces/ns-placeholder/pulumi/placeholder/" + name + "/" + tail)
	if err != nil {
		panic(err)
	}

	return key
}

func testCheckpointRoundTrip(t *testing.T, f fixture) {
	t.Helper()

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("latest checkpoint of a new stack: err = %v, want ErrNoState", err)
	}

	u := f.run(t, f.ref, "u1", mutating())

	// Not normalized JSON: key order, spacing and duplicates must survive.
	want := f.checkpoint(t, u, 0, "{\"b\": 1,  \"a\":[ ], \"b\":2}\n")
	want.Invalid = true

	if _, err := f.s.PutCheckpoint(t.Context(), want, f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	assertCheckpoint(t, f, f.ref, want)

	empty := f.checkpoint(t, u, 0, "")
	empty.Features = nil

	if _, err := f.s.PutCheckpoint(t.Context(), empty, f.fence); err != nil {
		t.Fatalf("put empty checkpoint: %v", err)
	}

	assertCheckpoint(t, f, f.ref, empty)

	if _, err := f.s.LatestCheckpoint(t.Context(), f.other(t)); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("latest checkpoint of another stack: err = %v, want ErrNoState", err)
	}
}

func testCheckpointLease(t *testing.T, f fixture) {
	t.Helper()

	waiting := f.create(t, f.ref, "waiting", mutating())
	if _, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, waiting, 0, `{}`), f.fence); !errors.Is(
		err, stack.ErrWrongUpdateState,
	) {
		t.Fatalf("checkpoint of a not-started update: err = %v, want ErrWrongUpdateState", err)
	}

	u := f.run(t, f.ref, "u1", mutating())

	forged := f.checkpoint(t, u, 0, `{}`)
	forged.Lease = sha256.Sum256([]byte("another lease"))

	if _, err := f.s.PutCheckpoint(t.Context(), forged, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("checkpoint under another lease: err = %v, want ErrWrongUpdateState", err)
	}

	// Authorized while running, written after a cancel: rejected.
	late := f.checkpoint(t, u, 0, `{}`)
	f.end(t, u, stack.StatusCanceled)

	if _, err := f.s.PutCheckpoint(t.Context(), late, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("checkpoint after cancel: err = %v, want ErrWrongUpdateState", err)
	}

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("a rejected checkpoint was saved: err = %v, want ErrNoState", err)
	}
}

func testCheckpointSequence(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	put := func(seq int64, body string) error {
		_, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, u, seq, body), f.fence)

		return err
	}

	if err := put(2, `{"n":2}`); err != nil {
		t.Fatalf("put sequence 2: %v", err)
	}

	if err := put(2, `{"n":2}`); err != nil {
		t.Fatalf("retry sequence 2: err = %v, want nil", err)
	}

	if err := put(2, `{"n":"other"}`); !errors.Is(err, stack.ErrCheckpointConflict) {
		t.Fatalf("different content at sequence 2: err = %v, want ErrCheckpointConflict", err)
	}

	if err := put(1, `{"n":1}`); !errors.Is(err, stack.ErrStaleCheckpoint) {
		t.Fatalf("sequence 1 after 2: err = %v, want ErrStaleCheckpoint", err)
	}

	assertCheckpoint(t, f, f.ref, f.checkpoint(t, u, 2, `{"n":2}`))

	if err := put(3, `{"n":3}`); err != nil {
		t.Fatalf("put sequence 3: %v", err)
	}

	// Without a sequence number the last write wins.
	if err := put(0, `{"n":"a"}`); err != nil {
		t.Fatalf("put without sequence: %v", err)
	}

	if err := put(0, `{"n":"b"}`); err != nil {
		t.Fatalf("put without sequence again: %v", err)
	}

	assertCheckpoint(t, f, f.ref, f.checkpoint(t, u, 0, `{"n":"b"}`))
}

func testCheckpointAcrossUpdates(t *testing.T, f fixture) {
	t.Helper()

	first := f.run(t, f.ref, "u1", mutating())
	if _, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, first, 5, `{"from":1}`), f.fence); err != nil {
		t.Fatalf("put checkpoint of u1: %v", err)
	}

	f.end(t, first, stack.StatusSucceeded)

	// Sequence numbers belong to an update: a new update starts again at 1.
	second := f.run(t, f.ref, "u2", mutating())
	want := f.checkpoint(t, second, 1, `{"from":2}`)

	if _, err := f.s.PutCheckpoint(t.Context(), want, f.fence); err != nil {
		t.Fatalf("put checkpoint of u2: %v", err)
	}

	assertCheckpoint(t, f, f.ref, want)

	other := f.run(t, f.other(t), "d1", mutating())
	if _, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, other, 9, `{"from":"dev"}`), f.fence); err != nil {
		t.Fatalf("put checkpoint of another stack: %v", err)
	}

	assertCheckpoint(t, f, f.ref, want)
}

// testCheckpointLatestIsHighest checks that the latest checkpoint is the one
// with the highest version, and that an update without a checkpoint does not
// hide the one before it.
func testCheckpointLatestIsHighest(t *testing.T, f fixture) {
	t.Helper()

	first := f.run(t, f.ref, "u1", mutating())
	firstCP := f.checkpoint(t, first, 0, `{"v":1}`)

	if _, err := f.s.PutCheckpoint(t.Context(), firstCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u1: %v", err)
	}

	f.end(t, first, stack.StatusSucceeded)

	second := f.run(t, f.ref, "u2", mutating())
	secondCP := f.checkpoint(t, second, 0, `{"v":2}`)

	if _, err := f.s.PutCheckpoint(t.Context(), secondCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u2: %v", err)
	}

	f.end(t, second, stack.StatusSucceeded)

	assertCheckpoint(t, f, f.ref, secondCP)

	// An update that saves no checkpoint leaves the latest one in place.
	third := f.run(t, f.ref, "u3", mutating())
	f.end(t, third, stack.StatusSucceeded)

	assertCheckpoint(t, f, f.ref, secondCP)
}

// testCheckpointByVersion checks that version N returns only N: no fallback
// to an older checkpoint, and ErrNoState for a missing or empty version.
func testCheckpointByVersion(t *testing.T, f fixture) {
	t.Helper()

	first := f.run(t, f.ref, "u1", mutating())
	firstCP := f.checkpoint(t, first, 0, `{"v":1}`)

	if _, err := f.s.PutCheckpoint(t.Context(), firstCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u1: %v", err)
	}

	f.end(t, first, stack.StatusSucceeded)

	second := f.run(t, f.ref, "u2", mutating())
	secondCP := f.checkpoint(t, second, 0, `{"v":2}`)

	if _, err := f.s.PutCheckpoint(t.Context(), secondCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u2: %v", err)
	}

	f.end(t, second, stack.StatusSucceeded)

	assertCheckpointVersion(t, f, f.ref, 1, firstCP)
	assertCheckpointVersion(t, f, f.ref, 2, secondCP)

	for _, version := range []int64{0, 3, 99} {
		if _, err := f.s.CheckpointByVersion(t.Context(), f.ref, version); !errors.Is(err, stack.ErrNoState) {
			t.Errorf("checkpoint of missing version %d: err = %v, want ErrNoState", version, err)
		}
	}

	// An update without a checkpoint is empty, not a fallback.
	third := f.run(t, f.ref, "u3", mutating())
	f.end(t, third, stack.StatusSucceeded)

	if _, err := f.s.CheckpointByVersion(t.Context(), f.ref, third.Version); !errors.Is(
		err, stack.ErrNoState,
	) {
		t.Errorf("checkpoint of empty version %d: err = %v, want ErrNoState", third.Version, err)
	}

	assertCheckpointVersion(t, f, f.ref, 1, firstCP)
	assertCheckpointVersion(t, f, f.ref, 2, secondCP)

	ghost := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "ghost"}
	if _, err := f.s.CheckpointByVersion(t.Context(), ghost, 1); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("checkpoint of a missing stack: err = %v, want ErrNoState", err)
	}
}

// testCheckpointBlobMetadata checks that a row with a blob key keeps the
// key with the size, the digest and the sequence, and no content: the content
// lives in blobs. Every reader returns the metadata with an empty deployment.
func testCheckpointBlobMetadata(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	want := newBlobCheckpoint(u, f.blobKey(t, u, 3), 3, "{\"b\": 1,  \"a\":[ ], \"b\":2}\n")
	want.Invalid = true

	if _, err := f.s.PutCheckpoint(t.Context(), want, f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	// The row keeps the metadata only; the content lives in blobs.
	stored := want
	stored.Deployment = nil

	assertCheckpoint(t, f, f.ref, stored)

	got, err := f.s.CheckpointByUpdate(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("checkpoint of update %s: %v", u.ID, err)
	}

	assertSameCheckpoint(t, got, stored, "checkpoint of update")

	if len(got.Deployment) != 0 {
		t.Fatalf("checkpoint deployment = %q, want empty: the content lives in blobs", got.Deployment)
	}

	assertCheckpointVersion(t, f, f.ref, u.Version, stored)
}

// testCheckpointByUpdate checks that the update checkpoint read returns only
// that update: no fallback to another update, and ErrNoState for an update
// without a checkpoint, for another stack and for a missing stack.
func testCheckpointByUpdate(t *testing.T, f fixture) {
	t.Helper()

	first := f.run(t, f.ref, "u1", mutating())
	want := f.checkpoint(t, first, 0, `{"v":1}`)

	if _, err := f.s.PutCheckpoint(t.Context(), want, f.fence); err != nil {
		t.Fatalf("put checkpoint of u1: %v", err)
	}

	f.end(t, first, stack.StatusSucceeded)

	second := f.run(t, f.ref, "u2", mutating())

	got, err := f.s.CheckpointByUpdate(t.Context(), f.ref, first.ID)
	if err != nil {
		t.Fatalf("checkpoint of update %s: %v", first.ID, err)
	}

	assertSameCheckpoint(t, got, want, "checkpoint of update")

	if _, err := f.s.CheckpointByUpdate(t.Context(), f.ref, second.ID); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("checkpoint of an update with no checkpoint: err = %v, want ErrNoState", err)
	}

	if _, err := f.s.CheckpointByUpdate(t.Context(), f.other(t), first.ID); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("checkpoint through another stack: err = %v, want ErrNoState", err)
	}

	ghost := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "ghost"}
	if _, err := f.s.CheckpointByUpdate(t.Context(), ghost, first.ID); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("checkpoint of a missing stack: err = %v, want ErrNoState", err)
	}

	f.end(t, second, stack.StatusSucceeded)
}

// testHistoryBlobSize checks that history reports the stored size for a blob
// row: the content column is empty, so the size cannot come from it.
func testHistoryBlobSize(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	body := `{"v":1,"more":"a longer deployment"}`
	want := newBlobCheckpoint(u, f.blobKey(t, u, 0), 0, body)

	if _, err := f.s.PutCheckpoint(t.Context(), want, f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	got, err := f.s.GetUpdate(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("get update %s: %v", u.ID, err)
	}

	if got.CheckpointBytes != int64(len(body)) {
		t.Fatalf("checkpoint size of %s = %d, want %d", u.ID, got.CheckpointBytes, len(body))
	}

	history, err := f.s.ListUpdates(t.Context(), f.ref, stack.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list updates: %v", err)
	}

	if len(history) != 1 || history[0].CheckpointBytes != int64(len(body)) {
		t.Fatalf("history checkpoint size = %+v, want one update with %d", history, len(body))
	}
}

// testReferencedBlobKeys checks that the referenced keys hold every
// blob key the stack's checkpoints name, ordered. A stack without
// checkpoints, or one that does not exist, yields an empty list.
func testReferencedBlobKeys(t *testing.T, f fixture) {
	t.Helper()

	if keys, err := f.s.ReferencedBlobKeys(t.Context(), f.stackIDOf(t, f.ref)); err != nil || len(keys) != 0 {
		t.Fatalf("referenced keys of a new stack = %v, %v; want empty", keys, err)
	}

	first := f.run(t, f.ref, "u1", mutating())
	firstKey := f.blobKey(t, first, 0)

	if _, err := f.s.PutCheckpoint(t.Context(), newBlobCheckpoint(first, firstKey, 0, `{"v":1}`), f.fence); err != nil {
		t.Fatalf("put checkpoint of u1: %v", err)
	}

	f.end(t, first, stack.StatusSucceeded)

	second := f.run(t, f.ref, "u2", mutating())
	secondKey := f.blobKey(t, second, 0)

	row := newBlobCheckpoint(second, secondKey, 0, `{"v":2}`)

	if _, err := f.s.PutCheckpoint(t.Context(), row, f.fence); err != nil {
		t.Fatalf("put checkpoint of u2: %v", err)
	}

	f.end(t, second, stack.StatusSucceeded)

	keys, err := f.s.ReferencedBlobKeys(t.Context(), f.stackIDOf(t, f.ref))
	if err != nil {
		t.Fatalf("referenced keys: %v", err)
	}

	if !slices.Equal(keys, []blob.Key{firstKey, secondKey}) {
		t.Fatalf("referenced keys = %v, want [%s %s]", keys, firstKey, secondKey)
	}

	if keys, err := f.s.ReferencedBlobKeys(t.Context(), f.stackIDOf(t, f.other(t))); err != nil || len(keys) != 0 {
		t.Fatalf("referenced keys of another stack = %v, %v; want empty", keys, err)
	}
}

// testReferencedBlobKeysByID checks the keep set of a sweep. The set is read
// by the stack ID, the key that the sweep lists the blobs by, so it survives
// a rename. A stack that is missing, or soft-deleted, returns ErrNotFound and
// no keys: an empty set there would let a sweep delete every blob.
func testReferencedBlobKeysByID(t *testing.T, f fixture) {
	t.Helper()

	id := f.stackIDOf(t, f.ref)

	u := f.run(t, f.ref, "u1", mutating())
	key := f.blobKey(t, u, 0)

	if _, err := f.s.PutCheckpoint(t.Context(), newBlobCheckpoint(u, key, 0, `{"v":1}`), f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	f.end(t, u, stack.StatusSucceeded)

	if err := f.s.RenameStack(t.Context(), f.ref, "renamed", f.fence); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	keys, err := f.s.ReferencedBlobKeys(t.Context(), id)
	if err != nil || !slices.Equal(keys, []blob.Key{key}) {
		t.Fatalf("referenced keys after a rename = %v, %v; want [%s]", keys, err, key)
	}

	empty := f.other(t)
	deleted := f.stackIDOf(t, empty)

	keys, err = f.s.ReferencedBlobKeys(t.Context(), deleted)
	if err != nil || len(keys) != 0 {
		t.Fatalf("referenced keys of a live stack without rows = %v, %v; want empty and no error", keys, err)
	}

	delErr := f.s.DeleteStack(
		t.Context(), empty, f.fence, stack.EmptyGuard(),
		inventory.Deletion{Entry: deleteEntry(empty.Namespace), PurgeAfter: purgeAt()},
	)
	if delErr != nil {
		t.Fatalf("delete stack: %v", delErr)
	}

	for _, tc := range []struct {
		name string
		id   stack.ID
	}{
		{name: "soft-deleted stack", id: deleted},
		{name: "unknown ID", id: "no-such-stack-id"},
		{name: "empty ID", id: ""},
	} {
		keys, err := f.s.ReferencedBlobKeys(t.Context(), tc.id)
		if !errors.Is(err, stack.ErrNotFound) || len(keys) != 0 {
			t.Fatalf("referenced keys of %s = %v, %v; want no keys and ErrNotFound", tc.name, keys, err)
		}
	}
}

// testCheckpointWithoutKeyRefused checks that a checkpoint write without a
// blob key fails with ErrCheckpointNoKey and stores nothing, through both the
// checkpoint save and the built checkpoint write. The store keeps no content,
// so such a row could never be read.
func testCheckpointWithoutKeyRefused(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	cp := f.checkpoint(t, u, 0, `{"v":1}`)
	cp.BlobKey = ""

	if _, err := f.s.PutCheckpoint(t.Context(), cp, f.fence); !errors.Is(err, stack.ErrCheckpointNoKey) {
		t.Fatalf("put checkpoint without a key: err = %v, want ErrCheckpointNoKey", err)
	}

	if _, err := f.s.PutBuiltCheckpoint(t.Context(), cp, f.fence); !errors.Is(err, stack.ErrCheckpointNoKey) {
		t.Fatalf("put built checkpoint without a key: err = %v, want ErrCheckpointNoKey", err)
	}

	if _, err := f.s.CheckpointByUpdate(t.Context(), f.ref, u.ID); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("checkpoint after refused writes: err = %v, want ErrNoState", err)
	}
}

// testKeylessRowRefused checks that the schema refuses a checkpoint row
// without a blob key, also from a direct SQL update: the store keeps no
// content, so such a row can never exist, and the checkpoint stays readable.
func testKeylessRowRefused(t *testing.T, f fixture) {
	t.Helper()

	eraser, ok := f.s.(KeyEraser)
	if !ok {
		t.Skip("store cannot try to plant a checkpoint row without a blob key")
	}

	u := f.run(t, f.ref, "u1", mutating())
	if _, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, u, 0, `{"v":1}`), f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	f.end(t, u, stack.StatusSucceeded)

	if err := eraser.EraseCheckpointKeys(t.Context()); err == nil {
		t.Fatal("erase the checkpoint keys: err = nil, want the schema to refuse a row without a blob key")
	}

	if _, err := f.s.CheckpointByUpdate(t.Context(), f.ref, u.ID); err != nil {
		t.Fatalf("checkpoint after the refused erase: %v", err)
	}
}

// testCheckpointPreviewRejected checks that a preview or a dry run saves no
// checkpoint.
func testCheckpointPreviewRejected(t *testing.T, f fixture) {
	t.Helper()

	for name, plan := range map[string]stack.Plan{
		"preview": preview(),
		"dry-run": dryRun(),
	} {
		u := f.run(t, f.ref, name, plan)
		if _, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, u, 0, `{}`), f.fence); !errors.Is(
			err, stack.ErrWrongUpdateState,
		) {
			t.Errorf("%s checkpoint: err = %v, want ErrWrongUpdateState", name, err)
		}

		f.end(t, u, stack.StatusSucceeded)
	}

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("latest checkpoint after rejected previews: err = %v, want ErrNoState", err)
	}
}

// testCheckpointKeepsAfterEnd checks that a canceled or failed update keeps
// the last checkpoint it saved.
func testCheckpointKeepsAfterEnd(t *testing.T, f fixture) {
	t.Helper()

	failed := f.run(t, f.ref, "u1", mutating())
	failedCP := f.checkpoint(t, failed, 0, `{"v":1}`)

	if _, err := f.s.PutCheckpoint(t.Context(), failedCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u1: %v", err)
	}

	f.end(t, failed, stack.StatusFailed)
	assertCheckpoint(t, f, f.ref, failedCP)
	assertCheckpointVersion(t, f, f.ref, failed.Version, failedCP)

	canceled := f.run(t, f.ref, "u2", mutating())
	canceledCP := f.checkpoint(t, canceled, 0, `{"v":2}`)

	if _, err := f.s.PutCheckpoint(t.Context(), canceledCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u2: %v", err)
	}

	f.end(t, canceled, stack.StatusCanceled)
	assertCheckpoint(t, f, f.ref, canceledCP)
	assertCheckpointVersion(t, f, f.ref, canceled.Version, canceledCP)
}

// testDeletedStackChildrenUnreachable checks that after a soft delete the
// stack and its updates, checkpoints and events are no longer reachable by
// the name of the stack (ADR 47). The rows stay; the portable export case
// proves that.
func testDeletedStackChildrenUnreachable(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	if _, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, u, 0, `{"v":1}`), f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	if err := f.s.AppendEvents(t.Context(), newEvents(u, 1, 0, 1), f.fence); err != nil {
		t.Fatalf("append events: %v", err)
	}

	f.end(t, u, stack.StatusSucceeded)

	err := f.s.DeleteStack(
		t.Context(),
		f.ref,
		f.fence,
		countedGuard(u, `{"v":1}`),
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	if _, err := f.s.GetStack(t.Context(), f.ref); !errors.Is(err, stack.ErrNotFound) {
		t.Errorf("get deleted stack: err = %v, want ErrNotFound", err)
	}

	if _, err := f.s.GetUpdate(t.Context(), f.ref, u.ID); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Errorf("get update after delete: err = %v, want ErrUpdateNotFound", err)
	}

	if history, err := f.s.ListUpdates(t.Context(), f.ref, stack.Page{Limit: 100, Offset: 0}); err != nil ||
		len(history) != 0 {
		t.Errorf("history after delete = %v, %v; want empty", history, err)
	}

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("latest checkpoint after delete: err = %v, want ErrNoState", err)
	}

	if _, err := f.s.CheckpointByVersion(t.Context(), f.ref, u.Version); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("checkpoint of version %d after delete: err = %v, want ErrNoState", u.Version, err)
	}

	if _, err := f.s.ListEvents(t.Context(), f.ref, u.ID, -1, 100); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Errorf("events after delete: err = %v, want ErrUpdateNotFound", err)
	}
}

// testRenameDuringRunningUpdate checks that a rename waits for the stack lock:
// while a mutating update runs it fails with ErrUpdateConflict and changes
// nothing, so the update keeps its stack.
func testRenameDuringRunningUpdate(t *testing.T, f fixture) {
	t.Helper()

	newRef := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "renamed"}

	running := f.run(t, f.ref, "u1", mutating())

	renameErr := f.s.RenameStack(t.Context(), f.ref, "renamed", f.fence)
	if !errors.Is(renameErr, stack.ErrUpdateConflict) {
		t.Fatalf("rename during a running update: err = %v, want ErrUpdateConflict", renameErr)
	}

	if _, err := f.s.GetStack(t.Context(), f.ref); err != nil {
		t.Errorf("get stack after a refused rename: %v", err)
	}

	if _, err := f.s.GetStack(t.Context(), newRef); !errors.Is(err, stack.ErrNotFound) {
		t.Errorf("get new name after a refused rename: err = %v, want ErrNotFound", err)
	}

	if _, err := f.s.ActiveUpdate(t.Context(), f.ref); err != nil {
		t.Errorf("active update after a refused rename: %v", err)
	}

	f.end(t, running, stack.StatusSucceeded)

	if err := f.s.RenameStack(t.Context(), f.ref, "renamed", f.fence); err != nil {
		t.Errorf("rename after the update ended: %v", err)
	}
}

// renameRaceRounds is the number of stacks that testRenameRaceWithNewUpdate
// races. One round hits one interleaving, so many rounds make a missing lock
// show on a store that runs the two writes at the same time.
const renameRaceRounds = 40

// testRenameRaceWithNewUpdate starts a mutating update and renames the same
// stack at the same time, once per round. Exactly one of the two wins: if the
// start wins, the rename refuses and the update keeps its stack name. If the
// rename wins, the start finds no stack and no update is left under any name.
// Both never win, because the update would run under a name that no stack has.
func testRenameRaceWithNewUpdate(t *testing.T, f fixture) {
	t.Helper()

	for round := range renameRaceRounds {
		name := fmt.Sprintf("race-%d", round)
		st := newStack(f.ref.Namespace, f.ref.Project.String(), name)

		if err := f.s.CreateStack(t.Context(), st, f.fence); err != nil {
			t.Fatalf("create stack %s: %v", st.Ref, err)
		}

		u := f.create(t, st.Ref, fmt.Sprintf("racer-%d", round), mutating())
		newRef := stack.Ref{Namespace: st.Ref.Namespace, Project: st.Ref.Project, Stack: stack.Name(name + "-renamed")}

		var (
			startErr  error
			renameErr error
			wg        sync.WaitGroup
			gate      = make(chan struct{})
		)

		wg.Go(func() {
			<-gate

			_, startErr = f.s.StartUpdate(t.Context(), started(t, u), f.fence)
		})
		wg.Go(func() {
			<-gate

			renameErr = f.s.RenameStack(t.Context(), st.Ref, newRef.Stack, f.fence)
		})

		close(gate)
		wg.Wait()

		checkRenameRace(t, f, renameRace{oldRef: st.Ref, newRef: newRef, startErr: startErr, renameErr: renameErr})
	}
}

// renameRace is the outcome of one round of testRenameRaceWithNewUpdate.
type renameRace struct {
	startErr  error
	renameErr error
	oldRef    stack.Ref
	newRef    stack.Ref
}

// checkRenameRace proves that a rename and a concurrent start of a mutating
// update of the same stack never both succeed, and that the loser changed
// nothing.
func checkRenameRace(t *testing.T, f fixture, r renameRace) {
	t.Helper()

	oldRef, newRef, startErr, renameErr := r.oldRef, r.newRef, r.startErr, r.renameErr

	switch {
	case startErr == nil && renameErr == nil:
		t.Fatalf("rename of %s and update start both succeeded: the update runs under a name that no stack has", oldRef)
	case startErr == nil:
		if !errors.Is(renameErr, stack.ErrUpdateConflict) {
			t.Fatalf("rename lost to the update start: err = %v, want ErrUpdateConflict", renameErr)
		}

		if _, err := f.s.GetStack(t.Context(), oldRef); err != nil {
			t.Fatalf("get stack %s after a refused rename: %v", oldRef, err)
		}

		if _, err := f.s.GetStack(t.Context(), newRef); !errors.Is(err, stack.ErrNotFound) {
			t.Fatalf("get %s after a refused rename: err = %v, want ErrNotFound", newRef, err)
		}

		if _, err := f.s.ActiveUpdate(t.Context(), oldRef); err != nil {
			t.Fatalf("active update of %s after a refused rename: %v", oldRef, err)
		}
	case renameErr == nil:
		checkUpdateLost(t, startErr)

		if _, err := f.s.GetStack(t.Context(), newRef); err != nil {
			t.Fatalf("get stack %s after the rename: %v", newRef, err)
		}

		if _, err := f.s.ActiveUpdate(t.Context(), newRef); !errors.Is(err, stack.ErrUpdateNotFound) {
			t.Fatalf("active update of %s after a lost start: err = %v, want ErrUpdateNotFound", newRef, err)
		}
	default:
		t.Fatalf("start err = %v, rename err = %v; want one side to win", startErr, renameErr)
	}
}

// testDeleteDuringRunningUpdate checks that the final delete waits for the
// stack lock: while a mutating update runs it fails with ErrUpdateConflict
// and removes nothing.
func testDeleteDuringRunningUpdate(t *testing.T, f fixture) {
	t.Helper()

	running := f.run(t, f.ref, "u1", mutating())

	deleteErr := f.s.DeleteStack(
		t.Context(),
		f.ref,
		f.fence,
		stack.EmptyGuard(),
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if conflict, ok := errors.AsType[*stack.UpdateConflictError](deleteErr); !ok ||
		!errors.Is(deleteErr, stack.ErrUpdateConflict) || conflict.Active != running.ID {
		t.Fatalf("delete during a running update: err = %v, want ErrUpdateConflict that names %s",
			deleteErr, running.ID)
	}

	if _, err := f.s.GetStack(t.Context(), f.ref); err != nil {
		t.Errorf("get stack after a refused delete: %v", err)
	}

	if _, err := f.s.ActiveUpdate(t.Context(), f.ref); err != nil {
		t.Errorf("active update after a refused delete: %v", err)
	}

	f.end(t, running, stack.StatusSucceeded)

	err := f.s.DeleteStack(
		t.Context(),
		f.ref,
		f.fence,
		stack.EmptyGuard(),
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Errorf("delete after the update ended: %v", err)
	}
}

// resourcedDeployment holds the automatic stack resource and one managed
// resource, so the stack counts as having resources.
const resourcedDeployment = `{"resources":[` +
	`{"type":"pulumi:pulumi:Stack"},` +
	`{"type":"custom:resource:Type"}]}`

// emptyDeployment holds no managed resources: only the automatic stack
// resource.
const emptyDeployment = `{"resources":[{"type":"pulumi:pulumi:Stack"}]}`

// finishWithDeployment runs one mutating update of f.ref with deployment as
// its checkpoint and ends it succeeded.
func finishWithDeployment(t *testing.T, f fixture, id, deployment string) stack.Update {
	t.Helper()

	u := f.run(t, f.ref, id, mutating())
	if _, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, u, 0, deployment), f.fence); err != nil {
		t.Fatalf("put checkpoint of %s: %v", id, err)
	}

	return f.end(t, u, stack.StatusSucceeded)
}

// countedGuard returns the guard of a delete that counted deployment as the
// latest checkpoint of u, as the service does outside the transaction.
func countedGuard(u stack.Update, deployment string) stack.DeleteGuard {
	return stack.DeleteGuard{
		UpdateID:      u.ID,
		Digest:        sha256.Sum256([]byte(deployment)),
		HasCheckpoint: true,
		HasResources:  stack.CountManagedResources([]byte(deployment)) > 0,
	}
}

// testDeleteRefusesResources finishes an update with one managed resource
// and checks that the final delete without force refuses with
// ErrHasResources and removes nothing.
func testDeleteRefusesResources(t *testing.T, f fixture) {
	t.Helper()

	u := finishWithDeployment(t, f, "u1", resourcedDeployment)

	err := f.s.DeleteStack(
		t.Context(),
		f.ref,
		f.fence,
		countedGuard(u, resourcedDeployment),
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if !errors.Is(err, stack.ErrHasResources) {
		t.Fatalf("delete a stack with resources: err = %v, want ErrHasResources", err)
	}

	if _, err := f.s.GetStack(t.Context(), f.ref); err != nil {
		t.Fatalf("get stack after a refused delete: %v", err)
	}

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); err != nil {
		t.Fatalf("latest checkpoint after a refused delete: %v", err)
	}
}

// testDeleteWithForceRemovesResources finishes an update with one managed
// resource and checks that the final delete with force removes the stack
// with its children.
func testDeleteWithForceRemovesResources(t *testing.T, f fixture) {
	t.Helper()

	u := finishWithDeployment(t, f, "u1", resourcedDeployment)

	err := f.s.ForceDeleteStack(
		t.Context(),
		f.ref,
		f.fence,
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete a stack with resources and force: %v", err)
	}

	if _, err := f.s.GetStack(t.Context(), f.ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get deleted stack: err = %v, want ErrNotFound", err)
	}

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("latest checkpoint after delete: err = %v, want ErrNoState", err)
	}

	if _, err := f.s.CheckpointByVersion(t.Context(), f.ref, u.Version); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("checkpoint of version %d after delete: err = %v, want ErrNoState", u.Version, err)
	}
}

// testDeleteFollowsLatestCheckpoint checks that the delete guard reads the
// latest checkpoint: resources in an older version do not block a delete
// when the latest version holds none, and the reverse refuses.
func testDeleteFollowsLatestCheckpoint(t *testing.T, f fixture) {
	t.Helper()

	finishWithDeployment(t, f, "u1", resourcedDeployment)
	latest := finishWithDeployment(t, f, "u2", emptyDeployment)

	err := f.s.DeleteStack(
		t.Context(),
		f.ref,
		f.fence,
		countedGuard(latest, emptyDeployment),
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete after the latest checkpoint dropped resources: %v", err)
	}

	other := f.other(t)
	g := fixture{s: f.s, ref: other, fence: f.fence}
	finishWithDeployment(t, g, "u1", emptyDeployment)
	latest = finishWithDeployment(t, g, "u2", resourcedDeployment)

	err = f.s.DeleteStack(
		t.Context(),
		other,
		f.fence,
		countedGuard(latest, resourcedDeployment),
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if !errors.Is(err, stack.ErrHasResources) {
		t.Fatalf("delete while the latest checkpoint manages resources: err = %v, want ErrHasResources", err)
	}
}

// testDeleteWithBlobCheckpoint covers the row every save writes: a blob key
// and no content. The store cannot count it, so the caller counts outside the
// transaction and passes the guard. An empty deployment deletes without
// force; a resourced one is refused with ErrHasResources; a stale guard
// answers ErrConcurrentModification so the caller recounts; force always
// removes. An empty guard on a blob row answers ErrConcurrentModification:
// the store answers only from the guard.
func testDeleteWithBlobCheckpoint(t *testing.T, f fixture) {
	t.Helper()

	finishWithBlobDeployment(t, f, "u1", emptyDeployment)

	other := f.other(t)
	g := fixture{s: f.s, ref: other, fence: f.fence}
	finishWithBlobDeployment(t, g, "u1", resourcedDeployment)

	err := f.s.DeleteStack(
		t.Context(),
		f.ref,
		f.fence,
		stack.EmptyGuard(),
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if !errors.Is(
		err,
		stack.ErrConcurrentModification,
	) {
		t.Fatalf(
			"delete a blob stack with an empty guard: err = %v, want ErrConcurrentModification",
			err,
		)
	}

	err = f.s.DeleteStack(
		t.Context(),
		other,
		f.fence,
		stack.EmptyGuard(),
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if !errors.Is(
		err,
		stack.ErrConcurrentModification,
	) {
		t.Fatalf(
			"delete a resourced blob stack with an empty guard: err = %v, want ErrConcurrentModification",
			err,
		)
	}

	emptyGuard := stack.DeleteGuard{
		UpdateID: "u1", Digest: sha256.Sum256([]byte(emptyDeployment)),
		HasCheckpoint: true, HasResources: false,
	}

	err = f.s.DeleteStack(
		t.Context(),
		f.ref,
		f.fence,
		emptyGuard,
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete a blob stack whose counted checkpoint holds no resources: %v", err)
	}

	if _, err = f.s.GetStack(t.Context(), f.ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get deleted stack: err = %v, want ErrNotFound", err)
	}

	resourcedGuard := stack.DeleteGuard{
		UpdateID: "u1", Digest: sha256.Sum256([]byte(resourcedDeployment)),
		HasCheckpoint: true, HasResources: true,
	}

	err = f.s.DeleteStack(
		t.Context(),
		other,
		f.fence,
		resourcedGuard,
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if !errors.Is(
		err,
		stack.ErrHasResources,
	) {
		t.Fatalf(
			"delete a blob stack whose counted checkpoint manages resources: err = %v, want ErrHasResources",
			err,
		)
	}

	if _, err = f.s.GetStack(t.Context(), other); err != nil {
		t.Fatalf("get stack after a refused delete: %v", err)
	}

	thirdSt := newStack(f.ref.Namespace, f.ref.Project.String(), "race")
	if err = f.s.CreateStack(t.Context(), thirdSt, f.fence); err != nil {
		t.Fatalf("create stack %s: %v", thirdSt.Ref, err)
	}

	thirdRef := thirdSt.Ref
	h := fixture{s: f.s, ref: thirdRef, fence: f.fence}

	// A new checkpoint between the count and the delete races: the stale
	// guard fails with ErrConcurrentModification, and the fresh guard
	// refuses with ErrHasResources.
	finishWithBlobDeployment(t, h, "u1", emptyDeployment)
	stale := stack.DeleteGuard{
		UpdateID: "u1", Digest: sha256.Sum256([]byte(emptyDeployment)),
		HasCheckpoint: true, HasResources: false,
	}
	finishWithBlobDeployment(t, h, "u2", resourcedDeployment)

	err = f.s.DeleteStack(
		t.Context(),
		thirdRef,
		f.fence,
		stale,
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if !errors.Is(
		err,
		stack.ErrConcurrentModification,
	) {
		t.Fatalf("delete with a stale blob guard: err = %v, want ErrConcurrentModification", err)
	}

	fresh := stack.DeleteGuard{
		UpdateID: "u2", Digest: sha256.Sum256([]byte(resourcedDeployment)),
		HasCheckpoint: true, HasResources: true,
	}

	err = f.s.DeleteStack(
		t.Context(),
		thirdRef,
		f.fence,
		fresh,
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if !errors.Is(err, stack.ErrHasResources) {
		t.Fatalf("delete after the race with a fresh guard: err = %v, want ErrHasResources", err)
	}

	err = f.s.ForceDeleteStack(
		t.Context(),
		thirdRef,
		f.fence,
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("force delete a resourced blob stack: %v", err)
	}
}

// finishWithBlobDeployment runs one mutating update of f.ref with deployment
// as its blob checkpoint under the key of the stack and ends it succeeded.
func finishWithBlobDeployment(t *testing.T, f fixture, id, deployment string) {
	t.Helper()

	u := f.run(t, f.ref, id, mutating())
	cp := newBlobCheckpoint(u, f.blobKey(t, u, 0), 0, deployment)

	if _, err := f.s.PutCheckpoint(t.Context(), cp, f.fence); err != nil {
		t.Fatalf("put blob checkpoint of %s: %v", id, err)
	}

	f.end(t, u, stack.StatusSucceeded)
}

// testDeleteRaceWithFinishingUpdate reproduces the race of issue #207: a
// delete without force runs while an update that adds resources finishes.
// The check runs inside the delete transaction, so the delete never drops
// the stack: it fails with ErrUpdateConflict while the update runs and with
// ErrConcurrentModification once it finished, because the empty guard
// counted no checkpoint.
func testDeleteRaceWithFinishingUpdate(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "racer", mutating())
	want := f.checkpoint(t, u, 0, resourcedDeployment)

	var (
		completeErr error
		deleteErr   error
	)

	var wg sync.WaitGroup

	wg.Go(func() {
		if _, err := f.s.PutCheckpoint(t.Context(), want, f.fence); err != nil {
			completeErr = err

			return
		}

		completeErr = f.s.SaveUpdate(t.Context(), ended(u, stack.StatusSucceeded), f.fence)
	})
	wg.Go(func() {
		deleteErr = f.s.DeleteStack(
			t.Context(),
			f.ref,
			f.fence,
			stack.EmptyGuard(),
			inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
		)
	})

	wg.Wait()

	if completeErr != nil {
		t.Fatalf("complete the update: %v", completeErr)
	}

	if deleteErr == nil {
		t.Fatal("delete during a finishing update succeeded, want ErrUpdateConflict or ErrConcurrentModification")
	}

	if !errors.Is(deleteErr, stack.ErrUpdateConflict) && !errors.Is(deleteErr, stack.ErrConcurrentModification) {
		t.Fatalf(
			"delete during a finishing update: err = %v, want ErrUpdateConflict or ErrConcurrentModification",
			deleteErr,
		)
	}

	if _, err := f.s.GetStack(t.Context(), f.ref); err != nil {
		t.Fatalf("get stack after a refused delete: %v", err)
	}
}

// testDeleteRaceWithNewUpdate reproduces the window of issue #207 at the
// store: a delete without force runs while a whole update starts and ends
// with resources. The delete and the update serialize on the stack, so they
// never both succeed: either the update wins and the delete refuses, or the
// delete wins and the update finds no stack.
func testDeleteRaceWithNewUpdate(t *testing.T, f fixture) {
	t.Helper()

	var (
		updateErr error
		deleteErr error
	)

	var wg sync.WaitGroup

	wg.Go(func() {
		updateErr = runResourcedUpdate(t, f)
	})
	wg.Go(func() {
		deleteErr = f.s.DeleteStack(
			t.Context(),
			f.ref,
			f.fence,
			stack.EmptyGuard(),
			inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
		)
	})

	wg.Wait()
	checkDeleteRace(t, f, updateErr, deleteErr)
}

// runResourcedUpdate creates, starts, checkpoints and ends one update with
// resources, returning the first error it meets.
func runResourcedUpdate(t *testing.T, f fixture) error {
	t.Helper()

	u := newUpdate(f.ref, "racer", mutating(), "", 0)
	if err := f.s.CreateUpdate(t.Context(), u, UnrecordedKey(), f.fence); err != nil {
		return err
	}

	startedUpdate, err := f.s.StartUpdate(t.Context(), started(t, u), f.fence)
	if err != nil {
		return err
	}

	cp := f.checkpoint(t, startedUpdate, 0, resourcedDeployment)
	if _, err := f.s.PutCheckpoint(t.Context(), cp, f.fence); err != nil {
		return err
	}

	return f.s.SaveUpdate(t.Context(), ended(startedUpdate, stack.StatusSucceeded), f.fence)
}

// checkDeleteRace proves that a delete without force and a concurrent update
// with resources never both succeed: either the update wins and the delete
// refuses, or the delete wins and the update finds no stack.
func checkDeleteRace(t *testing.T, f fixture, updateErr, deleteErr error) {
	t.Helper()

	switch {
	case updateErr == nil && deleteErr == nil:
		t.Fatal("delete and update both succeeded: the delete dropped resources")
	case updateErr == nil:
		checkDeleteLost(t, f, deleteErr)
	case deleteErr == nil:
		checkUpdateLost(t, updateErr)
	default:
		t.Fatalf("update err = %v, delete err = %v; want one side to win", updateErr, deleteErr)
	}
}

// checkDeleteLost proves a delete that lost to the update refused with the
// lock or the stale empty guard, and removed nothing.
func checkDeleteLost(t *testing.T, f fixture, deleteErr error) {
	t.Helper()

	if !errors.Is(deleteErr, stack.ErrUpdateConflict) && !errors.Is(deleteErr, stack.ErrConcurrentModification) {
		t.Fatalf(
			"delete lost to the update: err = %v, want ErrUpdateConflict or ErrConcurrentModification",
			deleteErr,
		)
	}

	if _, err := f.s.GetStack(t.Context(), f.ref); err != nil {
		t.Fatalf("get stack after a refused delete: %v", err)
	}
}

// checkUpdateLost proves an update that lost to the delete found no stack.
func checkUpdateLost(t *testing.T, updateErr error) {
	t.Helper()

	if !errors.Is(updateErr, stack.ErrNotFound) && !errors.Is(updateErr, stack.ErrUpdateNotFound) &&
		!errors.Is(updateErr, stack.ErrUpdateConflict) {
		t.Fatalf("update lost to the delete: err = %v, want ErrNotFound, ErrUpdateNotFound or ErrUpdateConflict",
			updateErr)
	}
}

// newEvents returns a batch for u under its current lease, one event per
// sequence number, with the payload naming the number and the given round.
func newEvents(u stack.Update, round int, seqs ...int64) stack.EventBatch {
	events := make([]stack.Event, 0, len(seqs))
	for _, seq := range seqs {
		events = append(events, stack.Event{
			Timestamp: now().Add(time.Duration(seq) * time.Millisecond),
			Payload:   fmt.Appendf(nil, "{\"sequence\": %d, \"round\":%d}", seq, round),
			Sequence:  seq,
		})
	}

	return stack.EventBatch{Events: events, Ref: u.Ref, Update: u.ID, Lease: u.Lease.Digest}
}

func testEvents(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())

	if got, err := f.s.ListEvents(t.Context(), f.ref, u.ID, -1, 10); err != nil || len(got) != 0 {
		t.Fatalf("events of a new update = %v, %v; want empty", got, err)
	}

	for _, batch := range []stack.EventBatch{
		newEvents(u, 1, 0, 1, 2),
		newEvents(u, 2, 2, 3, 5), // retried 2 is skipped, the first write wins
		newEvents(u, 3, 4),
		newEvents(u, 4),
	} {
		if err := f.s.AppendEvents(t.Context(), batch, f.fence); err != nil {
			t.Fatalf("append events: %v", err)
		}
	}

	other := f.run(t, f.other(t), "d1", mutating())
	if err := f.s.AppendEvents(t.Context(), newEvents(other, 9, 0, 1), f.fence); err != nil {
		t.Fatalf("append events of another update: %v", err)
	}

	want := slices.Concat(newEvents(u, 1, 0, 1, 2).Events, newEvents(u, 2, 3).Events,
		newEvents(u, 3, 4).Events, newEvents(u, 2, 5).Events)

	for _, tc := range []struct {
		want  []stack.Event
		after int64
		limit int
	}{
		{after: -1, limit: 100, want: want},
		{after: -1, limit: 2, want: want[:2]},
		{after: 1, limit: 3, want: want[2:5]},
		{after: 5, limit: 10, want: nil},
	} {
		got, err := f.s.ListEvents(t.Context(), f.ref, u.ID, tc.after, tc.limit)
		if err != nil {
			t.Fatalf("list events after %d: %v", tc.after, err)
		}

		if !sameEvents(got, tc.want) {
			t.Errorf(
				"events after %d, limit %d = %s, want %s",
				tc.after,
				tc.limit,
				eventsString(got),
				eventsString(tc.want),
			)
		}
	}

	// Events stay readable after the update ends.
	f.end(t, u, stack.StatusSucceeded)

	if got, err := f.s.ListEvents(t.Context(), f.ref, u.ID, -1, 100); err != nil || !sameEvents(got, want) {
		t.Fatalf("events after the update ended = %s, %v; want %s", eventsString(got), err, eventsString(want))
	}
}

func testEventsLease(t *testing.T, f fixture) {
	t.Helper()

	waiting := f.create(t, f.ref, "waiting", mutating())
	if err := f.s.AppendEvents(t.Context(), newEvents(waiting, 1, 0), f.fence); !errors.Is(
		err, stack.ErrWrongUpdateState,
	) {
		t.Fatalf("events of a not-started update: err = %v, want ErrWrongUpdateState", err)
	}

	u := f.run(t, f.ref, "u1", mutating())

	forged := newEvents(u, 1, 0)
	forged.Lease = sha256.Sum256([]byte("another lease"))

	if err := f.s.AppendEvents(t.Context(), forged, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("events under another lease: err = %v, want ErrWrongUpdateState", err)
	}

	late := newEvents(u, 1, 0)
	f.end(t, u, stack.StatusCanceled)

	if err := f.s.AppendEvents(t.Context(), late, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("events after cancel: err = %v, want ErrWrongUpdateState", err)
	}

	assertEvents(t, f, u)
}

// testRenameKeepsHistory shows that a rename keeps the stack ID, so its
// history, checkpoint and events stay with the stack.
func testRenameKeepsHistory(t *testing.T, f fixture) {
	t.Helper()

	ended, wantCP, wantEvents := seedRenameHistory(t, f)
	oldID := f.stackID(t, f.ref)
	newRef := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "renamed"}

	if err := f.s.RenameStack(t.Context(), f.ref, "renamed", f.fence); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	checkRenamedStack(t, f, newRef, oldID)
	checkRenamedUpdate(t, f, newRef, ended)
	checkRenamedHistory(t, f, newRef, ended)
	checkRenamedCheckpoint(t, f, newRef, wantCP)
	checkRenamedEvents(t, f, newRef, ended, wantEvents)

	next := f.run(t, newRef, "u2", mutating())
	if next.Version != 2 {
		t.Fatalf("update after rename version = %d, want 2", next.Version)
	}
}

// seedRenameHistory creates one finished update with a checkpoint and events.
func seedRenameHistory(
	t *testing.T, f fixture,
) (stack.Update, stack.Checkpoint, stack.EventBatch) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())

	wantCP := f.checkpoint(t, u, 0, `{"v":1}`)
	if _, err := f.s.PutCheckpoint(t.Context(), wantCP, f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	wantEvents := newEvents(u, 1, 0, 1)
	if err := f.s.AppendEvents(t.Context(), wantEvents, f.fence); err != nil {
		t.Fatalf("append events: %v", err)
	}

	ended := f.end(t, u, stack.StatusSucceeded)

	// Reads report the byte length of the saved checkpoint with the update.
	// Reload it so the want carries what the store reports.
	stored, err := f.s.GetUpdate(t.Context(), ended.Ref, ended.ID)
	if err != nil {
		t.Fatalf("get update %s: %v", ended.ID, err)
	}

	ended = stored

	return ended, wantCP, wantEvents
}

// checkRenamedStack checks the ID stayed and the old name is gone.
func checkRenamedStack(t *testing.T, f fixture, newRef stack.Ref, oldID string) {
	t.Helper()

	gotStack, err := f.s.GetStack(t.Context(), newRef)
	if err != nil {
		t.Fatalf("get renamed stack: %v", err)
	}

	if gotStack.ID.String() != oldID {
		t.Fatalf("renamed stack ID = %q, want %q", gotStack.ID, oldID)
	}

	_, oldErr := f.s.GetStack(t.Context(), f.ref)
	if !errors.Is(oldErr, stack.ErrNotFound) {
		t.Fatalf("get old stack: err = %v, want ErrNotFound", oldErr)
	}
}

// checkRenamedUpdate checks the update moved to the new name. The checkpoint
// size travels on the update itself, so it follows the rename; the history
// cases pin the sizes, so this check copies the stored size.
func checkRenamedUpdate(t *testing.T, f fixture, newRef stack.Ref, ended stack.Update) {
	t.Helper()

	gotUpdate, err := f.s.GetUpdate(t.Context(), newRef, ended.ID)
	if err != nil {
		t.Fatalf("get update after rename: %v", err)
	}

	ended.Ref = newRef
	ended.CheckpointBytes = gotUpdate.CheckpointBytes

	if !sameUpdate(gotUpdate, ended) {
		t.Fatalf("update after rename = %+v, want %+v", gotUpdate, ended)
	}

	_, oldErr := f.s.GetUpdate(t.Context(), f.ref, ended.ID)
	if !errors.Is(oldErr, stack.ErrUpdateNotFound) {
		t.Fatalf("get update through old name: err = %v, want ErrUpdateNotFound", oldErr)
	}
}

// checkRenamedHistory checks the history moved to the new name.
func checkRenamedHistory(t *testing.T, f fixture, newRef stack.Ref, ended stack.Update) {
	t.Helper()

	history, err := f.s.ListUpdates(t.Context(), newRef, stack.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("history after rename: %v", err)
	}

	if len(history) != 1 || history[0].ID != ended.ID || history[0].Ref != newRef {
		t.Fatalf("history after rename = %+v, want one update of %s", history, newRef)
	}

	oldHistory, oldErr := f.s.ListUpdates(t.Context(), f.ref, stack.Page{Limit: 100, Offset: 0})
	if oldErr != nil || len(oldHistory) != 0 {
		t.Fatalf("history of old name = %v, %v; want empty", oldHistory, oldErr)
	}
}

// checkRenamedCheckpoint checks the checkpoint moved to the new name.
func checkRenamedCheckpoint(t *testing.T, f fixture, newRef stack.Ref, wantCP stack.Checkpoint) {
	t.Helper()

	wantCP.Ref = newRef

	assertCheckpoint(t, fixture{s: f.s, ref: newRef, fence: f.fence}, newRef, wantCP)

	_, oldErr := f.s.LatestCheckpoint(t.Context(), f.ref)
	if !errors.Is(oldErr, stack.ErrNoState) {
		t.Fatalf("checkpoint of old name: err = %v, want ErrNoState", oldErr)
	}
}

// checkRenamedEvents checks the events moved to the new name.
func checkRenamedEvents(
	t *testing.T, f fixture, newRef stack.Ref, ended stack.Update, wantEvents stack.EventBatch,
) {
	t.Helper()

	gotEvents, err := f.s.ListEvents(t.Context(), newRef, ended.ID, -1, 100)
	if err != nil {
		t.Fatalf("events after rename: %v", err)
	}

	if !sameEvents(gotEvents, wantEvents.Events) {
		t.Fatalf(
			"events after rename = %s, want %s",
			eventsString(gotEvents),
			eventsString(wantEvents.Events),
		)
	}

	_, oldErr := f.s.ListEvents(t.Context(), f.ref, ended.ID, -1, 100)
	if !errors.Is(oldErr, stack.ErrUpdateNotFound) {
		t.Fatalf("events of old name: err = %v, want ErrUpdateNotFound", oldErr)
	}
}

// stackIDOf returns the stable ID of the stack that ref names now.
func (f fixture) stackIDOf(t *testing.T, ref stack.Ref) stack.ID {
	t.Helper()

	return stack.ID(f.stackID(t, ref))
}

func (f fixture) stackID(t *testing.T, ref stack.Ref) string {
	t.Helper()

	st, err := f.s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack %s: %v", ref, err)
	}

	return st.ID.String()
}

// testInventoryAfterUpdateFinish ends a mutating update and checks the entry
// follows the update's end time.
func testInventoryAfterUpdateFinish(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	done := f.end(t, u, stack.StatusSucceeded)

	entries, err := f.s.ListUnits(t.Context(), f.ref.Namespace)
	if err != nil {
		t.Fatalf("list inventory: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("inventory entries = %+v, want one", entries)
	}

	got := entries[0]
	wantName := f.ref.Project.String() + "/" + f.ref.Stack.String()

	if got.Tool != "pulumi" || got.Namespace != f.ref.Namespace || got.Name != wantName ||
		got.UnitID != f.stackID(t, f.ref) || got.DisplayName != wantName ||
		!got.LastChanged.Equal(done.EndedAt) || got.Status != "" || got.ResourceCount != nil {
		t.Fatalf("inventory entry = %+v, want pulumi %q changed %v", got, wantName, done.EndedAt)
	}

	if got.Lock != nil {
		t.Fatalf("inventory lock after finish = %+v, want none", got.Lock)
	}
}

// testInventoryHoldsRunningLock starts a mutating update and checks the
// entry holds its lock in the same transaction as the start: the creator
// with the update kind, since its start. Ending the update clears it in the
// same transaction as the end itself.
func testInventoryHoldsRunningLock(t *testing.T, f fixture) {
	t.Helper()

	plan := stack.Plan{Kind: stack.KindRefresh, DryRun: false, CreatedBy: "admin", RestoredFrom: 0, GitHead: ""}
	u := f.run(t, f.ref, "u1", plan)

	entries := listUpdatesInventory(t, f)
	if len(entries) != 1 {
		t.Fatalf("inventory entries = %+v, want one", entries)
	}

	assertStackLock(t, entries[0].Lock, "admin", "refresh", u.StartedAt)

	done := f.end(t, u, stack.StatusSucceeded)

	entries = listUpdatesInventory(t, f)
	if len(entries) != 1 {
		t.Fatalf("inventory entries after finish = %+v, want one", entries)
	}

	if entries[0].Lock != nil {
		t.Fatalf("inventory lock after finish = %+v, want none", entries[0].Lock)
	}

	if !entries[0].LastChanged.Equal(done.EndedAt) {
		t.Fatalf("inventory change = %v, want the end %v", entries[0].LastChanged, done.EndedAt)
	}
}

// testInventoryIgnoresPreviewLock starts a preview and a dry run and checks
// neither publishes a lock: only a mutating update holds the stack.
func testInventoryIgnoresPreviewLock(t *testing.T, f fixture) {
	t.Helper()

	f.run(t, f.ref, "u1", preview())

	entries := listUpdatesInventory(t, f)
	if len(entries) != 1 {
		t.Fatalf("inventory entries = %+v, want one", entries)
	}

	if entries[0].Lock != nil {
		t.Fatalf("inventory lock of a preview = %+v, want none", entries[0].Lock)
	}

	other := f.other(t)
	f.run(t, other, "u1", dryRun())

	entries = listUpdatesInventory(t, f)
	if len(entries) != 2 {
		t.Fatalf("inventory entries = %+v, want two", entries)
	}

	for i := range entries {
		if entries[i].Lock != nil {
			t.Fatalf("inventory lock of %q = %+v, want none", entries[i].Name, entries[i].Lock)
		}
	}
}

// testInventoryKeepsLockAfterLeaseExpiry starts an update whose lease already
// expired and checks the entry still holds its lock. An expired lease still
// holds the stack lock until someone cancels the update, so the projection
// clears only on the terminal transition, never on expiry.
func testInventoryKeepsLockAfterLeaseExpiry(t *testing.T, f fixture) {
	t.Helper()

	u := f.create(t, f.ref, "u1", mutating())

	tok, err := token.New()
	if err != nil {
		t.Fatalf("new lease token: %v", err)
	}

	u.Status = stack.StatusRunning
	u.StartedAt = now().Add(time.Second)
	u.Lease = stack.Lease{ExpiresAt: now().Add(-time.Minute), Digest: tok.Digest()}
	u.Revision++

	got, err := f.s.StartUpdate(t.Context(), u, f.fence)
	if err != nil {
		t.Fatalf("start update with an expired lease: %v", err)
	}

	entries := listUpdatesInventory(t, f)
	if len(entries) != 1 {
		t.Fatalf("inventory entries = %+v, want one", entries)
	}

	assertStackLock(t, entries[0].Lock, "", string(stack.KindUpdate), got.StartedAt)
}

// testUpdateInventoryLockFenced checks a stale fence fails both lock writes and
// writes nothing: a start publishes no lock, and a fenced end keeps the
// running lock it found.
func testUpdateInventoryLockFenced(t *testing.T, f fixture) {
	t.Helper()

	stale := namespace.Fence{Namespace: f.ref.Namespace, FailoverVersion: f.fence.FailoverVersion + 1}
	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "admin", RestoredFrom: 0, GitHead: ""}

	u := f.create(t, f.ref, "u1", plan)

	if _, err := f.s.StartUpdate(t.Context(), started(t, u), stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("start with a stale fence: err = %v, want ErrFenced", err)
	}

	entries := listUpdatesInventory(t, f)
	if len(entries) != 1 {
		t.Fatalf("inventory entries = %+v, want one", entries)
	}

	if entries[0].Lock != nil {
		t.Fatalf("inventory lock after a fenced start = %+v, want none", entries[0].Lock)
	}

	running := f.start(t, u)

	entries = listUpdatesInventory(t, f)
	assertStackLock(t, entries[0].Lock, "admin", "update", running.StartedAt)

	if err := f.s.SaveUpdate(t.Context(), ended(running, stack.StatusSucceeded), stale); !errors.Is(
		err, namespace.ErrFenced,
	) {
		t.Fatalf("end with a stale fence: err = %v, want ErrFenced", err)
	}

	entries = listUpdatesInventory(t, f)
	assertStackLock(t, entries[0].Lock, "admin", "update", running.StartedAt)
}

// assertStackLock checks a projection lock against its holder, operation and
// take time. It fails the test on a missing lock or a mismatch, so the holder
// is always the subject the update records, never a token or a secret.
func assertStackLock(t *testing.T, got *inventory.Lock, holder, operation string, since time.Time) {
	t.Helper()

	if got == nil {
		t.Fatalf("inventory lock is missing, want holder %q operation %q", holder, operation)
	}

	if got.Holder != holder || got.Operation != operation || !got.Since.Equal(since) {
		t.Fatalf("inventory lock = %+v, want holder %q operation %q since %v", got, holder, operation, since)
	}
}

// listUpdatesInventory returns the inventory entries of the fixture stack's
// namespace, failing the test on error.
func listUpdatesInventory(t *testing.T, f fixture) []inventory.Entry {
	t.Helper()

	entries, err := f.s.ListUnits(t.Context(), f.ref.Namespace)
	if err != nil {
		t.Fatalf("list inventory: %v", err)
	}

	return entries
}

func assertUpdate(t *testing.T, f fixture, want stack.Update) {
	t.Helper()

	got, err := f.s.GetUpdate(t.Context(), want.Ref, want.ID)
	if err != nil {
		t.Fatalf("get update %s: %v", want.ID, err)
	}

	if !sameUpdate(got, want) {
		t.Fatalf("update %s = %+v, want %+v", want.ID, got, want)
	}
}

func sameUpdate(a, b stack.Update) bool {
	return sameUpdateRecord(a, b) && sameUpdateTimes(a, b)
}

// sameUpdateRecord compares the fields of an update that are not times.
func sameUpdateRecord(a, b stack.Update) bool {
	return a.ID == b.ID && a.Ref == b.Ref && a.Kind == b.Kind && a.Status == b.Status &&
		a.DryRun == b.DryRun && a.Version == b.Version && a.Revision == b.Revision &&
		a.CreatedBy == b.CreatedBy && a.RestoredFrom == b.RestoredFrom && a.TokenID == b.TokenID &&
		a.CheckpointBytes == b.CheckpointBytes && sameLease(a.Lease, b.Lease)
}

func sameLease(a, b stack.Lease) bool {
	return a.Digest == b.Digest && sameTime(a.ExpiresAt, b.ExpiresAt)
}

func sameUpdateTimes(a, b stack.Update) bool {
	return sameTime(a.CreatedAt, b.CreatedAt) && sameTime(a.StartedAt, b.StartedAt) && sameTime(a.EndedAt, b.EndedAt)
}

// sameTime compares instants, and requires a zero time to stay zero.
func sameTime(a, b time.Time) bool {
	return a.IsZero() == b.IsZero() && a.Equal(b)
}

func assertCheckpoint(t *testing.T, f fixture, ref stack.Ref, want stack.Checkpoint) {
	t.Helper()

	got, err := f.s.LatestCheckpoint(t.Context(), ref)
	if err != nil {
		t.Fatalf("latest checkpoint of %s: %v", ref, err)
	}

	assertSameCheckpoint(t, got, want, "latest checkpoint")
}

// assertCheckpointVersion checks the checkpoint of one version.
func assertCheckpointVersion(t *testing.T, f fixture, ref stack.Ref, version int64, want stack.Checkpoint) {
	t.Helper()

	got, err := f.s.CheckpointByVersion(t.Context(), ref, version)
	if err != nil {
		t.Fatalf("checkpoint of version %d of %s: %v", version, ref, err)
	}

	assertSameCheckpoint(t, got, want, "checkpoint of version")
}

func assertSameCheckpoint(t *testing.T, got, want stack.Checkpoint, what string) {
	t.Helper()

	// The store keeps no content: a read returns the metadata only.
	if !sameCheckpointRow(got, want) || !sameCheckpointBlob(got, want) ||
		len(got.Deployment) != 0 || !slices.Equal(got.Features, want.Features) {
		t.Fatalf("%s = %+v (deployment %q), want %+v without a deployment", what, got, got.Deployment, want)
	}
}

// sameCheckpointRow compares the row fields of two checkpoints: the update,
// the lease, the version parts and the times.
func sameCheckpointRow(got, want stack.Checkpoint) bool {
	return got.Ref == want.Ref && got.Update == want.Update && got.Lease == want.Lease &&
		got.SchemaVersion == want.SchemaVersion && got.Sequence == want.Sequence &&
		got.Digest == want.Digest && got.Invalid == want.Invalid && sameTime(got.SavedAt, want.SavedAt)
}

// sameCheckpointBlob compares the blob metadata of two checkpoints: the key
// with the size and MD5 of the stored bytes.
func sameCheckpointBlob(got, want stack.Checkpoint) bool {
	return got.BlobKey == want.BlobKey && got.BlobSize == want.BlobSize && got.BlobMD5 == want.BlobMD5
}

// assertEvents checks that update u has no events.
func assertEvents(t *testing.T, f fixture, u stack.Update) {
	t.Helper()

	got, err := f.s.ListEvents(t.Context(), u.Ref, u.ID, -1, 100)
	if err != nil {
		t.Fatalf("list events of %s: %v", u.ID, err)
	}

	if len(got) != 0 {
		t.Fatalf("events of %s = %s, want none", u.ID, eventsString(got))
	}
}

func sameEvents(a, b []stack.Event) bool {
	return slices.EqualFunc(a, b, func(x, y stack.Event) bool {
		return x.Sequence == y.Sequence && sameTime(x.Timestamp, y.Timestamp) && bytes.Equal(x.Payload, y.Payload)
	})
}

func eventsString(events []stack.Event) string {
	var b bytes.Buffer

	for _, e := range events {
		fmt.Fprintf(&b, "[%d %s] ", e.Sequence, e.Payload)
	}

	return b.String()
}

// testCheckpointReadsCarryIDs checks that each read of a checkpoint returns the
// ID of the namespace row and the ID of the stack row, from the query of the
// read, and that the read of one stack returns the namespace ID too. The
// service builds the scope of the blob of a row from them, without a second
// read.
func testCheckpointReadsCarryIDs(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	if _, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, u, 0, `{"v":1}`), f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	ns, err := f.s.GetNamespace(t.Context(), f.ref.Namespace)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	st, err := f.s.GetStack(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if st.NamespaceID != ns.ID || ns.ID == "" {
		t.Fatalf("stack namespace ID = %q, want %q", st.NamespaceID, ns.ID)
	}

	latest, latestErr := f.s.LatestCheckpoint(t.Context(), f.ref)
	byVersion, versionErr := f.s.CheckpointByVersion(t.Context(), f.ref, u.Version)
	byUpdate, updateErr := f.s.CheckpointByUpdate(t.Context(), f.ref, u.ID)

	if err := errors.Join(latestErr, versionErr, updateErr); err != nil {
		t.Fatalf("read checkpoints: %v", err)
	}

	for name, cp := range map[string]*stack.Checkpoint{"latest": &latest, "by version": &byVersion, "by update": &byUpdate} {
		if cp.NamespaceID != ns.ID || cp.StackID != st.ID {
			t.Errorf("%s: namespace ID %q, stack ID %q; want %q, %q", name, cp.NamespaceID, cp.StackID, ns.ID, st.ID)
		}
	}
}

// testCheckpointKeyBelongsToUpdate checks that a checkpoint write names a key
// of its own update, stack and namespace, through both the save and the built
// checkpoint write. The key of another namespace, of the old layout, of
// another directory of the namespace, of another stack or another update of
// the same namespace, of a journal batch and a key that is deeper or has no
// number is refused with ErrCheckpointKey, and the store keeps no row. The
// service builds the key, so this layer holds when another caller of the store
// builds it by other means.
func testCheckpointKeyBelongsToUpdate(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	other := f.run(
		t,
		f.ref,
		"u2",
		stack.Plan{Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)

	st, err := f.s.GetStack(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	prefix := stackBlobPrefix(t, f.s, f.ref.Namespace)
	nsDir := strings.TrimSuffix(prefix, "pulumi/")
	update := u.ID.String()

	keys := map[string]string{
		"another namespace":             "namespaces/ns-other/pulumi/" + st.ID.String() + "/" + update + "/1-attempt0000",
		"no namespace directory":        "pulumi/" + st.ID.String() + "/" + update + "/1-attempt0000",
		"a directory of the namespace":  nsDir + "tofu/" + st.ID.String() + "/" + update + "/1-attempt0000",
		"same namespace, another stack": prefix + "other-stack/" + update + "/1-attempt0000",
		"same stack, another update":    prefix + st.ID.String() + "/" + other.ID.String() + "/1-attempt0000",
		"a journal batch":               prefix + st.ID.String() + "/" + update + "/journal/1",
		"a nested key":                  prefix + st.ID.String() + "/" + update + "/1-attempt0000/2",
		"no number":                     prefix + st.ID.String() + "/" + update + "/x-attempt0000",
		"the placeholder":               string(placeholderKey(u, 1)),
		"no attempt ID":                 prefix + st.ID.String() + "/" + update + "/1",
	}

	for name, key := range keys {
		cp := f.checkpoint(t, u, 1, `{"v":1}`)
		cp.BlobKey = blob.Key(key)

		if _, err := f.s.PutCheckpoint(t.Context(), cp, f.fence); !errors.Is(err, stack.ErrCheckpointKey) {
			t.Errorf("put checkpoint with %s in the key: err = %v, want ErrCheckpointKey", name, err)
		}

		if _, err := f.s.PutBuiltCheckpoint(t.Context(), cp, f.fence); !errors.Is(err, stack.ErrCheckpointKey) {
			t.Errorf("put built checkpoint with %s in the key: err = %v, want ErrCheckpointKey", name, err)
		}
	}

	if _, err := f.s.CheckpointByUpdate(t.Context(), f.ref, u.ID); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("checkpoint after refused writes: err = %v, want ErrNoState", err)
	}

	if _, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, u, 1, `{"v":1}`), f.fence); err != nil {
		t.Fatalf("put checkpoint with the key of the update: %v", err)
	}
}

// testCheckpointReplayOtherKey checks that a second write of one sequence and
// one digest under the key of another attempt is a replay: the store reports
// known, writes nothing and keeps the key of the first write, so the caller
// deletes only its own blob. A write that the store reports as new is not
// known.
func testCheckpointReplayOtherKey(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	first := f.attemptCheckpoint(t, u, 2, `{"n":2}`, "attempt0001")
	second := f.attemptCheckpoint(t, u, 2, `{"n":2}`, "attempt0002")

	known, err := f.s.PutCheckpoint(t.Context(), first, f.fence)
	if err != nil || known {
		t.Fatalf("first write: known = %v, err = %v, want false, nil", known, err)
	}

	known, err = f.s.PutCheckpoint(t.Context(), second, f.fence)
	if err != nil || !known {
		t.Fatalf("replay under another key: known = %v, err = %v, want true, nil", known, err)
	}

	got, err := f.s.CheckpointByUpdate(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("checkpoint by update: %v", err)
	}

	if got.BlobKey != first.BlobKey {
		t.Fatal("the row names another key after a replay: want the key of the first write")
	}

	// The same attempt again also answers known, and the row keeps its key.
	known, err = f.s.PutCheckpoint(t.Context(), first, f.fence)
	if err != nil || !known {
		t.Fatalf("replay under the same key: known = %v, err = %v, want true, nil", known, err)
	}

	assertCheckpoint(t, f, f.ref, first)
}

// testCheckpointConflictOtherKey checks that a write of an earlier sequence,
// or of one sequence with another digest, under the key of another attempt
// fails with its sentinel and keeps the row and the key of the first write.
func testCheckpointConflictOtherKey(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	first := f.attemptCheckpoint(t, u, 2, `{"n":2}`, "attempt0001")

	if _, err := f.s.PutCheckpoint(t.Context(), first, f.fence); err != nil {
		t.Fatalf("first write: %v", err)
	}

	tests := []struct {
		name string
		cp   stack.Checkpoint
		want error
	}{
		{
			"same sequence, other digest",
			f.attemptCheckpoint(t, u, 2, `{"n":"other"}`, "attempt0002"), stack.ErrCheckpointConflict,
		},
		{"earlier sequence", f.attemptCheckpoint(t, u, 1, `{"n":1}`, "attempt0003"), stack.ErrStaleCheckpoint},
	}

	for i := range tests {
		tc := &tests[i]

		known, err := f.s.PutCheckpoint(t.Context(), tc.cp, f.fence)
		if !errors.Is(err, tc.want) || known {
			t.Errorf("%s: known = %v, err = %v, want false, %v", tc.name, known, err, tc.want)
		}
	}

	assertCheckpoint(t, f, f.ref, first)
}

// testCheckpointUnsequencedKeys checks that a write without a sequence
// number is never a replay: each write replaces the row, with the key of its
// own attempt, even for the same digest.
func testCheckpointUnsequencedKeys(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())

	for _, attempt := range []string{"attempt0001", "attempt0002"} {
		cp := f.attemptCheckpoint(t, u, 0, `{"n":"a"}`, attempt)

		known, err := f.s.PutCheckpoint(t.Context(), cp, f.fence)
		if err != nil || known {
			t.Fatalf("write %s: known = %v, err = %v, want false, nil", attempt, known, err)
		}

		assertCheckpoint(t, f, f.ref, cp)
	}
}

// checkpointRounds is the number of rounds of the concurrent checkpoint case.
// A race that the row must decide is rare in one round, so the case repeats it.
const checkpointRounds = 12

// checkpointWriters is the number of writers in one round.
const checkpointWriters = 6

// testCheckpointConcurrentWinner shows that the row decides which of several
// writes of one sequence wins, each under the key of its own attempt. With
// different content, exactly one write lands and each other write fails with
// ErrCheckpointConflict. With the same content, exactly one write lands and
// each other write is known. The row names the key of the winner in both
// cases.
func testCheckpointConcurrentWinner(t *testing.T, f fixture) {
	t.Helper()

	for round := range checkpointRounds {
		for _, same := range []bool{false, true} {
			u := f.run(t, f.ref, "u"+strconv.Itoa(round)+strconv.FormatBool(same), mutating())
			cps := make([]stack.Checkpoint, checkpointWriters)

			for i := range cps {
				body := `{"n":"same"}`
				if !same {
					body = `{"n":` + strconv.Itoa(i) + `}`
				}

				cps[i] = f.attemptCheckpoint(t, u, 2, body, "attempt"+strconv.Itoa(1000+i))
			}

			known, errs := putCheckpointsAtOnce(t, f, cps)

			winner := wantOneCheckpointWinner(t, cps, known, errs, same)

			got, err := f.s.CheckpointByUpdate(t.Context(), f.ref, u.ID)
			if err != nil || got.BlobKey != cps[winner].BlobKey {
				t.Fatalf("round %d: the row names %v, %v; want the key of the winner", round, got.BlobKey, err)
			}

			f.end(t, u, stack.StatusSucceeded)
		}
	}
}

// putCheckpointsAtOnce sends each checkpoint to the store from its own
// goroutine, all at once, and returns the known flag and the error of each
// call, in the order of cps.
func putCheckpointsAtOnce(t *testing.T, f fixture, cps []stack.Checkpoint) ([]bool, []error) {
	t.Helper()

	known := make([]bool, len(cps))
	errs := make([]error, len(cps))

	var wg sync.WaitGroup

	start := make(chan struct{})

	for i := range cps {
		wg.Go(func() {
			<-start
			known[i], errs[i] = f.s.PutCheckpoint(t.Context(), cps[i], f.fence)
		})
	}

	close(start)
	wg.Wait()

	return known, errs
}

// wantOneCheckpointWinner fails the test unless exactly one write landed and
// the others got the answer of their content. It returns the index of the
// winner.
func wantOneCheckpointWinner(t *testing.T, cps []stack.Checkpoint, known []bool, errs []error, same bool) int {
	t.Helper()

	winner, won := -1, 0

	for i := range cps {
		switch {
		case errs[i] == nil && !known[i]:
			winner = i
			won++
		case same && errs[i] == nil && known[i]:
		case !same && errors.Is(errs[i], stack.ErrCheckpointConflict) && !known[i]:
		default:
			t.Fatalf("write %d: known = %v, err = %v, same content = %v", i, known[i], errs[i], same)
		}
	}

	if won != 1 {
		t.Fatalf("%d writes landed, want exactly one (same content = %v)", won, same)
	}

	return winner
}
