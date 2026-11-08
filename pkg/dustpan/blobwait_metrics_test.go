package dustpan_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestBlobDeleteWaitIsCounted drives each deleter that the server wires the
// wait hook to: a workspace purge, a stack purge and the artifact sweep. Each
// case records one running backup, drives the deleter, and proves that the
// refused take of the delete lease counts once as a wait for the backup, and
// that no other reason counts.
func TestBlobDeleteWaitIsCounted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		drive func(t *testing.T, w blobWaitWorld)
	}{
		{name: "a workspace purge", drive: driveWorkspacePurge},
		{name: "a stack purge", drive: driveStackPurge},
		{name: "the artifact sweep", drive: driveArtifactSweep},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newBlobWaitWorld(t)
			w.createNamespace(t, "acme", installationName)
			recordRunningBackup(t, w.store)

			tc.drive(t, w)

			got := scrapeMetrics(t, w.fixture)

			checkBlobDeleteWait(t, got, "backup", 1)

			for _, reason := range []string{"restore", "no-guard", "guard-error"} {
				checkBlobDeleteWait(t, got, reason, 0)
			}
		})
	}
}

// blobWaitWorld is a started server over SQLite with a blob store that
// reports each blob as older than the grace period, so the artifact sweep
// reaches the delete lease, and the raw blob store that writes the blobs of a
// case.
type blobWaitWorld struct {
	fixture

	blobs *fs.Store
}

// newBlobWaitWorld opens the stores, wraps the blob store, and starts the
// server. The server holds the job store and the durable store, so it wires
// the artifact sweep too.
func newBlobWaitWorld(t *testing.T) blobWaitWorld {
	t.Helper()

	st, blobs, authn := requireServerPorts(t)

	// The artifact sweep refuses a run whose instance clock differs from the
	// clock of the database by more than the skew, so the injected clock
	// reads the clock of the database once.
	dbNow, err := st.DatabaseNow(t.Context())
	if err != nil {
		t.Fatalf("read the database clock: %v", err)
	}

	srv, err := dustpan.NewServer(append(fullServerOptions(t, st, blobs, authn),
		dustpan.WithBlobStore(agedBlobs{Store: blobs}),
		dustpan.WithJobStore(st.Jobs()),
		dustpan.WithDurableStore(st.DurableOperations()),
		dustpan.WithClock(func() time.Time { return dbNow }),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	base := fixture{store: st, server: ts, srv: srv}

	return blobWaitWorld{fixture: base, blobs: blobs}
}

// agedBlobs reports every listed blob as older than the grace period, so a
// case proves the lease of a delete without moving a clock.
type agedBlobs struct{ blob.Store }

func (a agedBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	page, err := a.Store.ListPage(ctx, req)
	if err != nil {
		return blob.Page{}, err
	}

	for i := range page.Objects {
		page.Objects[i].ModifiedAt = page.Objects[i].ModifiedAt.Add(-2 * blob.UnnamedGracePeriod)
	}

	return page, nil
}

// recordRunningBackup records one running backup, so the guard of every blob
// delete refuses each take while it runs.
func recordRunningBackup(t *testing.T, st *sqlite.Store) {
	t.Helper()

	id, err := backup.ParseID("bkp-wait00000000000001")
	if err != nil {
		t.Fatalf("parse backup ID: %v", err)
	}

	started, err := st.DatabaseNow(t.Context())
	if err != nil {
		t.Fatalf("read the database clock: %v", err)
	}

	running := backup.Record{
		StartedAt: started,
		EndedAt:   time.Time{},
		ID:        id,
		Target:    "dir:/var/backups",
		Error:     "",
		State:     backup.StateRunning,
	}

	if err := st.CreateBackup(t.Context(), running); err != nil {
		t.Fatalf("record the running backup: %v", err)
	}
}

// driveWorkspacePurge creates a workspace with one state blob, deletes it and
// purges it through the inventory API.
func driveWorkspacePurge(t *testing.T, w blobWaitWorld) {
	t.Helper()

	svc := w.srv.Workspaces()

	ws := createLockedWorkspace(t, svc, w.store)
	uploadWorkspaceState(t, svc, ws.ID, `{"version":4,"serial":1,"lineage":"l1"}`)

	if _, err := svc.Unlock(t.Context(), ws.ID, "admin"); err != nil {
		t.Fatalf("unlock workspace: %v", err)
	}

	status, _, body := w.doRaw(
		t, http.MethodDelete, "/api/v2/organizations/acme/workspaces/e2e-workspace", "Bearer "+adminToken,
	)
	if status != http.StatusNoContent {
		t.Fatalf("delete workspace: status %d, body %s", status, body)
	}

	status, body = w.do(
		t, http.MethodDelete, "/api/inventory/deleted-units/opentofu/"+ws.ID.String()+"?organization=acme",
		adminToken, "",
	)
	if status != http.StatusNoContent {
		t.Fatalf("purge workspace: status %d, body %s", status, body)
	}
}

// driveStackPurge saves one checkpoint of a stack, deletes the stack and
// purges it through the inventory API.
func driveStackPurge(t *testing.T, w blobWaitWorld) {
	t.Helper()

	savePulumiCheckpoint(t, w.fixture)

	id := stackIDOf(t, w.fixture, "prod")

	status, body := w.do(t, http.MethodDelete, "/api/stacks/acme/web/prod", adminToken, "")
	if status != http.StatusNoContent {
		t.Fatalf("delete stack: status %d, body %s", status, body)
	}

	status, body = w.do(
		t, http.MethodDelete, "/api/inventory/deleted-units/pulumi/"+id+"?organization=acme", adminToken, "",
	)
	if status != http.StatusNoContent {
		t.Fatalf("purge stack: status %d, body %s", status, body)
	}
}

// driveArtifactSweep stores one queued job and one old artifact blob that no
// row names, and runs the artifact sweep of the server over the job.
func driveArtifactSweep(t *testing.T, w blobWaitWorld) {
	t.Helper()

	ctx := t.Context()

	ns, err := w.store.GetNamespace(ctx, "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	unit, err := identity.UnitObject("opentofu", "acme", "network")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	const (
		jobID  = job.ID("job-wait00000000000001")
		planID = "pln-wait00000000000001"
	)

	j := job.Job{
		ID:             jobID,
		Spec:           artifactJobSpec(unit.WithUnitID("ws-wait00000000000001")),
		State:          job.StateQueued,
		Wait:           job.WaitNone,
		Reason:         "",
		Phases:         nil,
		Current:        0,
		StartedBy:      "",
		Source:         job.SourceAPI,
		Agent:          "",
		AgentMode:      "",
		BreakGlass:     nil,
		Trigger:        nil,
		Delivery:       nil,
		Closed:         nil,
		Verification:   job.VerificationNone,
		IdempotencyKey: "",
		RequestHash:    "",
		Version:        0,
		CreatedAt:      time.Time{},
		UpdatedAt:      time.Time{},
		StateChangedAt: time.Time{},
	}

	if _, _, createErr := w.store.Jobs().Create(ctx, j, nil, ns.Fence(), nil); createErr != nil {
		t.Fatalf("create job: %v", createErr)
	}

	rel, err := job.PlanPartKey(jobID, planID, job.OutputPlanDisplay)
	if err != nil {
		t.Fatalf("plan part key: %v", err)
	}

	key := blob.Key("namespaces/" + ns.ID.String() + "/" + rel.String())
	if err := w.blobs.Put(ctx, key, strings.NewReader("old plan")); err != nil {
		t.Fatalf("put artifact blob: %v", err)
	}

	sweeper := w.srv.ArtifactSweeper()
	if sweeper == nil {
		t.Fatal("the server wired no artifact sweeper")
	}

	if _, err := sweeper.SweepJob(ctx, &j); err == nil {
		t.Fatal("the artifact sweep with a running backup: want an error")
	}
}

// artifactJobSpec returns the spec of the queued artifact job on unit. The
// sweep reads the unit of the job and its namespace.
func artifactJobSpec(unit identity.Object) job.Spec {
	return job.Spec{
		Unit:      unit,
		Operation: job.OperationPlan,
		Purpose:   "",
		Executor:  job.ExecutorRunner,
		Location: job.Location{
			Connection: "", Repository: "", Ref: "", Directory: "", Revision: "",
		},
		Steps:     nil,
		Pool:      "",
		Profile:   "",
		Edits:     nil,
		Delivery:  "",
		Defaulted: nil,
	}
}

// checkBlobDeleteWait checks the value of one reason series of
// dustpan_blob_deletes_waiting_total in output.
func checkBlobDeleteWait(t *testing.T, output, reason string, want float64) {
	t.Helper()

	prefix := `dustpan_blob_deletes_waiting_total{reason="` + reason + `"} `

	found := false

	for line := range strings.Lines(output) {
		if !strings.HasPrefix(line, prefix) {
			continue
		}

		found = true

		got, err := strconv.ParseFloat(strings.TrimSpace(line[len(prefix):]), 64)
		if err != nil {
			t.Fatalf("bad sample %q: %v", line, err)
		}

		if got != want {
			t.Fatalf("sample %q is %v, want %v", line, got, want)
		}
	}

	if !found {
		t.Fatalf("output misses the sample for reason %q:\n%s", reason, output)
	}
}
