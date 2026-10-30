package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

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
			s := &RestoreService{live: live}
			in := restoreInput{ID: MintRestoreID(), BackupID: "bkp-1"}

			err := s.copyBlob(
				t.Context(),
				in,
				durable.StepFence{},
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
	in := restoreInput{ID: MintRestoreID(), BackupID: "bkp-1"}

	live := &eventLive{RestoreLive: nil, events: nil, err: errEventStore}
	s := &RestoreService{live: live}

	err := s.refuseKeys(t.Context(), durable.StepFence{}, in, bad)
	if !errors.Is(err, ErrProtectedKey) || !errors.Is(err, errEventStore) {
		t.Fatalf("refuseKeys with a lost event: err = %v, want the refusal joined to the store error", err)
	}

	live.err = durable.ErrClaimLost

	err = s.refuseKeys(t.Context(), durable.StepFence{}, in, bad)
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("refuseKeys with a lost claim: err = %v, want ErrClaimLost", err)
	}
}
