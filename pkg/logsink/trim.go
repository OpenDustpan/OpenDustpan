package logsink

import (
	"unicode/utf8"
)

// TruncatedKey marks a record that lost text to fit a size cap.
const TruncatedKey = "truncated"

// TrimToFit trims the long string fields of r until its JSON line holds at
// most maxJSONBytes, and marks the result with "truncated": true. It never
// shortens a protected attribute, the time, the level or the truncated
// mark itself. It answers true when the result fits, and false when the
// record cannot fit even with every trimmable field empty.
func TrimToFit(r Record, maxJSONBytes int) (Record, bool, error) {
	line, err := MarshalJSON(r)
	if err != nil {
		return r, false, err
	}

	if len(line) <= maxJSONBytes {
		return r, true, nil
	}

	out := withTruncated(r)

	for {
		sized, err := MarshalJSON(out)
		if err != nil {
			return r, false, err
		}

		if len(sized) <= maxJSONBytes {
			return out, true, nil
		}

		if !shortenLongest(&out, len(sized)-maxJSONBytes) {
			return r, false, nil
		}
	}
}

// withTruncated answers a copy of r with the truncated mark set.
func withTruncated(r Record) Record {
	out := Record{
		Time:    r.Time,
		Level:   r.Level,
		Message: r.Message,
		Attrs:   append([]Attr(nil), r.Attrs...),
	}

	for i := range out.Attrs {
		if out.Attrs[i].Key == TruncatedKey {
			out.Attrs[i] = Attr{Key: TruncatedKey, Value: true}

			return out
		}
	}

	return Record{
		Time:    out.Time,
		Level:   out.Level,
		Message: out.Message,
		Attrs:   append(out.Attrs, Attr{Key: TruncatedKey, Value: true}),
	}
}

// shortenLongest shortens the longest trimmable string of out by excess
// bytes, to empty at most, and answers whether text was left to shorten.
func shortenLongest(out *Record, excess int) bool {
	longest := len(out.Message)
	best := -1

	for i := range out.Attrs {
		s, ok := out.Attrs[i].Value.(string)
		if !ok || isProtected(out.Attrs[i].Key) || len(s) <= longest {
			continue
		}

		longest = len(s)
		best = i
	}

	if longest == 0 {
		return false
	}

	if best < 0 {
		out.Message = cutString(out.Message, excess)

		return true
	}

	text, ok := out.Attrs[best].Value.(string)
	if !ok {
		return false
	}

	out.Attrs[best] = Attr{Key: out.Attrs[best].Key, Value: cutString(text, excess)}

	return true
}

// cutString shortens s by excess bytes, to empty at most, without splitting
// a UTF-8 sequence.
func cutString(s string, excess int) string {
	want := max(len(s)-excess, 0)

	for want > 0 && !utf8.ValidString(s[:want]) {
		want--
	}

	return s[:want]
}

// isProtected answers whether key is an attribute that TrimToFit never
// shortens: the truncated mark, or a field that the audit entry hash covers.
// The receiver needs every hashed field unchanged to check the hash chain,
// so trimming shortens only other fields and the message.
func isProtected(key string) bool {
	switch key {
	case TruncatedKey,
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
		"fenceVersion":
		return true
	default:
		return false
	}
}
