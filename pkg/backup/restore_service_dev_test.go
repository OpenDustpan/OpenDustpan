package backup_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
)

// setManifestKind rewrites the kind in the manifest of the backup of the
// setup. The manifest is not authenticated, so a test edits it like a person
// who can write to the backup target.
func setManifestKind(t *testing.T, setup *restoreServiceSetup, kind installation.Kind) {
	t.Helper()

	key := "backups/" + setup.id.String() + "/manifest.json"
	raw := string(setup.target.readObject(t, key))

	edited := strings.Replace(raw, `"installationKind":"production"`, `"installationKind":"`+kind.String()+`"`, 1)
	if edited == raw && kind != installation.KindProduction {
		t.Fatalf("manifest %s holds no production kind to rewrite", raw)
	}

	if err := setup.target.Delete(t.Context(), mustKey(t, key)); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	setup.target.storeObject(t, key, []byte(edited))
}

// dirtyReport returns a strip report with one dev principal and a count for
// the classes of a typical dev backup.
func dirtyReport() backup.DevStripReport {
	return backup.DevStripReport{
		Principals: []backup.DevRemoval{{
			Subject: "dev:admin", SubjectCut: false,
			Counts: map[backup.DevRemovalClass]int64{backup.RemovedGrants: 1, backup.RemovedSessions: 2},
		}},
		OtherPrincipals: 0,
		OtherCounts:     nil,
		Classes: map[backup.DevRemovalClass]int64{
			backup.RemovedPrincipals: 1, backup.RemovedGrants: 1, backup.RemovedSessions: 2,
			backup.RemovedProviders: 1,
		},
		Approvers:      nil,
		Warnings:       nil,
		PreBackupFound: nil,
		NotHeld:        nil,
		ErrorCode:      "",
	}
}

// startRestoreOf starts a restore of the backup of the setup and returns its
// ID and the error of Start.
func startRestoreOf(
	t *testing.T, setup *restoreServiceSetup, req backup.RestoreRequest,
) (backup.RestoreID, error) {
	t.Helper()

	id := backup.MintRestoreID()
	req.BackupID = setup.id
	req.RequestedBy = "tester"

	return id, setup.service.Start(t.Context(), id, req)
}

// Static errors that the fake live store returns.
var (
	errStripLastStep = errors.New("last step failed")
	errStripLook     = errors.New("cannot read")
	errAuditDown     = errors.New("audit store is down")
)

// restoreRequestOf returns a restore request with every field set. The test
// helper sets the backup and the requester.
func restoreRequestOf(checkOnly, keepAccess bool) backup.RestoreRequest {
	return backup.RestoreRequest{
		BackupID: "", PreBackupID: "", CheckOnly: checkOnly, Force: false, RequestedBy: "",
		KeepAccessTables: keepAccess,
	}
}

// plainRestore is a restore with no option.
func plainRestore() backup.RestoreRequest { return restoreRequestOf(false, false) }

// TestRestoreRefusesDevBackupIntoProduction sends a backup of the kind dev to a
// production installation with the setting off. The service compares the kinds
// before it enters maintenance mode, takes the backup of the live data or
// stages anything (layer 1b).
func TestRestoreRefusesDevBackupIntoProduction(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
		cfg.AllowDevHint = "`--allow-dev`"
	})
	setManifestKind(t, setup, installation.KindDev)

	_, err := startRestoreOf(t, setup, plainRestore())
	if !errors.Is(err, backup.ErrDevBackupRefused) {
		t.Fatalf("start: err = %v, want ErrDevBackupRefused", err)
	}

	text, ok := backup.RefusalText(err)
	if !ok || !strings.Contains(text, "To restore it, start the server with `--allow-dev`.") ||
		!strings.Contains(text, "The backup is from a dev installation.") {
		t.Fatalf("refusal text = %q, want the sentence with the hint", text)
	}

	assertNothingStarted(t, setup)
}

// assertNothingStarted proves that a refused restore entered no maintenance
// mode, staged nothing, took no pre-backup and wrote no row.
func assertNothingStarted(t *testing.T, setup *restoreServiceSetup) {
	t.Helper()

	if enters := setup.maint.counts().enters; enters != 0 {
		t.Fatalf("maintenance enters = %d, want 0", enters)
	}

	if got := setup.live.stageCallCount(); got != 0 {
		t.Fatalf("stage calls = %d, want 0", got)
	}

	if idx := setup.order.index("export"); idx >= 0 {
		t.Fatalf("order %v holds a pre-backup export, want none", setup.order.snapshot())
	}

	rows, err := setup.regs.ListRestores(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("restore rows = %+v, %v, want none", rows, err)
	}
}

// TestRestoreRefusesDevBackupCheckOnly proves that a check-only start is
// refused the same way.
func TestRestoreRefusesDevBackupCheckOnly(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)
	setManifestKind(t, setup, installation.KindDev)

	_, err := startRestoreOf(t, setup, restoreRequestOf(true, false))
	if !errors.Is(err, backup.ErrDevBackupRefused) {
		t.Fatalf("check-only start: err = %v, want ErrDevBackupRefused", err)
	}
}

// TestRestoreAllowsDevBackupWithTheSetting proves that, with the setting on,
// the restore goes on.
func TestRestoreAllowsDevBackupWithTheSetting(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AllowDevBackups = true })
	setManifestKind(t, setup, installation.KindDev)

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreSucceeded {
		t.Fatalf("state = %q (%s), want succeeded", row.State, row.Error)
	}
}

// TestRestoreStagesWithTheKindTheSettingAndTheClock proves that the service
// passes the pinned manifest kind, the setting and the time of its clock in
// the options of the one stage call.
func TestRestoreStagesWithTheKindTheSettingAndTheClock(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		manifest  installation.Kind
		allow     bool
		restoreAt time.Time
	}{
		{
			name: "production backup", manifest: installation.KindProduction, allow: false,
			restoreAt: time.Date(2026, time.November, 8, 9, 30, 0, 0, time.UTC),
		},
		{
			name: "dev backup, setting on", manifest: installation.KindDev, allow: true,
			restoreAt: time.Date(2026, time.November, 8, 10, 0, 0, 0, time.UTC),
		},
		{
			name: "production backup, setting on", manifest: installation.KindProduction, allow: true,
			restoreAt: time.Date(2026, time.November, 8, 10, 30, 0, 0, time.UTC),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
				cfg.AllowDevBackups = tc.allow
				cfg.Now = func() time.Time { return tc.restoreAt }
			})
			setManifestKind(t, setup, tc.manifest)

			id, err := startRestoreOf(t, setup, plainRestore())
			if err != nil {
				t.Fatalf("start: %v", err)
			}

			if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreSucceeded {
				t.Fatalf("state = %q (%s), want succeeded", row.State, row.Error)
			}

			want := backup.StageOptions{
				BackupKind: tc.manifest, AllowDev: tc.allow, RestoreAt: tc.restoreAt,
			}
			got := setup.live.devSnapshot().stageOpts

			if len(got) != 1 || got[0] != want {
				t.Fatalf("stage options = %+v, want one call with %+v", got, want)
			}
		})
	}
}

// TestRestoreRefusesAKindThatChangedAfterTheCheck changes the kind in the
// manifest while the stage step runs. The pin of the manifest refuses the
// restore in the check step, before the apply (layer 1e).
func TestRestoreRefusesAKindThatChangedAfterTheCheck(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AllowDevBackups = true })

	block := make(chan struct{})
	setup.live.setStageBlock(block)

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	ran := make(chan struct{})

	go func() {
		defer close(ran)

		setup.regs.runWorker(t.Context())
	}()

	awaitStageCalls(t, setup, 1)
	setManifestKind(t, setup, installation.KindDev)
	close(block)
	<-ran

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, backup.ErrCorrupt.Error()) {
		t.Fatalf("restore = %q (%s), want failed with the corrupt text", row.State, row.Error)
	}

	if applied := setup.live.counts().applied; applied != 0 {
		t.Fatalf("applies = %d, want 0", applied)
	}
}

// awaitStageCalls waits until the fake live store started n stage calls.
func awaitStageCalls(t *testing.T, setup *restoreServiceSetup, n int) {
	t.Helper()

	for range 1000 {
		if setup.live.stageCallCount() >= n {
			return
		}

		timer := time.NewTimer(10 * time.Millisecond)

		select {
		case <-t.Context().Done():
			timer.Stop()
			t.Fatalf("wait for stage calls: %v", t.Context().Err())
		case <-timer.C:
		}
	}

	t.Fatalf("the live store never started %d stage calls", n)
}

// TestRestoreStripsDevDataSettingOff runs a restore of a production
// installation over staged data that holds dev people with the setting off
// (layer 2). The manifest says production, so only the staged data tells.
func TestRestoreStripsDevDataSettingOff(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)
	setup.live.dev.detect = dirtyReport()

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, "production installation") {
		t.Fatalf("restore = %q (%s), want failed with the refusal text", row.State, row.Error)
	}

	snap := setup.live.devSnapshot()

	areas, err := setup.live.ListRestoreAreas(t.Context())
	if err != nil {
		t.Fatalf("list restore areas: %v", err)
	}

	if setup.live.counts().applied != 0 || setup.maint.counts().enters != 0 || len(areas) != 0 {
		t.Fatalf("applies %d, enters %d, areas %v, want a refusal before maintenance mode with no area",
			setup.live.counts().applied, setup.maint.counts().enters, areas)
	}

	if slices.Contains(snap.strips, backup.DevStripRemove) {
		t.Fatal("the service removed dev data with the setting off")
	}
}

// TestRestoreStripsDevDataSettingOn runs the strip with the setting on, with
// and without the keep-access option.
func TestRestoreStripsDevDataSettingOn(t *testing.T) {
	t.Parallel()

	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprintf("keep access %v", keep), func(t *testing.T) {
			t.Parallel()

			setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AllowDevBackups = true })
			setup.live.dev.detect = dirtyReport()
			setup.live.dev.remove = dirtyReport()

			id, err := startRestoreOf(t, setup, restoreRequestOf(false, keep))
			if err != nil {
				t.Fatalf("start: %v", err)
			}

			row := waitRestoreEnd(t, setup.regs, id)
			if row.State != backup.RestoreSucceeded {
				t.Fatalf("state = %q (%s), want succeeded", row.State, row.Error)
			}

			checkStripRecord(t, setup, row)
		})
	}
}

// checkStripRecord proves that the report is in the record before the swap,
// that the record holds the warning, and that the audit entries name the
// removed person.
func checkStripRecord(t *testing.T, setup *restoreServiceSetup, row backup.RestoreRecord) {
	t.Helper()

	if rec, ap := setup.order.index("record"), setup.order.index("apply"); rec < 0 || ap < 0 || rec > ap {
		t.Fatalf("order = %v, want the report recorded before the swap", setup.order.snapshot())
	}

	if row.DevReport == nil || len(row.Warnings()) != 1 ||
		row.Warnings()[0].Code != backup.WarnDevBackupIntoProduction {
		t.Fatalf("record = %+v, want the warning dev-backup-into-production", row.DevReport)
	}

	if !strings.Contains(row.Warnings()[0].Text, "We do not recommend this restore.") {
		t.Fatalf("warning text = %q", row.Warnings()[0].Text)
	}

	snap := setup.live.devSnapshot()
	if !strings.Contains(auditObjects(snap.appends), "dev principal \"dev:admin\"") {
		t.Fatalf("audit entries = %v, want one for dev:admin", auditObjects(snap.appends))
	}
}

// TestRestoreStripThatDoesNotFinishDiscardsTheStagingArea proves that a strip
// that does not finish discards the staging area.
func TestRestoreStripThatDoesNotFinishDiscardsTheStagingArea(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AllowDevBackups = true })
	setup.live.dev.detect = dirtyReport()
	setup.live.dev.stripErr[backup.DevStripRemove] = errStripLastStep

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreFailed || row.Error != backup.ErrDevStripIncomplete.Error() {
		t.Fatalf("restore = %q (%s), want failed with the strip text", row.State, row.Error)
	}

	if setup.live.counts().applied != 0 || setup.live.devSnapshot().discards == 0 {
		t.Fatal("the failed strip applied the staged data or kept the staging area")
	}
}

// TestRestoreFailsWhenTheStripCannotLook proves that a strip that cannot look
// fails the restore. With the setting off the strip only looks.
func TestRestoreFailsWhenTheStripCannotLook(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)
	setup.live.dev.stripErr[backup.DevStripDetect] = errStripLook

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreFailed {
		t.Fatalf("state = %q, want failed", row.State)
	}
}

// TestRestoreCheckOnlyReportsWhatItWouldRemove proves that a check-only
// restore reports what it would remove and changes nothing.
func TestRestoreCheckOnlyReportsWhatItWouldRemove(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AllowDevBackups = true })
	setup.live.dev.detect = dirtyReport()

	id, err := startRestoreOf(t, setup, restoreRequestOf(true, false))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreSucceeded || row.DevReport == nil ||
		row.DevReport.Classes[backup.RemovedPrincipals] != 1 {
		t.Fatalf("check-only restore = %q %+v, want the counts it would remove", row.State, row.DevReport)
	}

	if setup.live.counts().applied != 0 || setup.maint.counts().enters != 0 ||
		len(setup.live.devSnapshot().appends) != 0 {
		t.Fatal("a check-only restore changed something")
	}

	warnings := row.Warnings()
	if len(warnings) != 1 || warnings[0].Code != backup.WarnDevBackupWouldRemove ||
		!strings.Contains(warnings[0].Text, "would remove") || strings.Contains(warnings[0].Text, "removed the dev") {
		t.Fatalf("check-only warnings = %+v, want the warning that says it would remove", warnings)
	}
}

// TestRestoreSwapRefusalTextFitsAProductionClaim proves that the text of a
// refusal by the swap does not say that the backup is from a dev installation:
// the manifest of such a backup may claim production.
func TestRestoreSwapRefusalTextFitsAProductionClaim(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AllowDevBackups = true })
	setManifestKind(t, setup, installation.KindProduction)
	setup.live.dev.detect = dirtyReport()
	setup.live.dev.remove = dirtyReport()
	setup.live.applyErr = backup.ErrDevBackupRefused

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State == backup.RestoreSucceeded || !strings.Contains(row.Error, "or it is from a dev installation") ||
		strings.Contains(row.Error, "The backup is from a dev installation.") {
		t.Fatalf("restore = %q %q, want a refusal text that fits a production claim", row.State, row.Error)
	}
}

// TestRestoreIntoDevInstallationStripsNothing proves that a dev installation
// strips nothing and warns about a production backup.
func TestRestoreIntoDevInstallationStripsNothing(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)
	setup.live.dev.kind = installation.KindDev
	setup.live.dev.detect = dirtyReport()

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreSucceeded || len(row.Warnings()) != 1 ||
		row.Warnings()[0].Code != backup.WarnProductionBackupIntoDev {
		t.Fatalf("restore = %q %+v, want the production-backup-into-dev warning", row.State, row.Warnings())
	}

	if strips := setup.live.devSnapshot().strips; len(strips) != 0 {
		t.Fatalf("a dev installation ran the strip: %v", strips)
	}
}

// TestRestoreFailsWhenTheLiveKindCannotBeRead proves that a live kind that
// cannot be read fails the restore.
func TestRestoreFailsWhenTheLiveKindCannotBeRead(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)
	setup.live.dev.kindErr = installation.ErrUnavailable

	if _, err := startRestoreOf(t, setup, plainRestore()); !errors.Is(err, installation.ErrUnavailable) {
		t.Fatalf("start: err = %v, want ErrUnavailable", err)
	}

	if setup.live.counts().applied != 0 {
		t.Fatal("a restore ran with no live kind")
	}
}

// auditObjects joins the object text of the audit entries.
func auditObjects(entries []audit.Entry) string {
	objects := make([]string, 0, len(entries))
	for i := range entries {
		objects = append(objects, entries[i].Object)
	}

	return strings.Join(objects, "\n")
}

// boundedRemovalReport returns a report of 250 dev principals, of which the
// first MaxAuditedPrincipals are named. The first subject holds control
// characters.
func boundedRemovalReport() backup.DevStripReport {
	report := backup.DevStripReport{
		Principals:      nil,
		OtherPrincipals: 150,
		OtherCounts:     map[backup.DevRemovalClass]int64{backup.RemovedSessions: 300},
		Classes: map[backup.DevRemovalClass]int64{
			backup.RemovedPrincipals: 250, backup.RemovedSessions: 500, backup.RemovedProviders: 1,
			backup.RemovedDangling: 3,
		},
		Approvers:      nil,
		Warnings:       nil,
		PreBackupFound: nil,
		NotHeld:        nil,
		ErrorCode:      "",
	}

	for i := range backup.MaxAuditedPrincipals {
		subject := fmt.Sprintf("dev:user-%03d", i)
		if i == 0 {
			subject = "dev:evil\r\nforged\x1b[31m\x00"
		}

		report.Principals = append(report.Principals, backup.DevRemoval{
			Subject: subject, SubjectCut: false,
			Counts: map[backup.DevRemovalClass]int64{backup.RemovedSessions: 2},
		})
	}

	return report
}

// auditCounts holds the number of audit entries for each kind of removal.
type auditCounts struct {
	principals int
	others     int
	classes    int
}

// countAuditEntries counts the entries for a principal, for the others and
// for a class, and fails for an entry that holds a control character.
func countAuditEntries(t *testing.T, entries []audit.Entry) auditCounts {
	t.Helper()

	var counts auditCounts

	for i := range entries {
		object := entries[i].Object

		switch {
		case strings.Contains(object, " removed dev principal "):
			counts.principals++
		case strings.Contains(object, " other dev principals"):
			counts.others++
		case strings.Contains(object, " removed "):
			counts.classes++
		default:
			// Another entry of the chain: not counted.
		}

		for _, r := range object {
			if unicode.IsControl(r) {
				t.Fatalf("audit object %q holds control character %U", object, r)
			}
		}
	}

	return counts
}

// TestRestoreRecordsABoundedRemoval checks the entries after the swap for a
// report of 250 dev principals: 100 entries, one summary entry, and one entry
// for each class that has no principal (providers and dangling rows here). No
// entry holds a control character, a token or a session ID.
func TestRestoreRecordsABoundedRemoval(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AllowDevBackups = true })

	report := boundedRemovalReport()
	setup.live.dev.detect = report
	setup.live.dev.remove = report

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreSucceeded {
		t.Fatalf("state = %q (%s), want succeeded", row.State, row.Error)
	}

	got := countAuditEntries(t, setup.live.devSnapshot().appends)
	if got.principals != backup.MaxAuditedPrincipals || got.others != 1 || got.classes != 2 {
		t.Fatalf("entries: %d principals, %d summaries, %d classes, want %d, 1, 2",
			got.principals, got.others, got.classes, backup.MaxAuditedPrincipals)
	}
}

// TestRestoreSurvivesAFailedAuditAppend checks that a failing append after the
// swap ends the restore as finished with the warning audit-append-failed, and
// that the record holds the report.
func TestRestoreSurvivesAFailedAuditAppend(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AllowDevBackups = true })
	setup.live.dev.detect = dirtyReport()
	setup.live.dev.remove = dirtyReport()
	setup.live.dev.appendErr = errAuditDown

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreSucceeded || row.DevReport == nil {
		t.Fatalf("restore = %q %+v, want succeeded with the report", row.State, row.DevReport)
	}

	codes := row.DevReport.Warnings
	if len(codes) != 2 || codes[0] != backup.WarnDevBackupIntoProduction || codes[1] != backup.WarnAuditAppendFailed {
		t.Fatalf("warnings = %v, want dev-backup-into-production and audit-append-failed", codes)
	}

	if row.DevReport.Classes[backup.RemovedPrincipals] != 1 {
		t.Fatalf("report = %+v, want the removal counts", row.DevReport)
	}
}

// TestRestoreCountsDroppedTempAdmins checks that the temp admins that the
// keeping swap drops reach the report and the record.
func TestRestoreCountsDroppedTempAdmins(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AllowDevBackups = true })
	setup.live.dev.detect = dirtyReport()
	setup.live.dev.remove = dirtyReport()
	setup.live.dev.dropped = 2

	id, err := startRestoreOf(t, setup, restoreRequestOf(false, true))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.DevReport == nil || row.DevReport.Classes[backup.RemovedTempAdmins] != 2 {
		t.Fatalf("record = %+v, want 2 dropped temp admins", row.DevReport)
	}
}

// TestRestoreLogsOneWarningForEachClass checks that the strip logs one
// warning for each class that removed rows, with the count, and that no line
// holds a subject, a token or a session ID.
func TestRestoreLogsOneWarningForEachClass(t *testing.T) {
	t.Parallel()

	logs := &syncBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
		cfg.AllowDevBackups = true
		cfg.Logger = slog.New(slog.NewTextHandler(logs, nil))
	})
	setup.live.dev.detect = dirtyReport()
	setup.live.dev.remove = dirtyReport()

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreSucceeded {
		t.Fatalf("state = %q (%s), want succeeded", row.State, row.Error)
	}

	text := logs.String()

	for class, count := range dirtyReport().Classes {
		want := fmt.Sprintf("class=%s count=%d", class, count)
		if strings.Count(text, want) != 1 {
			t.Fatalf("log holds %d lines for %q, want 1:\n%s", strings.Count(text, want), want, text)
		}
	}

	if strings.Contains(text, "dev:admin") {
		t.Fatalf("log holds a subject:\n%s", text)
	}
}
