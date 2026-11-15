package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/principal"
)

// listOps is a durable store whose List returns ops.
type listOps struct {
	durable.Store

	ops []durable.Operation
}

func (l listOps) List(context.Context, durable.Filter) ([]durable.Operation, error) {
	return l.ops, nil
}

// errListOps is a durable store whose List fails.
type errListOps struct {
	durable.Store
}

func (errListOps) List(context.Context, durable.Filter) ([]durable.Operation, error) {
	return nil, errThrottled
}

// opOf returns an operation of key with input.
func opOf(key, input string) durable.Operation {
	var op durable.Operation
	op.Key, op.Input = key, []byte(input)

	return op
}

// TestStartedForRestoreRefusesHostileInput sends hostile operation inputs to
// the read of the restore that started a backup. Only a decoded input of the
// same backup with a valid restore ID names a restore; every other input
// names none, so the caller counts the backup as one that no restore started.
func TestStartedForRestoreRefusesHostileInput(t *testing.T) {
	t.Parallel()

	restore := MintRestoreID()
	key := OperationKey(preBackupID)

	for _, tc := range []struct {
		name string
		op   durable.Operation
		want RestoreID
	}{
		{
			name: "the input of this restore", op: opOf(key, `{"id":"bkp-pre","restore":"`+restore.String()+`"}`),
			want: restore,
		},
		{name: "an input that does not decode", op: opOf(key, `{"id":`), want: ""},
		{
			name: "an input of another backup", op: opOf(key, `{"id":"bkp-other","restore":"`+restore.String()+`"}`),
			want: "",
		},
		{name: "a restore ID that is not valid", op: opOf(key, `{"id":"bkp-pre","restore":"rst-../x"}`), want: ""},
		{name: "no restore ID", op: opOf(key, `{"id":"bkp-pre"}`), want: ""},
		{
			name: "an operation of another key",
			op:   opOf(OperationKey("bkp-other"), `{"id":"bkp-pre","restore":"`+restore.String()+`"}`), want: "",
		},
	} {
		s := backupService(nil, nil, nil)
		s.operations = listOps{Store: nil, ops: []durable.Operation{tc.op}}

		got, err := s.StartedForRestore(t.Context(), preBackupID)
		if err != nil || got != tc.want {
			t.Errorf("%s: StartedForRestore = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

// TestCheckDamagedOriginRefusesAForeignDamagedList proves that a damaged list
// is accepted only in a backup that a restore of this installation started.
// A manifest of any other backup with a damaged list is corrupt, and a store
// of operations that is not there or cannot be read refuses too.
func TestCheckDamagedOriginRefusesAForeignDamagedList(t *testing.T) {
	t.Parallel()

	key := OperationKey(preBackupID)
	withList := testManifest(t, nil, []DamagedBlob{entryOf(liveKey, DamageMissing)})

	ownedBy := func(input string) *Service {
		s := backupService(nil, nil, nil)
		s.operations = listOps{Store: nil, ops: []durable.Operation{opOf(key, input)}}

		return s
	}

	unreadable := backupService(nil, nil, nil)
	unreadable.operations = errListOps{Store: nil}

	for _, tc := range []struct {
		name     string
		backups  *Service
		manifest Manifest
		want     error
	}{
		{name: "a backup before a restore", backups: ownedBy(`{"id":"bkp-pre","restore":"` +
			MintRestoreID().String() + `"}`), manifest: withList, want: nil},
		{
			name: "an ordinary backup", backups: ownedBy(`{"id":"bkp-pre"}`), manifest: withList,
			want: ErrDamagedListForeign,
		},
		{
			name: "a backup with no operation", backups: ownedBy(`{"id":"bkp-other"}`), manifest: withList,
			want: ErrDamagedListForeign,
		},
		{name: "no backups service", backups: nil, manifest: withList, want: ErrDamagedListForeign},
		{name: "operations that cannot be read", backups: unreadable, manifest: withList, want: errThrottled},
		{name: "no damaged list", backups: nil, manifest: testManifest(t, nil, nil), want: nil},
	} {
		err := checkDamagedOrigin(t.Context(), tc.backups, tc.manifest)

		switch {
		case tc.want == nil && err != nil:
			t.Errorf("%s: checkDamagedOrigin = %v, want nil", tc.name, err)
		case tc.want != nil && !errors.Is(err, tc.want):
			t.Errorf("%s: checkDamagedOrigin = %v, want %v", tc.name, err, tc.want)
		case errors.Is(tc.want, ErrDamagedListForeign) && !errors.Is(err, ErrCorrupt):
			t.Errorf("%s: checkDamagedOrigin = %v, want ErrCorrupt too", tc.name, err)
		default:
		}
	}
}

// TestCheckRestoreRefusesADamagedList proves that the restore check with no
// record of operations refuses a backup with a damaged list: it cannot prove
// that a restore took it.
func TestCheckRestoreRefusesADamagedList(t *testing.T) {
	t.Parallel()

	target := newMapStore(false, nil)
	putManifest(t, target, testManifest(t, nil, []DamagedBlob{entryOf(liveKey, DamageMissing)}))

	_, _, err := CheckRestore(t.Context(), CheckInput{
		Target: target, Live: checkLive{CheckStore: nil}, Key: nil, ID: preBackupID, Installation: "",
		Stage: StageOptions{BackupKind: "", AllowDev: false, RestoreAt: time.Time{}}, AfterStage: nil,
	})
	if !errors.Is(err, ErrDamagedListForeign) || !errors.Is(err, ErrCorrupt) {
		t.Fatalf("CheckRestore = %v, want ErrDamagedListForeign and ErrCorrupt", err)
	}
}

// checkLive is a CheckStore whose export gives the baseline schema version
// and nothing else. Every other method fails the test with a panic of the
// embedded nil interface, so a check that goes past the refusal shows.
type checkLive struct {
	CheckStore
}

func (checkLive) ExportPortable(context.Context, PortableSink) (Summary, []PortableFile, error) {
	return Summary{
		Dialect: DialectSQLite, PrimaryInstallations: nil, WrapperIDs: nil, RowCounts: nil, Blobs: nil,
		Unverified: nil, SchemaVersion: BaselineSchemaVersion,
	}, nil, nil
}

// stepContextWith returns the step context of the copy step of an operation
// whose pre-backup step stored result, or no pre-backup step when result is
// nil.
func stepContextWith(t *testing.T, result *preBackupResult) durable.StepContext {
	t.Helper()

	var op durable.Operation

	if result != nil {
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("encode the result: %v", err)
		}

		op.Steps = []durable.StepRecord{{Name: RestoreStepPreBackup, Result: raw, DoneAt: time.Time{}}}
	}

	return durable.StepContext{Operation: op, Fence: durable.StepFence{Operation: "op", Token: 1, Step: "copy"}}
}

// TestCheckPreBackupResultRefusesBeforeTheCopy sends hostile step records to
// the layer at the copy step directly, as if the pre-backup step had not
// stopped the restore: no result, a result that does not decode, damaged live
// blobs, and missing live blobs above the bound. Each refuses with one audit
// event, and the copy step writes nothing. A result within the bound passes
// with no event.
func TestCheckPreBackupResultRefusesBeforeTheCopy(t *testing.T) {
	t.Parallel()

	garbled := stepContextWith(t, nil)
	garbled.Operation.Steps = []durable.StepRecord{
		{Name: RestoreStepPreBackup, Result: []byte(`{"found":`), DoneAt: time.Time{}},
	}

	for _, tc := range []struct {
		name string
		sc   durable.StepContext
		want error
	}{
		{name: "no result", sc: stepContextWith(t, nil), want: ErrPreBackupForeign},
		{name: "a result that does not decode", sc: garbled, want: ErrPreBackupForeign},
		{name: "a damaged live blob", sc: stepContextWith(t, &preBackupResult{Found: BlobFindings{
			Named: 40, Missing: 0, Damaged: 1, Keys: []string{liveKey},
		}}), want: ErrLiveBlobsDamaged},
		{name: "11 missing of 1000", sc: stepContextWith(t, &preBackupResult{Found: BlobFindings{
			Named: 1000, Missing: 11, Damaged: 0, Keys: []string{liveKey},
		}}), want: ErrLiveBlobsMissing},
		{name: "a count that cannot be true", sc: stepContextWith(t, &preBackupResult{Found: BlobFindings{
			Named: 1, Missing: 2, Damaged: 0, Keys: nil,
		}}), want: ErrLiveBlobsMissing},
		{name: "1 missing of 2", sc: stepContextWith(t, &preBackupResult{Found: BlobFindings{
			Named: 2, Missing: 1, Damaged: 0, Keys: []string{liveKey},
		}}), want: nil},
	} {
		u := newLiveBlobSetup(true)

		err := u.svc.checkPreBackupResult(t.Context(), tc.sc, keyInput())

		switch {
		case tc.want == nil && err != nil:
			t.Errorf("%s: checkPreBackupResult = %v, want nil", tc.name, err)
		case tc.want != nil && !errors.Is(err, tc.want):
			t.Errorf("%s: checkPreBackupResult = %v, want %v", tc.name, err, tc.want)
		case tc.want != nil && len(u.live.events) != 1:
			t.Errorf("%s: audit events = %v, want the refusal", tc.name, u.live.events)
		case tc.want == nil && len(u.live.events) != 0:
			t.Errorf("%s: audit events = %v, want none", tc.name, u.live.events)
		default:
		}
	}
}

// TestRestoreErrorCodeOfEachRefusal proves the code of each refusal, also
// when the refusal is wrapped, and no code for other errors.
func TestRestoreErrorCodeOfEachRefusal(t *testing.T) {
	t.Parallel()

	limit := EffectiveMissingBlobLimit()

	for _, tc := range []struct {
		err  error
		want RestoreErrorCode
	}{
		{
			err:  &LiveBlobsError{Sentinel: ErrLiveBlobsDamaged, Findings: BlobFindings{Named: 0, Missing: 0, Damaged: 0, Keys: nil}, Limit: limit, Keys: nil},
			want: CodeLiveBlobsDamaged,
		},
		{
			err:  &LiveBlobsError{Sentinel: ErrLiveBlobsMissing, Findings: BlobFindings{Named: 0, Missing: 0, Damaged: 0, Keys: nil}, Limit: limit, Keys: nil},
			want: CodeLiveBlobsMissing,
		},
		{err: errors.Join(errThrottled, ErrPreBackupForeign), want: CodePreBackupForeign},
		{err: &BlobsNotBackedError{Count: 1, First: liveKey}, want: CodeBlobsNotBacked},
		{
			err:  fmt.Errorf("apply the restore: %w", principal.ErrNoWorkingBreakGlass),
			want: CodeNoWorkingBreakGlass,
		},
		{err: ErrCorrupt, want: ""},
		{err: errThrottled, want: ""},
	} {
		if got := restoreErrorCode(tc.err); got != tc.want {
			t.Errorf("restoreErrorCode(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// TestCheckPreBackupFreeRefusesAnExistingBackup proves that a restore refuses
// a pre-backup ID that names a backup that exists, and fails closed when it
// cannot tell.
func TestCheckPreBackupFreeRefusesAnExistingBackup(t *testing.T) {
	t.Parallel()

	svc := keyService(nil)

	if err := svc.checkPreBackupFree(t.Context(), ""); err != nil {
		t.Fatalf("checkPreBackupFree of no ID = %v, want nil", err)
	}

	if err := svc.checkPreBackupFree(t.Context(), preBackupID); !errors.Is(err, ErrPreBackupForeign) {
		t.Fatalf("checkPreBackupFree with no backups service = %v, want ErrPreBackupForeign", err)
	}
}
