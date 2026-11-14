package service_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/registry/modulecheck"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// moduleFile is one file of a test archive. A link has its target in body.
type moduleFile struct {
	name string
	body string
	link bool
}

// moduleZip returns a zip archive of a module.
func moduleZip(t *testing.T, files []moduleFile) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	for _, f := range files {
		var h zip.FileHeader

		h.Name, h.Method = f.name, zip.Deflate
		h.SetMode(0o644)

		if f.link {
			h.SetMode(os.ModeSymlink | 0o777)
		}

		w, err := zw.CreateHeader(&h)
		if err != nil {
			t.Fatalf("zip: %v", err)
		}

		if _, err = w.Write([]byte(f.body)); err != nil {
			t.Fatalf("zip: %v", err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip: %v", err)
	}

	return buf.Bytes()
}

// moduleTarGz returns a tar.gz archive of a module with the file main.tf.
func moduleTarGz(t *testing.T, body string) []byte {
	t.Helper()

	var (
		buf bytes.Buffer
		h   tar.Header
	)

	h.Name, h.Mode, h.Size, h.Typeflag = "main.tf", 0o644, int64(len(body)), tar.TypeReg

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if err := tw.WriteHeader(&h); err != nil {
		t.Fatalf("tar: %v", err)
	}

	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatalf("tar: %v", err)
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("tar: %v", err)
	}

	if err := gz.Close(); err != nil {
		t.Fatalf("gzip: %v", err)
	}

	return buf.Bytes()
}

// plainZip returns a zip archive with the one file main.tf.
func plainZip(t *testing.T) []byte {
	t.Helper()

	return moduleZip(t, []moduleFile{{name: "main.tf", body: "x", link: false}})
}

// moduleAddr returns the address of the module vpc/aws in ns.
func moduleAddr(ns namespace.Name) registry.Address {
	return registry.Address{Namespace: ns, Kind: registry.KindModule, Name: "vpc", System: "aws"}
}

// publishModule stages data and publishes it as vpc/aws version v in acme.
func (e *env) publishModule(v string, data []byte) (registry.Release, error) {
	ctx := context.Background()

	st, err := e.svc.Stage(ctx, e.caller, acme, bytes.NewReader(data))
	if err != nil {
		return registry.Release{}, fmt.Errorf("stage: %w", err)
	}

	rel, err := e.svc.PublishModule(ctx, e.caller, service.ModulePublishRequest{
		Namespace: acme, Name: "vpc", System: "aws", Version: v, Upload: st.Token,
	})
	if err != nil {
		return registry.Release{}, fmt.Errorf("publish: %w", err)
	}

	return rel, nil
}

func TestPublishModuleThenRead(t *testing.T) {
	t.Parallel()

	linked := []moduleFile{{name: "main.tf", body: "x", link: false}, {name: "l", body: "main.tf", link: true}}

	tests := []struct {
		name string
		data []byte
		file string
	}{
		{"zip", moduleZip(t, linked), "vpc-aws-1.0.0.zip"},
		{"tar.gz", moduleTarGz(t, "x"), "vpc-aws-1.0.0.tar.gz"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, false)

			rel, err := e.publishModule("1.0.0", tc.data)
			if err != nil {
				t.Fatalf("PublishModule: %v", err)
			}

			if rel.Address.Kind != registry.KindModule || rel.PublishedBy != subject || len(rel.Archives) != 1 ||
				rel.Archives[0].FileName != tc.file || rel.Archives[0].Size != int64(len(tc.data)) ||
				rel.KeyFingerprint != "" {
				t.Fatalf("release = %+v", rel)
			}

			e.checkStored(t, rel, tc.data)
			e.checkModuleAudit(t, rel)
		})
	}
}

// checkStored proves that the staged file is gone, that one content key holds
// the archive, and that the archive opens through the release.
func (e *env) checkStored(t *testing.T, rel registry.Release, data []byte) {
	t.Helper()

	if got := e.listing(t, e.ids[acme], "staging"); len(got) != 0 {
		t.Fatalf("staged files left: %v", got)
	}

	if got := e.listing(t, e.ids[acme], "sha256"); len(got) != 1 {
		t.Fatalf("content keys = %v, want 1", got)
	}

	size, r, err := e.svc.OpenModuleFile(context.Background(), moduleAddr(acme), "1.0.0", rel.Archives[0].SHA256)
	if err != nil || size != int64(len(data)) || !bytes.Equal(readAll(t, r), data) {
		t.Fatalf("OpenModuleFile = %d, %v", size, err)
	}
}

// checkModuleAudit proves that the publish wrote one entry that names the
// module, the version and the hash of the archive.
func (e *env) checkModuleAudit(t *testing.T, rel registry.Release) {
	t.Helper()

	if len(e.store.audits) != 1 {
		t.Fatalf("%d audit entries, want 1", len(e.store.audits))
	}

	entry := e.store.audits[0]
	if entry.Action != string(identity.ActionRegistryPublish) || entry.Namespace != acme ||
		entry.Subject != subject || len(entry.Changes) != 2 ||
		entry.Changes[0].New != "module/vpc/aws@1.0.0" || entry.Changes[1].New != rel.Archives[0].SHA256 {
		t.Fatalf("audit entry = %+v", entry)
	}
}

func TestPublishModuleRepeatAndConflict(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	first := moduleTarGz(t, "one")

	for _, v := range []string{"1.10.0", "1.2.0"} {
		if _, err := e.publishModule(v, first); err != nil {
			t.Fatalf("PublishModule %s: %v", v, err)
		}
	}

	// The same bytes again are a repeat: no new audit entry.
	if _, err := e.publishModule("1.2.0", first); err != nil || len(e.store.audits) != 2 {
		t.Fatalf("repeat = %v, %d audit entries, want nil and 2", err, len(e.store.audits))
	}

	// Other bytes for a published version are refused.
	if _, err := e.publishModule("1.2.0", moduleTarGz(t, "two")); !errors.Is(err, registry.ErrVersionExists) {
		t.Fatalf("publish of other bytes = %v, want ErrVersionExists", err)
	}

	rels, err := e.svc.ModuleReleases(context.Background(), moduleAddr(acme))
	if err != nil || len(rels) != 2 || rels[0].Version != "1.2.0" || rels[1].Version != "1.10.0" {
		t.Fatalf("ModuleReleases = %+v, %v", rels, err)
	}
}

// TestPublishModuleVersionRows proves that a version that exists with other
// content is refused before a content key is written. The key count and the
// audit count show what each publish wrote.
func TestPublishModuleVersionRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		version    string
		data       string
		wantErr    error
		wantKeys   int
		wantAudits int
	}{
		{
			name:       "new version",
			version:    "1.1.0",
			data:       "two",
			wantErr:    nil,
			wantKeys:   1,
			wantAudits: 1,
		},
		{
			name:       "same version with the same content",
			version:    "1.0.0",
			data:       "one",
			wantErr:    nil,
			wantKeys:   0,
			wantAudits: 0,
		},
		{
			name:       "same version with other content",
			version:    "1.0.0",
			data:       "two",
			wantErr:    registry.ErrVersionExists,
			wantKeys:   0,
			wantAudits: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, false)

			if _, err := e.publishModule("1.0.0", moduleTarGz(t, "one")); err != nil {
				t.Fatalf("PublishModule: %v", err)
			}

			keysBefore := len(e.listing(t, e.ids[acme], "sha256"))
			auditsBefore := len(e.store.audits)

			_, err := e.publishModule(tc.version, moduleTarGz(t, tc.data))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("PublishModule = %v, want %v", err, tc.wantErr)
			}

			if got := len(e.listing(t, e.ids[acme], "sha256")) - keysBefore; got != tc.wantKeys {
				t.Fatalf("new content keys = %d, want %d", got, tc.wantKeys)
			}

			if got := len(e.store.audits) - auditsBefore; got != tc.wantAudits {
				t.Fatalf("new audit entries = %d, want %d", got, tc.wantAudits)
			}
		})
	}
}

func TestModulesAreNotProviders(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	ctx := context.Background()

	if _, err := e.publishModule("1.2.0", moduleTarGz(t, "one")); err != nil {
		t.Fatalf("PublishModule: %v", err)
	}

	addrs, next, err := e.svc.Modules(ctx, acme, "", registry.MaxAddressPage)
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}

	if !slices.Equal(addrs, []registry.Address{moduleAddr(acme)}) || next != "" {
		t.Fatalf("Modules = %+v, %q, want %+v, no cursor", addrs, next, moduleAddr(acme))
	}

	none, nextNone, err := e.svc.Modules(ctx, beta, "", registry.MaxAddressPage)
	if err != nil {
		t.Fatalf("Modules of another namespace: %v", err)
	}

	if len(none) != 0 || nextNone != "" {
		t.Fatalf("Modules of another namespace = %+v, %q, want none", none, nextNone)
	}

	if _, relErr := e.svc.Release(ctx, acme, "vpc", "1.2.0"); !errors.Is(relErr, registry.ErrNotFound) {
		t.Fatalf("Release of a module as a provider = %v, want ErrNotFound", relErr)
	}

	provs, nextProv, err := e.svc.Providers(ctx, acme, "", registry.MaxAddressPage)
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}

	if len(provs) != 0 || nextProv != "" {
		t.Fatalf("Providers = %v, %q, want none", provs, nextProv)
	}
}

// TestOpenModuleFileRefusesAProviderAddress proves that the module route does
// not open the file of an address of another kind.
func TestOpenModuleFileRefusesAProviderAddress(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	ctx := context.Background()

	if _, err := e.publishModule("1.2.0", moduleTarGz(t, "one")); err != nil {
		t.Fatalf("PublishModule: %v", err)
	}

	rel, err := e.svc.ModuleRelease(ctx, acme, "vpc", "aws", "1.2.0")
	if err != nil {
		t.Fatalf("ModuleRelease: %v", err)
	}

	asProvider := registry.Address{Namespace: acme, Kind: registry.KindProvider, Name: "vpc", System: ""}
	_, _, openErr := e.svc.OpenModuleFile(ctx, asProvider, "1.2.0", rel.Archives[0].SHA256)
	if !errors.Is(openErr, registry.ErrNotFound) {
		t.Fatalf("OpenModuleFile of a provider address = %v, want ErrNotFound", openErr)
	}
}

func TestPublishModuleRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{"path traversal", moduleZip(t, []moduleFile{{name: "../evil.tf", body: "x", link: false}})},
		{"link outside", moduleZip(t, []moduleFile{{name: "l", body: "../../etc/passwd", link: true}})},
		{"not an archive", []byte("module {}\n")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, false)

			if _, err := e.publishModule("1.0.0", tc.data); !errors.Is(err, modulecheck.ErrRefused) {
				t.Fatalf("PublishModule = %v, want ErrRefused", err)
			}

			e.checkNothingKept(t)
		})
	}
}

// checkNothingKept proves that a refused publish left no content key, no
// staged file, no audit entry and no release.
func (e *env) checkNothingKept(t *testing.T) {
	t.Helper()

	if got := e.listing(t, e.ids[acme], "sha256"); len(got) != 0 {
		t.Fatalf("content keys after a refusal = %v", got)
	}

	if got := e.listing(t, e.ids[acme], "staging"); len(got) != 0 {
		t.Fatalf("staged files after a refusal = %v", got)
	}

	if len(e.store.audits) != 0 {
		t.Fatalf("a refusal wrote %d audit entries", len(e.store.audits))
	}

	_, err := e.svc.ModuleRelease(context.Background(), acme, "vpc", "aws", "1.0.0")
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("ModuleRelease after a refusal = %v, want ErrNotFound", err)
	}
}

func TestPublishModuleTooLarge(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)

	// The test service limits a file to 1 MiB, so the staging refuses this body.
	big := make([]byte, 2<<20)
	for i := range big {
		big[i] = byte(i * 7 % 251)
	}

	_, err := e.svc.Stage(context.Background(), e.caller, acme, bytes.NewReader(big))
	if !errors.Is(err, service.ErrTooLarge) {
		t.Fatalf("Stage = %v, want ErrTooLarge", err)
	}
}

func TestPublishModuleRequestChecks(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	ctx := context.Background()

	st, err := e.svc.Stage(ctx, e.caller, acme, bytes.NewReader(plainZip(t)))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	good := service.ModulePublishRequest{
		Namespace: acme,
		Name:      "vpc",
		System:    "aws",
		Version:   "1.0.0",
		Upload:    st.Token,
	}
	never := strings.Repeat("a", 32)

	tests := []struct {
		name   string
		mutate func(*service.ModulePublishRequest)
		caller *service.Caller
		want   error
	}{
		{"name with a dot", func(r *service.ModulePublishRequest) { r.Name = "v.pc" }, e.caller, registry.ErrInvalid},
		{"name with a slash", func(r *service.ModulePublishRequest) { r.Name = "../x" }, e.caller, registry.ErrInvalid},
		{
			"system in capitals",
			func(r *service.ModulePublishRequest) { r.System = "AWS" },
			e.caller,
			registry.ErrInvalid,
		},
		{
			"system with a dash",
			func(r *service.ModulePublishRequest) { r.System = "a-b" },
			e.caller,
			registry.ErrInvalid,
		},
		{"no system", func(r *service.ModulePublishRequest) { r.System = "" }, e.caller, registry.ErrInvalid},
		{
			"version with metadata",
			func(r *service.ModulePublishRequest) { r.Version = "1.0.0+x" },
			e.caller,
			registry.ErrInvalid,
		},
		{
			"namespace with a capital",
			func(r *service.ModulePublishRequest) { r.Namespace = "Acme" },
			e.caller,
			registry.ErrInvalid,
		},
		{
			"token that is a path",
			func(r *service.ModulePublishRequest) { r.Upload = "../../sha256/x" },
			e.caller,
			service.ErrBadRequest,
		},
		{
			"token never staged",
			func(r *service.ModulePublishRequest) { r.Upload = never },
			e.caller,
			service.ErrNotStaged,
		},
		{
			"unknown namespace",
			func(r *service.ModulePublishRequest) { r.Namespace = "gamma" },
			e.caller,
			namespace.ErrNotFound,
		},
		{"no caller", func(*service.ModulePublishRequest) {}, nil, identity.ErrUnauthenticated},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := good
			tc.mutate(&req)

			if _, err := e.svc.PublishModule(ctx, tc.caller, req); !errors.Is(err, tc.want) {
				t.Fatalf("PublishModule = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestModuleReadsRefuseWhatIsNotPublished(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)

	rel, err := e.publishModule("1.0.0", moduleTarGz(t, "x"))
	if err != nil {
		t.Fatalf("PublishModule: %v", err)
	}

	sum := rel.Archives[0].SHA256
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{"unknown module", func() error {
			_, err := e.svc.ModuleReleases(
				ctx,
				registry.Address{Namespace: acme, Kind: registry.KindModule, Name: "net", System: "aws"},
			)

			return err
		}},
		{
			"unknown system",
			func() error { _, err := e.svc.ModuleRelease(ctx, acme, "vpc", "gcp", "1.0.0"); return err },
		},
		{
			"unknown version",
			func() error { _, err := e.svc.ModuleRelease(ctx, acme, "vpc", "aws", "2.0.0"); return err },
		},
		{
			"another namespace",
			func() error { _, err := e.svc.ModuleRelease(ctx, beta, "vpc", "aws", "1.0.0"); return err },
		},
		{"unknown namespace", func() error { _, err := e.svc.ModuleReleases(ctx, moduleAddr("gamma")); return err }},
		{"hash of no file", func() error {
			_, _, err := e.svc.OpenModuleFile(ctx, moduleAddr(acme), "1.0.0", strings.Repeat("0", 64))

			return err
		}},
		{"hash in another namespace", func() error {
			_, _, err := e.svc.OpenModuleFile(ctx, moduleAddr(beta), "1.0.0", sum)

			return err
		}},
		{"hash with another version", func() error {
			_, _, err := e.svc.OpenModuleFile(ctx, moduleAddr(acme), "1.0.1", sum)

			return err
		}},
	}

	for _, tc := range tests {
		if err := tc.call(); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("%s = %v, want ErrNotFound", tc.name, err)
		}
	}
}
