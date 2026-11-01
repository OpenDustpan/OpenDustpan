package backup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// eventLive is a RestoreLive that records the audit events and nothing else.
// A call to any other method panics on the nil interface, so a test also
// proves that the code under test calls nothing else.
type eventLive struct {
	RestoreLive

	events []string
	err    error
}

// RecordRestoreEvent implements RestoreOperations.
func (l *eventLive) RecordRestoreEvent(_ context.Context, _ durable.StepFence, _ RestoreID, object string) error {
	if l.err != nil {
		return l.err
	}

	l.events = append(l.events, object)

	return nil
}

var errEventStore = errors.New("event store down")

// keyService returns a RestoreService over live that has nothing else: a call
// that needs a blob store panics on it.
func keyService(live RestoreLive) *RestoreService {
	return &RestoreService{
		live: live, operations: nil, canceler: nil, backups: nil, maintenance: nil, blobs: nil, target: nil,
		key: nil, installation: "", afterApply: nil, admit: nil, allowDev: false, devHint: "", lease: 0,
		lockWait: 0, now: nil, logger: nil, runner: nil,
	}
}

// keyInput returns the input of a restore of a backup.
func keyInput() restoreInput {
	return restoreInput{
		ID: MintRestoreID(), BackupID: "bkp-1", PreBackupID: "", CheckOnly: false, Force: false,
		RequestedBy: "", KeepAccessTables: false, evidence: preBackupResult{Digest: "", Damaged: 0, Created: false},
	}
}

// noFence is a fence with no claim: the stub live store checks none.
func noFence() durable.StepFence {
	return durable.StepFence{Operation: "", Token: 0, Step: ""}
}

// TestCopyBlobRefusesProtectedKeys sends a hostile key straight to copyBlob,
// as if the steps before it had not checked the manifest. The copy refuses
// before it reads or writes any store: the service has no blob store, and the
// call would panic on it.
func TestCopyBlobRefusesProtectedKeys(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"audit-checkpoints/epochs/0001/6163/abc",
		"store-checks/chk-1/probe",
		"restore-tmp/rst-1/1/k",
		"logs/app/1",
	} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			live := &eventLive{RestoreLive: nil, events: nil, err: nil}
			s := keyService(live)
			in := keyInput()

			err := s.copyBlob(
				t.Context(),
				in,
				noFence(),
				ManifestBlob{Key: key, Size: 1, MD5: "", Sealed: false},
			)
			if !errors.Is(err, ErrProtectedKey) {
				t.Fatalf("copyBlob(%q): err = %v, want ErrProtectedKey", key, err)
			}

			if len(live.events) != 1 || !strings.Contains(live.events[0], "refused blob key") {
				t.Fatalf("events = %v, want one refusal", live.events)
			}
		})
	}
}

// TestRefuseKeysFailsClosedWhenTheEventIsLost proves that a refusal stands
// when the audit event cannot be stored, and that the error of the store is
// joined to it. A lost claim stops the step instead.
func TestRefuseKeysFailsClosedWhenTheEventIsLost(t *testing.T) {
	t.Parallel()

	bad := []ManifestBlob{{Key: "audit-checkpoints/x", Size: 1, MD5: "", Sealed: false}}
	in := keyInput()

	live := &eventLive{RestoreLive: nil, events: nil, err: errEventStore}
	s := keyService(live)

	err := s.refuseKeys(t.Context(), noFence(), in, bad)
	if !errors.Is(err, ErrProtectedKey) || !errors.Is(err, errEventStore) {
		t.Fatalf("refuseKeys with a lost event: err = %v, want the refusal joined to the store error", err)
	}

	live.err = durable.ErrClaimLost

	err = s.refuseKeys(t.Context(), noFence(), in, bad)
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("refuseKeys with a lost claim: err = %v, want ErrClaimLost", err)
	}
}

// TestCopyManifestBlobsChecksTheWholeManifestFirst sends a manifest with a good
// key and then a hostile key straight to the copy, as if the stage, the check
// and the pin of the manifest had not refused it. The copy refuses before it
// copies the first key: the service has no blob store, and a copy of the good
// key would panic on it.
func TestCopyManifestBlobsChecksTheWholeManifestFirst(t *testing.T) {
	t.Parallel()

	live := &eventLive{RestoreLive: nil, events: nil, err: nil}
	s := keyService(live)
	manifest := manifestWith(
		ManifestBlob{Key: "namespaces/ns-abc/pulumi/stk-1/upd-1/1", Size: 1, MD5: "", Sealed: false},
		ManifestBlob{Key: "audit-checkpoints/epochs/0001/6163/abc", Size: 1, MD5: "", Sealed: false},
	)

	err := s.copyManifestBlobs(t.Context(), noFence(), keyInput(), manifest)
	if !errors.Is(err, ErrProtectedKey) {
		t.Fatalf("copyManifestBlobs: err = %v, want ErrProtectedKey", err)
	}

	if len(live.events) != 1 {
		t.Fatalf("events = %v, want one refusal", live.events)
	}
}

// TestManifestDigestCoversTheBlobList proves that a change of the blob list
// changes the digest that the stage pins.
func TestManifestDigestCoversTheBlobList(t *testing.T) {
	t.Parallel()

	one := manifestWith(ManifestBlob{Key: "namespaces/ns-a/x", Size: 1, MD5: "", Sealed: false})
	two := manifestWith(
		ManifestBlob{Key: "namespaces/ns-a/x", Size: 1, MD5: "", Sealed: false},
		ManifestBlob{Key: "namespaces/ns-a/y", Size: 1, MD5: "", Sealed: false},
	)

	a, err := manifestDigest(one)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}

	again, err := manifestDigest(one)
	if err != nil || again != a {
		t.Fatalf("digest of one manifest twice = %q and %q, %v, want equal", a, again, err)
	}

	b, err := manifestDigest(two)
	if err != nil || b == a {
		t.Fatalf("digest of two manifests = %q and %q, %v, want different", a, b, err)
	}

	pinned := pinnedManifest{
		sqlSize: 0, sqlSHA: "", schema: 0, dialect: "", kind: "", wrappers: nil, digest: a,
	}
	if err := checkPinnedManifest(manifestWith(), "bkp-1", pinned); err == nil {
		t.Fatal("checkPinnedManifest accepted a manifest that is not the pinned one")
	}
}

// manifestWith returns a manifest that holds blobs and nothing else.
func manifestWith(blobs ...ManifestBlob) Manifest {
	return Manifest{
		CreatedAt: time.Time{}, ID: "bkp-1", Dialect: "", PrimaryInstallations: nil, WrapperIDs: nil, Blobs: blobs,
		Version: "", SQLSHA256: "", Format: 0, SchemaVersion: 0, SQLSize: 0, RowCounts: nil, SQLFormat: "",
		SQLFiles: nil, InstallationKind: "", Unverified: nil, Damaged: nil,
	}
}
