package storetest

import (
	"testing"

	"opendustpan.org/opendustpan/internal/cron"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// seedBackupTriggers writes one trigger of each kind in the namespace acme,
// when s keeps triggers. A store with no trigger port skips it: the fake of this
// package's own tests is one.
func seedBackupTriggers(t *testing.T, s BackupStore, acme namespace.Namespace) {
	t.Helper()

	keeper, ok := s.(interface{ Triggers() trigger.Store })
	if !ok {
		return
	}

	entry := audit.Entry{
		At: now(), Namespace: acme.Name, Subject: "alice", IDPSubject: "", TokenID: "", Action: "trigger:put",
		Object: acme.Name.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}

	app := graph.Unit{Namespace: acme.Name, Tool: "opentofu", ID: "wks-seed-app"}
	net := graph.Unit{Namespace: acme.Name, Tool: "pulumi", ID: "stk-seed-net"}
	none := graph.Unit{Namespace: "", Tool: "", ID: ""}

	for _, tr := range []*trigger.Trigger{
		{
			ID: "seed-follows", Unit: app, Kind: trigger.KindFollows,
			Frequency: trigger.Frequency{Preset: "", Cron: ""}, Source: net, Enabled: true, LastSaver: "alice", LastSaverID: "prn-alice",
		},
		{
			ID: "seed-schedule", Unit: app, Kind: trigger.KindSchedule,
			Frequency: trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}, Source: none, Enabled: false,
			LastSaver: "alice", LastSaverID: "prn-alice",
		},
		{
			ID: "seed-custom", Unit: net, Kind: trigger.KindSchedule,
			Frequency: trigger.Frequency{Preset: "", Cron: "30 4 * * 1-5"}, Source: none, Enabled: true,
			LastSaver: "alice", LastSaverID: "prn-alice",
		},
		{
			ID: "seed-review", Unit: net, Kind: trigger.KindReviewRequestPush,
			Frequency: trigger.Frequency{Preset: "", Cron: ""}, Source: none, Enabled: true, LastSaver: "alice", LastSaverID: "prn-alice",
		},
	} {
		if err := keeper.Triggers().Put(t.Context(), *tr, acme.Fence(), entry); err != nil {
			t.Fatalf("put trigger %s: %v", tr.ID, err)
		}
	}
}
