package history_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/secret"
)

// validTombstone returns a tombstone that passes Validate.
func validTombstone() history.Tombstone {
	return history.Tombstone{
		At:        time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC),
		Unit:      history.Unit{Namespace: "acme", Tool: "pulumi", ID: "stk-one"},
		ID:        history.NewTombstoneID(),
		By:        history.Actor{Subject: "ana", TokenID: "", JobID: ""},
		AuditHash: "",
		ItemKeys:  []secret.ItemKeyID{secret.NewItemKeyID()},
		Prefixes:  []string{"pulumi/stk-one/"},
		Targets:   nil,
	}
}

func TestTombstoneValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		edit func(tb *history.Tombstone)
		want error
		name string
	}{
		{name: "a valid tombstone", edit: func(*history.Tombstone) {}, want: nil},
		{name: "a unit with no item", edit: func(tb *history.Tombstone) { tb.ItemKeys = nil }, want: nil},
		{
			name: "an ID without the prefix",
			edit: func(tb *history.Tombstone) { tb.ID = "rdx-abc" },
			want: history.ErrInvalidTombstone,
		},
		{name: "no actor", edit: func(tb *history.Tombstone) { tb.By.Subject = "" }, want: history.ErrInvalidTombstone},
		{
			name: "a bad item key",
			edit: func(tb *history.Tombstone) { tb.ItemKeys = []secret.ItemKeyID{"key"} },
			want: history.ErrInvalidTombstone,
		},
		{name: "no prefix", edit: func(tb *history.Tombstone) { tb.Prefixes = nil }, want: history.ErrInvalidTombstone},
		{
			name: "a prefix without a slash at the end",
			edit: func(tb *history.Tombstone) { tb.Prefixes = []string{"pulumi/stk-one"} },
			want: history.ErrInvalidTombstone,
		},
		{
			name: "a prefix of the key blobs",
			edit: func(tb *history.Tombstone) { tb.Prefixes = []string{"item-keys/"} },
			want: history.ErrInvalidTombstone,
		},
		{
			name: "a prefix that holds the ledger",
			edit: func(tb *history.Tombstone) { tb.Prefixes = []string{"tombstones/"} },
			want: history.ErrInvalidTombstone,
		},
		{
			name: "a bad unit",
			edit: func(tb *history.Tombstone) { tb.Unit.Tool = "" },
			want: history.ErrInvalidItem,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := validTombstone()
			tc.edit(&tb)

			if err := tb.Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestTombstoneLedger(t *testing.T) {
	t.Parallel()

	tb := validTombstone()
	tb.AuditHash = "4f2a"
	l := tb.Ledger()

	if l.ID != tb.ID || l.Cause != history.CausePurge || l.UnitID != "stk-one" || l.AuditHash != "4f2a" ||
		len(l.ItemKeys) != 1 || len(l.Prefixes) != 1 || l.AtMs != tb.At.UnixMilli() {
		t.Errorf("ledger = %+v, want the fields of %+v", l, tb)
	}

	if got := history.TombstoneObject(tb.ID).String(); got != "tombstones/"+string(tb.ID)+".json" {
		t.Errorf("tombstone object = %q", got)
	}
}

// TestGaps checks the rule that backup check and restore share: a blob that
// a redaction or a purge names is an expected gap, and any other blob is
// missing.
func TestGaps(t *testing.T) {
	t.Parallel()

	r := validRedaction()
	r.Item.Unit.ID = "stk-two"
	r.Blobs = []blob.Key{"pulumi/stk-two/upd-1/checkpoint-1"}
	tb := validTombstone()
	g := history.NewGaps([]history.LedgerRecord{r.Ledger()}, []history.TombstoneRecord{tb.Ledger()})

	cases := []struct {
		name string
		key  blob.Key
		want bool
	}{
		{name: "a content blob of a redaction", key: "pulumi/stk-two/upd-1/checkpoint-1", want: true},
		{name: "the key blob of a redaction", key: r.ItemKey.Object(), want: true},
		{name: "a key blob of a purged unit", key: tb.ItemKeys[0].Object(), want: true},
		{name: "a blob under the prefix of a purged unit", key: "pulumi/stk-one/upd-9/checkpoint-3", want: true},
		{name: "another blob of the redacted unit", key: "pulumi/stk-two/upd-2/checkpoint-1", want: false},
		{name: "another key blob", key: secret.NewItemKeyID().Object(), want: false},
		{name: "a unit with a similar ID", key: "pulumi/stk-one-b/upd-1/checkpoint-1", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := g.Expected(tc.key); got != tc.want {
				t.Errorf("Expected(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}

	if !g.Purged(tb.Unit) {
		t.Errorf("Purged(%v) = false, want true", tb.Unit)
	}

	if g.Purged(r.Item.Unit) {
		t.Errorf("Purged(%v) = true, want false: a redaction does not purge its unit", r.Item.Unit)
	}
}
