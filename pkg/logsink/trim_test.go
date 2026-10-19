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
			logsink.String("note", strings.Repeat("n", 1000)),
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
		"scope":     "acme",
		"time":      "2026-10-13T12:00:00.000Z",
		"action":    "unit:read-secrets",
		"object":    strings.Repeat("o", 500),
		"valuePath": strings.Repeat("v", 500),
		"userAgent": strings.Repeat("u", 500),
		"kind":      "reveal",
		"outcome":   "success",
		"prevHash":  "abc",
		"hash":      "def",
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

	gotVal, _ := attrValue(got.Attrs, "note")
	if text, ok := gotVal.(string); ok && len(text) < 1000 {
		return
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

func TestTrimToFitNeverShortensProtectedAttribute(t *testing.T) {
	t.Parallel()

	protected := []string{
		"scope",
		"sequence",
		"subject",
		"idpSubject",
		"tokenID",
		"action",
		"object",
		"version",
		"valuePath",
		"atMs",
		"time",
		"sourceIP",
		"userAgent",
		"channel",
		"kind",
		"outcome",
		"requestID",
		"prevHash",
		"hash",
		"valueCount",
		"objectID",
		"changes",
		"fenceVersion",
	}

	for _, key := range protected {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			checkProtectedKeySurvivesTrim(t, key)
		})
	}
}

func checkProtectedKeySurvivesTrim(t *testing.T, key string) {
	t.Helper()

	long := strings.Repeat("x", 5000)
	rec := logsink.Record{
		Time:    time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC),
		Level:   logsink.LevelInfo,
		Message: "audit entry",
		Attrs: []logsink.Attr{
			logsink.String(key, long),
			logsink.String("note", strings.Repeat("n", 5000)),
		},
	}

	got, ok, err := logsink.TrimToFit(rec, 480)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}

	if ok {
		assertAttrUnchanged(t, got.Attrs, key, long)

		return
	}

	// A record that still exceeds the limit after trimming is skipped and
	// the caller keeps the original: the protected value never ships shortened.
	assertAttrUnchanged(t, rec.Attrs, key, long)
}

func assertAttrUnchanged(t *testing.T, attrs []logsink.Attr, key, want string) {
	t.Helper()

	kept, found := attrValue(attrs, key)
	if !found {
		t.Fatalf("protected field %q is missing after trim", key)
	}

	text, isString := kept.(string)
	if !isString || text != want {
		t.Fatalf("protected field %q was shortened", key)
	}
}

func TestTrimToFitKeepsAuditChainFields(t *testing.T) {
	t.Parallel()

	entry := audit.Entry{
		At:            time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC),
		Namespace:     "acme",
		Subject:       "admin",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "unit:read-secrets",
		Object:        strings.Repeat("o", 800),
		Version:       "sv-1",
		ValuePath:     strings.Repeat("v", 800),
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     strings.Repeat("u", 800),
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindReveal,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "req-7",
		ObjectID:      "",
		PrevHash:      "prev-hash-value",
		Hash:          "hash-value",
		Sequence:      7,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}

	rec := audit.ToRecord(entry)
	rec.Attrs = append(rec.Attrs, logsink.String("note", strings.Repeat("n", 800)))
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

	for _, key := range []string{"scope", "subject", "object", "valuePath", "userAgent", "requestID", "prevHash", "hash"} {
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

	keptNote, _ := attrValue(got.Attrs, "note")
	if text, ok := keptNote.(string); !ok || len(text) >= 800 {
		t.Fatalf("trimmable field note was not shortened: %v", keptNote)
	}
}
