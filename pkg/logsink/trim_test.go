package logsink_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/logsink"
)

func trimSample() logsink.Record {
	return logsink.Record{
		Time:    time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC),
		Level:   logsink.LevelInfo,
		Message: "audit entry",
		Attrs: []logsink.Attr{
			logsink.String("scope", "acme"),
			logsink.Int64("sequence", 7),
			logsink.String("time", "2026-10-13T12:00:00.000Z"),
			logsink.String("action", "unit:read-secrets"),
			logsink.String("object", strings.Repeat("o", 500)),
			logsink.String("valuePath", strings.Repeat("v", 500)),
			logsink.String("userAgent", strings.Repeat("u", 500)),
			logsink.String("kind", "reveal"),
			logsink.String("outcome", "success"),
			logsink.String("prevHash", "abc"),
			logsink.String("hash", "def"),
		},
	}
}

func attrValue(attrs []logsink.Attr, key string) (any, bool) {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value, true
		}
	}

	return nil, false
}

func marshalLine(t *testing.T, rec logsink.Record) []byte {
	t.Helper()

	line, err := logsink.MarshalJSON(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return line
}

func checkMarked(t *testing.T, got logsink.Record) {
	t.Helper()

	v, found := attrValue(got.Attrs, logsink.TruncatedKey)
	marked, ok := v.(bool)

	if !found || !ok || !marked {
		t.Fatalf("trimmed record lacks truncated true: %+v", got.Attrs)
	}
}

func checkProtected(t *testing.T, got logsink.Record) {
	t.Helper()

	for key, want := range map[string]string{
		"scope":    "acme",
		"time":     "2026-10-13T12:00:00.000Z",
		"action":   "unit:read-secrets",
		"kind":     "reveal",
		"outcome":  "success",
		"prevHash": "abc",
		"hash":     "def",
	} {
		gotVal, found := attrValue(got.Attrs, key)
		text, ok := gotVal.(string)

		if !found || !ok || text != want {
			t.Fatalf("protected field %q: got %v, want %q", key, gotVal, want)
		}
	}

	gotVal, found := attrValue(got.Attrs, "sequence")
	num, ok := gotVal.(int64)

	if !found || !ok || num != 7 {
		t.Fatalf("protected field sequence: got %v", gotVal)
	}
}

func checkShortened(t *testing.T, got logsink.Record) {
	t.Helper()

	for _, key := range []string{"object", "valuePath", "userAgent"} {
		gotVal, _ := attrValue(got.Attrs, key)
		if text, ok := gotVal.(string); ok && len(text) < 500 {
			return
		}
	}

	t.Fatal("trim: no long field got shorter")
}

func TestTrimToFitUnderCapLeavesRecordAlone(t *testing.T) {
	t.Parallel()

	rec := trimSample()
	line := marshalLine(t, rec)

	got, ok, err := logsink.TrimToFit(rec, len(line)+100)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}

	if !ok {
		t.Fatal("trim: reported no fit for a small record")
	}

	if _, found := attrValue(got.Attrs, logsink.TruncatedKey); found {
		t.Fatal("trim: small record carries a truncated mark")
	}

	if gotLine := marshalLine(t, got); !bytes.Equal(gotLine, line) {
		t.Fatalf("trim changed a small record:\n got %s\nwant %s", gotLine, line)
	}
}

func TestTrimToFitTrimsLongFieldsAndMarks(t *testing.T) {
	t.Parallel()

	rec := trimSample()
	budget := len(marshalLine(t, rec)) - 500

	got, ok, err := logsink.TrimToFit(rec, budget)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}

	if !ok {
		t.Fatal("trim: reported no fit for a trimmable record")
	}

	if trimmed := marshalLine(t, got); len(trimmed) > budget {
		t.Fatalf("trimmed line holds %d bytes, want at most %d", len(trimmed), budget)
	}

	checkMarked(t, got)
	checkProtected(t, got)
	checkShortened(t, got)
}

func TestTrimToFitSkipsOnlyWhenNothingFits(t *testing.T) {
	t.Parallel()

	rec := logsink.Record{
		Time:    time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC),
		Level:   logsink.LevelInfo,
		Message: "audit entry",
		Attrs: []logsink.Attr{
			logsink.String("scope", "acme"),
			logsink.Int64("sequence", 7),
			logsink.String("action", strings.Repeat("a", 2000)),
			logsink.String("object", ""),
			logsink.String("kind", "reveal"),
			logsink.String("outcome", "success"),
		},
	}

	if _, ok, err := logsink.TrimToFit(rec, 480); err != nil {
		t.Fatalf("trim: %v", err)
	} else if ok {
		t.Fatal("trim: reported fit for a record that cannot fit")
	}
}

func TestTrimToFitRejectsBadJSON(t *testing.T) {
	t.Parallel()

	rec := logsink.Record{
		Time:    time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC),
		Level:   logsink.LevelInfo,
		Message: "audit entry",
		Attrs: []logsink.Attr{
			logsink.RawJSON("changes", json.RawMessage("not json")),
		},
	}

	if _, _, err := logsink.TrimToFit(rec, 8192); err == nil {
		t.Fatal("trim: got nil error for invalid JSON")
	}
}

func TestTrimToFitKeepsAuditChainFields(t *testing.T) {
	t.Parallel()

	entry := audit.Entry{
		At:         time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC),
		Namespace:  "acme",
		Subject:    "admin",
		IDPSubject: "",
		TokenID:    "",
		Action:     "unit:read-secrets",
		Object:     strings.Repeat("o", 800),
		Version:    "sv-1",
		ValuePath:  strings.Repeat("v", 800),
		ValueCount: nil,
		Changes:    nil,
		SourceIP:   "192.0.2.1",
		UserAgent:  strings.Repeat("u", 800),
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindReveal,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  "req-7",
		ObjectID:   "",
		PrevHash:   "prev-hash-value",
		Hash:       "hash-value",
		Sequence:   7,
	}

	rec := audit.ToRecord(entry)
	budget := len(marshalLine(t, rec)) - 500

	got, ok, err := logsink.TrimToFit(rec, budget)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}

	if !ok {
		t.Fatal("trim: reported no fit for a trimmable audit record")
	}

	if trimmed := marshalLine(t, got); len(trimmed) > budget {
		t.Fatalf("trimmed line holds %d bytes, want at most %d", len(trimmed), budget)
	}

	checkMarked(t, got)

	for _, key := range []string{"scope", "prevHash", "hash"} {
		want, _ := attrValue(rec.Attrs, key)
		kept, found := attrValue(got.Attrs, key)

		if !found || kept != want {
			t.Fatalf("chain field %q: got %v, want %v", key, kept, want)
		}
	}

	wantSeq, _ := attrValue(rec.Attrs, "sequence")
	keptSeq, found := attrValue(got.Attrs, "sequence")

	if !found || keptSeq != wantSeq {
		t.Fatalf("chain field sequence: got %v, want %v", keptSeq, wantSeq)
	}
}
