// Package logsink defines the output port for structured log records (ADR 60
// decision 11). The audit log and the application log both deliver records
// through it. The port knows nothing about audit entries or log/slog: a
// record holds a time, a level, a message and named attributes.
//
// Adapters live in subpackages (logsink/file, logsink/syslog,
// logsink/webhook, logsink/objectstore) or in user code.
// logsink/logsinktest is the conformance suite every adapter must pass.
package logsink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Sentinel errors returned by this package and by Sink implementations.
var (
	// ErrClosed reports a Write on a sink after Close.
	ErrClosed = errors.New("log sink closed")
	// ErrInvalidConfig reports a sink configuration that an adapter refuses.
	ErrInvalidConfig = errors.New("invalid log sink configuration")
	// ErrRefused reports a target that answered and refused the batch, for
	// example a webhook receiver that answered a status other than 2xx.
	ErrRefused = errors.New("log sink target refused the batch")
	// ErrSkipped reports a Write that delivered the batch except the records
	// that the target cannot hold, for example a record above the size cap
	// of the target. A SkipError carries it.
	ErrSkipped = errors.New("log sink skipped records")
)

// SkipError reports the count of records that a Write skipped, and why. A
// Write that answers a SkipError delivered every other record of the batch,
// so the caller counts the batch as delivered and never sends the skipped
// records again: a retry cannot deliver them.
type SkipError struct {
	Reason  string
	Skipped int
}

// Error implements error.
func (e *SkipError) Error() string {
	return fmt.Sprintf("%s: %d records: %s", ErrSkipped, e.Skipped, e.Reason)
}

// Is reports whether target is ErrSkipped.
func (e *SkipError) Is(target error) bool {
	return target == ErrSkipped
}

// Level is the severity of a record. The values follow log/slog, so an
// adapter can compare them, but the port does not import log/slog.
type Level int

// Levels known to the port.
const (
	LevelDebug Level = -4
	LevelInfo  Level = 0
	LevelWarn  Level = 4
	LevelError Level = 8
)

// String returns the lower-case name of the level: debug, info, warn or
// error. A value between two levels takes the name of the lower one.
func (l Level) String() string {
	switch {
	case l >= LevelError:
		return "error"
	case l >= LevelWarn:
		return "warn"
	case l >= LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

// Attr is one named attribute of a record. Value holds a string, an int64,
// a float64, a bool, a json.RawMessage or nil. A JSON adapter writes a
// json.RawMessage as it is. An adapter writes any other type with its
// default JSON form.
type Attr struct {
	Value any
	Key   string
}

// String returns an attribute with a string value.
func String(key, value string) Attr {
	return Attr{Key: key, Value: value}
}

// Int64 returns an attribute with an integer value.
func Int64(key string, value int64) Attr {
	return Attr{Key: key, Value: value}
}

// RawJSON returns an attribute whose value is the JSON text raw. raw must
// be valid JSON: MarshalJSON fails on any other text.
func RawJSON(key string, raw json.RawMessage) Attr {
	return Attr{Key: key, Value: raw}
}

// Bool returns an attribute with a boolean value.
func Bool(key string, value bool) Attr {
	return Attr{Key: key, Value: value}
}

// Record is one structured log record. Attrs keeps its order: an adapter
// writes the attributes in that order.
type Record struct {
	Time    time.Time
	Message string
	Attrs   []Attr
	Level   Level
}

// Sink is the output port for structured records. Write delivers one batch
// in order. When Write returns nil, the target holds every record of the
// batch, so a caller that keeps a cursor can move it past the batch. When
// Write returns an error, the target can hold none, some or all records of
// the batch, so a caller that retries can deliver a record twice.
type Sink interface {
	// Write delivers records in order. An empty batch is not an error and
	// delivers nothing. It returns ErrClosed after Close, and ctx.Err() when
	// ctx is done before the delivery ends. It returns a *SkipError when it
	// delivered every record except the ones the target cannot hold.
	Write(ctx context.Context, records []Record) error
	// Close releases the resources of the sink. A second Close is not an
	// error.
	Close() error
}

// MarshalJSON writes r as one JSON object: "time" (RFC 3339 with
// milliseconds, UTC), "level", "msg", then each attribute in order. An
// attribute named time, level or msg keeps its place and its value, so a
// reader can see both. The output holds no newline.
func MarshalJSON(r Record) ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteString(`{"time":`)
	buf.WriteString(strconv.Quote(r.Time.UTC().Format(TimeFormat)))
	buf.WriteString(`,"level":`)
	buf.WriteString(strconv.Quote(r.Level.String()))
	buf.WriteString(`,"msg":`)

	if err := writeJSONValue(&buf, r.Message); err != nil {
		return nil, err
	}

	for _, a := range r.Attrs {
		buf.WriteByte(',')

		if err := writeJSONValue(&buf, a.Key); err != nil {
			return nil, err
		}

		buf.WriteByte(':')

		if err := writeJSONValue(&buf, a.Value); err != nil {
			return nil, fmt.Errorf("attribute %q: %w", a.Key, err)
		}
	}

	buf.WriteByte('}')

	return buf.Bytes(), nil
}

// TimeFormat is the time layout of MarshalJSON: RFC 3339 in UTC with
// milliseconds.
const TimeFormat = "2006-01-02T15:04:05.000Z07:00"

// writeJSONValue appends the JSON form of v. It does not escape HTML
// characters, so a reader sees "<" and ">" as they are.
func writeJSONValue(buf *bytes.Buffer, v any) error {
	var tmp bytes.Buffer

	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode log record value: %w", err)
	}

	// Encode appends a newline. A record line holds none.
	buf.Write(bytes.TrimSuffix(tmp.Bytes(), []byte("\n")))

	return nil
}
