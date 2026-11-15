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

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/internal/registry/pgpfixture"
)

// providerName is the provider that the test publishes.
const providerName = "payments"

// registryProvider is a provider release as a build tool writes it: the zip
// archive of one platform, the checksums file and the detached signature.
type registryProvider struct {
	name, version string
	archiveName   string
	archive       []byte
	sums          []byte
	signature     []byte
}

// buildProvider makes the release of a provider for linux/amd64. OpenTofu
// unpacks the archive and checks its hash and the signature of the checksums
// file; it does not run the binary during init, so the binary is a script.
func buildProvider(t *testing.T, sg pgpfixture.Signer, version string) registryProvider {
	t.Helper()

	var zipped bytes.Buffer

	zw := zip.NewWriter(&zipped)

	f, err := zw.Create(fmt.Sprintf("terraform-provider-%s_v%s", providerName, version))
	if err != nil {
		t.Fatalf("make the archive: %v", err)
	}

	if _, err = f.Write([]byte("#!/bin/sh\nexit 0\n")); err != nil {
		t.Fatalf("write the archive: %v", err)
	}

	if err = zw.Close(); err != nil {
		t.Fatalf("close the archive: %v", err)
	}

	p := registryProvider{
		name: providerName, version: version, archiveName: "", archive: zipped.Bytes(), sums: nil, signature: nil,
	}
	p.archiveName = fmt.Sprintf("terraform-provider-%s_%s_linux_amd64.zip", providerName, version)

	sum := sha256.Sum256(p.archive)
	p.sums = []byte(hex.EncodeToString(sum[:]) + "  " + p.archiveName + "\n")

	p.signature = sg.Detach(t, p.sums)

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

func (r registryAPI) addKey(ctx context.Context, t *testing.T, sg pgpfixture.Signer) {
	t.Helper()

	body, err := json.Marshal(map[string]string{"armoredPublicKey": string(sg.Key.Armored)})
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

	resp, err := r.env.ServerAPIStatusTyped(ctx, e2e.TypedRequest{
		Token: r.env.TokenA(), Method: http.MethodPost, Path: r.base + "/uploads",
		ContentType: "application/octet-stream", Body: string(data), IfMatch: "",
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}

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

// uploadBytes are the bytes of the three files that an upload sends. A test
// changes them after the signature.
type uploadBytes struct {
	archive, sums, signature []byte
}

// asBuilt returns the files of p as the build tool wrote them.
func (p registryProvider) asBuilt() uploadBytes {
	return uploadBytes{archive: p.archive, sums: p.sums, signature: p.signature}
}

// publish stages the files of p and publishes them. up holds the bytes that the
// upload sends.
func (r registryAPI) publish(ctx context.Context, t *testing.T, p registryProvider, up uploadBytes) e2e.APIResponse {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"protocols": []string{"5.0"},
		"files": []map[string]string{
			{"role": "sums", "name": p.sumsName(), "upload": r.stage(ctx, t, up.sums)},
			{"role": "signature", "name": p.sumsName() + ".sig", "upload": r.stage(ctx, t, up.signature)},
			{"role": "archive", "name": p.archiveName, "upload": r.stage(ctx, t, up.archive)},
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

	fetcher := sharedFetcher(t)

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

// registryScenario holds what the steps of the registry test share.
type registryScenario struct {
	env     *e2e.Env
	api     registryAPI
	signer  pgpfixture.Signer
	address string
	// good is the release 1.0.0 that publishGood published.
	good registryProvider
}

func runRegistryOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)
	sc := &registryScenario{
		env:     env,
		api:     registryAPI{env: env, base: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/registry"},
		signer:  pgpfixture.NewSigner(t),
		address: env.RegistryAddress(providerName),
		good:    registryProvider{name: "", version: "", archiveName: "", archive: nil, sums: nil, signature: nil},
	}

	sc.publishGood(ctx, t)
	sc.answerNotFound(ctx, t)
	sc.refuseHostileReleases(ctx, t)
	sc.removeASecondKey(ctx, t)
	sc.removeTheKey(ctx, t)
}

// publishGood publishes version 1.0.0 and installs it with the real CLI.
func (sc *registryScenario) publishGood(ctx context.Context, t *testing.T) {
	t.Helper()

	// No signing key yet: a publish is refused, and nothing is readable.
	early := buildProvider(t, sc.signer, "1.0.0")

	resp := sc.api.publish(ctx, t, early, early.asBuilt())
	if resp.Status != http.StatusUnprocessableEntity || !strings.Contains(resp.Body, "registry.no-keys") {
		t.Fatalf("publish with no trusted key = %d %s", resp.Status, resp.Body)
	}

	sc.checkProviders(ctx, t, `"providers":[]`)

	sc.api.addKey(ctx, t, sc.signer)

	good := buildProvider(t, sc.signer, "1.0.0")
	sc.good = good

	resp = sc.api.publish(ctx, t, good, good.asBuilt())
	if resp.Status != http.StatusOK || !strings.Contains(resp.Body, sc.signer.Key.Fingerprint) {
		t.Fatalf("publish = %d %s", resp.Status, resp.Body)
	}

	sc.checkProviders(ctx, t, `"name":"`+good.name+`"`)

	// A page token that is not a provider name is refused.
	bad := sc.api.call(ctx, t, http.MethodGet, "/providers?pageToken=../x", "")
	if bad.Status != http.StatusBadRequest {
		t.Fatalf("list providers with a hostile page token = %d %s", bad.Status, bad.Body)
	}

	// The same files again are a repeat and change nothing.
	if again := sc.api.publish(ctx, t, good, good.asBuilt()); again.Status != http.StatusOK {
		t.Fatalf("repeat of the publish = %d %s", again.Status, again.Body)
	}

	dir := sc.env.WriteRegistryProject(t, "project-registry", fmt.Sprintf(registryConfig, sc.address, "1.0.0"))

	const name = "registry init"

	sc.env.StepIn(ctx, t, dir, step(name, sc.checkInit(name, dir, good), "init", "-input=false", "-no-color"))
}

// checkProviders proves that the provider list answers 200 with one page that
// holds want and no next page token.
func (sc *registryScenario) checkProviders(ctx context.Context, t *testing.T, want string) {
	t.Helper()

	resp := sc.api.call(ctx, t, http.MethodGet, "/providers", "")
	if resp.Status != http.StatusOK || !strings.Contains(resp.Body, want) ||
		!strings.Contains(resp.Body, `"nextPageToken":""`) {
		t.Fatalf("list providers = %d %s, want %s", resp.Status, resp.Body, want)
	}
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

// checkInit proves init installed the provider from the registry, checked the
// signature and wrote the zh: hash of the archive to the lock file.
func (sc *registryScenario) checkInit(name, dir string, p registryProvider) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, "Installed "+sc.address+" v"+p.version+" (signed") {
			return fmt.Errorf("%w: init did not install a signed provider: %s", errUnexpected, r.Stdout)
		}

		lockPath := filepath.Join(dir, ".terraform.lock.hcl")

		lock, err := os.ReadFile(lockPath) //nolint:gosec // The path is in the test project.
		if err != nil {
			return fmt.Errorf("read the lock file: %w", err)
		}

		if !strings.Contains(string(lock), "zh:"+p.archiveSHA()) {
			return fmt.Errorf("%w: the lock file lacks zh:%s: %s", errUnexpected, p.archiveSHA(), lock)
		}

		versions := "/v1/providers/" + e2e.Namespace + "/payments/versions"
		download := "/v1/providers/" + e2e.Namespace + "/payments/" + p.version + "/download/linux/amd64"

		if !sc.env.Requested(name, http.MethodGet, versions) ||
			!sc.env.Requested(name, http.MethodGet, download) {
			return fmt.Errorf("%w: init never asked for the version list and the download document", errUnexpected)
		}

		return nil
	}
}

// refuseHostileReleases sends the hostile releases and proves that none of them
// becomes readable.
func (sc *registryScenario) refuseHostileReleases(ctx context.Context, t *testing.T) {
	t.Helper()

	// 1.0.1: signed over an archive, then the upload swaps the archive.
	tampered := buildProvider(t, sc.signer, "1.0.1")
	up := tampered.asBuilt()
	up.archive = bytes.Clone(tampered.archive)
	up.archive[len(up.archive)-1] ^= 0xff

	resp := sc.api.publish(ctx, t, tampered, up)
	if resp.Status != http.StatusUnprocessableEntity || !strings.Contains(resp.Body, tampered.archiveName) {
		t.Fatalf("publish of a tampered archive = %d %s, want 422 naming the archive", resp.Status, resp.Body)
	}

	// 1.0.2: signed by a key that the namespace does not trust.
	stranger := buildProvider(t, pgpfixture.NewSigner(t), "1.0.2")

	resp = sc.api.publish(ctx, t, stranger, stranger.asBuilt())
	if resp.Status != http.StatusUnprocessableEntity ||
		!strings.Contains(resp.Body, "registry.signature-untrusted-key") {
		t.Fatalf("publish by an untrusted key = %d %s", resp.Status, resp.Body)
	}

	// 1.0.3: the checksums file changed after the signature.
	edited := buildProvider(t, sc.signer, "1.0.3")
	up = edited.asBuilt()
	up.sums = append(bytes.Clone(edited.sums), '\n')

	resp = sc.api.publish(ctx, t, edited, up)
	if resp.Status != http.StatusUnprocessableEntity || !strings.Contains(resp.Body, "registry.signature-invalid") {
		t.Fatalf("publish with edited checksums = %d %s", resp.Status, resp.Body)
	}

	// None of the three versions exists.
	list := sc.api.call(ctx, t, http.MethodGet, "/providers/payments/versions", "")
	if list.Status != http.StatusOK || strings.Count(list.Body, `"version":`) != 1 ||
		!strings.Contains(list.Body, `"version":"1.0.0"`) {
		t.Fatalf("version list after the refusals = %d %s, want only 1.0.0", list.Status, list.Body)
	}

	// The CLI cannot install a refused version.
	dir := sc.env.WriteRegistryProject(t, "project-refused", fmt.Sprintf(registryConfig, sc.address, "1.0.1"))
	sc.env.StepIn(ctx, t, dir, failStep("registry init of a refused version",
		outputContains("Could not resolve provider"), "init", "-input=false", "-no-color"))
}

// removeASecondKey registers a second key B, publishes 1.1.0 signed by B, and
// removes B. The release of B stays in the record, the registry stops serving
// it, and tofu init with a range installs 1.0.0 that the first key signed. A
// repeat of the publish of 1.1.0 is refused, because no trusted key signed it.
func (sc *registryScenario) removeASecondKey(ctx context.Context, t *testing.T) {
	t.Helper()

	second := pgpfixture.NewSigner(t)
	sc.api.addKey(ctx, t, second)

	newer := buildProvider(t, second, "1.1.0")

	resp := sc.api.publish(ctx, t, newer, newer.asBuilt())
	if resp.Status != http.StatusOK || !strings.Contains(resp.Body, second.Key.Fingerprint) {
		t.Fatalf("publish 1.1.0 with the second key = %d %s", resp.Status, resp.Body)
	}

	resp = sc.api.call(ctx, t, http.MethodDelete, "/keys/"+second.Key.Fingerprint, "")
	if resp.Status != http.StatusNoContent {
		t.Fatalf("remove the second key = %d %s", resp.Status, resp.Body)
	}

	if again := sc.api.publish(ctx, t, newer, newer.asBuilt()); again.Status != http.StatusUnprocessableEntity {
		t.Fatalf("repeat of the publish after the key was removed = %d %s, want 422", again.Status, again.Body)
	}

	dir := sc.env.WriteRegistryProject(t, "project-range", fmt.Sprintf(registryConfig, sc.address, ">= 1.0"))

	const name = "registry init of a range after the second key was removed"

	sc.env.StepIn(ctx, t, dir, step(name, sc.checkInit(name, dir, sc.good), "init", "-input=false", "-no-color"))
}

// removeTheKey removes the key of the namespace: the signed release stays
// published, and the registry stops serving it.
func (sc *registryScenario) removeTheKey(ctx context.Context, t *testing.T) {
	t.Helper()

	resp := sc.api.call(ctx, t, http.MethodDelete, "/keys/"+sc.signer.Key.Fingerprint, "")
	if resp.Status != http.StatusNoContent {
		t.Fatalf("remove the key = %d %s", resp.Status, resp.Body)
	}

	const name = "registry init after the key was removed"

	fresh := sc.env.WriteRegistryProject(t, "project-removed", fmt.Sprintf(registryConfig, sc.address, "1.0.0"))
	sc.env.StepIn(ctx, t, fresh, failStep(name, sc.checkRefusedList(name), "init", "-input=false", "-no-color"))
}

// registryNotFoundBody checks the 404 body of a registry route: the errors
// member of the protocol, a list of strings, and next to it the code of the
// catalog in the code member, with no arguments and none of the camelCase
// members.
func registryNotFoundBody(body string) error {
	var doc struct {
		Errors []string        `json:"errors"`
		Code   string          `json:"code"`
		Meta   json.RawMessage `json:"meta"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: the 404 body is not a registry error document: %s", errUnexpected, body)
	}

	if len(doc.Errors) != 1 || doc.Errors[0] != "not found" || doc.Code != "registry.not-found" ||
		doc.Meta != nil || strings.Contains(body, "errorCode") || strings.Contains(body, "errorArgs") {
		return fmt.Errorf("%w: the 404 body = %s, want one text and the code registry.not-found", errUnexpected, body)
	}

	return nil
}

// answerNotFound proves that a registry route answers 404 for a provider that
// the namespace does not hold, with the media type and the document of the
// registry protocol.
func (sc *registryScenario) answerNotFound(ctx context.Context, t *testing.T) {
	t.Helper()

	resp, err := sc.env.ServerAPIStatusTyped(ctx, e2e.TypedRequest{
		Token: sc.env.TokenA(), Method: http.MethodGet,
		Path:        "/v1/providers/" + e2e.Namespace + "/unknown/versions",
		ContentType: "", Body: "", IfMatch: "",
	})
	if err != nil {
		t.Fatalf("GET the versions of an unknown provider: %v", err)
	}

	if resp.Status != http.StatusNotFound || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("versions of an unknown provider = %d %q %s, want 404 application/json",
			resp.Status, resp.Header.Get("Content-Type"), resp.Body)
	}

	if err := registryNotFoundBody(resp.Body); err != nil {
		t.Fatal(err)
	}
}

// checkRefusedList proves that the version list, which init asked for in the
// step, answered 404 with the registry error document.
func (sc *registryScenario) checkRefusedList(name string) func(e2e.Result) error {
	return func(e2e.Result) error {
		versions := "/v1/providers/" + e2e.Namespace + "/payments/versions"

		entries := sc.env.Recorder.Entries()

		for i := range entries {
			e := &entries[i]
			if e.Step != name || e.Method != http.MethodGet || e.Path != versions {
				continue
			}

			if e.Status != http.StatusNotFound {
				return fmt.Errorf("%w: the version list answered %d, want 404", errUnexpected, e.Status)
			}

			return registryNotFoundBody(e.ResponseBody)
		}

		return fmt.Errorf("%w: init never asked for the version list", errUnexpected)
	}
}
