package storetest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// testRestoreKeepsUsedTickets proves that a restore keeps the records of the
// CAS tickets that the live installation used, and brings back none from the
// backup. Ticket A is used before the backup and ticket B after it, both on
// the live store. A restore that dropped either record would let a ticket that
// a server used work again. A ticket that only the store of the backup used
// is not a live record, so the restore does not bring it in.
func testRestoreKeepsUsedTickets(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	src := newStore(t)
	dst := newStore(t)

	srcTickets, srcOK := any(src).(session.TicketStore)
	dstTickets, dstOK := any(dst).(session.TicketStore)

	if !srcOK || !dstOK {
		t.Skip("the store keeps no records of used tickets")
	}

	ps := portableRestoreStores(t, src, dst)
	ctx := t.Context()

	now := time.UnixMilli(1_800_000_000_000)
	until := now.Add(session.UsedTicketTTL)
	use := func(s session.TicketStore, ticket string) error {
		return s.UseTicket(ctx, "corp", session.DigestOfTicket(ticket), now, until)
	}

	if err := use(srcTickets, "ST-only-in-backup-store"); err != nil {
		t.Fatalf("use of the ticket of the backup store: %v", err)
	}

	if err := use(dstTickets, "ST-before-backup"); err != nil {
		t.Fatalf("use of ticket A: %v", err)
	}

	seedBackupStore(t, ps.src)
	sink, sum, files := exportPortable(t, ps.src)

	if err := use(dstTickets, "ST-after-backup"); err != nil {
		t.Fatalf("use of ticket B: %v", err)
	}

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	for _, ticket := range []string{"ST-before-backup", "ST-after-backup"} {
		if err := use(dstTickets, ticket); !errors.Is(err, session.ErrReplay) {
			t.Errorf("use of %s after the restore = %v, want ErrReplay", ticket, err)
		}
	}

	if err := use(dstTickets, "ST-only-in-backup-store"); err != nil {
		t.Errorf(
			"use of the ticket of the backup store after the restore = %v, want nil: a backup holds no ticket records",
			err,
		)
	}
}
