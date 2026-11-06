package unitsettings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
)

// notSet is the audit value of a field that has no value.
const notSet = "(not set)"

// ChangeEntry returns e with the action AuditActionChanged and one change for
// each field that differs between old and next. old is nil for a unit with no
// record. A store calls it in the transaction of the write, with the record
// that the write replaces, so Old is the value that the write replaced. The
// steps are recorded as their number and a SHA-256 digest, because an
// argument of a step may hold a value that the audit log must not keep. It
// returns e with no change when the settings are equal.
func ChangeEntry(e audit.Entry, old, next *Settings) audit.Entry {
	var none Settings
	if old == nil {
		old = &none
	}

	out := e
	out.Action = AuditActionChanged
	out.Changes = append([]audit.Change(nil), e.Changes...)

	out.Changes = append(out.Changes, auditFields(old, next)...)

	return out
}

// ResetEntry returns e with the action AuditActionReset and one change for
// each field that the record had set.
func ResetEntry(e audit.Entry, old *Settings) audit.Entry {
	var none Settings

	out := ChangeEntry(e, old, &none)
	out.Action = AuditActionReset

	return out
}

// DefaultsEntry returns e with the action AuditActionDefaultsChanged and a
// change for the pool when it differs.
func DefaultsEntry(e audit.Entry, old, next NamespaceDefaults) audit.Entry {
	out := e
	out.Action = AuditActionDefaultsChanged
	out.Changes = append([]audit.Change(nil), e.Changes...)

	if old.Pool != next.Pool {
		out.Changes = append(out.Changes, change("pool", old.Pool, next.Pool))
	}

	return out
}

// Equal reports whether a and b hold the same fields. It ignores the unit, the
// version and the time.
func Equal(a, b *Settings) bool {
	return len(auditFields(a, b)) == 0
}

// auditFields returns the change of each field that differs, in the order of
// the record.
func auditFields(a, b *Settings) []audit.Change {
	var out []audit.Change

	add := func(key, oldV, newV string) {
		if oldV != newV {
			out = append(out, change(key, oldV, newV))
		}
	}

	add("executor", string(a.Executor), string(b.Executor))
	add("pool", a.Pool, b.Pool)
	add("codeLocation.connection", a.CodeLocation.Connection, b.CodeLocation.Connection)
	add("codeLocation.repository", a.CodeLocation.Repository, b.CodeLocation.Repository)
	add("codeLocation.ref", a.CodeLocation.Ref, b.CodeLocation.Ref)
	add("codeLocation.directory", a.CodeLocation.Directory, b.CodeLocation.Directory)
	add("writeBack.delivery", string(a.WriteBack.Delivery), string(b.WriteBack.Delivery))
	add("writeBack.files", strings.Join(a.WriteBack.Files, ","), strings.Join(b.WriteBack.Files, ","))
	add(
		"reviewRequests.enabled",
		strconv.FormatBool(a.ReviewRequests.Enabled),
		strconv.FormatBool(b.ReviewRequests.Enabled),
	)
	add("steps", stepsDigest(a.Steps), stepsDigest(b.Steps))

	return out
}

// stepsDigest summarizes steps for the audit log: their number and the digest
// of their stored form.
func stepsDigest(steps []job.Step) string {
	if len(steps) == 0 {
		return ""
	}

	doc, err := json.Marshal(encodeSteps(steps))
	if err != nil {
		return fmt.Sprintf("%d steps (not encoded)", len(steps))
	}

	sum := sha256.Sum256(doc)

	return fmt.Sprintf("%d steps, sha256:%s", len(steps), hex.EncodeToString(sum[:8]))
}

// change makes one audit change. An empty value shows as notSet; an old
// value that is empty has no Old.
func change(key, oldV, newV string) audit.Change {
	var old *string

	if oldV != "" {
		old = &oldV
	}

	if newV == "" {
		newV = notSet
	}

	return audit.Change{Key: key, Old: old, New: newV, Layer: "", TakesEffect: ""}
}
