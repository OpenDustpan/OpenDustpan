package storetest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

const (
	devJobStarter  = "dev:starter"
	devJobApprover = "dev:approver"
)

// devJobBody is the stored body of a job that the dev person devJobStarter
// started and the dev person devJobApprover approved, on a gate that needs one
// approval.
const devJobBody = `{"spec":{"unit":{"tool":"opentofu","namespace":"acme","name":"net","unitID":"unt-dev"},` +
	`"operation":"apply","purpose":"","executor":"","location":{"connection":"","repository":"","ref":"",` +
	`"directory":"","revision":""},"steps":[],"pool":"","profile":"","edits":[],"delivery":""},` +
	`"reason":"","phases":[{"kind":"plan","operation":"","claims":0,"executor":"","steps":[],"stateVersion":"",` +
	`"plan":null,"gates":[{"type":"approval","approvals":1,"groups":[],"groupIds":[],"allowSelf":false,` +
	`"timeoutNanos":0,"bindPlan":false}],"approvals":[{"gate":"approval","subject":"` + devJobApprover + `",` +
	`"agent":"","verdict":"approve","comment":"","plan":null,"at":"2026-10-20T09:30:00Z","auth":"",` +
	`"authAt":"0001-01-01T00:00:00Z"}],"startedAt":"0001-01-01T00:00:00Z","finishedAt":"0001-01-01T00:00:00Z"}],` +
	`"startedBy":"` + devJobStarter + `","source":"api","agent":"","agentMode":"","breakGlass":null,"delivery":null,` +
	`"closed":null,"verification":"","requestHash":""}`

// devJobCase is the strip case of a job that a dev person started and approved.
// The strip removes the principals, and the body of the job keeps both
// subjects as history text. The approval does not count on a production
// installation.
func devJobCase() devStripCase {
	var tc devStripCase

	tc.name = "a job that a dev person started and approved keeps its history and gets no approval"
	tc.add = func(t *testing.T, e *devExport) *devExport {
		t.Helper()

		var namespaceID string

		for _, row := range readPortableLines(t, "namespaces", e.files[backup.PortablePath("namespaces")]) {
			if textOf(row, "name") == "acme" {
				namespaceID = textOf(row, "id")
			}
		}

		if namespaceID == "" {
			t.Fatal("the seeded export has no namespace acme")
		}

		out := e.with(t, "principals", DevPrincipalRow("prn-starter", devJobStarter),
			DevPrincipalRow("prn-approver", devJobApprover))

		return out.with(t, "jobs", map[string]any{
			"id": "job-dev", "namespace_id": namespaceID, "unit_id": "unt-dev", "seq": 1000, "state": "queued",
			"wait": "", "current_phase": 0, "current_changes": false, "current_operation": "",
			"idempotency_key": "", "version": 1, "body": devJobBody, "created_at_ms": 1, "updated_at_ms": 1,
			"state_changed_at_ms": 1,
		})
	}
	tc.classes = map[backup.DevRemovalClass]int64{backup.RemovedPrincipals: 2}
	tc.after = checkDevJobAfterStrip

	return tc
}

// checkDevJobAfterStrip reads the job after the swap. The two subjects stay in
// the body, and a production policy counts neither.
func checkDevJobAfterStrip(t *testing.T, dst DevDataStore) {
	t.Helper()

	var mine map[string]any

	for _, row := range tableRows(t, dst, "jobs") {
		if textOf(row, "id") == "job-dev" {
			mine = row
		}
	}

	if mine == nil {
		t.Fatal("job job-dev is gone after the strip")
	}

	if body := textOf(mine, "body"); body != devJobBody {
		t.Fatalf("job body after the strip = %s, want the body unchanged: %s", body, devJobBody)
	}

	row := jobstore.Row{
		ID: "job-dev", Namespace: "acme", NamespaceID: "", UnitID: "unt-dev", Seq: 1000, State: job.StateQueued,
		Wait: job.WaitNone, Phase: 0, Changes: false, Operation: "", Key: "", Version: 1, Body: textOf(mine, "body"),
		CreatedMs: 1, UpdatedMs: 1, StateChangedMs: 1,
	}

	got, err := jobstore.Decode(&row)
	if err != nil {
		t.Fatalf("decode the job after the strip: %v", err)
	}

	if got.StartedBy != devJobStarter || len(got.Phases) != 1 || len(got.Phases[0].Approvals) != 1 ||
		got.Phases[0].Approvals[0].Subject != devJobApprover {
		t.Fatalf("job after the strip = started by %q, phases %+v; want the history text of both subjects",
			got.StartedBy, got.Phases)
	}

	gates, approvals := got.Phases[0].Gates, got.Phases[0].Approvals

	production := gate.ApproverPolicy{Production: true, IsDev: principal.IsDevSubject}
	if s := job.GatesStatus(gates, approvals, got.StartedBy, production); s != job.GateWaiting {
		t.Fatalf("status of the dev approval on production = %s, want %s", s, job.GateWaiting)
	}

	dev := gate.ApproverPolicy{Production: false, IsDev: principal.IsDevSubject}
	if s := job.GatesStatus(gates, approvals, got.StartedBy, dev); s != job.GateOpen {
		t.Fatalf("status of the dev approval on a dev installation = %s, want %s", s, job.GateOpen)
	}
}
