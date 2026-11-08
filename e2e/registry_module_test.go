//go:build e2e

package e2e_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// moduleSource is a file of a module as a team writes it.
const (
	moduleMain = "output \"greeting\" {\n  value = %q\n}\n"
)

// moduleZip returns the zip archive of a module with one file.
func moduleZip(t *testing.T, greeting string, extra map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	files := map[string]string{"main.tf": fmt.Sprintf(moduleMain, greeting)}
	maps.Copy(files, extra)

	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("make the module archive: %v", err)
		}

		if _, err = w.Write([]byte(body)); err != nil {
			t.Fatalf("write the module archive: %v", err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("close the module archive: %v", err)
	}

	return buf.Bytes()
}

// moduleTarGz returns the tar.gz archive of a module with one file.
func moduleTarGz(t *testing.T, greeting string) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := fmt.Sprintf(moduleMain, greeting)

	var h tar.Header

	h.Name, h.Mode, h.Size, h.Typeflag = "main.tf", 0o644, int64(len(body)), tar.TypeReg

	if err := tw.WriteHeader(&h); err != nil {
		t.Fatalf("make the module archive: %v", err)
	}

	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatalf("write the module archive: %v", err)
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close the module archive: %v", err)
	}

	if err := gz.Close(); err != nil {
		t.Fatalf("close the module archive: %v", err)
	}

	return buf.Bytes()
}

// publishModule stages data and publishes it at path, which is the part of the
// module route after /modules, for example "vpc/aws/versions/1.0.0".
func (r registryAPI) publishModule(ctx context.Context, t *testing.T, path string, data []byte) e2e.APIResponse {
	t.Helper()

	body, err := json.Marshal(map[string]string{"upload": r.stage(ctx, t, data)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return r.call(ctx, t, http.MethodPut, "/modules/"+path, string(body))
}

// TestRegistryModuleTofuInit proves the private registry with the real OpenTofu
// CLI, on each store: a team publishes modules through the API as a zip file
// and as a tar.gz file, and tofu init downloads them from OpenDustpan. An
// archive with a path that leaves the archive, and an archive with a link that
// points outside it, are refused and never reach the CLI.
func TestRegistryModuleTofuInit(t *testing.T) {
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
					runRegistryModulesOnStore(t, cli, db)
				})
			}
		})
	}
}

const moduleConfig = `module "vpc" {
  source  = "%s/vpc/aws"
  version = %q
}

module "net" {
  source  = "%s/net/aws"
  version = %q
}
`

const (
	vpcPath = "vpc/aws/versions/1.0.0"
	netPath = "net/aws/versions/2.1.0"
)

func runRegistryModulesOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)
	api := registryAPI{env: env, base: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/registry"}

	publishModules(ctx, t, api)
	refuseHostileModules(ctx, t, api)

	if list := api.call(ctx, t, http.MethodGet, "/modules", ""); list.Status != http.StatusOK ||
		!strings.Contains(list.Body, `"name":"vpc"`) || !strings.Contains(list.Body, `"name":"net"`) ||
		strings.Contains(list.Body, "evil") {
		t.Fatalf("list modules = %d %s", list.Status, list.Body)
	}

	address := strings.TrimSuffix(env.RegistryAddress(""), "/")
	dir := env.WriteRegistryProject(
		t,
		"project-modules",
		fmt.Sprintf(moduleConfig, address, "1.0.0", address, "~> 2.0"),
	)

	const name = "registry module init"

	env.StepIn(ctx, t, dir, step(name, checkModuleInit(env, name, dir), "init", "-input=false", "-no-color"))

	missing := env.WriteRegistryProject(t, "project-missing-module",
		fmt.Sprintf(moduleConfig, address, "9.9.9", address, "~> 2.0"))

	env.StepIn(ctx, t, missing, failStep("registry module init of a missing version",
		outputContains("Unresolvable module version constraint"), "init", "-input=false", "-no-color"))
}

// publishModules publishes a module as a zip file and a module as a tar.gz
// file, and proves the repeat and the conflict. A module needs no signing key:
// the namespace has none here.
func publishModules(ctx context.Context, t *testing.T, api registryAPI) {
	t.Helper()

	zipped := api.publishModule(ctx, t, vpcPath, moduleZip(t, "from the zip file", nil))
	if zipped.Status != http.StatusOK || !strings.Contains(zipped.Body, `"name":"vpc-aws-1.0.0.zip"`) {
		t.Fatalf("publish the zip module = %d %s", zipped.Status, zipped.Body)
	}

	tarred := api.publishModule(ctx, t, netPath, moduleTarGz(t, "from the tar.gz file"))
	if tarred.Status != http.StatusOK || !strings.Contains(tarred.Body, `"name":"net-aws-2.1.0.tar.gz"`) {
		t.Fatalf("publish the tar.gz module = %d %s", tarred.Status, tarred.Body)
	}

	// The same files again are a repeat.
	again := api.publishModule(ctx, t, vpcPath, moduleZip(t, "from the zip file", nil))
	if again.Status != http.StatusOK {
		t.Fatalf("repeat of the publish = %d %s", again.Status, again.Body)
	}

	// Other files for the version are a conflict.
	other := api.publishModule(ctx, t, vpcPath, moduleZip(t, "changed", nil))
	if other.Status != http.StatusConflict {
		t.Fatalf("publish of other files for a published version = %d %s, want 409", other.Status, other.Body)
	}
}

// moduleSymlinkEscape returns a tar.gz archive with a symbolic link that points
// outside the archive.
func moduleSymlinkEscape(t *testing.T) []byte {
	t.Helper()

	var (
		buf bytes.Buffer
		h   tar.Header
	)

	h.Name, h.Typeflag, h.Linkname, h.Mode = "escape", tar.TypeSymlink, "../../../etc/passwd", 0o777

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if err := tw.WriteHeader(&h); err != nil {
		t.Fatalf("make the archive: %v", err)
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("make the archive: %v", err)
	}

	if err := gz.Close(); err != nil {
		t.Fatalf("make the archive: %v", err)
	}

	return buf.Bytes()
}

// refuseHostileModules sends archives that a client must never unpack and
// proves that the server refuses each one and publishes nothing.
func refuseHostileModules(ctx context.Context, t *testing.T, api registryAPI) {
	t.Helper()

	hostile := map[string][]byte{
		"path traversal": moduleZip(t, "x", map[string]string{"../../evil.tf": "x"}),
		"link outside":   moduleSymlinkEscape(t),
		"not an archive": []byte("module {}"),
	}

	for name, data := range hostile {
		resp := api.publishModule(ctx, t, "evil/aws/versions/1.0.0", data)
		if resp.Status != http.StatusUnprocessableEntity ||
			!strings.Contains(resp.Body, "registry.module-archive-refused") {
			t.Fatalf("publish of %s = %d %s, want 422 registry.module-archive-refused", name, resp.Status, resp.Body)
		}
	}

	if got := api.call(ctx, t, http.MethodGet, "/modules/evil/aws/versions", ""); got.Status != http.StatusNotFound {
		t.Fatalf("versions of a refused module = %d %s, want 404", got.Status, got.Body)
	}
}

// checkModuleInit proves that init downloaded both modules from the registry,
// and unpacked the files that the team published.
func checkModuleInit(env *e2e.Env, name, dir string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		for module, greeting := range map[string]string{"vpc": "from the zip file", "net": "from the tar.gz file"} {
			path := filepath.Join(dir, ".terraform", "modules", module, "main.tf")

			data, err := os.ReadFile(path) //nolint:gosec // The path is in the test project.
			if err != nil {
				return fmt.Errorf("read the unpacked module %s: %w (init output: %s)", module, err, r.Stdout)
			}

			if !strings.Contains(string(data), greeting) {
				return fmt.Errorf("%w: module %s holds %q, want %q", errUnexpected, module, data, greeting)
			}
		}

		for _, path := range []string{
			"/v1/modules/" + e2e.Namespace + "/vpc/aws/versions",
			"/v1/modules/" + e2e.Namespace + "/vpc/aws/1.0.0/download",
			"/v1/modules/" + e2e.Namespace + "/net/aws/versions",
			"/v1/modules/" + e2e.Namespace + "/net/aws/2.1.0/download",
		} {
			if !env.Requested(name, http.MethodGet, path) {
				return fmt.Errorf("%w: init never asked for %s", errUnexpected, path)
			}
		}

		return nil
	}
}
