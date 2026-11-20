package blobrefs_test

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/internal/store/blobrefs"
)

// sizedRelease returns a release with one archive for each size. Each archive
// has its own content hash, so the collector counts each one.
func sizedRelease(name string, first int, sizes ...int64) registry.Release {
	rel := release(nil)
	rel.Address.Name = name
	rel.Archives = nil
	rel.Files = nil

	for i, size := range sizes {
		hash := fmt.Sprintf("%064x", first+i+1)

		rel.Archives = append(rel.Archives, registry.Archive{
			Platform: "", FileName: fmt.Sprintf("a%d.zip", first+i), SHA256: hash, Size: size, H1: "",
		})
		rel.Files = append(rel.Files, registry.StoredFile{SHA256: hash, Size: size, MD5: md5Hex})
	}

	rel.PublishedAt = time.Unix(1, 0).UTC()

	return rel
}

// TestCollectorReleaseRegistryFileLimits sends the sizes at, under and over
// the limits of one file and of one namespace. The backup refuses a size that
// a restore refuses, so no backup is written that no restore applies. The
// collector is the adapter layer: it does not rely on the service check that
// runs after the export.
func TestCollectorReleaseRegistryFileLimits(t *testing.T) {
	t.Parallel()

	perFile := backup.MaxRegistryFileBytes
	perNamespace := backup.MaxRegistryFilesTotalBytes
	// The count of files of the largest size that fit in one namespace.
	full := int(perNamespace / perFile)

	filled := func(extra ...int64) []int64 {
		sizes := make([]int64, 0, full+len(extra))
		for range full {
			sizes = append(sizes, perFile)
		}

		return append(sizes, extra...)
	}

	tests := []struct {
		name    string
		sizes   []int64
		wantErr bool
	}{
		{name: "one byte under the file limit", sizes: []int64{perFile - 1}, wantErr: false},
		{name: "exactly the file limit", sizes: []int64{perFile}, wantErr: false},
		{name: "one byte over the file limit", sizes: []int64{perFile + 1}, wantErr: true},
		{name: "the largest int64", sizes: []int64{math.MaxInt64}, wantErr: true},
		{name: "exactly the namespace limit", sizes: filled(), wantErr: false},
		{name: "one byte over the namespace limit", sizes: filled(1), wantErr: true},
		{name: "a size that overflows the total", sizes: filled(math.MaxInt64), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var c blobrefs.Collector

			err := c.Release(blobrefs.ReleaseRow{NamespaceID: nsID, Release: sizedRelease("net", 0, tt.sizes...)})
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("Release = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, backup.ErrRegistryFilesTooLarge) {
				t.Fatalf("Release = %v, want ErrRegistryFilesTooLarge", err)
			}
		})
	}
}

// TestCollectorReleaseNegativeSize sends a negative size. It is not a size
// that a restore accepts, so the backup refuses it as corrupt data.
func TestCollectorReleaseNegativeSize(t *testing.T) {
	t.Parallel()

	var c blobrefs.Collector

	err := c.Release(blobrefs.ReleaseRow{NamespaceID: nsID, Release: sizedRelease("net", 0, -1)})
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("Release = %v, want ErrCorrupt", err)
	}
}

// fillNamespace sends releases of the largest allowed file size until the
// namespace holds the largest allowed total. Each release has its own hash.
func fillNamespace(t *testing.T, c *blobrefs.Collector, namespace, prefix string) int {
	t.Helper()

	perFile := backup.MaxRegistryFileBytes
	full := int(backup.MaxRegistryFilesTotalBytes / perFile)

	for i := range full {
		row := blobrefs.ReleaseRow{
			NamespaceID: namespace,
			Release:     sizedRelease(fmt.Sprintf("%s%d", prefix, i), i, perFile),
		}
		if err := c.Release(row); err != nil {
			t.Fatalf("Release %d = %v, want nil", i, err)
		}
	}

	return full
}

// TestCollectorReleaseManySmallFilesAddUp sends many releases whose files are
// each under the limit and whose total is over the limit of the namespace. The
// total counts each content hash once and each namespace by itself.
func TestCollectorReleaseManySmallFilesAddUp(t *testing.T) {
	t.Parallel()

	t.Run("the next release over the total is refused", func(t *testing.T) {
		t.Parallel()

		var c blobrefs.Collector

		full := fillNamespace(t, &c, nsID, "m")

		row := blobrefs.ReleaseRow{NamespaceID: nsID, Release: sizedRelease("last", full, 1)}
		if err := c.Release(row); !errors.Is(err, backup.ErrRegistryFilesTooLarge) {
			t.Fatalf("Release over the total = %v, want ErrRegistryFilesTooLarge", err)
		}
	})

	t.Run("a shared hash counts once", func(t *testing.T) {
		t.Parallel()

		var c blobrefs.Collector

		for i := range 40 {
			row := blobrefs.ReleaseRow{NamespaceID: nsID, Release: sizedRelease(fmt.Sprintf("s%d", i), 0, 1)}
			if err := c.Release(row); err != nil {
				t.Fatalf("Release %d of one shared file = %v, want nil", i, err)
			}
		}
	})

	t.Run("another namespace has its own total", func(t *testing.T) {
		t.Parallel()

		var c blobrefs.Collector

		full := fillNamespace(t, &c, nsID, "n")

		other := blobrefs.ReleaseRow{
			NamespaceID: "ns-bbbbbbbbbbbbbbbbbbbb",
			Release:     sizedRelease("o", full, backup.MaxRegistryFileBytes),
		}
		if err := c.Release(other); err != nil {
			t.Fatalf("Release in another namespace = %v, want nil", err)
		}
	})
}

// TestCollectorReleaseSharedLargeHashCountsOnce pins the skip of a content
// hash that the namespace already counted. The namespace holds the largest
// allowed total. A release that names the hash of a file it already holds adds
// no bytes, so the collector accepts it. Without the skip, the check counts
// the shared file a second time and refuses a backup that a restore accepts.
func TestCollectorReleaseSharedLargeHashCountsOnce(t *testing.T) {
	t.Parallel()

	var c blobrefs.Collector

	fillNamespace(t, &c, nsID, "full")

	again := blobrefs.ReleaseRow{
		NamespaceID: nsID,
		Release:     sizedRelease("again", 0, backup.MaxRegistryFileBytes),
	}
	if err := c.Release(again); err != nil {
		t.Fatalf("Release of a file the namespace already holds = %v, want nil", err)
	}

	want := int(backup.MaxRegistryFilesTotalBytes / backup.MaxRegistryFileBytes)
	if got := len(c.RegistryFiles()[nsID]); got != want {
		t.Fatalf("manifest entries = %d, want %d", got, want)
	}
}

// TestCollectorReleaseRefusalLeavesNoEntry checks that a refused release adds
// no manifest entry and no blob reference.
func TestCollectorReleaseRefusalLeavesNoEntry(t *testing.T) {
	t.Parallel()

	var c blobrefs.Collector

	row := blobrefs.ReleaseRow{NamespaceID: nsID, Release: sizedRelease("big", 0, backup.MaxRegistryFileBytes+1)}
	if err := c.Release(row); !errors.Is(err, backup.ErrRegistryFilesTooLarge) {
		t.Fatalf("Release = %v, want ErrRegistryFilesTooLarge", err)
	}

	if got := c.RegistryFiles(); len(got) != 0 {
		t.Fatalf("RegistryFiles = %v, want none", got)
	}

	assertCollected(t, &c, collected{keys: nil, unverified: nil})
}
