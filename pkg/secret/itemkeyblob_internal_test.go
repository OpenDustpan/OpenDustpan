package secret

import (
	"context"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestFilterGarbageRefusesAKeyOfAnotherNamespace sends the filter of the
// reaper rule a listing that holds old, unnamed item key blobs of another
// namespace and of another family, as if the view had failed to scope the
// listing. Only the blob of the namespace of the call is garbage.
func TestFilterGarbageRefusesAKeyOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	const (
		ns      = namespace.ID("ns-aaaaaaaaaaaaaaaaaaaa")
		other   = namespace.ID("ns-bbbbbbbbbbbbbbbbbbbb")
		attempt = "abcdefghij0123456789"
	)

	now := time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * ItemKeyBlobGracePeriod)
	own := blob.Key("namespaces/" + string(ns) + "/item-keys/ik-1/v1-" + attempt)

	objects := []blob.Object{
		{Key: blob.Key("namespaces/" + string(other) + "/item-keys/ik-1/v1-" + attempt), ModifiedAt: old},
		{Key: blob.Key("namespaces/" + string(other) + "/item-keys/ik-2/v3-" + attempt), ModifiedAt: old},
		{Key: blob.Key("namespaces/" + string(ns) + "/pulumi/stk-1/upd-1/checkpoint-1"), ModifiedAt: old},
		{Key: own, ModifiedAt: old},
	}

	var asked []ItemKeyBlob

	named := func(_ context.Context, part ItemKeyBlob) (bool, error) {
		asked = append(asked, part)

		return false, nil
	}

	got, err := filterGarbage(t.Context(), ns, objects, named, now)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}

	if !slices.Equal(got, []blob.Key{own}) {
		t.Errorf("garbage = %v, want only %s", got, own)
	}

	if len(asked) != 1 || asked[0].Namespace != ns {
		t.Errorf("records asked for %v, want only the blob of %s", asked, ns)
	}
}
