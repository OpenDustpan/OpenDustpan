package audit_test

import (
	"crypto/sha256"
	"encoding/hex"
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

// encodedHash answers the hex SHA-256 of the documented encoding: the
// fields of testEntry, each followed by the unit separator, then the four
// named fields with the given values. The test writes the encoding out,
// so it does not use the code under test to compute the expected hash.
func encodedHash(prevHash, valueCount, objectID, changes, fenceVersion string) string {
	fields := []string{
		"acme", "1", "admin", "", "", "unit:decrypt", "pulumi:acme/web/prod", "", "",
		"1791806400000", "192.0.2.1", "pulumi-cli/3.200.0", "api", "reveal", "success", "req-1",
		prevHash,
		"value_count=" + valueCount,
		"object_id=" + objectID,
		"changes=" + changes,
		"fence_version=" + fenceVersion,
	}

	var sb strings.Builder

	for _, field := range fields {
		sb.WriteString(field)
		sb.WriteByte(0x1F)
	}

	sum := sha256.Sum256([]byte(sb.String()))

	return hex.EncodeToString(sum[:])
}

// TestComputeHashEncodesEveryField proves that the hash encoding writes
// every field in a fixed order, with an empty value when the field is not
// set: no field is optional. A value count that is not set and the value
// count 0 give different hashes, and fence version 0 is in the encoding.
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
	}{
		{"no field set", func(*audit.Entry) {}, "", "", "", "", "0"},
		{"previous hash", func(*audit.Entry) {}, "prev", "", "", "", "0"},
		{"value count 0", func(e *audit.Entry) { e.ValueCount = &zero }, "", "0", "", "", "0"},
		{"value count 2", func(e *audit.Entry) { e.ValueCount = &two }, "", "2", "", "", "0"},
		{"object ID", func(e *audit.Entry) { e.ObjectID = "stk-1" }, "", "", "stk-1", "", "0"},
		{"fence version", func(e *audit.Entry) { e.FenceVersion = 2 }, "", "", "", "", "2"},
		{"every field", func(e *audit.Entry) {
			e.ValueCount = &two
			e.ObjectID = "stk-1"
			e.Changes = []audit.Change{{Key: "region", Old: nil, New: "eu-west", Layer: "file", TakesEffect: ""}}
			e.FenceVersion = 3
		}, "prev", "2", "stk-1", `[{"key":"region","new":"eu-west","layer":"file"}]`, "3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := testEntry()
			tc.edit(&e)

			want := encodedHash(tc.prev, tc.count, tc.id, tc.chg, tc.fence)
			if got := audit.ComputeHash(e, tc.prev); got != want {
				t.Fatalf("hash = %s, want %s", got, want)
			}
		})
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

func TestPageNormalizedAppliesDefaultsAndCaps(t *testing.T) {
	t.Parallel()

	if got := (audit.Page{Number: 0, Size: 0}).Normalized(); got.Number != 1 || got.Size != audit.DefaultPageSize {
		t.Fatalf("zero page = %+v, want 1/%d", got, audit.DefaultPageSize)
	}

	if got := (audit.Page{Number: 2, Size: audit.MaxPageSize + 1}).Normalized(); got.Size != audit.MaxPageSize {
		t.Fatalf("oversize page = %+v, want capped at %d", got, audit.MaxPageSize)
	}
}
