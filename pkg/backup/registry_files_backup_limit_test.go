package backup_test

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// registryEntries returns one entry for each size, each with its own hash.
func registryEntries(sizes ...int64) []backup.RegistryFileEntry {
	out := make([]backup.RegistryFileEntry, 0, len(sizes))

	for i, size := range sizes {
		out = append(out, backup.RegistryFileEntry{
			SHA256: fmt.Sprintf("%064x", i+1), Size: size, Release: fmt.Sprintf("rel%d", i), Version: "1.0.0",
		})
	}

	return out
}

// fullNamespace returns the sizes of files that fill one namespace to its
// limit, then extra.
func fullNamespace(extra ...int64) []int64 {
	sizes := make([]int64, 0, 8)
	for range backup.MaxRegistryFilesTotalBytes / backup.MaxRegistryFileBytes {
		sizes = append(sizes, backup.MaxRegistryFileBytes)
	}

	return append(sizes, extra...)
}

// TestCheckRegistryFileLimits sends the hostile sizes at the check of the
// service layer, as if the collector of the store did not check.
func TestCheckRegistryFileLimits(t *testing.T) {
	t.Parallel()

	file := backup.MaxRegistryFileBytes

	tests := []struct {
		name  string
		sizes []int64
		want  error
	}{
		{name: "no file", sizes: nil, want: nil},
		{name: "one byte under the file limit", sizes: []int64{file - 1}, want: nil},
		{name: "exactly the file limit", sizes: []int64{file}, want: nil},
		{name: "one byte over the file limit", sizes: []int64{file + 1}, want: backup.ErrRegistryFilesTooLarge},
		{name: "the largest int64", sizes: []int64{math.MaxInt64}, want: backup.ErrRegistryFilesTooLarge},
		{name: "a negative size", sizes: []int64{-1}, want: backup.ErrCorrupt},
		{name: "exactly the namespace limit", sizes: fullNamespace(), want: nil},
		{name: "one byte over the namespace limit", sizes: fullNamespace(1), want: backup.ErrRegistryFilesTooLarge},
		{
			name: "a size that overflows the total", sizes: fullNamespace(math.MaxInt64),
			want: backup.ErrRegistryFilesTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := backup.CheckRegistryFileLimits(
				map[string][]backup.RegistryFileEntry{"ns-a": registryEntries(tt.sizes...)},
			)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("CheckRegistryFileLimits = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestCheckRegistryFileLimitsManySmallFilesAddUp sends many files that are
// each far under the limit of one file and that add up over the limit of the
// namespace. Each namespace has its own total.
func TestCheckRegistryFileLimitsManySmallFilesAddUp(t *testing.T) {
	t.Parallel()

	const chunk = int64(1) << 20 // 1 MiB.

	count := int(backup.MaxRegistryFilesTotalBytes / chunk)
	sizes := make([]int64, 0, count+1)

	for range count {
		sizes = append(sizes, chunk)
	}

	atLimit := registryEntries(sizes...)

	if err := backup.CheckRegistryFileLimits(map[string][]backup.RegistryFileEntry{"ns-a": atLimit}); err != nil {
		t.Fatalf("files that sum to the limit = %v, want nil", err)
	}

	over := registryEntries(append(sizes, 1)...)

	err := backup.CheckRegistryFileLimits(map[string][]backup.RegistryFileEntry{"ns-a": over})
	if !errors.Is(err, backup.ErrRegistryFilesTooLarge) {
		t.Fatalf("files that sum above the limit = %v, want ErrRegistryFilesTooLarge", err)
	}

	two := map[string][]backup.RegistryFileEntry{"ns-a": atLimit, "ns-b": atLimit}
	if err := backup.CheckRegistryFileLimits(two); err != nil {
		t.Fatalf("two namespaces at the limit = %v, want nil", err)
	}
}

// TestRegistryFilesTooLargeErrorText checks the text of the refusal: strict
// STE, the release, the version, the size, the limit and the action.
func TestRegistryFilesTooLargeErrorText(t *testing.T) {
	t.Parallel()

	file := backup.CheckRegistryFile("net", "1.0.0", backup.MaxRegistryFileBytes+1, 0)

	var refused *backup.RegistryFilesTooLargeError
	if file == nil || !errors.As(file, &refused) || refused.Limit != backup.RegistryFileLimitFile {
		t.Fatalf("file refusal = %v, want a file limit error", file)
	}

	for _, want := range []string{
		`release "net" version "1.0.0"`, strconv.FormatInt(backup.MaxRegistryFileBytes+1, 10),
		strconv.FormatInt(backup.MaxRegistryFileBytes, 10), "wrote no manifest", "run the backup again",
	} {
		if !strings.Contains(file.Error(), want) {
			t.Fatalf("file refusal text = %q, want %q", file.Error(), want)
		}
	}

	nsErr := backup.CheckRegistryFile("net", "1.0.0", 1, backup.MaxRegistryFilesTotalBytes)
	if nsErr == nil || !errors.As(nsErr, &refused) || refused.Limit != backup.RegistryFileLimitNamespace {
		t.Fatalf("namespace refusal = %v, want a namespace limit error", nsErr)
	}

	for _, want := range []string{strconv.FormatInt(backup.MaxRegistryFilesTotalBytes, 10), "wrote no manifest", "run the backup again"} {
		if !strings.Contains(nsErr.Error(), want) {
			t.Fatalf("namespace refusal text = %q, want %q", nsErr.Error(), want)
		}
	}
}

// TestBackupRefusesRegistryFilesAboveTheLimit runs a backup whose exporter
// reports a file above the limit of a restore. The service refuses before the
// first copy and before the manifest, so no manifest exists that a restore
// refuses.
func TestBackupRefusesRegistryFilesAboveTheLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sizes   []int64
		refused bool
	}{
		{name: "exactly the file limit", sizes: []int64{backup.MaxRegistryFileBytes}, refused: false},
		{name: "one byte over the file limit", sizes: []int64{backup.MaxRegistryFileBytes + 1}, refused: true},
		{name: "exactly the namespace limit", sizes: fullNamespace(), refused: false},
		{name: "one byte over the namespace limit", sizes: fullNamespace(1), refused: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const id = backup.ID("bkp-registry-limit")

			// The summary holds a blob that the source stores, so that a
			// backup that copies before it checks the limits leaves a key
			// in the target.
			data := []byte("registry file bytes")
			ref := blobRef("namespaces/ns-aaaaaaaaaaaaaaaaaaaa/registry/files/f1", data)

			sum := testSummary(ref)
			sum.RegistryFiles = map[string][]backup.RegistryFileEntry{
				"ns-aaaaaaaaaaaaaaaaaaaa": registryEntries(tc.sizes...),
			}

			fix := newUnverifiedFixture(t, &stubExporter{sum: sum, data: []byte("{}"), err: nil}, 0)
			fix.source.storeObject(t, ref.Key, data)

			_, runErr := fix.service.Run(t.Context(), id)

			if !tc.refused {
				if runErr != nil && errors.Is(runErr, backup.ErrRegistryFilesTooLarge) {
					t.Fatalf("run = %v, want no registry file refusal", runErr)
				}

				return
			}

			checkRegistryRefusal(t, fix, id, runErr)
		})
	}
}

// checkRegistryRefusal proves that a refused backup failed, named the cause in
// its row and the log, and wrote no manifest.
func checkRegistryRefusal(t *testing.T, fix *unverifiedFixture, id backup.ID, runErr error) {
	t.Helper()

	if !errors.Is(runErr, backup.ErrFailed) {
		t.Fatalf("run = %v, want ErrFailed", runErr)
	}

	row := mustGetRecord(t, fix.registry, id)
	for _, want := range []string{"above the limit", "wrote no manifest", "run the backup again"} {
		if !strings.Contains(row.Error, want) {
			t.Fatalf("backup error = %q, want %q", row.Error, want)
		}
	}

	if fix.target.has(manifestKeyOf(id)) {
		t.Fatal("a refused backup wrote a manifest")
	}

	blobs, listErr := fix.target.list(t.Context(), "blobs/")
	if listErr != nil {
		t.Fatalf("list target blobs: %v", listErr)
	}

	if len(blobs) != 0 {
		t.Fatalf("a refused backup copied %d blobs to the target, want 0", len(blobs))
	}

	if !strings.Contains(fix.logs.String(), "backup refused") {
		t.Fatalf("the log lacks the refusal:\n%s", fix.logs.String())
	}
}
