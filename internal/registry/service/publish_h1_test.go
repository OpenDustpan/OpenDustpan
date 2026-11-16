package service_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"testing"

	"opendustpan.org/opendustpan/internal/registry/pgpfixture"
	"opendustpan.org/opendustpan/pkg/registry"
)

// zipFile is one entry of a test provider archive. When link is set, the
// entry is a symlink to link.
type zipFile struct {
	name string
	data []byte
	link string
}

// fileEntry returns a regular zip entry with the given name and bytes.
func fileEntry(name string, data []byte) zipFile {
	return zipFile{name: name, data: data, link: ""}
}

// linkEntry returns a zip entry that is a symlink with the given name and
// target.
func linkEntry(name, target string) zipFile {
	return zipFile{name: name, data: nil, link: target}
}

// makeZip returns the bytes of a zip archive with the given entries in order.
func makeZip(t *testing.T, files []zipFile) []byte {
	t.Helper()

	var buf bytes.Buffer

	w := zip.NewWriter(&buf)

	for _, f := range files {
		header := new(zip.FileHeader)
		header.Name = f.name
		header.Method = zip.Deflate

		if f.link != "" {
			header.SetMode(os.ModeSymlink | 0o777)
		}

		entry, err := w.CreateHeader(header)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", f.name, err)
		}

		if f.link != "" {
			if _, err := entry.Write([]byte(f.link)); err != nil {
				t.Fatalf("write symlink %s: %v", f.name, err)
			}

			continue
		}

		if _, err := entry.Write(f.data); err != nil {
			t.Fatalf("write zip entry %s: %v", f.name, err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	return buf.Bytes()
}

// manifestJSON returns a terraform-registry-manifest.json with the given
// protocol versions.
func manifestJSON(protocols ...string) []byte {
	var out bytes.Buffer

	out.WriteString(`{"version":1,"metadata":{"protocol_versions":[`)

	for i, p := range protocols {
		if i > 0 {
			out.WriteString(",")
		}

		fmt.Fprintf(&out, "%q", p)
	}

	out.WriteString(`]}}`)

	return out.Bytes()
}

// buildZipSet returns the archive set of release 1.0.0 of provider payments
// for linux_amd64, whose single archive holds data, with the checksums file
// and the signature of sg.
func buildZipSet(t *testing.T, sg pgpfixture.Signer, data []byte) *set {
	t.Helper()

	file := "terraform-provider-payments_1.0.0_linux_amd64.zip"
	sums := hashOf(data) + "  " + file + "\n"

	return &set{
		name: "payments", version: "1.0.0", sums: []byte(sums), sig: sg.Detach(t, []byte(sums)),
		archives: map[string][]byte{file: data}, order: []string{file},
	}
}

// requireNoRelease fails the test when release version of provider payments
// of the test namespace exists.
func requireNoRelease(t *testing.T, e *env, version string) {
	t.Helper()

	addr := registry.Address{Namespace: acme, Kind: registry.KindProvider, Name: "payments", System: ""}

	_, err := e.store.Get(t.Context(), addr, version)
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("release after refused publish: got %v, want %v", err, registry.ErrNotFound)
	}
}

// providerZip returns the bytes of a provider archive with a binary and a
// manifest with the given protocols.
func providerZip(t *testing.T, protocols ...string) []byte {
	t.Helper()

	return makeZip(t, []zipFile{
		fileEntry("terraform-provider-payments", []byte("fake provider binary")),
		fileEntry("terraform-registry-manifest.json", manifestJSON(protocols...)),
	})
}

// TestPublish_storesH1FromUnpackedBytes checks that a publish computes the H1
// digest over the unpacked archive bytes and stores it on the archive. It
// fails on main: the stored archive lacks H1.
func TestPublish_storesH1FromUnpackedBytes(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	s := buildZipSet(t, sg, providerZip(t, "5.0"))

	rel, err := e.publish(t, acme, s)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if len(rel.Archives) != 1 {
		t.Fatalf("archives: got %d, want 1", len(rel.Archives))
	}

	if rel.Archives[0].H1 == "" {
		t.Fatal("stored archive has no H1 digest")
	}

	stored, err := e.store.Get(t.Context(), rel.Address, rel.Version)
	if err != nil {
		t.Fatalf("read the stored release: %v", err)
	}

	if stored.Archives[0].H1 != rel.Archives[0].H1 {
		t.Fatal("stored H1 differs from the H1 of the publish answer")
	}

	again, err := e.publish(t, acme, s)
	if err != nil {
		t.Fatalf("republish the same bytes: %v", err)
	}

	if again.Archives[0].H1 != rel.Archives[0].H1 {
		t.Fatal("republish of the same bytes changed H1")
	}
}
