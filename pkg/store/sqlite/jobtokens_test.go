package sqlite_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/jobtoken/jobtokentest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// jobTokenMs is the time of the audit entries that the harness writes, in
// Unix milliseconds.
const jobTokenMs = 1_700_000_000_000

// jobTokenNow returns the time of the audit entries that the harness writes.
func jobTokenNow() time.Time {
	return time.UnixMilli(jobTokenMs).UTC()
}

// jobTokenWorld makes the world of a job token case on one SQLite store. The
// raw handle writes the rows that the suite changes without a check.
type jobTokenWorld struct {
	s   *sqlite.Store
	raw *sql.DB
}

// TestJobTokensConformance runs the conformance suite of the job token store.
func TestJobTokensConformance(t *testing.T) {
	t.Parallel()

	jobtokentest.Run(t, func(t *testing.T) jobtokentest.Harness {
		t.Helper()

		s, raw := openJobTokenStore(t)

		w := &jobTokenWorld{s: s, raw: raw}

		return w.harness()
	})
}

// harness returns the hooks of the suite over the world.
func (w *jobTokenWorld) harness() jobtokentest.Harness {
	return jobtokentest.Harness{
		Store:               w.s.JobTokens(),
		Namespace:           w.makeNamespace,
		DeleteNamespace:     w.deleteNamespace,
		RenameNamespace:     w.renameNamespace,
		Unit:                w.makeUnit,
		Principal:           w.makePrincipal,
		Disable:             w.disable,
		Delete:              w.deletePrincipal,
		Rename:              w.rename,
		Job:                 w.makeJob,
		Takeover:            w.takeover,
		SetJobState:         w.setJobState,
		EndClaim:            w.endClaim,
		ReleaseClaim:        w.releaseClaim,
		EndJob:              w.endJob,
		MoveJob:             w.moveJob,
		WriteRaw:            w.writeRaw,
		WriteRawInNamespace: w.writeRawInNamespace,
		Audit:               w.entries,
	}
}

// makeNamespace makes the namespace called name and returns its fence.
func (w *jobTokenWorld) makeNamespace(t *testing.T, name string) namespace.Fence {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           jobTokenNow(),
		Name:                namespace.Name(name),
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := w.s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}

	return ns.Fence()
}

// makeUnit makes the reference of a unit. No port stores a unit reference, so
// the world keeps it in memory.
func (w *jobTokenWorld) makeUnit(t *testing.T, ns, tool, name string) identity.Object {
	t.Helper()

	ref, err := identity.UnitObject(tool, namespace.Name(ns), name)
	if err != nil {
		t.Fatalf("unit reference: %v", err)
	}

	return ref.WithUnitID(ident.NewID("unt-"))
}

// makePrincipal makes the principal with the subject subject.
func (w *jobTokenWorld) makePrincipal(t *testing.T, subject string) jobtokentest.Starter {
	t.Helper()

	id := ident.NewID("prn-")
	p := principal.Principal{
		CreatedAt: jobTokenNow(), UpdatedAt: jobTokenNow(), DisabledAt: time.Time{}, ID: id, Subject: subject,
		DisplayName: subject, Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}

	entry := storetest.ServerChange(jobTokenNow(), principal.Ref(id))

	if err := w.s.CreatePrincipal(t.Context(), p, entry); err != nil {
		t.Fatalf("create principal %q: %v", subject, err)
	}

	var starter identity.Principal

	starter.Subject = subject
	starter.DisplayName = subject
	starter.Kind = identity.KindPerson

	return jobtokentest.Starter{ID: id, Principal: starter}
}

// disable disables the principal s.
func (w *jobTokenWorld) disable(t *testing.T, s jobtokentest.Starter) {
	t.Helper()

	entry := storetest.ServerChange(jobTokenNow(), principal.Ref(s.ID))

	if err := w.s.DisablePrincipal(t.Context(), s.ID, entry); err != nil {
		t.Fatalf("disable principal %s: %v", s.ID, err)
	}
}

// deletePrincipal deletes the principal s.
func (w *jobTokenWorld) deletePrincipal(t *testing.T, s jobtokentest.Starter) {
	t.Helper()

	entry := storetest.ServerChange(jobTokenNow(), principal.Ref(s.ID))

	if err := w.s.DeletePrincipal(t.Context(), s.ID, entry); err != nil {
		t.Fatalf("delete principal %s: %v", s.ID, err)
	}
}

// deleteNamespace deletes the namespace row of ns, as a restore does with the
// foreign key checks off.
func (w *jobTokenWorld) deleteNamespace(t *testing.T, ns namespace.Name) {
	t.Helper()

	err := sqlite.RunWithoutForeignKeys(
		t.Context(), w.raw, `DELETE FROM namespaces WHERE name = ?`, ns.String(),
	)
	if err != nil {
		t.Fatalf("delete namespace %q: %v", ns, err)
	}
}

// renameNamespace changes the name of the namespace ns to newName, as a
// failover does. The stable namespace ID stays.
func (w *jobTokenWorld) renameNamespace(t *testing.T, ns namespace.Name, newName string) {
	t.Helper()

	err := sqlite.RunWithoutForeignKeys(
		t.Context(), w.raw, `UPDATE namespaces SET name = ? WHERE name = ?`, newName, ns.String(),
	)
	if err != nil {
		t.Fatalf("rename namespace %q to %q: %v", ns, newName, err)
	}
}

// rename returns the reference of unit under newName. The stable unit ID stays.
func (w *jobTokenWorld) rename(t *testing.T, unit identity.Object, newName string) identity.Object {
	t.Helper()

	ref, err := identity.UnitObject(unit.Tool(), unit.Namespace(), newName)
	if err != nil {
		t.Fatalf("renamed unit reference: %v", err)
	}

	return ref.WithUnitID(unit.UnitID())
}

// makeJob makes the job of the case with op as its current operation.
func (w *jobTokenWorld) makeJob(
	t *testing.T, unit identity.Object, starter jobtokentest.Starter, op jobtokentest.Operation,
) job.Job {
	t.Helper()

	kinds, err := job.DefaultPhases(job.ExecutorRunner, job.OperationPlan)
	if err != nil {
		t.Fatalf("default phases: %v", err)
	}

	phases := make([]job.Phase, len(kinds))
	for i := range kinds {
		phases[i].Kind = kinds[i]
	}

	phases[0].Operation = string(op.ID)

	var location job.Location

	j := job.Job{
		ID: job.ID(ident.NewID(job.IDPrefix)),
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationPlan, Purpose: "", Executor: job.ExecutorRunner,
			Location: location, Steps: nil, Pool: "", Profile: "", Edits: nil, Delivery: "", Defaulted: nil,
		},
		State: job.StateQueued, Wait: job.WaitNone, Reason: "", Phases: phases, Current: 0,
		StartedBy: starter.Principal.Subject, Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil,
		Trigger: nil, Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "",
		RequestHash: "", Version: 0, CreatedAt: time.Time{}, UpdatedAt: time.Time{}, StateChangedAt: time.Time{},
	}

	stored, _, err := w.s.Jobs().Create(t.Context(), j, nil, namespace.Fence{
		Namespace: unit.Namespace(), FailoverVersion: w.failoverVersion(t, unit.Namespace()),
	}, nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	_, err = w.raw.ExecContext(
		t.Context(), "UPDATE jobs SET state = ? WHERE id = ?", string(job.StateRunning), string(stored.ID),
	)
	if err != nil {
		t.Fatalf("set job running: %v", err)
	}

	_, err = w.raw.ExecContext(t.Context(), `
		INSERT INTO durable_operations (
			id, start_key, kind, input, version, state, attempt, expiries, failures, last_error, claim_owner,
			claim_token, claim_expires_at_ms, cancel_requested, not_before_ms, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, 0, ?, 1, 0, 0, '', 'runner', ?, ?, 0, ?, ?, ?)`,
		string(op.ID), string(op.ID), "job.runner.test", []byte{}, string(op.State), op.Token,
		op.LeaseEnd.UnixMilli(), jobTokenNow().UnixMilli(), jobTokenNow().UnixMilli(), jobTokenNow().UnixMilli(),
	)
	if err != nil {
		t.Fatalf("insert durable operation: %v", err)
	}

	stored.State = job.StateRunning

	return stored
}

// failoverVersion returns the failover version of the namespace ns.
func (w *jobTokenWorld) failoverVersion(t *testing.T, ns namespace.Name) int64 {
	t.Helper()

	got, err := w.s.GetNamespace(t.Context(), ns)
	if err != nil {
		t.Fatalf("get namespace %q: %v", ns, err)
	}

	return got.FailoverVersion
}

// takeover gives the claim of the job id a new token and lease, and returns
// the end of the new lease.
func (w *jobTokenWorld) takeover(t *testing.T, id job.ID) time.Time {
	t.Helper()

	op := w.currentOperation(t, id)

	var lease sql.NullInt64

	err := w.raw.QueryRowContext(
		t.Context(), "SELECT claim_expires_at_ms FROM durable_operations WHERE id = ?", op,
	).Scan(&lease)
	if err != nil {
		t.Fatalf("read claim lease: %v", err)
	}

	next := lease.Int64 + time.Hour.Milliseconds()

	_, err = w.raw.ExecContext(t.Context(),
		"UPDATE durable_operations SET claim_token = claim_token + 1, claim_expires_at_ms = ? WHERE id = ?",
		next, op,
	)
	if err != nil {
		t.Fatalf("take over claim: %v", err)
	}

	return time.UnixMilli(next).UTC()
}

// setJobState sets the state of the job id.
func (w *jobTokenWorld) setJobState(t *testing.T, id job.ID, state job.State) {
	t.Helper()

	_, err := w.raw.ExecContext(
		t.Context(), "UPDATE jobs SET state = ? WHERE id = ?", string(state), string(id),
	)
	if err != nil {
		t.Fatalf("set job state: %v", err)
	}
}

// endClaim ends the claim of the job id as succeeded.
func (w *jobTokenWorld) endClaim(t *testing.T, id job.ID) {
	t.Helper()

	w.exec(t, "UPDATE durable_operations SET state = ? WHERE id = ?", string(durable.StateSucceeded),
		w.currentOperation(t, id))
}

// releaseClaim returns the claim of the job id to pending with no claim.
func (w *jobTokenWorld) releaseClaim(t *testing.T, id job.ID) {
	t.Helper()

	w.exec(t, "UPDATE durable_operations SET state = ?, claim_token = 0, claim_expires_at_ms = NULL WHERE id = ?",
		string(durable.StatePending), w.currentOperation(t, id))
}

// endJob moves the job id to a terminal state.
func (w *jobTokenWorld) endJob(t *testing.T, id job.ID) {
	t.Helper()

	w.exec(t, "UPDATE jobs SET state = ? WHERE id = ?", string(job.StateSucceeded), string(id))
}

// moveJob makes op the current operation of the job id.
func (w *jobTokenWorld) moveJob(t *testing.T, id job.ID, op durable.ID) {
	t.Helper()

	w.exec(t, "UPDATE jobs SET current_operation = ? WHERE id = ?", string(op), string(id))
}

// writeRaw writes tok straight into the store with no check.
func (w *jobTokenWorld) writeRaw(t *testing.T, tok jobtoken.Token) {
	t.Helper()

	w.exec(t, `
		INSERT INTO job_tokens (
			digest, namespace_id, unit_namespace, starter_id, job_id, unit_tool, unit_id, unit_name,
			claim_operation, claim_token, expires_at_ms, created_at_ms
		) VALUES (
			?, (SELECT id FROM namespaces WHERE name = ?), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		)`,
		tok.Digest.String(), tok.Namespace.String(), tok.Namespace.String(), tok.StarterID, string(tok.JobID),
		tok.Unit.Tool(), tok.Unit.UnitID(), tok.Unit.UnitName(), string(tok.ClaimOperation), tok.ClaimToken,
		tok.ExpiresAt.UnixMilli(), tok.CreatedAt.UnixMilli(),
	)
}

// writeRawInNamespace writes tok straight into the store with no check, with
// the namespace ID of the namespace called ns.
func (w *jobTokenWorld) writeRawInNamespace(t *testing.T, tok jobtoken.Token, ns namespace.Name) {
	t.Helper()

	w.exec(t, `
		INSERT INTO job_tokens (
			digest, namespace_id, unit_namespace, starter_id, job_id, unit_tool, unit_id, unit_name,
			claim_operation, claim_token, expires_at_ms, created_at_ms
		) VALUES (
			?, (SELECT id FROM namespaces WHERE name = ?), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		)`,
		tok.Digest.String(), ns.String(), tok.Namespace.String(), tok.StarterID, string(tok.JobID),
		tok.Unit.Tool(), tok.Unit.UnitID(), tok.Unit.UnitName(), string(tok.ClaimOperation), tok.ClaimToken,
		tok.ExpiresAt.UnixMilli(), tok.CreatedAt.UnixMilli(),
	)
}

// entries returns the audit entries of ns, oldest first.
func (w *jobTokenWorld) entries(t *testing.T, ns namespace.Name) []audit.Entry {
	t.Helper()

	var out []audit.Entry

	for page := 1; ; page++ {
		listing, err := w.s.List(t.Context(), ns, audit.Page{Number: page, Size: audit.MaxPageSize})
		if err != nil {
			t.Fatalf("list audit: %v", err)
		}

		out = append(out, listing.Entries...)

		if len(listing.Entries) < audit.MaxPageSize {
			break
		}
	}

	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out
}

// currentOperation returns the current operation of the job id.
func (w *jobTokenWorld) currentOperation(t *testing.T, id job.ID) string {
	t.Helper()

	var op string

	err := w.raw.QueryRowContext(
		t.Context(), "SELECT current_operation FROM jobs WHERE id = ?", string(id),
	).Scan(&op)
	if err != nil {
		t.Fatalf("read current operation: %v", err)
	}

	return op
}

// exec runs the statement with args and fails the test on an error.
func (w *jobTokenWorld) exec(t *testing.T, query string, args ...any) {
	t.Helper()

	if _, err := w.raw.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("run statement: %v", err)
	}
}

// openJobTokenStore returns a fresh store and a raw handle to its database.
func openJobTokenStore(t *testing.T) (*sqlite.Store, *sql.DB) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	raw, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	})

	return openPath(t, path), raw
}

// IssueRestoreJobToken implements the restore case of the suite: it makes a
// namespace, a principal, a job and a claim, and issues a job token through
// the store.
func (s *backupStore) IssueRestoreJobToken(t *testing.T, nsName namespace.Name) jobtoken.Digest {
	t.Helper()

	raw, err := openRawDB(s.path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	})

	w := &jobTokenWorld{s: s.Store, raw: raw}
	fence := w.makeNamespace(t, nsName.String())
	unit := w.makeUnit(t, nsName.String(), "pulumi", "web/prod")
	starter := w.makePrincipal(t, "alice")

	at := jobTokenNow()
	op := jobtokentest.Operation{
		ID: durable.ID(ident.NewID("op-")), State: durable.StateRunning, Token: 1, LeaseEnd: at.Add(time.Hour),
	}
	j := w.makeJob(t, unit, starter, op)

	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	tok := jobtoken.Token{
		Digest: jobtoken.DigestOf(secret), StarterID: starter.ID, JobID: j.ID, Namespace: unit.Namespace(),
		Unit: unit, ClaimOperation: op.ID, ClaimToken: op.Token,
		ExpiresAt: at.Add(10 * time.Minute), CreatedAt: at,
	}

	var entry audit.Entry

	entry.At = at
	entry.Namespace = fence.Namespace
	entry.Subject = "alice"
	entry.Action = "job:issue"
	entry.Object = fence.Namespace.String()
	entry.Channel = audit.ChannelAPI
	entry.Kind = audit.KindChange
	entry.Outcome = audit.OutcomeSuccess

	if err := s.Store.JobTokens().Issue(t.Context(), tok, fence, entry); err != nil {
		t.Fatalf("issue job token: %v", err)
	}

	return tok.Digest
}

// HasJobToken implements the restore case of the suite.
func (s *backupStore) HasJobToken(t *testing.T, d jobtoken.Digest) bool {
	t.Helper()

	_, err := s.Store.JobTokens().GetByDigest(t.Context(), d)
	if errors.Is(err, jobtoken.ErrNotFound) {
		return false
	}

	if err != nil {
		t.Fatalf("GetByDigest: %v", err)
	}

	return true
}
