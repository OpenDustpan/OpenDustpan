package sqlite_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cron"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// triggerOf returns an enabled trigger for unit, with the given kind. A schedule
// trigger runs daily, and a follows trigger follows source.
func triggerOf(id trigger.ID, kind trigger.Kind, unit, source graph.Unit) trigger.Trigger {
	tr := trigger.Trigger{
		ID:        id,
		Unit:      unit,
		Kind:      kind,
		Frequency: trigger.Frequency{Preset: "", Cron: ""},
		Source: graph.Unit{
			Namespace: "",
			Tool:      "",
			ID:        "",
		},
		Enabled:     true,
		LastSaver:   "user:ada",
		LastSaverID: "prn-user:ada",
	}

	if kind == trigger.KindSchedule {
		tr.Frequency.Preset = cron.PresetDaily
	}

	if kind == trigger.KindFollows {
		tr.Source = source
	}

	return tr
}

// triggerEntry returns a valid audit entry for a write of a trigger in acme.
func triggerEntry() audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1), Namespace: "acme", Subject: "user:ada", IDPSubject: "", TokenID: "",
		Action: "trigger:put", Object: "opentofu:acme/b", Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

// putTrigger stores tr and fails the test when the store refuses it.
func putTrigger(t *testing.T, store trigger.Store, fence namespace.Fence, tr trigger.Trigger) {
	t.Helper()

	if err := store.Put(t.Context(), tr, fence, triggerEntry()); err != nil {
		t.Fatalf("put trigger %s: %v", tr.ID, err)
	}
}
