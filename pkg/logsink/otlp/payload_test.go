package otlp_test

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
)

// TestPayloadShape checks the request against the fields of the OTLP logs
// JSON schema.
func TestPayloadShape(t *testing.T) {
	t.Parallel()

	r := newReceiver(t, nil)
	s := newSink(t, r, nil)

	when := time.Date(2026, time.October, 13, 12, 0, 0, 123456789, time.UTC)
	levels := []logsink.Level{logsink.LevelDebug, logsink.LevelInfo, logsink.LevelWarn, logsink.LevelError}
	recs := make([]logsink.Record, 0, len(levels))

	for _, level := range levels {
		recs = append(recs, logsink.Record{Time: when, Level: level, Message: level.String(), Attrs: nil})
	}

	if err := s.Write(t.Context(), recs); err != nil {
		t.Fatalf("write: %v", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.paths) != 1 || r.paths[0] != "POST /v1/logs" {
		t.Fatalf("requests = %v, want one POST /v1/logs", r.paths)
	}

	// The top level holds resourceLogs only, as the schema names it.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(r.bodies[0], &top); err != nil {
		t.Fatalf("body is no JSON: %v", err)
	}

	if _, ok := top["resourceLogs"]; !ok || len(top) != 1 {
		t.Fatalf("top level keys = %v, want only resourceLogs", top)
	}

	var exp exportRequest
	if err := json.Unmarshal(r.bodies[0], &exp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	checkResource(t, exp.ResourceLogs[0])

	got := exp.ResourceLogs[0].ScopeLogs[0].LogRecords
	if len(got) != len(levels) {
		t.Fatalf("logRecords = %d, want %d", len(got), len(levels))
	}

	checkSeverities(t, got, when)
}

func checkSeverities(t *testing.T, got []logRecord, when time.Time) {
	t.Helper()

	want := []struct {
		text   string
		number int
	}{{"DEBUG", 5}, {"INFO", 9}, {"WARN", 13}, {"ERROR", 17}}

	for i, w := range want {
		if got[i].SeverityText != w.text || got[i].SeverityNumber != w.number {
			t.Errorf(
				"record %d severity = %s %d, want %s %d",
				i,
				got[i].SeverityText,
				got[i].SeverityNumber,
				w.text,
				w.number,
			)
		}

		if got[i].TimeUnixNano != strconv.FormatInt(when.UnixNano(), 10) {
			t.Errorf("record %d timeUnixNano = %s, want %d", i, got[i].TimeUnixNano, when.UnixNano())
		}
	}
}

func checkResource(t *testing.T, rl resourceLogs) {
	t.Helper()

	res := rl.Resource.Attributes
	if len(res) != 1 || res[0].Key != "service.name" || res[0].Value.StringValue == nil ||
		*res[0].Value.StringValue != "opendustpan" {
		t.Fatalf("resource attributes = %+v, want service.name=opendustpan", res)
	}

	if rl.ScopeLogs[0].Scope.Name == "" {
		t.Fatal("scope name is empty")
	}
}

// TestAttributeTypes checks that each attribute type maps to its AnyValue.
func TestAttributeTypes(t *testing.T) {
	t.Parallel()

	r := newReceiver(t, nil)
	s := newSink(t, r, nil)

	raw := json.RawMessage(`{"a":[1,"x",null,true,2.5],"b":{"c":"d"}}`)

	err := s.Write(t.Context(), []logsink.Record{recWith("m",
		logsink.String("s", "v"), logsink.Int64("n", -7), logsink.Bool("b", true),
		logsink.Attr{Key: "f", Value: 1.5}, logsink.Attr{Key: "nil", Value: nil},
		logsink.RawJSON("raw", raw),
	)})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	attrs := r.recs[0].Attributes
	if len(attrs) != 6 {
		t.Fatalf("attributes = %d, want 6", len(attrs))
	}

	if v := attrs[0].Value.StringValue; v == nil || *v != "v" {
		t.Errorf("string attribute = %+v", attrs[0])
	}

	if v := attrs[1].Value.IntValue; v == nil || *v != "-7" {
		t.Errorf("int attribute = %+v, want intValue \"-7\"", attrs[1])
	}

	if v := attrs[2].Value.BoolValue; v == nil || !*v {
		t.Errorf("bool attribute = %+v", attrs[2])
	}

	if v := attrs[3].Value.DoubleValue; v == nil || *v != 1.5 {
		t.Errorf("float attribute = %+v", attrs[3])
	}

	if !isEmpty(attrs[4].Value) {
		t.Errorf("nil attribute = %+v, want an empty AnyValue", attrs[4])
	}

	checkRawObject(t, attrs[5].Value)
}

func checkRawObject(t *testing.T, v anyValue) {
	t.Helper()

	kv := v.KvlistValue
	if kv == nil || len(kv.Values) != 2 || kv.Values[0].Key != "a" || kv.Values[1].Key != "b" {
		t.Fatalf("raw object = %+v, want a kvlistValue with the keys a and b in order", v)
	}

	arr := kv.Values[0].Value.ArrayValue
	if arr == nil || len(arr.Values) != 5 {
		t.Fatalf("raw array = %+v, want 5 values", arr)
	}

	checkRawArray(t, arr)

	if c := kv.Values[1].Value.KvlistValue; c == nil || len(c.Values) != 1 || c.Values[0].Key != "c" {
		t.Errorf("object b = %+v, want a kvlistValue with key c", kv.Values[1].Value)
	}
}

func checkRawArray(t *testing.T, arr *arrayValue) {
	t.Helper()

	if v := arr.Values[0].IntValue; v == nil || *v != "1" {
		t.Errorf("array[0] = %+v, want intValue 1", arr.Values[0])
	}

	if v := arr.Values[1].StringValue; v == nil || *v != "x" {
		t.Errorf("array[1] = %+v, want stringValue x", arr.Values[1])
	}

	if !isEmpty(arr.Values[2]) {
		t.Errorf("array[2] = %+v, want an empty AnyValue for null", arr.Values[2])
	}

	if v := arr.Values[3].BoolValue; v == nil || !*v {
		t.Errorf("array[3] = %+v, want boolValue true", arr.Values[3])
	}

	if v := arr.Values[4].DoubleValue; v == nil || *v != 2.5 {
		t.Errorf("array[4] = %+v, want doubleValue 2.5", arr.Values[4])
	}
}
