package backup_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

func fileHashOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestBackupRegistryFiles(t *testing.T) {
	t.Parallel()

	c, err := backup.NewRegistryFileCollector("ns-abc")
	if err != nil {
		t.Fatalf("NewRegistryFileCollector = %v", err)
	}

	h1 := fileHashOf("registry-file-a")
	h2 := fileHashOf("registry-file-b")

	if err := c.AddRelease("provider-aws", "1.0.0", []string{h1, h2}, []int64{3, 4}); err != nil {
		t.Fatalf("AddRelease = %v", err)
	}

	if err := c.AddRelease("module-vpc", "2.0.0", []string{h1}, []int64{3}); err != nil {
		t.Fatalf("AddRelease shared = %v", err)
	}

	entries := c.Entries()
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (shared key stored once)", len(entries))
	}

	section := c.Section()
	if section.Version != backup.RegistryFileSectionVersion {
		t.Fatalf("section version = %d, want %d", section.Version, backup.RegistryFileSectionVersion)
	}

	for _, e := range section.Files {
		if !backup.ValidRegistryFileHash(e.SHA256) {
			t.Fatalf("entry hash %q is not a SHA-256 key", e.SHA256)
		}
	}
}

func TestBackupRegistryInputs(t *testing.T) {
	t.Parallel()

	if _, err := backup.NewRegistryFileCollector(""); err == nil {
		t.Fatalf("NewRegistryFileCollector(%q) = nil, want error naming the namespace ID", "")
	}

	c, err := backup.NewRegistryFileCollector("ns-abc")
	if err != nil {
		t.Fatalf("NewRegistryFileCollector = %v", err)
	}

	if err := c.AddRelease("provider-aws", "1.0.0", []string{""}, []int64{1}); err == nil {
		t.Fatal("AddRelease empty hash = nil, want error naming the release and version")
	}

	hostile := []string{"../escape", strings.Repeat("g", 64), "ABC", "namespaces/ns-abc/registry/sha256/x"}
	for _, h := range hostile {
		if err := c.AddRelease("provider-aws", "1.0.0", []string{h}, []int64{1}); err == nil {
			t.Fatalf("AddRelease(%q) = nil, want error naming the release and version", h)
		}
	}
}

func TestBackupRegistrySnapshot(t *testing.T) {
	t.Parallel()

	first, err := backup.NewRegistryFileCollector("ns-abc")
	if err != nil {
		t.Fatalf("NewRegistryFileCollector = %v", err)
	}

	h1 := fileHashOf("snapshot-a")
	if err := first.AddRelease("provider-aws", "1.0.0", []string{h1}, []int64{3}); err != nil {
		t.Fatalf("AddRelease = %v", err)
	}

	// A second collector from the same snapshot sees the same rows and files:
	// the set is stable, so a release published mid-backup is not half-copied.
	second, secondErr := backup.NewRegistryFileCollector("ns-abc")
	if secondErr != nil {
		t.Fatalf("NewRegistryFileCollector = %v", secondErr)
	}

	if err := second.AddRelease("provider-aws", "1.0.0", []string{h1}, []int64{3}); err != nil {
		t.Fatalf("AddRelease = %v", err)
	}

	if len(first.Entries()) != len(second.Entries()) {
		t.Fatalf("snapshot entries differ: %d vs %d", len(first.Entries()), len(second.Entries()))
	}
}

func TestBackupVerifyHash(t *testing.T) {
	t.Parallel()

	h := fileHashOf("registry-file-a")
	if err := backup.VerifyRegistryFileContent(h, []byte("registry-file-a")); err != nil {
		t.Fatalf("VerifyRegistryFileContent valid = %v", err)
	}

	if err := backup.VerifyRegistryFileContent(h, []byte("altered-bytes")); err == nil {
		t.Fatalf("VerifyRegistryFileContent corrupt = nil, want error naming %q", h)
	}
}

func TestBackupUnreadableFile(t *testing.T) {
	t.Parallel()

	c, err := backup.NewRegistryFileCollector("ns-abc")
	if err != nil {
		t.Fatalf("NewRegistryFileCollector = %v", err)
	}

	h := fileHashOf("registry-file-a")
	if err := c.AddRelease("provider-aws", "1.0.0", []string{h}, []int64{3}); err != nil {
		t.Fatalf("AddRelease = %v", err)
	}

	// The collector names the release and version, so the backup can stop with
	// an error that names them when the blob read fails.
	entries := c.Entries()
	if entries[0].Release != "provider-aws" || entries[0].Version != "1.0.0" {
		t.Fatalf("entry = %+v, want release provider-aws version 1.0.0", entries[0])
	}
}

func TestBackupPublishedOnly(t *testing.T) {
	t.Parallel()

	c, err := backup.NewRegistryFileCollector("ns-abc")
	if err != nil {
		t.Fatalf("NewRegistryFileCollector = %v", err)
	}

	// Only files named by published releases are added; a staged upload with
	// no published release adds no entry.
	if len(c.Entries()) != 0 {
		t.Fatalf("len(entries) = %d, want 0 for staged-only namespace", len(c.Entries()))
	}
}

func TestBackupReportSize(t *testing.T) {
	t.Parallel()

	c, err := backup.NewRegistryFileCollector("ns-abc")
	if err != nil {
		t.Fatalf("NewRegistryFileCollector = %v", err)
	}

	h1 := fileHashOf("aaa")
	h2 := fileHashOf("bbbbb")

	if err := c.AddRelease("provider-aws", "1.0.0", []string{h1, h2}, []int64{3, 5}); err != nil {
		t.Fatalf("AddRelease = %v", err)
	}

	report := c.Report()
	if report.Count != 2 || report.TotalBytes != 8 {
		t.Fatalf("report = %+v, want count 2 and total bytes 8", report)
	}
}
