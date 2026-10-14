package cli

import (
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// AuditSettings answers the changes of a start entry (ADR 63 decision 10):
// each resolved setting with its key, its effective value as the new value,
// and its layer. A start entry records values, not a change, so Old stays
// nil.
// Resolve already replaced the value of a sensitive setting with set or not
// set, so no sensitive value reaches the entry (decision 6).
func AuditSettings(resolved []Resolved) []audit.Change {
	out := make([]audit.Change, 0, len(resolved))

	for _, r := range resolved {
		out = append(out, audit.Change{
			Key: r.Setting.Name(), Old: nil, New: r.Value, Layer: string(r.Source), TakesEffect: "",
		})
	}

	return out
}

// SettingsEntry answers an audit entry of the settings of this instance in
// the server scope: the kind change, the action audit.ActionManageSettings,
// the instance ID as the subject and channel as the channel (ADR 63
// decisions 9 and 10).
func SettingsEntry(channel audit.Channel, instanceID string, at time.Time, changes []audit.Change) audit.Entry {
	return audit.Entry{
		At:         at.UTC(),
		Namespace:  audit.ServerScope,
		Subject:    instanceID,
		IDPSubject: "",
		TokenID:    "",
		Action:     audit.ActionManageSettings,
		Object:     audit.ServerScope.String(),
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		Changes:    changes,
		SourceIP:   "",
		UserAgent:  "",
		Channel:    channel,
		Kind:       audit.KindChange,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  "",
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}
}
