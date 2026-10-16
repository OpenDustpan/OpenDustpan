package audit_test

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
)

func TestTruncateUserAgentCapsLongValuesAt512Bytes(t *testing.T) {
	t.Parallel()

	got := audit.TruncateUserAgent(strings.Repeat("a", 4096))
	if len(got) != audit.MaxUserAgentLen {
		t.Fatalf("truncated user agent holds %d bytes, want %d", len(got), audit.MaxUserAgentLen)
	}
}

func TestTruncateUserAgentKeepsShortValues(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"", "OpenTofu/1.11", strings.Repeat("a", audit.MaxUserAgentLen)} {
		if got := audit.TruncateUserAgent(s); got != s {
			t.Fatalf("TruncateUserAgent(%d bytes) changed a value that fits", len(s))
		}
	}
}

func TestTruncateUserAgentCutsOnARuneBoundary(t *testing.T) {
	t.Parallel()

	// 511 ASCII bytes and one two-byte rune: byte 512 sits inside the rune.
	s := strings.Repeat("a", 511) + "é"

	got := audit.TruncateUserAgent(s)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated user agent is not valid UTF-8: %q", got)
	}

	if len(got) != 511 {
		t.Fatalf("truncated user agent holds %d bytes, want 511", len(got))
	}

	multi := strings.Repeat("é", 300)

	got = audit.TruncateUserAgent(multi)
	if !utf8.ValidString(got) || len(got) > audit.MaxUserAgentLen {
		t.Fatalf("truncated user agent holds %d bytes of invalid UTF-8", len(got))
	}
}

func boundSample() audit.Entry {
	return audit.Entry{
		At:           time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC),
		Namespace:    "acme",
		Subject:      "admin",
		IDPSubject:   "",
		TokenID:      "",
		Action:       "unit:read-secrets",
		Object:       "opentofu:acme/prod",
		Version:      "sv-1",
		ValuePath:    "/outputs/db",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "OpenTofu/1.11",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindToolOperation,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "req-1",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
}

func TestBoundKeepsAFittingEntry(t *testing.T) {
	t.Parallel()

	if got := audit.Bound(boundSample()); !reflect.DeepEqual(got, boundSample()) {
		t.Fatalf("Bound changed an entry that fits: %+v", got)
	}
}

func TestBoundCapsEveryClientField(t *testing.T) {
	t.Parallel()

	old := strings.Repeat("o", audit.MaxChangeTextLen+10)
	e := boundSample()
	e.Subject = strings.Repeat("s", audit.MaxSubjectLen+10)
	e.IDPSubject = strings.Repeat("i", audit.MaxIDPSubjectLen+10)
	e.Object = strings.Repeat("o", audit.MaxObjectLen+10)
	e.ValuePath = strings.Repeat("v", audit.MaxValuePathLen+10)
	e.SourceIP = strings.Repeat("1", audit.MaxSourceIPLen+10)
	e.UserAgent = strings.Repeat("u", 4096)
	e.ObjectID = strings.Repeat("w", audit.MaxObjectIDLen+10)
	e.Changes = []audit.Change{{
		Key:         strings.Repeat("k", audit.MaxChangeTextLen+10),
		Old:         &old,
		New:         strings.Repeat("n", audit.MaxChangeTextLen+10),
		Layer:       strings.Repeat("l", audit.MaxChangeTextLen+10),
		TakesEffect: strings.Repeat("t", audit.MaxChangeTextLen+10),
	}}

	got := audit.Bound(e)

	type fieldCap struct {
		text string
		max  int
	}

	for name, f := range map[string]fieldCap{
		"subject":      {got.Subject, audit.MaxSubjectLen},
		"idp subject":  {got.IDPSubject, audit.MaxIDPSubjectLen},
		"object":       {got.Object, audit.MaxObjectLen},
		"value path":   {got.ValuePath, audit.MaxValuePathLen},
		"source IP":    {got.SourceIP, audit.MaxSourceIPLen},
		"user agent":   {got.UserAgent, audit.MaxUserAgentLen},
		"object ID":    {got.ObjectID, audit.MaxObjectIDLen},
		"change key":   {got.Changes[0].Key, audit.MaxChangeTextLen},
		"change old":   {*got.Changes[0].Old, audit.MaxChangeTextLen},
		"change new":   {got.Changes[0].New, audit.MaxChangeTextLen},
		"change layer": {got.Changes[0].Layer, audit.MaxChangeTextLen},
	} {
		if len(f.text) > f.max || !utf8.ValidString(f.text) {
			t.Fatalf("%s holds %d bytes, want at most %d valid UTF-8 bytes", name, len(f.text), f.max)
		}
	}

	if len(got.Changes[0].TakesEffect) > audit.MaxChangeTextLen {
		t.Fatalf("change takes effect holds %d bytes, want at most %d",
			len(got.Changes[0].TakesEffect), audit.MaxChangeTextLen)
	}

	if err := got.Validate(); err != nil {
		t.Fatalf("bounded entry fails validation: %v", err)
	}
}

func TestBoundKeepsNilChangesNil(t *testing.T) {
	t.Parallel()

	if got := audit.Bound(boundSample()); got.Changes != nil {
		t.Fatalf("Bound set changes %+v on an entry without any", got.Changes)
	}
}

// TestBoundLongUserAgentStores512AndChainPasses proves the reported failure
// mode is gone: without the cap a 4096-byte user agent fails validation, so
// the store refuses the entry and its action never reaches the log. With the
// cap the entry stores exactly 512 bytes, and the chain over the capped
// values checks out.
func TestBoundLongUserAgentStores512AndChainPasses(t *testing.T) {
	t.Parallel()

	raw := boundSample()
	raw.UserAgent = strings.Repeat("u", 4096)

	if err := raw.Validate(); err == nil {
		t.Fatal("validate accepted a 4096-byte user agent")
	}

	stored := audit.Bound(raw)
	if len(stored.UserAgent) != audit.MaxUserAgentLen {
		t.Fatalf("stored user agent holds %d bytes, want %d", len(stored.UserAgent), audit.MaxUserAgentLen)
	}

	if err := stored.Validate(); err != nil {
		t.Fatalf("bounded entry fails validation: %v", err)
	}

	walk := audit.NewChainWalk(audit.ChainScope{Record: audit.ChainRecord{
		ChangedAt:          time.Time{},
		ChangedBy:          "",
		State:              audit.ChainStateOn,
		Pending:            false,
		EnteredMaintenance: false,
		MigratingAt:        time.Time{},
		LogReplaced:        false,
	}, Start: 0})

	first := stored
	first.Sequence = 1
	first.Hash = audit.ComputeHash(first, "")

	if !walk.Step(first) {
		t.Fatal("chain walk refused the first bounded entry")
	}

	second := stored
	second.Subject = "bob"
	second.Sequence = 2
	second.PrevHash = first.Hash
	second.Hash = audit.ComputeHash(second, first.Hash)

	if !walk.Step(second) {
		t.Fatal("chain walk refused the second bounded entry")
	}

	if !walk.Finish() {
		t.Fatal("chain walk over the bounded entries did not finish valid")
	}
}
