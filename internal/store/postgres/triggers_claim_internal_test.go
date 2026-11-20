package postgres

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// TestTriggerTxNowReadsTheClockOfTheDatabase proves the layer of the clock: the
// time of a trigger write comes from the database, in the transaction of the
// write. A canceled context fails the read of the database; the clock of the
// process never fails.
func TestTriggerTxNowReadsTheClockOfTheDatabase(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := (triggerTx{q: s.queries}).Now(ctx); err == nil {
		t.Fatal("Now with a canceled context gave no error")
	}
}

// statementLog is a pgx.QueryTracer that keeps the text of each statement that
// its pool sends. The pool can call it from more than one connection, so a
// mutex guards the list.
type statementLog struct {
	mu  sync.Mutex
	sql []string
}

// TraceQueryStart implements pgx.QueryTracer.
func (l *statementLog) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	l.mu.Lock()
	l.sql = append(l.sql, data.SQL)
	l.mu.Unlock()

	return ctx
}

// TraceQueryEnd implements pgx.QueryTracer.
func (*statementLog) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// reset drops the statements that the pool sent so far.
func (l *statementLog) reset() {
	l.mu.Lock()
	l.sql = nil
	l.mu.Unlock()
}

// saw reports whether the pool sent a statement whose text contains query.
func (l *statementLog) saw(query string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, sql := range l.sql {
		if strings.Contains(sql, query) {
			return true
		}
	}

	return false
}

// openWithStatementLog opens a store whose pool reports each statement to log.
func openWithStatementLog(t *testing.T, log *statementLog) *Store {
	t.Helper()

	s, err := Open(t.Context(), membershipURL(t), WithQueryTracer(log))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

// TestTriggerWritesReadTheClockOfTheDatabase proves that Put and ClaimDue each
// read the clock of the database in their transaction, through the
// DatabaseNowMs statement. Without it the slot of a schedule would come from the
// clock of the process, which one instance can have wrong.
func TestTriggerWritesReadTheClockOfTheDatabase(t *testing.T) {
	t.Parallel()

	log := &statementLog{mu: sync.Mutex{}, sql: nil}
	s := openWithStatementLog(t, log)
	seedTriggers(t, s)

	found, err := s.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("read the namespace: %v", err)
	}

	fence := found.Fence()

	// Put reads the clock of the database.
	log.reset()

	tr := trigger.Trigger{
		ID:   "h1",
		Unit: graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "b"},
		Kind: trigger.KindSchedule,
		Frequency: trigger.Frequency{
			Preset: "",
			Cron:   "0 * * * *",
		},
		Source:      graph.Unit{Namespace: "", Tool: "", ID: ""},
		Enabled:     true,
		LastSaver:   "user:ada",
		LastSaverID: "prn-user:ada",
	}

	putErr := s.Triggers().Put(t.Context(), tr, fence, triggerAuditEntry())
	if putErr != nil {
		t.Fatalf("put trigger: %v", putErr)
	}

	if !log.saw("DatabaseNowMs") {
		t.Error("Put did not read the clock of the database")
	}

	// ClaimDue reads the clock of the database too. The slot is the start of the
	// hour before this one on the clock of the database, so a claim of it is due.
	nowMs, err := storeNow(t.Context(), s.queries)
	if err != nil {
		t.Fatalf("read the clock of the database: %v", err)
	}

	past := time.UnixMilli(nowMs).UTC().Truncate(time.Hour).Add(-time.Hour)

	_, execErr := s.pool.Exec(t.Context(), `UPDATE triggers SET next_slot_ms = $1 WHERE id = 'h1'`, past.UnixMilli())
	if execErr != nil {
		t.Fatalf("set the next slot: %v", execErr)
	}

	log.reset()

	claim := trigger.Claim{Namespace: "acme", Trigger: "h1", Slot: past}

	claimErr := s.Triggers().ClaimDue(t.Context(), claim, fence, triggerAuditEntry())
	if claimErr != nil {
		t.Fatalf("claim: %v", claimErr)
	}

	if !log.saw("DatabaseNowMs") {
		t.Error("ClaimDue did not read the clock of the database")
	}
}

// TestSlotColumnRefusesAnEnabledScheduleTriggerWithoutOne proves the layer of the
// table: an enabled schedule trigger cannot lose its next slot, whatever the code
// above does, and a trigger that is off needs none.
func TestSlotColumnRefusesAnEnabledScheduleTriggerWithoutOne(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	seedTriggers(t, s)

	if _, err := s.pool.Exec(t.Context(), `UPDATE triggers SET next_slot_ms = 0 WHERE id = 's1'`); err == nil {
		t.Fatal("an enabled schedule trigger lost its next slot")
	}

	const off = `UPDATE triggers SET enabled = 0, next_slot_ms = 0 WHERE id = 's1'`

	if _, err := s.pool.Exec(t.Context(), off); err != nil {
		t.Fatalf("switch the trigger off: %v", err)
	}

	got, err := s.Triggers().NextSlot(t.Context(), "acme", "s1")
	if err != nil || !got.IsZero() {
		t.Fatalf("next slot of a trigger that is off = %v, %v, want none", got, err)
	}
}

// TestNextSlotColumnRefusesANegativeValue proves the layer of the table: a
// negative slot cannot be stored, whatever the code above does.
func TestNextSlotColumnRefusesANegativeValue(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	seedTriggers(t, s)

	if _, err := s.pool.Exec(t.Context(), `UPDATE triggers SET next_slot_ms = -1 WHERE id = 's1'`); err == nil {
		t.Fatal("a negative next slot was stored")
	}
}

// TestMoveNextSlotComparesTheOldValue proves the guard of the statement: a move
// from a value that is not the stored one changes no row.
func TestMoveNextSlotComparesTheOldValue(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	seedTriggers(t, s)

	slot, err := s.Triggers().NextSlot(t.Context(), "acme", "s1")
	if err != nil || slot.IsZero() {
		t.Fatalf("next slot = %v, %v", slot, err)
	}

	tx := triggerTx{q: s.queries}

	moved, err := tx.MoveNextSlot(t.Context(), "acme", "s1", slot.UnixMilli()+1, slot.UnixMilli()+2)
	if err != nil || moved {
		t.Fatalf("move from a wrong value = %v, %v, want false", moved, err)
	}

	moved, err = tx.MoveNextSlot(t.Context(), "acme", "s1", slot.UnixMilli(), slot.UnixMilli()+2)
	if err != nil || !moved {
		t.Fatalf("move from the stored value = %v, %v, want true", moved, err)
	}
}
