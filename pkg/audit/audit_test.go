package audit_test

import (
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
		At:         time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC),
		Namespace:  ns,
		Subject:    "admin",
		IDPSubject: "",
		TokenID:    "",
		Action:     "unit:decrypt",
		Object:     "pulumi:acme/web/prod",
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		Changes:    nil,
		SourceIP:   "192.0.2.1",
		UserAgent:  "pulumi-cli/3.200.0",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindReveal,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  "req-1",
		PrevHash:   "",
		Hash:       "",
		Sequence:   1,
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

func TestComputeHashKeepsEntriesWithoutACountValid(t *testing.T) {
	t.Parallel()

	// want pins the encoding of one fixed entry with a nil value count, as
	// the code before the count computed it. An entry written before the
	// count keeps its hash only while this hash stays.
	const want = "a58b9c9f9ef0c3ce0e47e4137c6fed46c9ff48dfd1944380cdfe1d8c5de48b66"

	if got := audit.ComputeHash(testEntry(), ""); got != want {
		t.Fatalf("hash of an entry with a nil count = %q, want %q", got, want)
	}
}

func TestComputeHashCoversTheValueCount(t *testing.T) {
	t.Parallel()

	before := audit.ComputeHash(testEntry(), "")

	with := testEntry()
	count := int64(2)
	with.ValueCount = &count

	got := audit.ComputeHash(with, "")
	if got == "" {
		t.Fatal("hash is empty")
	}

	if got == before {
		t.Fatal("hash did not change after the value count changed")
	}

	if again := audit.ComputeHash(with, ""); again != got {
		t.Fatalf("hash = %q, want stable %q", again, got)
	}

	other := testEntry()
	otherCount := int64(3)
	other.ValueCount = &otherCount

	if same := audit.ComputeHash(other, ""); same == got {
		t.Fatal("hash did not change after the value count changed")
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
