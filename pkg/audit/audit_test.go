package audit_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func testEntry() audit.Entry {
	ns, err := namespace.ParseName("acme")
	if err != nil {
		panic(err)
	}

	return audit.Entry{
		At:           time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC),
		Namespace:    ns,
		Subject:      "admin",
		IDPSubject:   "",
		TokenID:      "",
		Action:       "unit:decrypt",
		Object:       "pulumi:acme/web/prod",
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "pulumi-cli/3.200.0",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindReveal,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "req-1",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     1,
		FenceVersion: 0,
		Reason:       "",
	}
}

func TestValidateAcceptsACompleteEntry(t *testing.T) {
	t.Parallel()

	if err := testEntry().Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidateRejectsAnUnknownKind(t *testing.T) {
	t.Parallel()

	e := testEntry()
	e.Kind = "export"

	if err := e.Validate(); err == nil {
		t.Fatal("validate accepted an unknown kind")
	}
}

func TestNormalizeChannelMapsUnknownValuesToAPI(t *testing.T) {
	t.Parallel()

	cases := map[string]audit.Channel{
		"":        audit.ChannelAPI,
		"api":     audit.ChannelAPI,
		"console": audit.ChannelConsole,
		"Console": audit.ChannelConsole,
		"other":   audit.ChannelAPI,
	}

	for raw, want := range cases {
		if got := audit.NormalizeChannel(raw); got != want {
			t.Errorf("NormalizeChannel(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestComputeHashIsStableAndCoversTheFields(t *testing.T) {
	t.Parallel()

	e := testEntry()

	first := audit.ComputeHash(e, "")
	if first == "" {
		t.Fatal("hash is empty")
	}

	if got := audit.ComputeHash(e, ""); got != first {
		t.Fatalf("hash = %q, want stable %q", got, first)
	}

	e.Subject = "mallory"

	if got := audit.ComputeHash(e, ""); got == first {
		t.Fatal("hash did not change after the subject changed")
	}

	if got := audit.ComputeHash(testEntry(), "prev"); got == first {
		t.Fatal("hash did not change after the previous hash changed")
	}
}

// hashTail holds the fields that follow the previous hash in the hash
// encoding, as text.
type hashTail struct {
	valueCount   string
	objectID     string
	changes      string
	fenceVersion string
	reason       string
}

// encodedHash answers the hex SHA-256 of the documented encoding of
// testEntry with the given previous hash, value count, object ID, changes
// text, fence version and reason: each field as its byte length, a colon and its
// bytes. A count of "-" writes the marker of a count that is not set. The
// test writes the encoding out, so it does not use the code under test to
// compute the expected hash.
func encodedHash(prevHash string, tail hashTail) string {
	fields := []string{
		"acme", "1", "admin", "", "", "unit:decrypt", "pulumi:acme/web/prod", "", "",
		"1791806400000", "192.0.2.1", "pulumi-cli/3.200.0", "api", "reveal", "success", "req-1",
		prevHash,
	}

	var sb strings.Builder

	frame := func(s string) {
		sb.WriteString(strconv.Itoa(len(s)))
		sb.WriteByte(':')
		sb.WriteString(s)
	}

	for _, field := range fields {
		frame(field)
	}

	if tail.valueCount == "-" {
		sb.WriteByte('-')
	} else {
		frame(tail.valueCount)
	}

	frame(tail.objectID)
	frame(tail.changes)
	frame(tail.fenceVersion)
	frame(tail.reason)

	sum := sha256.Sum256([]byte(sb.String()))

	return hex.EncodeToString(sum[:])
}

// TestComputeHashEncodesEveryField proves that the hash encoding writes
// every field in a fixed order, each with its length, also when the field
// is not set: no field is optional. A value count that is not set and the
// value count 0 give different hashes, and fence version 0 is in the
// encoding.
func TestComputeHashEncodesEveryField(t *testing.T) {
	t.Parallel()

	zero := int64(0)
	two := int64(2)

	cases := []struct {
		name  string
		edit  func(e *audit.Entry)
		prev  string
		count string
		id    string
		chg   string
		fence string
		why   string
	}{
		{"no field set", func(*audit.Entry) {}, "", "-", "", "", "0", ""},
		{"previous hash", func(*audit.Entry) {}, "prev", "-", "", "", "0", ""},
		{"value count 0", func(e *audit.Entry) { e.ValueCount = &zero }, "", "0", "", "", "0", ""},
		{"value count 2", func(e *audit.Entry) { e.ValueCount = &two }, "", "2", "", "", "0", ""},
		{"object ID", func(e *audit.Entry) { e.ObjectID = "stk-1" }, "", "-", "stk-1", "", "0", ""},
		{"fence version", func(e *audit.Entry) { e.FenceVersion = 2 }, "", "-", "", "", "2", ""},
		{"every field", func(e *audit.Entry) {
			e.ValueCount = &two
			e.ObjectID = "stk-1"
			e.Changes = []audit.Change{{Key: "region", Old: nil, New: "eu-west", Layer: "file", TakesEffect: ""}}
			e.FenceVersion = 3
			e.Reason = "lock released after the run crashed"
		}, "prev", "2", "stk-1", `[{"key":"region","new":"eu-west","layer":"file"}]`, "3", "lock released after the run crashed"},
		{"reason", func(e *audit.Entry) { e.Reason = "run crashed" }, "", "-", "", "", "0", "run crashed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := testEntry()
			tc.edit(&e)

			want := encodedHash(tc.prev, hashTail{tc.count, tc.id, tc.chg, tc.fence, tc.why})
			if got := audit.ComputeHash(e, tc.prev); got != want {
				t.Fatalf("hash = %s, want %s", got, want)
			}
		})
	}
}

// TestComputeHashGoldenVector pins the hash of one entry in which every
// field holds its own value, so a swap of two fields changes the hash. A
// script outside Go computed the value from the documented encoding.
func TestComputeHashGoldenVector(t *testing.T) {
	t.Parallel()

	const want = "86aa124d85b1f880cfa3742095388f336e896ef9836824039be1e0978d1a990f"

	count := int64(4)
	e := testEntry()
	e.Sequence = 7
	e.IDPSubject = "idp-sub-1"
	e.TokenID = "tok-1"
	e.Version = "v3"
	e.ValuePath = "secret.db"
	e.RequestID = "req-9"
	e.ValueCount = &count
	e.ObjectID = "stk-1"
	e.Changes = []audit.Change{{Key: "region", Old: nil, New: "eu-west", Layer: "file", TakesEffect: ""}}
	e.FenceVersion = 5
	e.Reason = "lock released after the run crashed"

	if got := audit.ComputeHash(e, "prev-hash-6"); got != want {
		t.Fatalf("hash = %s, want %s", got, want)
	}
}

// TestComputeHashTellsANilCountFromZero proves that an entry without a
// value count and an entry with the value count 0 hash differently.
func TestComputeHashTellsANilCountFromZero(t *testing.T) {
	t.Parallel()

	zero := int64(0)
	counted := testEntry()
	counted.ValueCount = &zero

	if audit.ComputeHash(counted, "") == audit.ComputeHash(testEntry(), "") {
		t.Fatal("a nil value count and the value count 0 gave the same hash")
	}
}

// hashSlot is one text field of the hash encoding that a collision test
// can set: a field of the entry, the previous hash or the changes text.
type hashSlot struct {
	set  func(e *audit.Entry, prev, changes *string, v string)
	name string
}

// hashSlots lists the text fields of the hash encoding in their order.
// next is true when the slot after it in the list is also the field after
// it in the encoding.
//
//nolint:gochecknoglobals // A fixed test table that several tests read.
var hashSlots = []struct {
	slot hashSlot
	next bool
}{
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.Subject = v }, "subject"}, true},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.IDPSubject = v }, "idp subject"}, true},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.TokenID = v }, "token ID"}, true},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.Action = v }, "action"}, true},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.Object = v }, "object"}, true},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.Version = v }, "version"}, true},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.ValuePath = v }, "value path"}, false},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.SourceIP = v }, "source IP"}, true},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.UserAgent = v }, "user agent"}, true},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.Channel = audit.Channel(v) }, "channel"}, true},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.Kind = audit.Kind(v) }, "kind"}, true},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.Outcome = audit.Outcome(v) }, "outcome"}, true},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.RequestID = v }, "request ID"}, true},
	{hashSlot{func(_ *audit.Entry, prev, _ *string, v string) { *prev = v }, "previous hash"}, false},
	{hashSlot{func(e *audit.Entry, _, _ *string, v string) { e.ObjectID = v }, "object ID"}, true},
	{hashSlot{func(_ *audit.Entry, _, changes *string, v string) { *changes = v }, "changes"}, false},
}

// slotHash answers the stored hash of testEntry with the two slots set to
// first and second.
func slotHash(a, b hashSlot, first, second string) string {
	e := testEntry()
	prev, changes := "", ""
	a.set(&e, &prev, &changes, first)
	b.set(&e, &prev, &changes, second)

	return audit.ComputeStoredHash(e, prev, changes)
}

// TestComputeHashHasNoCollisionAcrossAdjacentFields proves that bytes moved
// from one field to the next change the hash, for each pair of adjacent
// text fields, also when the bytes hold the old separator 0x1F.
func TestComputeHashHasNoCollisionAcrossAdjacentFields(t *testing.T) {
	t.Parallel()

	for i := range len(hashSlots) - 1 {
		if !hashSlots[i].next {
			continue
		}

		a, b := hashSlots[i].slot, hashSlots[i+1].slot

		t.Run(a.name+" and "+b.name, func(t *testing.T) {
			t.Parallel()

			left := slotHash(a, b, "x\x1fy", "z")
			right := slotHash(a, b, "x", "y\x1fz")

			if left == right {
				t.Fatalf("moving bytes from %s to %s kept the hash %s", a.name, b.name, left)
			}

			if slotHash(a, b, "xy", "z") == slotHash(a, b, "x", "yz") {
				t.Fatalf("moving one byte from %s to %s kept the hash", a.name, b.name)
			}
		})
	}
}

// TestComputeHashHasNoKnownCollision proves that two pairs of entries that
// gave the same hash under the old separator encoding now hash differently.
func TestComputeHashHasNoKnownCollision(t *testing.T) {
	t.Parallel()

	first := testEntry()
	first.Version = "v1\x1fsecret.a"
	first.ValuePath = "b"

	second := testEntry()
	second.Version = "v1"
	second.ValuePath = "secret.a\x1fb"

	if audit.ComputeHash(first, "") == audit.ComputeHash(second, "") {
		t.Fatal("a version and value path split at another place gave the same hash")
	}

	withID := testEntry()
	withID.ObjectID = "stk-1\x1fchanges=X"

	plain := testEntry()
	plain.ObjectID = "stk-1"

	if audit.ComputeStoredHash(withID, "", "Y") == audit.ComputeStoredHash(plain, "", "X\x1fchanges=Y") {
		t.Fatal("an object ID and changes split at another place gave the same hash")
	}
}

func TestPageNormalizedAppliesDefaultsAndCaps(t *testing.T) {
	t.Parallel()

	if got := (audit.Page{Number: 0, Size: 0}).Normalized(); got.Number != 1 || got.Size != audit.DefaultPageSize {
		t.Fatalf("zero page = %+v, want 1/%d", got, audit.DefaultPageSize)
	}

	if got := (audit.Page{Number: 2, Size: audit.MaxPageSize + 1}).Normalized(); got.Size != audit.MaxPageSize {
		t.Fatalf("oversize page = %+v, want capped at %d", got, audit.MaxPageSize)
	}
}

// TestValidateReason proves the limits of a reason: at most 500 Unicode code
// points, valid UTF-8 and no control character, format character or
// line or paragraph separator.
func TestValidateReason(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		reason  string
		invalid bool
	}{
		{"empty", "", false},
		{"text", "the run crashed", false},
		{"500 code points", strings.Repeat("a", audit.MaxReasonLen), false},
		{"500 code points of 3 bytes", strings.Repeat("€", audit.MaxReasonLen), false},
		{"501 code points", strings.Repeat("a", audit.MaxReasonLen+1), true},
		{"line break", "first\nsecond", true},
		{"tab", "first\tsecond", true},
		{"null byte", "first\x00second", true},
		{"delete character", "first\x7fsecond", true},
		{"C1 control character", "first\u0085second", true},
		{"right-to-left override", "first\u202esecond", true},
		{"line separator", "first\u2028second", true},
		{"paragraph separator", "first\u2029second", true},
		{"zero width joiner", "first\u200dsecond", true},
		{"invalid UTF-8", "first\xffsecond", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := audit.ValidateReason(tc.reason)
			if tc.invalid != errors.Is(err, audit.ErrInvalidReason) {
				t.Fatalf("ValidateReason error = %v, want invalid = %t", err, tc.invalid)
			}

			e := testEntry()
			e.Reason = tc.reason

			if tc.invalid != errors.Is(e.Validate(), audit.ErrInvalidEntry) {
				t.Fatalf("Entry.Validate error = %v, want invalid = %t", e.Validate(), tc.invalid)
			}
		})
	}
}
