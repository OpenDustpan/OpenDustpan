package audit

import (
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
)

// cefVendor and cefProduct name the sender in the header of each CEF line.
// cefVersion is the version of the field mapping below, not of the server.
const (
	cefVendor  = "OpenDustpan"
	cefProduct = "OpenDustpan"
	cefVersion = "1"
)

// cefEscape escapes s for a CEF field. The backslash and the line ends are
// escaped in both places. The pipe ends a header field, and the equals sign
// ends the key of an extension value, so each is escaped where it matters:
// extension is true for a value of the extension, false for the header. A
// line feed or a carriage return would end the line, so they leave as the
// two characters "\n" and "\r".
func cefEscape(s string, extension bool) string {
	var sb strings.Builder

	for _, r := range s {
		switch {
		case r == '\\':
			sb.WriteString(`\\`)
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\r':
			sb.WriteString(`\r`)
		case r == '|' && !extension:
			sb.WriteString(`\|`)
		case r == '=' && extension:
			sb.WriteString(`\=`)
		default:
			sb.WriteRune(r)
		}
	}

	return sb.String()
}

// cefSeverity maps the outcome of an entry to the severity of its line:
// success 3, failure 5, denied 7. An outcome that the port does not know
// reads as 10, so a reader looks at it first.
func cefSeverity(o audit.Outcome) string {
	switch o {
	case audit.OutcomeSuccess:
		return "3"
	case audit.OutcomeFailure:
		return "5"
	case audit.OutcomeDenied:
		return "7"
	default:
		return "10"
	}
}

// cefPair is one key and value of the extension of a CEF line. A non-empty
// label adds the label key of the value.
type cefPair struct {
	key, value, label string
}

// cefLine returns the CEF line of e, with no line end. The header holds the
// vendor, the product, the version of the mapping, the action as the
// signature ID, the kind as the name, and the severity. The extension keys
// are fixed by the table of the audit log guide. A field with no value
// writes no key. A custom key gets a label key, so a reader knows what it
// holds.
func cefLine(e audit.Entry) string {
	pairs := []cefPair{
		{"rt", strconv.FormatInt(e.AtMs(), 10), ""},
		{"suser", e.Subject, ""},
		{"src", e.SourceIP, ""},
		{"act", e.Action, ""},
		{"outcome", string(e.Outcome), ""},
		{"requestClientApplication", e.UserAgent, ""},
		{"reason", e.Reason, ""},
		{"cn1", strconv.FormatInt(e.Sequence, 10), "sequence"},
		{"cs1", e.Namespace.String(), "scope"},
		{"cs2", e.Object, "object"},
		{"cs3", string(e.Channel), "channel"},
		{"cs4", e.RequestID, "requestID"},
		{"cs5", e.Hash, "hash"},
		{"cs6", e.PrevHash, "prevHash"},
		{"flexString1", e.ObjectID, "objectID"},
		{"flexString2", e.PrincipalKind, "principalKind"},
	}

	var sb strings.Builder

	for _, field := range []string{
		"CEF:0", cefVendor, cefProduct, cefVersion, e.Action, string(e.Kind), cefSeverity(e.Outcome),
	} {
		sb.WriteString(cefEscape(field, false))
		sb.WriteByte('|')
	}

	for _, p := range pairs {
		if p.value == "" {
			continue
		}

		if sb.String()[sb.Len()-1] != '|' {
			sb.WriteByte(' ')
		}

		sb.WriteString(p.key + "=" + cefEscape(p.value, true))

		if p.label != "" {
			sb.WriteString(" " + p.key + "Label=" + p.label)
		}
	}

	return sb.String()
}
