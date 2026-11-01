package otlp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"

	"opendustpan.org/opendustpan/pkg/logsink"
)

// errNesting reports an attribute value that nests deeper than maxNesting.
var errNesting = errors.New("attribute value nests too deep")

// encode writes records as one ExportLogsServiceRequest in the OTLP JSON
// encoding.
func encode(service string, records []logsink.Record) ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteString(`{"resourceLogs":[{"resource":{"attributes":[`)

	if err := writeKeyValue(&buf, "service.name", service); err != nil {
		return nil, err
	}

	buf.WriteString(`]},"scopeLogs":[{"scope":{"name":`)

	if err := writeJSON(&buf, scopeName); err != nil {
		return nil, err
	}

	buf.WriteString(`},"logRecords":[`)

	for i := range records {
		if i > 0 {
			buf.WriteByte(',')
		}

		if err := writeRecord(&buf, records[i]); err != nil {
			return nil, err
		}
	}

	buf.WriteString(`]}]}]}`)

	return buf.Bytes(), nil
}

// writeRecord writes one LogRecord.
func writeRecord(buf *bytes.Buffer, r logsink.Record) error {
	number, text := severity(r.Level)

	buf.WriteString(`{"timeUnixNano":"`)
	buf.WriteString(strconv.FormatInt(r.Time.UnixNano(), 10))
	buf.WriteString(`","severityNumber":`)
	buf.WriteString(strconv.Itoa(number))
	buf.WriteString(`,"severityText":`)

	if err := writeJSON(buf, text); err != nil {
		return err
	}

	buf.WriteString(`,"body":{"stringValue":`)

	if err := writeJSON(buf, r.Message); err != nil {
		return err
	}

	buf.WriteString(`},"attributes":[`)

	for i, a := range r.Attrs {
		if i > 0 {
			buf.WriteByte(',')
		}

		if err := writeKeyValue(buf, a.Key, a.Value); err != nil {
			return fmt.Errorf("attribute %q: %w", a.Key, err)
		}
	}

	buf.WriteString(`]}`)

	return nil
}

// severity answers the OTLP severity number and text of a level.
func severity(l logsink.Level) (int, string) {
	switch {
	case l >= logsink.LevelError:
		return severityError, "ERROR"
	case l >= logsink.LevelWarn:
		return severityWarn, "WARN"
	case l >= logsink.LevelInfo:
		return severityInfo, "INFO"
	default:
		return severityDebug, "DEBUG"
	}
}

// writeJSON appends the JSON form of v without a newline and without HTML
// escaping.
func writeJSON(buf *bytes.Buffer, v any) error {
	var tmp bytes.Buffer

	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode OTLP value: %w", err)
	}

	buf.Write(bytes.TrimSuffix(tmp.Bytes(), []byte("\n")))

	return nil
}

// writeKeyValue writes {"key":...,"value":<AnyValue>}.
func writeKeyValue(buf *bytes.Buffer, key string, value any) error {
	buf.WriteString(`{"key":`)

	if err := writeJSON(buf, key); err != nil {
		return err
	}

	buf.WriteString(`,"value":`)

	if err := writeAnyValue(buf, value); err != nil {
		return err
	}

	buf.WriteByte('}')

	return nil
}

// writeAnyValue writes v as an AnyValue. The cases cover the types that the
// port names. Any other type goes through its JSON form.
func writeAnyValue(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString(`{}`)

		return nil
	case string:
		return writeTyped(buf, "stringValue", x)
	case bool:
		return writeTyped(buf, "boolValue", x)
	case int:
		return writeInt(buf, int64(x))
	case int64:
		return writeInt(buf, x)
	case float64:
		return writeFloat(buf, x)
	case json.RawMessage:
		return writeRaw(buf, x)
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return fmt.Errorf("encode OTLP value: %w", err)
		}

		return writeRaw(buf, raw)
	}
}

// writeTyped writes {"<field>":<value>}.
func writeTyped(buf *bytes.Buffer, field string, value any) error {
	buf.WriteString(`{"` + field + `":`)

	if err := writeJSON(buf, value); err != nil {
		return err
	}

	buf.WriteByte('}')

	return nil
}

// writeInt writes an intValue. The JSON mapping of OTLP writes an int64 as
// a string.
func writeInt(buf *bytes.Buffer, n int64) error {
	buf.WriteString(`{"intValue":"` + strconv.FormatInt(n, 10) + `"}`)

	return nil
}

// writeFloat writes a doubleValue. JSON has no NaN or infinity, so those
// values fail.
func writeFloat(buf *bytes.Buffer, f float64) error {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("encode OTLP value: %w", errNotFinite)
	}

	buf.WriteString(`{"doubleValue":`)

	if err := writeJSON(buf, f); err != nil {
		return err
	}

	buf.WriteByte('}')

	return nil
}

// errNotFinite reports a float that JSON cannot hold.
var errNotFinite = errors.New("float is not finite")

// errTrailing reports raw JSON with text after the first value.
var errTrailing = errors.New("JSON has text after the value")

// writeRaw converts the JSON text raw to an AnyValue. An object becomes a
// kvlistValue in the order of its keys, an array becomes an arrayValue, and
// a scalar keeps its type.
func writeRaw(buf *bytes.Buffer, raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	if err := writeToken(buf, dec, 0); err != nil {
		return fmt.Errorf("encode OTLP value: %w", err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("encode OTLP value: %w", errTrailing)
	}

	return nil
}

// writeToken reads one JSON value from dec and writes it as an AnyValue.
func writeToken(buf *bytes.Buffer, dec *json.Decoder, depth int) error {
	if depth > maxNesting {
		return errNesting
	}

	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read JSON: %w", err)
	}

	switch t := tok.(type) {
	case nil:
		buf.WriteString(`{}`)

		return nil
	case string:
		return writeTyped(buf, "stringValue", t)
	case bool:
		return writeTyped(buf, "boolValue", t)
	case json.Number:
		return writeNumber(buf, t)
	case json.Delim:
		if t == '{' {
			return writeObject(buf, dec, depth)
		}

		return writeArray(buf, dec, depth)
	default:
		return fmt.Errorf("%w: %T", errUnexpectedToken, tok)
	}
}

// errUnexpectedToken reports a JSON token of an unknown type.
var errUnexpectedToken = errors.New("unexpected JSON token")

// writeNumber writes an intValue for an integer in the range of int64, else
// a doubleValue.
func writeNumber(buf *bytes.Buffer, n json.Number) error {
	if i, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
		return writeInt(buf, i)
	}

	f, err := strconv.ParseFloat(n.String(), 64)
	if err != nil {
		return fmt.Errorf("read JSON number: %w", err)
	}

	return writeFloat(buf, f)
}

// writeObject writes the members of a JSON object, whose "{" dec has read,
// as a kvlistValue.
func writeObject(buf *bytes.Buffer, dec *json.Decoder, depth int) error {
	buf.WriteString(`{"kvlistValue":{"values":[`)

	for first := true; dec.More(); first = false {
		if !first {
			buf.WriteByte(',')
		}

		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("read JSON: %w", err)
		}

		key, isString := keyTok.(string)
		if !isString {
			return fmt.Errorf("%w: %T", errUnexpectedToken, keyTok)
		}

		buf.WriteString(`{"key":`)

		if err := writeJSON(buf, key); err != nil {
			return err
		}

		buf.WriteString(`,"value":`)

		if err := writeToken(buf, dec, depth+1); err != nil {
			return err
		}

		buf.WriteByte('}')
	}

	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("read JSON: %w", err)
	}

	buf.WriteString(`]}}`)

	return nil
}

// writeArray writes the elements of a JSON array, whose "[" dec has read,
// as an arrayValue.
func writeArray(buf *bytes.Buffer, dec *json.Decoder, depth int) error {
	buf.WriteString(`{"arrayValue":{"values":[`)

	for first := true; dec.More(); first = false {
		if !first {
			buf.WriteByte(',')
		}

		if err := writeToken(buf, dec, depth+1); err != nil {
			return err
		}
	}

	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("read JSON: %w", err)
	}

	buf.WriteString(`]}}`)

	return nil
}
