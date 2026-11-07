package history_test

import (
	"errors"
	"strconv"
	"strings"
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

// nsID is a namespace ID for the prefixes of the tests.
const nsID = "ns-abcdefghijklmnopqrst"

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
			name: "a prefix of the item keys of one namespace",
			edit: func(tb *history.Tombstone) { tb.Prefixes = []string{"namespaces/" + nsID + "/item-keys/"} },
			want: history.ErrInvalidTombstone,
		},
		{
			name: "a prefix inside the item keys of one namespace",
			edit: func(tb *history.Tombstone) {
				tb.Prefixes = []string{"namespaces/" + nsID + "/item-keys/" + string(tb.ItemKeys[0]) + "/"}
			},
			want: history.ErrInvalidTombstone,
		},
		{
			name: "a prefix of one namespace, which holds its item keys",
			edit: func(tb *history.Tombstone) { tb.Prefixes = []string{"namespaces/" + nsID + "/"} },
			want: history.ErrInvalidTombstone,
		},
		{
			name: "a prefix of every namespace",
			edit: func(tb *history.Tombstone) { tb.Prefixes = []string{"namespaces/"} },
			want: history.ErrInvalidTombstone,
		},
		{
			name: "a content prefix of one namespace",
			edit: func(tb *history.Tombstone) { tb.Prefixes = []string{"namespaces/" + nsID + "/tofu/ws-one/"} },
			want: nil,
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

	got, err := history.TombstoneObject(tb.ID)
	if err != nil {
		t.Fatalf("tombstone object: %v", err)
	}

	if got.String() != "tombstones/"+string(tb.ID)+".json" {
		t.Errorf("tombstone object = %q", got)
	}
}

// TestObjectKeysKeepTheStoredFormat checks that a valid ID gives the same key
// as the key format that is already stored. Blob keys are stored data, so the
// text of a valid key must not change.
func TestObjectKeysKeepTheStoredFormat(t *testing.T) {
	t.Parallel()

	ids := []string{
		"rdx-abc",
		"rdx-a.b_c-d",
		"rdx-0",
		"rdx-" + strings.Repeat("a", 492),
		string(history.NewRedactionID()),
	}

	for _, id := range ids {
		ledger, err := history.LedgerObject(history.RedactionID(id))
		if err != nil {
			t.Errorf("LedgerObject(%q) err = %v, want nil", id, err)
		} else if want := "redactions/" + id + ".json"; ledger.String() != want {
			t.Errorf("LedgerObject(%q) = %q, want %q", id, ledger, want)
		}

		tombstone, err := history.TombstoneObject(history.TombstoneID(strings.Replace(id, "rdx-", "tmb-", 1)))
		if err != nil {
			t.Errorf("TombstoneObject(%q) err = %v, want nil", id, err)
		} else if want := "tombstones/" + strings.Replace(id, "rdx-", "tmb-", 1) + ".json"; tombstone.String() != want {
			t.Errorf("TombstoneObject(%q) = %q, want %q", id, tombstone, want)
		}
	}
}

// TestLedgerObjectsRefuseBadIDs sends each bad ID straight to the key
// constructors, as if the ID had not come from the store or a new ID.
func TestLedgerObjectsRefuseBadIDs(t *testing.T) {
	t.Parallel()

	ids := []string{
		"rdx-ABC",
		"../x",
		"a//b",
		"rdx-a b",
		"rdx-" + strings.Repeat("a", 493),
		strings.Repeat("a", 600),
	}

	for _, id := range ids {
		ledger, err := history.LedgerObject(history.RedactionID(id))
		if !errors.Is(err, blob.ErrInvalidKey) || ledger != "" {
			t.Errorf("LedgerObject(%q) = %q, %v, want blob.ErrInvalidKey", id, ledger, err)
		}

		tombstone, err := history.TombstoneObject(history.TombstoneID(id))
		if !errors.Is(err, blob.ErrInvalidKey) || tombstone != "" {
			t.Errorf("TombstoneObject(%q) = %q, %v, want blob.ErrInvalidKey", id, tombstone, err)
		}
	}
}

// TestGaps checks the rule that backup check and restore share: a blob that
// a redaction or a purge names is an expected gap, and any other blob is
// missing.
// nsID2 is a second namespace ID for the tests.
const nsID2 = "ns-tsrqponmlkjihgfedcba"

// itemKeyBlob returns the blob key of version of the item key id in the
// namespace ns.
func itemKeyBlob(t *testing.T, ns string, id secret.ItemKeyID, version int) blob.Key {
	t.Helper()

	key, err := blob.ParseKey(
		"namespaces/" + ns + "/item-keys/" + string(id) + "/v" + strconv.Itoa(version) + "-abcdefghij0123456789",
	)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	return key
}

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
		{name: "the key blob of a redaction", key: itemKeyBlob(t, nsID, r.ItemKey, 1), want: true},
		{name: "a later version of the key blob of a redaction", key: itemKeyBlob(t, nsID, r.ItemKey, 7), want: true},
		{
			name: "another attempt of the key blob of a redaction",
			key:  blob.Key("namespaces/" + nsID + "/item-keys/" + string(r.ItemKey) + "/v1-0123456789abcdefghij"),
			want: true,
		},
		{name: "a key blob of a purged unit", key: itemKeyBlob(t, nsID, tb.ItemKeys[0], 1), want: true},
		{
			name: "a key blob of a purged unit in another namespace ID",
			key:  itemKeyBlob(t, nsID2, tb.ItemKeys[0], 1),
			want: true,
		},
		{name: "a blob under the prefix of a purged unit", key: "pulumi/stk-one/upd-9/checkpoint-3", want: true},
		{name: "another blob of the redacted unit", key: "pulumi/stk-two/upd-2/checkpoint-1", want: false},
		{name: "another key blob", key: itemKeyBlob(t, nsID, secret.NewItemKeyID(), 1), want: false},
		{name: "the key blob in the old layout", key: blob.Key("item-keys/" + string(r.ItemKey)), want: false},
		{
			name: "a blob that only holds the item key ID",
			key:  blob.Key("pulumi/" + string(r.ItemKey) + "/x"),
			want: false,
		},
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

// TestTombstonePrefixCannotReachAnItemKey sends hostile prefixes straight to
// Validate, as if the layers before it had failed. A prefix that can list or
// shred an item key must be refused, whatever the namespace ID is.
func TestTombstonePrefixCannotReachAnItemKey(t *testing.T) {
	t.Parallel()

	refused := []string{
		"namespaces/",
		"namespaces/" + nsID + "/",
		"namespaces/" + nsID2 + "/",
		"namespaces/" + nsID + "/item-keys/",
		"namespaces/" + nsID + "/item-keys/ik-abc/",
		"namespaces/" + nsID + "/ITEM-KEYS/",
		"namespaces/" + nsID + "/Item-Keys/ik-abc/",
		"Namespaces/",
		"NAMESPACES/" + nsID + "/",
		"namespaces//",
		"namespaces/" + nsID + "//item-keys/",
		"namespaces/./",
		"namespaces/../",
		"namespaces/" + nsID + "/./item-keys/",
		"namespaces/" + nsID + "/../item-keys/",
		"item-keys/",
		"ITEM-KEYS/",
		"item-keys/ik-abc/",
		"./item-keys/",
		"../namespaces/",
		"/namespaces/",
		"tombstones/",
		"redactions/",
	}

	for _, p := range refused {
		t.Run(p, func(t *testing.T) {
			t.Parallel()

			tb := validTombstone()
			tb.Prefixes = []string{p}

			if err := tb.Validate(); !errors.Is(err, history.ErrInvalidTombstone) {
				t.Errorf("Validate with prefix %q err = %v, want ErrInvalidTombstone", p, err)
			}
		})
	}

	allowed := []string{
		"pulumi/stk-one/",
		"namespaces/" + nsID + "/tofu/",
		"namespaces/" + nsID + "/pulumi/stk-one/",
		"namespaces/" + nsID + "/item-keys-extra/",
	}

	for _, p := range allowed {
		t.Run("allowed "+p, func(t *testing.T) {
			t.Parallel()

			tb := validTombstone()
			tb.Prefixes = []string{p}

			if err := tb.Validate(); err != nil {
				t.Errorf("Validate with prefix %q err = %v, want nil", p, err)
			}
		})
	}
}
