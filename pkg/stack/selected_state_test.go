package stack_test

import (
	"bytes"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// savedVersion starts an update, saves body as its checkpoint and completes
// it, and returns its version.
func (f updatesFixture) savedVersion(t *testing.T, body string) int64 {
	t.Helper()

	s := f.started(t, stack.KindUpdate)

	if err := f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, checkpoint(0, body)); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete: %v", err)
	}

	return s.Update.Version
}

// TestSelectState proves that a selector names the latest checkpoint with
// the version of its update, or the checkpoint of a version, and that the
// previous deployment is the one of the version before.
func TestSelectState(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	first := f.savedVersion(t, `{"v":1}`)
	second := f.savedVersion(t, `{"v":2}`)

	latest, err := f.svc.SelectState(t.Context(), f.ref, stack.StateSelector{Latest: true, Version: 0})
	if err != nil || latest.Version != second || !bytes.Equal(latest.Checkpoint.Deployment, []byte(`{"v":2}`)) {
		t.Fatalf("latest = %d %s %v, want version %d with the second deployment", latest.Version,
			latest.Checkpoint.Deployment, err, second)
	}

	if latest.VersionLabel() == "latest" || latest.VersionLabel() == "" {
		t.Fatalf("version label = %q, want the number", latest.VersionLabel())
	}

	older, err := f.svc.SelectState(t.Context(), f.ref, stack.StateSelector{Latest: false, Version: first})
	if err != nil || !bytes.Equal(older.Checkpoint.Deployment, []byte(`{"v":1}`)) {
		t.Fatalf("version %d = %s %v, want the first deployment", first, older.Checkpoint.Deployment, err)
	}

	checkPrevious(t, f, latest, older)

	_, err = f.svc.SelectState(t.Context(), f.ref, stack.StateSelector{Latest: false, Version: second + 5})
	if !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("version without a checkpoint: %v, want ErrNoState", err)
	}
}

// TestSelectCheckpointNeedsNoUpdate proves that a read of the latest
// checkpoint that needs no version does not read the update that saved it:
// SelectCheckpoint answers when that update cannot be read, and only
// SelectState, which names the version, fails.
func TestSelectCheckpointNeedsNoUpdate(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	f.savedVersion(t, `{"v":1}`)

	latest := stack.StateSelector{Latest: true, Version: 0}

	cp, err := f.svc.SelectCheckpoint(t.Context(), f.ref, latest)
	if err != nil {
		t.Fatalf("select checkpoint: %v", err)
	}

	// The update that saved the checkpoint can no longer be read.
	f.store.mu.Lock()
	delete(f.store.updates, cp.Update)
	f.store.mu.Unlock()

	again, err := f.svc.SelectCheckpoint(t.Context(), f.ref, latest)
	if err != nil || !bytes.Equal(again.Deployment, []byte(`{"v":1}`)) {
		t.Fatalf("select checkpoint without the update = %s %v, want the deployment", again.Deployment, err)
	}

	if _, err := f.svc.SelectState(t.Context(), f.ref, latest); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("select state without the update: %v, want ErrUpdateNotFound", err)
	}
}

// checkPrevious checks that the previous deployment of latest is the one of
// first, and that first has none.
func checkPrevious(t *testing.T, f updatesFixture, latest, first stack.SelectedState) {
	t.Helper()

	prev, err := f.svc.PreviousDeployment(t.Context(), f.ref, latest)
	if err != nil || !bytes.Equal(prev, first.Checkpoint.Deployment) {
		t.Fatalf("previous of the latest = %s %v, want the first deployment", prev, err)
	}

	prev, err = f.svc.PreviousDeployment(t.Context(), f.ref, first)
	if err != nil || prev != nil {
		t.Fatalf("previous of the first version = %s %v, want none", prev, err)
	}
}

// TestParseStateSelector proves the values that a selector takes.
func TestParseStateSelector(t *testing.T) {
	t.Parallel()

	if sel, err := stack.ParseStateSelector("latest"); err != nil || !sel.Latest {
		t.Fatalf("latest = %+v %v, want the latest checkpoint", sel, err)
	}

	if sel, err := stack.ParseStateSelector("7"); err != nil || sel.Latest || sel.Version != 7 {
		t.Fatalf("7 = %+v %v, want version 7", sel, err)
	}

	for _, raw := range []string{"", "-1", "Latest", "seven", "1.5"} {
		if _, err := stack.ParseStateSelector(raw); !errors.Is(err, stack.ErrInvalidUpdate) {
			t.Errorf("ParseStateSelector(%q) = %v, want ErrInvalidUpdate", raw, err)
		}
	}
}

// TestCheckServiceSecrets proves that only a deployment of the service
// secrets provider passes, and that a deployment that names none reads as
// the service provider.
func TestCheckServiceSecrets(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{`{}`, `{"secrets_providers":{"type":"service"}}`, `not json`} {
		if err := stack.CheckServiceSecrets([]byte(ok)); err != nil {
			t.Errorf("CheckServiceSecrets(%s) = %v, want nil", ok, err)
		}
	}

	err := stack.CheckServiceSecrets([]byte(`{"secrets_providers":{"type":"passphrase"}}`))
	if !errors.Is(err, stack.ErrOtherSecretsProvider) {
		t.Fatalf("passphrase provider: %v, want ErrOtherSecretsProvider", err)
	}
}
