package connectiontest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
)

const (
	// receiptBound is the number of rows that the table of receipts holds at
	// most. It is the bound of the contract, not a value that an adapter may
	// choose.
	receiptBound = 100000
	// receiptMemory is the time that a receipt stays in the table.
	receiptMemory = 24 * time.Hour
	// bodyDigestLength is the length of a hex SHA-256 digest.
	bodyDigestLength = 64

	actionEventAccepted = "git-host-app:event-accepted-old-secret"
)

// receiptCases returns the cases of the event receipts: the two keys of the
// replay rule, the purge, the bound, the checks of a receipt and the audit
// entry in the transaction of the receipt.
func receiptCases() []suiteCase {
	return []suiteCase{
		{"a receipt is stored once", testReceiptStored},
		{"the same delivery ID twice is a duplicate", testReceiptSameDelivery},
		{"the same body digest with another delivery ID is a duplicate", testReceiptSameDigest},
		{"a duplicate writes no audit entry", testReceiptDuplicateNoAudit},
		{"a receipt with an audit entry writes both in one transaction", testReceiptAuditSameTx},
		{"an invalid audit entry stores no receipt", testReceiptBadEntry},
		{"a receipt whose audit append fails stores nothing", testReceiptFailedAppendStoresNothing},
		{"a receipt older than 24 hours is purged by the next receipt", testReceiptPurge},
		{"the table is bounded and the bound counts after the purge", testReceiptBound},
		{"receipts at the same time never pass the bound", testReceiptConcurrentBound},
		{"a receipt that fails its checks is invalid and stores nothing", testReceiptInvalid},
	}
}

func digestOf(seed string) string {
	sum := sha256.Sum256([]byte(seed))

	return hex.EncodeToString(sum[:])
}

// newReceipt returns a valid receipt for the delivery ID id. Its body digest
// derives from id too, so two receipts with different IDs differ in both keys.
func newReceipt(id string, at time.Time) forge.EventReceipt {
	return forge.EventReceipt{
		ReceivedAt: at, Audit: nil, Kind: forge.KindGitHub, DeliveryID: id, BodyDigest: digestOf(id),
		InstallationID: "101", EventKind: forge.EventReview, Repo: "acme/widgets", ReviewID: "7",
	}
}

func receiptTime() time.Time {
	return time.UnixMilli(testTimeMs).UTC()
}

func record(t *testing.T, h Harness, r forge.EventReceipt) bool {
	t.Helper()

	stored, err := h.Store.RecordEventReceipt(t.Context(), r)
	if err != nil {
		t.Fatalf("record receipt %q: %v", r.DeliveryID, err)
	}

	return stored
}

func testReceiptStored(t *testing.T, h Harness) {
	t.Helper()

	if !record(t, h, newReceipt("d1", receiptTime())) {
		t.Fatal("first receipt = false, want true")
	}

	ping := newReceipt("d2", receiptTime())
	ping.EventKind, ping.InstallationID, ping.Repo, ping.ReviewID = forge.EventPing, "", "", ""

	if !record(t, h, ping) {
		t.Fatal("a ping receipt with no installation = false, want true")
	}

	if n := count(t, h, audit.ServerScope); n != 0 {
		t.Fatalf("%d entries, want 0 for receipts with no entry", n)
	}
}

func testReceiptSameDelivery(t *testing.T, h Harness) {
	t.Helper()

	first := newReceipt("d1", receiptTime())

	if !record(t, h, first) {
		t.Fatal("first receipt = false, want true")
	}

	again := first
	again.BodyDigest = digestOf("another body")

	if record(t, h, again) {
		t.Fatal("same delivery ID with another body = true, want false")
	}

	if record(t, h, first) {
		t.Fatal("the same receipt twice = true, want false")
	}

	// The refused receipt wrote nothing: its digest is still free.
	free := newReceipt("d2", receiptTime())
	free.BodyDigest = again.BodyDigest

	if !record(t, h, free) {
		t.Fatal("the digest of a refused receipt = false, want true (the refused receipt wrote nothing)")
	}
}

func testReceiptSameDigest(t *testing.T, h Harness) {
	t.Helper()

	first := newReceipt("d1", receiptTime())

	if !record(t, h, first) {
		t.Fatal("first receipt = false, want true")
	}

	replay := first
	replay.DeliveryID = "d1-with-a-new-delivery-id"

	if record(t, h, replay) {
		t.Fatal("same body digest with another delivery ID = true, want false")
	}

	// The refused receipt wrote nothing: its delivery ID is still free.
	free := newReceipt(replay.DeliveryID, receiptTime())

	if !record(t, h, free) {
		t.Fatal("the delivery ID of a refused receipt = false, want true (the refused receipt wrote nothing)")
	}
}

func testReceiptDuplicateNoAudit(t *testing.T, h Harness) {
	t.Helper()

	first := newReceipt("d1", receiptTime())
	e := entry(audit.ServerScope, actionEventAccepted)
	first.Audit = &e

	if !record(t, h, first) {
		t.Fatal("first receipt = false, want true")
	}

	sameDigest := first
	sameDigest.DeliveryID = "other"

	if record(t, h, first) {
		t.Error("duplicate with the same delivery ID = true, want false")
	}

	if record(t, h, sameDigest) {
		t.Error("duplicate with the same digest = true, want false")
	}

	if n := count(t, h, audit.ServerScope); n != 1 {
		t.Fatalf("%d entries, want 1: a duplicate must write no entry", n)
	}
}

func testReceiptAuditSameTx(t *testing.T, h Harness) {
	t.Helper()

	r := newReceipt("d1", receiptTime())
	e := entry(audit.ServerScope, actionEventAccepted)
	r.Audit = &e

	if !record(t, h, r) {
		t.Fatal("receipt with an entry = false, want true")
	}

	entries := h.Audit(t, audit.ServerScope)
	if len(entries) != 1 || entries[0].Action != actionEventAccepted {
		t.Fatalf("entries = %+v, want one old-secret entry", entries)
	}
}

func testReceiptBadEntry(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme.String())

	r := newReceipt("d1", receiptTime())
	e := entry(nsAcme, actionEventAccepted)
	r.Audit = &e

	if _, err := h.Store.RecordEventReceipt(t.Context(), r); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("record with the entry of a namespace = %v, want ErrInvalidEntry", err)
	}

	// The refusal stored nothing, so the same keys are free.
	r.Audit = nil

	if !record(t, h, r) {
		t.Fatal("receipt after a refused entry = false, want true: the receipt and its entry commit together")
	}
}

func testReceiptPurge(t *testing.T, h Harness) {
	t.Helper()

	t0 := receiptTime()
	old := newReceipt("old", t0)

	if !record(t, h, old) {
		t.Fatal("old receipt = false, want true")
	}

	// Exactly 24 hours old is not older than 24 hours: the receipt stays.
	if !record(t, h, newReceipt("at-limit", t0.Add(receiptMemory))) {
		t.Fatal("receipt at the limit = false, want true")
	}

	if record(t, h, old) {
		t.Fatal("receipt of exactly 24 hours = purged, want kept")
	}

	// One millisecond more: the old receipt is purged in the transaction of
	// this one, so its two keys are free again.
	if !record(t, h, newReceipt("later", t0.Add(receiptMemory+time.Millisecond))) {
		t.Fatal("later receipt = false, want true")
	}

	if !record(t, h, old) {
		t.Fatal("receipt older than 24 hours = still stored, want purged")
	}

	// The purge is by age only: the receipt of the limit stays.
	if record(t, h, newReceipt("at-limit", t0.Add(receiptMemory))) {
		t.Fatal("receipt younger than 24 hours = purged, want kept")
	}
}

func testReceiptBound(t *testing.T, h Harness) {
	t.Helper()

	if h.SeedReceipts == nil {
		t.Skip("the harness has no SeedReceipts, so the bound cannot be reached")
	}

	now := receiptTime()

	// Below the bound by one row: the next receipt is the last one.
	h.SeedReceipts(t, receiptBound-1, now)

	if !record(t, h, newReceipt("last", now)) {
		t.Fatal("receipt below the bound = false, want true")
	}

	_, err := h.Store.RecordEventReceipt(t.Context(), newReceipt("over", now))
	if !errors.Is(err, forge.ErrReceiptsFull) {
		t.Fatalf("receipt at the bound = %v, want ErrReceiptsFull", err)
	}

	// The refused receipt wrote nothing: the bound is exactly the bound.
	_, err = h.Store.RecordEventReceipt(t.Context(), newReceipt("over", now))
	if !errors.Is(err, forge.ErrReceiptsFull) {
		t.Fatalf("receipt at the bound, again = %v, want ErrReceiptsFull", err)
	}

	// The purge comes before the count: when the rows expire, a receipt fits.
	if !record(t, h, newReceipt("after-expiry", now.Add(receiptMemory+time.Millisecond))) {
		t.Fatal("receipt after the rows expired = false, want true: the purge runs before the bound check")
	}
}

func testReceiptInvalid(t *testing.T, h Harness) {
	t.Helper()

	const prefix = "sha256:"

	good := digestOf("good")

	for name, edit := range map[string]func(r *forge.EventReceipt){
		"an empty delivery ID":      func(r *forge.EventReceipt) { r.DeliveryID = "" },
		"an empty digest":           func(r *forge.EventReceipt) { r.BodyDigest = "" },
		"an upper case digest":      func(r *forge.EventReceipt) { r.BodyDigest = strings.ToUpper(good) },
		"a digest of 63 digits":     func(r *forge.EventReceipt) { r.BodyDigest = good[:bodyDigestLength-1] },
		"a digest of 65 digits":     func(r *forge.EventReceipt) { r.BodyDigest = good + "0" },
		"a digest that is no hex":   func(r *forge.EventReceipt) { r.BodyDigest = strings.Repeat("g", bodyDigestLength) },
		"a digest with a prefix":    func(r *forge.EventReceipt) { r.BodyDigest = prefix + good[:bodyDigestLength-len(prefix)] },
		"a digest with a space":     func(r *forge.EventReceipt) { r.BodyDigest = good[:bodyDigestLength-1] + " " },
		"a kind that is not served": func(r *forge.EventReceipt) { r.Kind = forge.KindGitLab },
		"no kind":                   func(r *forge.EventReceipt) { r.Kind = "" },
		"no time":                   func(r *forge.EventReceipt) { r.ReceivedAt = time.Time{} },
	} {
		r := newReceipt("d1", receiptTime())
		r.BodyDigest = good
		edit(&r)

		if _, err := h.Store.RecordEventReceipt(t.Context(), r); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("record with %s = %v, want ErrInvalid", name, err)
		}
	}

	// Each refusal stored nothing: a valid receipt with the same keys is new.
	r := newReceipt("d1", receiptTime())
	r.BodyDigest = good

	if !record(t, h, r) {
		t.Fatal("receipt after the refusals = false, want true")
	}
}

func testReceiptFailedAppendStoresNothing(t *testing.T, h Harness) {
	t.Helper()

	if h.BreakAuditAppends == nil {
		t.Skip("the harness cannot make an audit append fail")
	}

	r := newReceipt("d1", receiptTime())
	e := entry(audit.ServerScope, actionEventAccepted)
	r.Audit = &e

	h.BreakAuditAppends(t)

	if _, err := h.Store.RecordEventReceipt(t.Context(), r); err == nil {
		t.Fatal("record with a broken audit append = nil, want an error")
	}

	// The refusal stored nothing, so the same keys are free for a receipt that
	// writes no entry.
	r.Audit = nil

	if !record(t, h, r) {
		t.Fatal("receipt after a failed append = false, want true: the receipt and its entry commit together")
	}
}

func testReceiptConcurrentBound(t *testing.T, h Harness) {
	t.Helper()

	if h.SeedReceipts == nil {
		t.Skip("the harness has no SeedReceipts, so the bound cannot be reached")
	}

	now := receiptTime()

	const (
		free    = 2
		writers = 10
	)

	h.SeedReceipts(t, receiptBound-free, now)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
		full int
		oops []error
	)

	for i := range writers {
		wg.Go(func() {
			stored, err := h.Store.RecordEventReceipt(t.Context(), newReceipt("race-"+strconv.Itoa(i), now))

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil && stored:
				wins++
			case errors.Is(err, forge.ErrReceiptsFull):
				full++
			default:
				oops = append(oops, err)
			}
		})
	}

	wg.Wait()

	if wins != free || full != writers-free || len(oops) != 0 {
		t.Fatalf("%d stored, %d full, other %v; want %d stored and %d full", wins, full, oops, free, writers-free)
	}
}
