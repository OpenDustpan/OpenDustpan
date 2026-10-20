package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// revPause is how long a case waits to prove that a call is still blocked.
const revPause = 300 * time.Millisecond

// revSeed creates one person with one session, and returns the session ID.
func revSeed(t *testing.T, s *Store) string {
	t.Helper()

	ctx := t.Context()
	at := time.Date(2026, time.October, 15, 9, 0, 0, 0, time.UTC)

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-race", Subject: "race", DisplayName: "Race",
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}
	if err := s.CreatePrincipal(ctx, p, storetest.ServerChange(at, principal.Ref(p.ID))); err != nil {
		t.Fatalf("create principal: %v", err)
	}

	ss := session.Session{
		CreatedAt: at, LastSeenAt: at, AuthTime: at, EndedAt: time.Time{}, ID: "ses-race", PrincipalID: p.ID,
		Provider: "corp", EndReason: "", SourceIP: "192.0.2.1", UserAgent: "race",
	}

	digest := sha256.Sum256([]byte("race"))
	if err := s.CreateSession(ctx, ss, digest, storetest.ServerChange(at, session.FullRef(p.ID, ss.ID))); err != nil {
		t.Fatalf("create session: %v", err)
	}

	return ss.ID
}

// revJournalCount returns the count of the rows of the revocation journal.
func revJournalCount(t *testing.T, s *Store) int64 {
	t.Helper()

	var n int64
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM maintenance_revocations`).Scan(&n); err != nil {
		t.Fatalf("count the journal rows: %v", err)
	}

	return n
}

// revFinishesWithinPause waits for done and reports whether it answered
// within revPause, with its error.
func revFinishesWithinPause(done <-chan error) (bool, error) {
	select {
	case err := <-done:
		return true, err
	case <-time.After(revPause):
		return false, nil
	}
}

// TestEnterWaitsForARevocationInFlight proves that an enter cannot slip past a
// revocation whose transaction read the state normal: the journal insert holds
// the installation row FOR SHARE until the revocation commits, and the enter
// updates that row, so it waits. The revocation then commits before the stay
// starts and belongs to normal operation.
func TestEnterWaitsForARevocationInFlight(t *testing.T) {
	t.Parallel()

	s := openKeepLockStore(t, testPostgresURL(t))
	id := revSeed(t, s)

	inFlight, release := make(chan struct{}), make(chan struct{})
	revoked := make(chan error, 1)

	go func() {
		e := storetest.ServerChange(time.Now().UTC(), session.FullRef("prn-race", id))
		rev := revocation{kind: maintenance.RevokedSession, target: id, endReason: string(session.ReasonEnded)}

		revoked <- s.inRevokingTx(context.WithoutCancel(t.Context()), e, rev, func(_ *db.Queries) error {
			close(inFlight)
			<-release

			return nil
		})
	}()

	<-inFlight

	entered := make(chan error, 1)

	go func() {
		entered <- s.SetStateIf(context.WithoutCancel(t.Context()), maintenance.StateNormal, maintenance.StateMaintenance)
	}()

	if finished, early := revFinishesWithinPause(entered); finished {
		t.Fatalf("the enter finished while a revocation was in flight (err = %v), want it to wait", early)
	}

	close(release)

	if revokeErr := <-revoked; revokeErr != nil {
		t.Fatalf("revocation: %v", revokeErr)
	}

	if enterErr := <-entered; enterErr != nil {
		t.Fatalf("enter: %v", enterErr)
	}

	if n := revJournalCount(t, s); n != 0 {
		t.Fatalf("journal rows = %d, want 0: the revocation committed before the stay", n)
	}
}

// TestRevocationDuringEnterIsJournaled proves the other order. An enter has
// written maintenance and has not committed yet. A revocation that starts now
// must not read the old state normal: the journal insert waits for the row,
// then reads maintenance and journals. Without the lock on the row the
// revocation commits at once with no journal row, and a restore would bring
// the revoked session back.
func TestRevocationDuringEnterIsJournaled(t *testing.T) {
	t.Parallel()

	s := openKeepLockStore(t, testPostgresURL(t))
	id := revSeed(t, s)

	enter, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin the enter: %v", err)
	}

	committed := false

	t.Cleanup(func() {
		if !committed {
			if rollbackErr := enter.Rollback(context.WithoutCancel(t.Context())); rollbackErr != nil {
				t.Errorf("roll back the enter: %v", rollbackErr)
			}
		}
	})

	const writeMaintenance = `UPDATE installation SET state = 'maintenance' WHERE id = 1`

	_, execErr := enter.Exec(t.Context(), writeMaintenance)
	if execErr != nil {
		t.Fatalf("write maintenance: %v", execErr)
	}

	revoked := make(chan error, 1)

	go func() {
		e := storetest.ServerChange(time.Now().UTC(), session.FullRef("prn-race", id))
		rev := revocation{kind: maintenance.RevokedSession, target: id, endReason: string(session.ReasonEnded)}

		revoked <- s.inRevokingTx(context.WithoutCancel(t.Context()), e, rev, func(q *db.Queries) error {
			_, endErr := q.EndSession(t.Context(), db.EndSessionParams{
				AtMs: e.At.UnixMilli(), EndReason: string(session.ReasonEnded), ID: id,
			})

			return endErr
		})
	}()

	if finished, early := revFinishesWithinPause(revoked); finished {
		t.Fatalf("the revocation finished while the enter was open (err = %v), want it to wait", early)
	}

	if commitErr := enter.Commit(t.Context()); commitErr != nil {
		t.Fatalf("commit the enter: %v", commitErr)
	}

	committed = true

	if revokeErr := <-revoked; revokeErr != nil {
		t.Fatalf("revocation: %v", revokeErr)
	}

	if n := revJournalCount(t, s); n != 1 {
		t.Fatalf("journal rows = %d, want 1: the revocation ran in the stay", n)
	}
}

// TestRestoreSwapWaitsForARevocationInFlight proves that the swap sees every
// revocation that journaled. A revocation is in flight in the stay: its
// journal row is written and its change is not committed. The swap takes the
// journal in share mode first, so it waits for the revocation. The revocation
// commits in the schema that the swap then drops, and the swap replays its
// journal row, so the restored session ends. Without the lock the swap reads
// the journal without the row, and the restore brings the session back.
func TestRestoreSwapWaitsForARevocationInFlight(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := openKeepLockStore(t, testPostgresURL(t))
	id := revSeed(t, s)

	sink := &keepLockSink{files: map[string]*bytes.Buffer{}}

	sum, files, err := s.ExportPortable(ctx, sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	if _, stageErr := s.StagePortable(ctx, sink.read, sum, files, storetest.StageAs(t, s)); stageErr != nil {
		t.Fatalf("stage portable: %v", stageErr)
	}

	if enterErr := s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance); enterErr != nil {
		t.Fatalf("enter: %v", enterErr)
	}

	inFlight, release := make(chan struct{}), make(chan struct{})
	revoked := make(chan error, 1)

	go func() {
		e := storetest.ServerChange(time.Now().UTC(), session.FullRef("prn-race", id))
		rev := revocation{kind: maintenance.RevokedSession, target: id, endReason: string(session.ReasonEnded)}

		revoked <- s.inRevokingTx(context.WithoutCancel(ctx), e, rev, func(q *db.Queries) error {
			_, endErr := q.EndSession(ctx, db.EndSessionParams{
				AtMs: e.At.UnixMilli(), EndReason: string(session.ReasonEnded), ID: id,
			})

			close(inFlight)
			<-release

			return endErr
		})
	}()

	<-inFlight

	var wg sync.WaitGroup

	applied := make(chan error, 1)

	wg.Go(func() {
		applied <- s.ApplyStaging(context.WithoutCancel(ctx))
	})

	if finished, early := revFinishesWithinPause(applied); finished {
		t.Fatalf("the swap finished while a revocation was in flight (err = %v), want it to wait", early)
	}

	close(release)

	if revokeErr := <-revoked; revokeErr != nil {
		t.Fatalf("revocation: %v", revokeErr)
	}

	wg.Wait()

	if swapErr := <-applied; swapErr != nil {
		t.Fatalf("swap: %v", swapErr)
	}

	got, getErr := s.GetSession(ctx, id)
	if getErr != nil || got.EndedAt.IsZero() {
		t.Fatalf("restored session = %+v, %v, want it ended by the replay", got, getErr)
	}

	var replayed int64

	query := `SELECT count(*) FROM audit_entries WHERE action = $1`
	countErr := s.pool.QueryRow(ctx, query, maintenance.ActionReplayRevocation).Scan(&replayed)
	if countErr != nil || replayed != 1 {
		t.Fatalf("replay audit entries = %d, %v, want 1", replayed, countErr)
	}
}

// TestFailedReplayRollsBackTheSwap proves that a replay step which fails in
// the middle of the swap undoes the whole swap. The staged sessions table
// holds a trigger that refuses the update of the replay. The apply returns
// the error, the live schema still holds the session as the stay ended it,
// and the journal keeps its row, so a later restore can replay it.
func TestFailedReplayRollsBackTheSwap(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := openKeepLockStore(t, testPostgresURL(t))
	id := revSeed(t, s)

	sink := &keepLockSink{files: map[string]*bytes.Buffer{}}

	sum, files, err := s.ExportPortable(ctx, sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	if _, stageErr := s.StagePortable(ctx, sink.read, sum, files, storetest.StageAs(t, s)); stageErr != nil {
		t.Fatalf("stage portable: %v", stageErr)
	}

	if enterErr := s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance); enterErr != nil {
		t.Fatalf("enter: %v", enterErr)
	}

	e := storetest.ServerChange(time.Now().UTC(), session.FullRef("prn-race", id))
	if endErr := s.EndSession(ctx, id, session.ReasonEnded, e); endErr != nil {
		t.Fatalf("end the session in maintenance mode: %v", endErr)
	}

	refuseStagedSessionUpdates(t, s)

	if applyErr := s.ApplyStaging(ctx); applyErr == nil || !strings.Contains(applyErr.Error(), "replay refused") {
		t.Fatalf("apply staging = %v, want the error of the failed replay step", applyErr)
	}

	if n := revJournalCount(t, s); n != 1 {
		t.Fatalf("journal rows after the failed swap = %d, want 1", n)
	}

	var endedMs int64
	row := s.pool.QueryRow(ctx, `SELECT ended_at_ms FROM sessions WHERE id = $1`, id)
	if scanErr := row.Scan(&endedMs); scanErr != nil {
		t.Fatalf("read the live session after the failed swap: %v", scanErr)
	}

	if endedMs == 0 {
		t.Fatal("live session is active after the failed swap, want it still ended")
	}

	if got, stateErr := s.GetState(ctx); stateErr != nil || got != maintenance.StateMaintenance {
		t.Fatalf("state after the failed swap = %q, %v, want maintenance without error", got, stateErr)
	}
}

// refuseStagedSessionUpdates adds a trigger to the staged sessions table that
// fails each update, so the replay of a session revocation fails.
func refuseStagedSessionUpdates(t *testing.T, s *Store) {
	t.Helper()

	staging := pgx.Identifier{s.stagingSchema}.Sanitize()
	refuse := pgx.Identifier{s.stagingSchema, "refuse_replay"}.Sanitize()

	for _, stmt := range []string{
		"CREATE FUNCTION " + refuse + "() RETURNS trigger LANGUAGE plpgsql AS " +
			"$$ BEGIN RAISE EXCEPTION 'replay refused'; END $$",
		"CREATE TRIGGER refuse_replay BEFORE UPDATE ON " + staging + ".sessions " +
			"FOR EACH ROW EXECUTE FUNCTION " + refuse + "()",
	} {
		if _, execErr := s.pool.Exec(t.Context(), stmt); execErr != nil {
			t.Fatalf("add the failing step to the staged schema: %v", execErr)
		}
	}
}
