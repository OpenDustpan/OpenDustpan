package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// sweepStates is a StateStore whose keep set is fixed.
type sweepStates struct {
	StateStore

	keys []blob.Key
	err  error
}

// ReferencedContentKeys implements StateStore.
func (s sweepStates) ReferencedContentKeys(context.Context, ID) ([]blob.Key, error) {
	return s.keys, s.err
}

// failingDeleteStore is a blob.Store that refuses every delete with err.
type failingDeleteStore struct {
	blob.Store

	err error
}

// Delete implements blob.Store.
func (s failingDeleteStore) Delete(context.Context, blob.Key) error {
	return s.err
}

// errSweepDelete is the failure of a delete that the sweep reports. It wraps
// ErrInvalidKey, so the retry does not wait for a failure that cannot change.
var errSweepDelete = fmt.Errorf("blob store down: %w", blob.ErrInvalidKey)

// TestSweepPageHidesTheKeyOfAFailedDelete proves that the error of an
// unreferenced-blob delete names the operation and the workspace, not the blob
// key.
func TestSweepPageHidesTheKeyOfAFailedDelete(t *testing.T) {
	t.Parallel()

	const (
		ns = namespace.ID("ns-acmeacmeacmeacmeacme")
		ws = ID("ws-1")
	)

	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	key := blob.Key("namespaces/" + ns.String() + "/tofu/" + ws.String() + "/sv-1/a1")

	svc := new(Service)
	svc.states = sweepStates{StateStore: nil, keys: nil, err: nil}
	svc.blobs = &stateBlobs{store: failingDeleteStore{Store: nil, err: errSweepDelete}, refusals: nil}
	svc.now = func() time.Time { return now }

	w := new(Workspace)
	w.Ref = Ref{Namespace: namespace.Name("acme"), Name: Name("dev")}
	w.ID = ws
	w.NamespaceID = ns

	err := svc.sweepPage(
		t.Context(),
		*w,
		[]blob.Object{{Key: key, ModifiedAt: now.Add(-2 * SweepGracePeriod)}},
	)
	if err == nil {
		t.Fatal("err = nil, want the delete failure")
		return
	}

	if !errors.Is(err, errSweepDelete) {
		t.Fatalf("err = %v, want the delete failure", err)
	}

	if !strings.Contains(err.Error(), "delete unreferenced content") {
		t.Errorf("error text %q does not name the operation", err)
	}

	if !strings.Contains(err.Error(), "acme/dev") {
		t.Errorf("error text %q does not name the workspace", err)
	}

	if strings.Contains(err.Error(), key.String()) || strings.Contains(err.Error(), "namespaces/") {
		t.Errorf("error text %q names the key", err)
	}
}
