package objectcontext

import (
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
)

// ChangesOf returns the audit changes of a write that moves the API fields of
// one object from before to after. It names each field in fields that
// differs. A text field holds its length in bytes, because an entry never
// holds a runbook or a summary. The owners and the on-call text hold their
// values, because a person needs them during an incident. Links hold their
// count.
func ChangesOf(before, after Fields, fields []Field) []audit.Change {
	var out []audit.Change

	for _, f := range fields {
		old, now := fieldText(before, f), fieldText(after, f)
		if old == now {
			continue
		}

		out = append(out, audit.Change{Key: string(f), Old: &old, New: now, Layer: "", TakesEffect: ""})
	}

	return out
}

// fieldText returns the text of field in f for an audit change.
func fieldText(f Fields, field Field) string {
	switch field {
	case FieldSummary:
		return strconv.Itoa(len(f.Summary)) + " bytes"
	case FieldRunbook:
		return strconv.Itoa(len(f.Runbook)) + " bytes"
	case FieldOwners:
		return strings.Join(f.Owners, ", ")
	case FieldOnCall:
		return f.OnCall
	case FieldLinks:
		return strconv.Itoa(len(f.Links)) + " links"
	default:
		return ""
	}
}

// OwnerSet returns the distinct owners of f, sorted: the owners that the
// owner index of a store holds for an entry, one row for each.
func OwnerSet(f Fields) []string {
	out := slices.Clone(f.Owners)
	slices.Sort(out)

	return slices.Compact(out)
}
