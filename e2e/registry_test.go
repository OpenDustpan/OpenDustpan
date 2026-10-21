//go:build e2e

package e2e_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"opendustpan.org/opendustpan/e2e"
)

// registryProvider is a provider release as a build tool writes it: the zip
// archive of one platform, the checksums file and the detached signature.
type registryProvider struct {
	name, version string
	archiveName   string
	archive       []byte
	sums          []byte
	signature     []byte
}

// registrySigner is an OpenPGP key pair for the signature of a release.
type registrySigner struct {
	entity      *openpgp.Entity
	armored     string
	fingerprint string
}

func newRegistrySigner(t *testing.T) registrySigner {
	t.Helper()

	entity, err := openpgp.NewEntity("Payments release", "", "release@example.invalid",
		&packet.Config{Algorithm: packet.PubKeyAlgoEdDSA})
	if err != nil {
		t.Fatalf("make the signing key: %v", err)
	}

	var buf bytes.Buffer

	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatalf("armor the key: %v", err)
	}

	if err = entity.Serialize(w); err != nil {
		t.Fatalf("write the key: %v", err)
	}

	if err = w.Close(); err != nil {
		t.Fatalf("close the armor: %v", err)
	}

	return registrySigner{
		entity: entity, armored: buf.String(),
		fingerprint: strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint)),
	}
}

// buildProvider makes the release of a provider for linux/amd64. OpenTofu
// unpacks the archive and checks its hash and the signature of the checksums
// file; it does not run the binary during init, so the binary is a script.
func buildProvider(t *testing.T, sg registrySigner, name, version string) registryProvider {
	t.Helper()

	var zipped bytes.Buffer

	zw := zip.NewWriter(&zipped)

	f, err := zw.Create(fmt.Sprintf("terraform-provider-%s_v%s", name, version))
	if err != nil {
		t.Fatalf("make the archive: %v", err)
	}

	if _, err = f.Write([]byte("#!/bin/sh\nexit 0\n")); err != nil {
		t.Fatalf("write the archive: %v", err)
	}

	if err = zw.Close(); err != nil {
		t.Fatalf("close the archive: %v", err)
	}

	p := registryProvider{name: name, version: version, archive: zipped.Bytes()}
	p.archiveName = fmt.Sprintf("terraform-provider-%s_%s_linux_amd64.zip", name, version)

	sum := sha256.Sum256(p.archive)
	p.sums = []byte(hex.EncodeToString(sum[:]) + "  " + p.archiveName + "\n")

	var sig bytes.Buffer
	if err = openpgp.DetachSign(&sig, sg.entity, bytes.NewReader(p.sums), nil); err != nil {
		t.Fatalf("sign the checksums file: %v", err)
	}

	p.signature = sig.Bytes()

	return p
}

func (p registryProvider) sumsName() string {
	return fmt.Sprintf("terraform-provider-%s_%s_SHA256SUMS", p.name, p.version)
}

func (p registryProvider) archiveSHA() string {
	sum := sha256.Sum256(p.archive)

	return hex.EncodeToString(sum[:])
}

// registryAPI drives the registry API of the server with the token of the
// bootstrap administrator.
type registryAPI struct {
	env  *e2e.Env
	base string
}

func (r registryAPI) call(ctx context.Context, t *testing.T, method, path, body string) e2e.APIResponse {
	t.Helper()

	resp, err := r.env.APIStatusHeaders(ctx, method, r.base+path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	return resp
}

func (r registryAPI) addKey(ctx context.Context, t *testing.T, sg registrySigner) {
	t.Helper()

	body, err := json.Marshal(map[string]string{"armoredPublicKey": sg.armored})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if resp := r.call(ctx, t, http.MethodPost, "/keys", string(body)); resp.Status != http.StatusCreated {
		t.Fatalf("register the key = %d %s", resp.Status, resp.Body)
	}
}

// stage uploads one file and returns its token.
func (r registryAPI) stage(ctx context.Context, t *testing.T, data []byte) string {
	t.Helper()

	resp := r.call(ctx, t, http.MethodPost, "/uploads", string(data))
	if resp.Status != http.StatusCreated {
		t.Fatalf("stage = %d %s", resp.Status, resp.Body)
	}

	var doc struct {
		Upload string `json:"upload"`
	}

	if err := json.Unmarshal([]byte(resp.Body), &doc); err != nil || doc.Upload == "" {
		t.Fatalf("stage answer %q: %v", resp.Body, err)
	}

	return doc.Upload
}

// publish stages the files of p and publishes them. archive and signature are
// the bytes that the upload sends, so a test can change them after the
// signature.
func (r registryAPI) publish(
	ctx context.Context, t *testing.T, p registryProvider, archive, sums, signature []byte,
) e2e.APIResponse {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"protocols": []string{"5.0"},
		"files": []map[string]string{
			{"role": "sums", "name": p.sumsName(), "upload": r.stage(ctx, t, sums)},
			{"role": "signature", "name": p.sumsName() + ".sig", "upload": r.stage(ctx, t, signature)},
			{"role": "archive", "name": p.archiveName, "upload": r.stage(ctx, t, archive)},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return r.call(ctx, t, http.MethodPut, "/providers/"+p.name+"/versions/"+p.version, string(body))
}

// TestRegistryTofuInit proves the private registry with the real OpenTofu CLI,
// on each store: a platform team registers its signing key and publishes one
// signed provider version through the API, and tofu init installs it from
// OpenDustpan and checks the signature. A tampered archive, a signature by a
// key that the namespace does not trust and a signed release whose key the
// namespace removed never reach the CLI.
func TestRegistryTofuInit(t *testing.T) {
	t.Parallel()

	tool := e2e.OpenTofu()

	if runtime.GOOS != "linux" {
		t.Skipf("the OpenTofu suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, label := range tool.Versions() {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
			defer cancel()

			cli, installErr := fetcher.Install(installCtx, tool, label)
			if installErr != nil {
				t.Fatalf("install %s %s: %v", tool.Name, label, installErr)
			}

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()
					runRegistryOnStore(t, cli, db)
				})
			}
		})
	}
}

func runRegistryOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)
	api := registryAPI{env: env, base: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/registry"}

	signer := newRegistrySigner(t)

	// No signing key yet: a publish is refused, and nothing is readable.
	early := buildProvider(t, signer, "payments", "1.0.0")
	if resp := api.publish(ctx, t, early, early.archive, early.sums, early.signature); resp.Status != http.StatusUnprocessableEntity ||
		!strings.Contains(resp.Body, "registry.no-keys") {
		t.Fatalf("publish with no trusted key = %d %s", resp.Status, resp.Body)
	}

	api.addKey(ctx, t, signer)

	good := buildProvider(t, signer, "payments", "1.0.0")

	published := api.publish(ctx, t, good, good.archive, good.sums, good.signature)
	if published.Status != http.StatusOK || !strings.Contains(published.Body, signer.fingerprint) {
		t.Fatalf("publish = %d %s", published.Status, published.Body)
	}

	// The same files again are a repeat and change nothing.
	if resp := api.publish(ctx, t, good, good.archive, good.sums, good.signature); resp.Status != http.StatusOK {
		t.Fatalf("repeat of the publish = %d %s", resp.Status, resp.Body)
	}

	address := env.RegistryAddress("payments")
	dir := env.WriteRegistryProject(t, "project-registry", fmt.Sprintf(registryConfig, address, "1.0.0"))

	env.StepIn(ctx, t, dir, step("registry init", checkRegistryInit(env, dir, address, good), "init", "-input=false", "-no-color"))

	checkRefusals(ctx, t, env, api, signer, address)
}

const registryConfig = `terraform {
  required_providers {
    payments = {
      source  = %q
      version = %q
    }
  }
}
`

// checkRegistryInit proves init installed the provider from the registry,
// checked the signature and wrote the zh: hash of the archive to the lock file.
func checkRegistryInit(env *e2e.Env, dir, address string, p registryProvider) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, "Installed "+address+" v"+p.version+" (signed") {
			return fmt.Errorf("%w: init did not install a signed provider: %s", errUnexpected, r.Stdout)
		}

		lock, err := os.ReadFile(filepath.Join(dir, ".terraform.lock.hcl"))
		if err != nil {
			return fmt.Errorf("read the lock file: %w", err)
		}

		if !strings.Contains(string(lock), "zh:"+p.archiveSHA()) {
			return fmt.Errorf("%w: the lock file lacks zh:%s: %s", errUnexpected, p.archiveSHA(), lock)
		}

		for _, want := range []struct{ path, why string }{
			{"/v1/providers/" + e2e.Namespace + "/payments/versions", "the version list"},
			{"/v1/providers/" + e2e.Namespace + "/payments/" + p.version + "/download/linux/amd64", "the download document"},
		} {
			if !env.Requested("registry init", http.MethodGet, want.path) {
				return fmt.Errorf("%w: init never asked for %s", errUnexpected, want.why)
			}
		}

		return nil
	}
}

// checkRefusals sends the hostile releases and proves that none of them
// becomes readable.
func checkRefusals(
	ctx context.Context, t *testing.T, env *e2e.Env, api registryAPI, signer registrySigner, address string,
) {
	t.Helper()

	// 1.0.1: signed over an archive, then the upload swaps the archive.
	tampered := buildProvider(t, signer, "payments", "1.0.1")
	other := bytes.Clone(tampered.archive)
	other[len(other)-1] ^= 0xff

	resp := api.publish(ctx, t, tampered, other, tampered.sums, tampered.signature)
	if resp.Status != http.StatusUnprocessableEntity || !strings.Contains(resp.Body, tampered.archiveName) {
		t.Fatalf("publish of a tampered archive = %d %s, want 422 naming the archive", resp.Status, resp.Body)
	}

	// 1.0.2: signed by a key that the namespace does not trust.
	stranger := buildProvider(t, newRegistrySigner(t), "payments", "1.0.2")

	resp = api.publish(ctx, t, stranger, stranger.archive, stranger.sums, stranger.signature)
	if resp.Status != http.StatusUnprocessableEntity || !strings.Contains(resp.Body, "registry.signature-untrusted-key") {
		t.Fatalf("publish by an untrusted key = %d %s", resp.Status, resp.Body)
	}

	// 1.0.3: the checksums file changed after the signature.
	edited := buildProvider(t, signer, "payments", "1.0.3")

	resp = api.publish(ctx, t, edited, edited.archive, append(bytes.Clone(edited.sums), '\n'), edited.signature)
	if resp.Status != http.StatusUnprocessableEntity || !strings.Contains(resp.Body, "registry.signature-invalid") {
		t.Fatalf("publish with edited checksums = %d %s", resp.Status, resp.Body)
	}

	// None of the three versions exists.
	list := api.call(ctx, t, http.MethodGet, "/providers/payments/versions", "")
	if list.Status != http.StatusOK || strings.Count(list.Body, `"version":`) != 1 || !strings.Contains(list.Body, `"version":"1.0.0"`) {
		t.Fatalf("version list after the refusals = %d %s, want only 1.0.0", list.Status, list.Body)
	}

	// The CLI cannot install a refused version.
	dir := env.WriteRegistryProject(t, "project-refused", fmt.Sprintf(registryConfig, address, "1.0.1"))
	env.StepIn(ctx, t, dir, failStep("registry init of a refused version", outputContains("Could not resolve provider"),
		"init", "-input=false", "-no-color"))

	// The namespace removes the key: the signed release stays published, and the
	// registry stops serving it.
	if resp = api.call(ctx, t, http.MethodDelete, "/keys/"+signer.fingerprint, ""); resp.Status != http.StatusNoContent {
		t.Fatalf("remove the key = %d %s", resp.Status, resp.Body)
	}

	fresh := env.WriteRegistryProject(t, "project-removed", fmt.Sprintf(registryConfig, address, "1.0.0"))
	env.StepIn(ctx, t, fresh, failStep("registry init after the key was removed", nil, "init", "-input=false", "-no-color"))
}
