package tfe_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const (
	mirrorIndexPath   = "/v1/mirror/registry.example.com/acme/payments/index.json"
	mirrorVersionPath = "/v1/mirror/registry.example.com/acme/payments/1.0.0.json"
)

// mirrorVersionWire decodes a version document of the mirror protocol.
type mirrorVersionWire struct {
	// Archives holds one entry per platform, for example "linux_amd64".
	Archives map[string]mirrorArchiveWire `json:"archives"`
}

type mirrorArchiveWire struct {
	// URL is the signed file URL of the provider registry protocol.
	URL string `json:"url"`
	// Hashes holds the "zh:" SHA-256 of the archive.
	Hashes []string `json:"hashes"`
}

func TestMirrorIndex(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	rec := f.get(t, mirrorIndexPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("mirror index = %d %s", rec.Code, rec.Body.String())
	}

	var doc struct {
		Versions map[string]struct{} `json:"versions"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("mirror index = %s, %v", rec.Body.String(), err)
	}

	if len(doc.Versions) != 1 {
		t.Fatalf("mirror index lists %d versions, want 1.0.0 only: %s", len(doc.Versions), rec.Body.String())
	}

	if _, ok := doc.Versions["1.0.0"]; !ok {
		t.Fatalf("mirror index lacks 1.0.0: %s", rec.Body.String())
	}
}

func TestMirrorVersion(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	rec := f.get(t, mirrorVersionPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("mirror version = %d %s", rec.Code, rec.Body.String())
	}

	var doc mirrorVersionWire
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("mirror version = %s, %v", rec.Body.String(), err)
	}

	archive, ok := doc.Archives["linux_amd64"]
	if !ok || len(archive.Hashes) != 1 || archive.Hashes[0] != "zh:"+zipHash {
		t.Fatalf("mirror version archives = %s, want linux_amd64 with zh:%s", rec.Body.String(), zipHash)
	}

	u, err := url.Parse(archive.URL)
	if err != nil || u.Host != registryHost || u.Scheme != "https" {
		t.Fatalf("archive URL %q is not on the registry host: %v", archive.URL, err)
	}

	if !strings.Contains(u.Path, "/v1/providers/acme/payments/1.0.0/files/"+zipHash) {
		t.Fatalf("archive URL %q is not the signed file route", archive.URL)
	}

	file := f.serve(t, regReq{host: registryHost, target: u.RequestURI(), token: "", headers: nil})
	if file.Code != http.StatusOK || file.Body.String() != "zip" {
		t.Fatalf("archive file = %d %q, want the bytes", file.Code, file.Body.String())
	}
}

func TestMirrorRefusals(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	for name, target := range map[string]string{
		"unknown provider":       "/v1/mirror/registry.example.com/acme/unknown/index.json",
		"unknown version":        "/v1/mirror/registry.example.com/acme/payments/9.9.9.json",
		"other namespace":        "/v1/mirror/registry.example.com/other/payments/index.json",
		"other host":             "/v1/mirror/evil.example.com/acme/payments/index.json",
		"other host, version":    "/v1/mirror/evil.example.com/acme/payments/1.0.0.json",
		"version without suffix": "/v1/mirror/registry.example.com/acme/payments/1.0.0",
		"not a version file":     "/v1/mirror/registry.example.com/acme/payments/latest",
	} {
		rec := f.get(t, target)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", name, rec.Code)
		}
	}

	// Defense in depth: a file name with the .json suffix that is not a valid
	// version is refused before the store is read.
	for _, file := range []string{"1.0.0+x.json", "v1.0.0.json", "..json", "1.0.0.json.json", ".json"} {
		before := f.reg.reads()

		rec := f.get(t, "/v1/mirror/registry.example.com/acme/payments/"+file)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("version file %q = %d, want 404", file, rec.Code)
		}

		if after := f.reg.reads(); after != before {
			t.Fatalf("version file %q read the store %d times, want 0", file, after-before)
		}
	}

	// A removed key leaves the version out of the index and the version
	// document.
	f.reg.keyGone = true

	for name, target := range map[string]string{
		"index after the key was removed":   mirrorIndexPath,
		"version after the key was removed": mirrorVersionPath,
	} {
		rec := f.get(t, target)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", name, rec.Code)
		}
	}
}
