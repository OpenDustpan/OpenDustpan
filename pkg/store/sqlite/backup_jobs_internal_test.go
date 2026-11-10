package sqlite

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// stagedJobID is the ID of the job of a staged backup in these tests.
const stagedJobID = job.ID("job-aaaaaaaaaaaaaaaaaaaa")

// seedJobRow makes the namespace acme, its workspace network, and one queued
// job of that workspace. It returns the ID of the job.
func seedJobRow(t *testing.T, s *Store) job.ID {
	t.Helper()

	now := time.UnixMilli(1_700_000_000_000).UTC()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   now,
		UpdatedAt:   now,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID("ws-1"),
		Ref:         workspace.Ref{Namespace: ns.Name, Name: "network"},
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Revision: 1,
		Origin:   identity.StateOrigin{Job: "", Commit: ""},
	}
	if err := s.CreateWorkspace(t.Context(), w, ns.Fence()); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	unit, err := identity.UnitObject("opentofu", ns.Name, "network")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	unit = unit.WithUnitID(string(w.ID))

	phases, err := job.DefaultPhases(job.ExecutorRunner, job.OperationPlan)
	if err != nil {
		t.Fatalf("default phases: %v", err)
	}

	j := job.Job{
		ID: stagedJobID, State: job.StateQueued, Wait: job.WaitNone, Reason: "", Current: 0,
		StartedBy: "al", Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil, Trigger: nil,
		Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "", RequestHash: "",
		Version: 0, CreatedAt: time.Time{}, UpdatedAt: time.Time{}, StateChangedAt: time.Time{}, NamespaceID: "",
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationPlan, Purpose: job.PurposeNone, Executor: job.ExecutorRunner,
			Location: job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""},
			Steps: []job.Step{{
				Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
				Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
			}},
			Pool: "rpl-default", Profile: "prf-default", Edits: nil, Delivery: "", Defaulted: nil,
		},
		Phases: make([]job.Phase, len(phases)),
	}

	for i, kind := range phases {
		j.Phases[i].Kind = kind
	}

	start := &durable.StartRequest{
		Key: job.PhaseKey(j.ID, 0), Kind: job.RunnerKind(j.Spec.Pool), Version: 1, Input: nil,
	}

	stored, created, err := s.Jobs().Create(t.Context(), j, start, ns.Fence(), nil)
	if err != nil || !created {
		t.Fatalf("create job = created %v, %v", created, err)
	}

	return stored.ID
}

// tamperedJobBodies lists the changes that make the stored body fail the
// decode of a job.
func tamperedJobBodies() map[string]string {
	return map[string]string{
		"a body that is not JSON":        `UPDATE jobs SET body = '{'`,
		"a source that is unknown":       `UPDATE jobs SET body = replace(body, '"source":"api"', '"source":"bogus"')`,
		"an agent mode that is unknown":  `UPDATE jobs SET body = replace(body, '"agentMode":""', '"agentMode":"bogus"')`,
		"a verification that is unknown": `UPDATE jobs SET body = replace(body, '"verification":""', '"verification":"bogus"')`,
		"a unit that the row disagrees with": `UPDATE jobs SET body = replace(body, '"unitID":"ws-1"',
			'"unitID":"ws-2"')`,
	}
}

// TestCheckStagedJobs proves the restore check of the job rows: it accepts a
// row that the store wrote, and it refuses each body that a reader does not
// decode. A body that fails is also not served by a read.
func TestCheckStagedJobs(t *testing.T) {
	t.Parallel()

	t.Run("a row that the store wrote passes", func(t *testing.T) {
		t.Parallel()

		s := openInternalTemplated(t)
		seedJobRow(t, s)

		if err := checkStagedJobs(t.Context(), s.conn); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for name, update := range tamperedJobBodies() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			checkTamperedJobBody(t, update)
		})
	}
}

// checkTamperedJobBody changes the only row with update, and checks that the
// restore check refuses it and that a read does not serve it.
func checkTamperedJobBody(t *testing.T, update string) {
	t.Helper()

	s := openInternalTemplated(t)
	id := seedJobRow(t, s)

	if _, err := s.conn.ExecContext(t.Context(), update); err != nil {
		t.Fatalf("change the body: %v", err)
	}

	if err := checkStagedJobs(t.Context(), s.conn); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	if _, err := s.Jobs().Get(t.Context(), id); !errors.Is(err, job.ErrDamaged) {
		t.Fatalf("get of a damaged job = %v, want ErrDamaged", err)
	}
}

// TestCheckStagedJobsRefusesAMissingNamespace proves that a job whose
// namespace the backup does not hold is corrupt. An inner join would skip such
// a row, and the restore would bring it in.
func TestCheckStagedJobsRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedJobRow(t, s)

	// The foreign key refuses this change, so the connection that makes it
	// switches the key check off. It is closed before the check runs.
	pinned, err := s.conn.Conn(t.Context())
	if err != nil {
		t.Fatalf("take a connection: %v", err)
	}

	for _, stmt := range []string{`PRAGMA foreign_keys = OFF`, `UPDATE jobs SET namespace_id = 'ns-gone'`} {
		if _, execErr := pinned.ExecContext(t.Context(), stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}

	if closeErr := pinned.Close(); closeErr != nil {
		t.Fatalf("release the connection: %v", closeErr)
	}

	err = checkStagedJobs(t.Context(), s.conn)
	if err == nil || !errors.Is(err, backup.ErrCorrupt) || !strings.Contains(err.Error(), "has no namespace") {
		t.Fatalf("check = %v, want the refusal of a job with no namespace", err)
	}
}
