package storetest

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// savedViewOwner returns the principal ID of owner i of the saved view limit
// cases.
func savedViewOwner(i int) string {
	return fmt.Sprintf("prn-limitowner%06d", i)
}

// savedViewRow returns a saved_views row in the portable form, with the empty
// query. The name differs for each n, so the unique index of the names of an
// owner holds.
func savedViewRow(ns namespace.Namespace, owner string, n int) map[string]any {
	return map[string]any{
		"id": string(savedview.NewID()), "namespace_id": string(ns.ID), "owner": owner,
		"name": fmt.Sprintf("view %d", n), "query_json": `{"labels":[],"references":[]}`, "created_at_ms": 1,
	}
}

// stageSavedViews exports a store with the namespace acme and no saved view,
// adds the rows of perOwner to the saved views file of the export, and stages
// the export into dst. perOwner holds the number of views of each owner, in
// owner order. It returns the error of the stage.
func stageSavedViews(t *testing.T, newStore PortableFactory, dst PortableStore, perOwner []int) error {
	t.Helper()

	s := newStore(t)
	if _, ok := s.(interface{ SavedViews() savedview.Store }); !ok {
		t.Skip("the store keeps no saved views")
	}

	ns := newWorkspaceNamespace(t, s, "acme")

	files, sum, descs := PortableFiles(t, s)

	var rows []map[string]any

	for i, count := range perOwner {
		for n := range count {
			rows = append(rows, savedViewRow(ns, savedViewOwner(i), n))
		}
	}

	files, descs, sum = AddPortableRows(t, files, descs, sum, "saved_views", rows...)

	_, err := dst.StagePortable(t.Context(), PortableOpener(files), sum, descs, StageAs(t, dst))

	return err
}

// requireSavedViewLimitRefusal checks that err is the refusal of a stage over
// a saved view limit: ErrCorrupt with want in its text. It also checks that
// the live data of dst stays empty.
func requireSavedViewLimitRefusal(t *testing.T, err error, dst PortableStore, want string) {
	t.Helper()

	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("stage over the limit: err = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(err.Error(), want) {
		t.Fatalf("stage over the limit: err = %v, want the text %q", err, want)
	}

	if left := mustListNamespaces(t, dst); len(left) != 0 {
		t.Fatalf("namespaces after a refused stage = %+v, want none", left)
	}
}

// testPortableSavedViewOwnerLimit stages an owner with savedview.MaxPerOwner
// views in one namespace, which passes, and an owner with one view more,
// which fails with ErrCorrupt. The error names the owner, the count and the
// limit. No port writes a view over the limit, so the test edits the export by
// hand.
func testPortableSavedViewOwnerLimit(t *testing.T, newStore PortableFactory) {
	t.Helper()

	t.Run("at the limit", func(t *testing.T) {
		t.Parallel()

		if err := stageSavedViews(t, newStore, newStore(t), []int{savedview.MaxPerOwner}); err != nil {
			t.Fatalf("stage %d views of one owner: %v", savedview.MaxPerOwner, err)
		}
	})

	t.Run("over the limit", func(t *testing.T) {
		t.Parallel()

		dst := newStore(t)
		err := stageSavedViews(t, newStore, dst, []int{savedview.MaxPerOwner + 1})

		want := fmt.Sprintf("owner %q has %d saved views in namespace %q, the limit is %d",
			savedViewOwner(0), savedview.MaxPerOwner+1, "acme", savedview.MaxPerOwner)
		requireSavedViewLimitRefusal(t, err, dst, want)
	})
}

// testPortableSavedViewNamespaceLimit stages a namespace with
// savedview.MaxPerNamespace views, which passes, and a namespace with one view
// more, which fails with ErrCorrupt. No owner is over its own limit. The error
// names the namespace, the count and the limit.
func testPortableSavedViewNamespaceLimit(t *testing.T, newStore PortableFactory) {
	t.Helper()

	full := make([]int, 0, savedview.MaxPerNamespace/savedview.MaxPerOwner+1)
	for range savedview.MaxPerNamespace / savedview.MaxPerOwner {
		full = append(full, savedview.MaxPerOwner)
	}

	t.Run("at the limit", func(t *testing.T) {
		t.Parallel()

		if err := stageSavedViews(t, newStore, newStore(t), full); err != nil {
			t.Fatalf("stage %d views of one namespace: %v", savedview.MaxPerNamespace, err)
		}
	})

	t.Run("over the limit", func(t *testing.T) {
		t.Parallel()

		dst := newStore(t)
		err := stageSavedViews(t, newStore, dst, append(full, 1))

		want := fmt.Sprintf("namespace %q has %d saved views, the limit is %d",
			"acme", savedview.MaxPerNamespace+1, savedview.MaxPerNamespace)
		requireSavedViewLimitRefusal(t, err, dst, want)
	})
}
