package registry_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	apiregistry "opendustpan.org/opendustpan/internal/api/registry"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/pagetoken"
	"opendustpan.org/opendustpan/internal/registry/pgpfixture"
	"opendustpan.org/opendustpan/internal/registry/releasecheck"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/audit"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/pgpverify"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

const (
	read   = identity.ActionRegistryRead
	pub    = identity.ActionRegistryPublish
	manage = identity.ActionRegistryManageKeys
)

const (
	maxUpload       = 4096
	providerName    = "payments"
	providerVersion = "1.0.0"
)

// grantsOf returns the actions of a subject. The token of a subject is its name.
func grantsOf(subject string) []identity.Action {
	switch subject {
	case "admin":
		return []identity.Action{read, pub, manage}
	case "reader":
		return []identity.Action{read}
	case "publisher":
		return []identity.Action{pub}
	case "keyholder":
		return []identity.Action{manage}
	default:
		return nil
	}
}

type testAuthn struct{}

func (testAuthn) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if grantsOf(token) == nil {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject: token, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: "", JobID: "",
	}, nil
}

// testAuthz allows the actions of the subject, minus the denied ones.
type testAuthz struct{ deny map[identity.Action]bool }

func (a testAuthz) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if slices.Contains(grantsOf(p.Subject), action) && !a.deny[action] {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (testAuthz) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// countingRegistryStore wraps the registry store of the fixture and counts
// the calls of the address list, so a test can prove that a hostile page
// token never reaches the store.
type countingRegistryStore struct {
	registry.Store

	addresses atomic.Int64
}

// Addresses counts the call and passes it to the wrapped store.
func (c *countingRegistryStore) Addresses(
	ctx context.Context, ns namespace.Name, kind registry.Kind, after string, limit int,
) ([]registry.Address, string, error) {
	c.addresses.Add(1)

	return c.Store.Addresses(ctx, ns, kind, after, limit)
}

type fixture struct {
	handler *apiregistry.Handler
	// served is the handler that the tests call: it fails a test when a
	// handler answers a success status that the description does not give.
	served http.Handler
	store  *sqlite.Store
	// service is the registry service that the handler uses, for a test that
	// compares a management answer with the rule of the registry protocol.
	service *service.Service
	// registry counts the calls that reach the address list of the store.
	registry *countingRegistryStore
}

func newFixture(t *testing.T, deny ...identity.Action) fixture {
	t.Helper()

	return newFixtureWithAuthorizer(t, nil, deny...)
}

// newFixtureWithAuthorizer is newFixture with authz in place of the test
// authorizer. A nil authz keeps the test authorizer.
func newFixtureWithAuthorizer(t *testing.T, override identity.Authorizer, deny ...identity.Action) fixture {
	t.Helper()

	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	for _, name := range []namespace.Name{"acme", "beta"} {
		if _, err = namespaces.Create(t.Context(), name); err != nil {
			t.Fatalf("create namespace: %v", err)
		}
	}

	blobs, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blobs: %v", err)
	}

	t.Cleanup(func() {
		_ = blobs.Close() //nolint:errcheck // The test directory is removed after the test.
	})

	verifier := pgpverify.Verifier{Now: nil}
	registryStore := &countingRegistryStore{Store: store.Registry(), addresses: atomic.Int64{}}

	svc, err := service.New(service.Config{
		Store:                 registryStore,
		Keys:                  store.RegistryKeys(),
		Verifier:              verifier,
		Inspector:             verifier,
		Blobs:                 blobs,
		Namespaces:            store,
		Audit:                 store,
		Logger:                slog.New(slog.DiscardHandler),
		OnAuditFailure:        func() {},
		Now:                   nil,
		MaxArchiveBytes:       maxUpload,
		MaxReleaseBytes:       8 * maxUpload,
		MaxModuleArchiveBytes: 0,
	})
	if err != nil {
		t.Fatalf("registry service: %v", err)
	}

	denied := map[identity.Action]bool{}
	for _, a := range deny {
		denied[a] = true
	}

	var authz identity.Authorizer = testAuthz{deny: denied}
	if override != nil {
		authz = override
	}

	idem, err := idempotent.New(idempotent.Config{
		Store:  store.Idempotency(idempotencytest.TestWrapper(t, 1)),
		Logger: slog.New(slog.DiscardHandler), Now: nil, MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("idempotency middleware: %v", err)
	}

	handler, err := apiregistry.NewHandler(apiregistry.Config{
		Service: svc, Authenticator: testAuthn{}, Authorizer: authz, MaxUploadBytes: maxUpload, Audit: store,
		Namespaces: namespaces, OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil, Idempotency: idem,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return fixture{
		handler:  handler,
		served:   apidesctest.CheckStatus(t, handler),
		store:    store,
		service:  svc,
		registry: registryStore,
	}
}

const base = "/api/dustpan/v1/namespaces/acme/registry"

type request struct {
	token, method, target string
	body                  []byte
	headers               map[string]string
}

type response struct {
	code   int
	body   string
	header http.Header
}

func (f fixture) do(t *testing.T, r request) response {
	t.Helper()

	var reader io.Reader = http.NoBody
	if r.body != nil {
		reader = bytes.NewReader(r.body)
	}

	req := httptest.NewRequestWithContext(t.Context(), r.method, r.target, reader)
	if r.token != "" {
		req.Header.Set("Authorization", "token "+r.token)
	}

	// A client sends the bytes of an upload as a binary body and every other
	// body as JSON. A case that tests the media type sets the header itself.
	if strings.HasSuffix(r.target, "/uploads") {
		req.Header.Set("Content-Type", "application/octet-stream")
	}

	for k, v := range r.headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	f.served.ServeHTTP(rec, req)

	return response{code: rec.Code, body: rec.Body.String(), header: rec.Header()}
}

func (f fixture) call(t *testing.T, token, method, target string, body []byte) response {
	t.Helper()

	return f.do(t, request{token: token, method: method, target: target, body: body, headers: nil})
}

func wantCode(t *testing.T, what string, got response, want int) {
	t.Helper()

	if got.code != want {
		t.Errorf("%s: %d %s, want %d", what, got.code, got.body, want)
	}
}

// The archive set follows the layout of a build tool.
type archiveSet struct {
	sums, sig []byte
	archive   map[string][]byte
	names     []string
}

func buildSet(t *testing.T, sg pgpfixture.Signer) archiveSet {
	t.Helper()

	return buildSetFor(t, sg, providerName)
}

// buildSetFor builds the archive set of version 1.0.0 of the provider name.
func buildSetFor(t *testing.T, sg pgpfixture.Signer, name string) archiveSet {
	t.Helper()

	return buildSetVersion(t, sg, name, providerVersion)
}

// buildSetVersion builds the archive set of version of the provider name.
func buildSetVersion(t *testing.T, sg pgpfixture.Signer, name, version string) archiveSet {
	t.Helper()

	s := archiveSet{sums: nil, sig: nil, archive: map[string][]byte{}, names: nil}

	var sums strings.Builder

	for _, p := range []string{"linux_amd64", "darwin_arm64"} {
		file := fmt.Sprintf("terraform-provider-%s_%s_%s.zip", name, version, p)
		data := []byte("zip bytes of " + file)
		sum := sha256.Sum256(data)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), file)

		s.archive[file] = data
		s.names = append(s.names, file)
	}

	s.sums = []byte(sums.String())

	s.sig = sg.Detach(t, s.sums)

	return s
}

func js(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return b
}

// trust registers sg in ns through the API.
func (f fixture) trust(t *testing.T, ns string, sg pgpfixture.Signer) {
	t.Helper()

	got := f.call(t, "admin", http.MethodPost, "/api/dustpan/v1/namespaces/"+ns+"/registry/keys",
		js(t, map[string]string{"armoredPublicKey": string(sg.Key.Armored)}))
	wantCode(t, "register the key", got, http.StatusCreated)
}

// stage uploads data and returns the token.
func (f fixture) stage(t *testing.T, data []byte) string {
	t.Helper()

	got := f.call(t, "admin", http.MethodPost, base+"/uploads", data)
	wantCode(t, "stage", got, http.StatusCreated)

	var doc struct {
		Upload string `json:"upload"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}

	if err := json.Unmarshal([]byte(got.body), &doc); err != nil || doc.Upload == "" {
		t.Fatalf("stage answer = %q, %v", got.body, err)
	}

	sum := sha256.Sum256(data)
	if doc.SHA256 != hex.EncodeToString(sum[:]) || doc.Size != int64(len(data)) {
		t.Fatalf("stage answer = %+v, want the server's own hash and size", doc)
	}

	return doc.Upload
}

func (f fixture) publishBody(t *testing.T, s archiveSet) []byte {
	t.Helper()

	return f.publishBodyFor(t, s, providerName)
}

// publishBodyFor makes the publish body of s for the provider name.
func (f fixture) publishBodyFor(t *testing.T, s archiveSet, name string) []byte {
	t.Helper()

	return f.publishBodyVersion(t, s, name, providerVersion)
}

// publishBodyVersion makes the publish body of s for the provider name and
// version.
func (f fixture) publishBodyVersion(t *testing.T, s archiveSet, name, version string) []byte {
	t.Helper()

	files := make([]map[string]string, 0, 2+len(s.names))
	files = append(
		files,
		map[string]string{
			"role":   "sums",
			"name":   releasecheck.SumsName(name, version),
			"upload": f.stage(t, s.sums),
		},
		map[string]string{
			"role":   "signature",
			"name":   releasecheck.SignatureName(name, version),
			"upload": f.stage(t, s.sig),
		},
	)

	for _, n := range s.names {
		files = append(files, map[string]string{
			"role": "archive", "name": n, "upload": f.stage(t, s.archive[n]),
		})
	}

	return js(t, map[string]any{"protocols": []string{"5.0"}, "files": files})
}

// publishedWire is the part of a publish answer that the tests read.
type publishedWire struct {
	Version        string          `json:"version"`
	Protocols      []string        `json:"protocols"`
	KeyFingerprint string          `json:"keyFingerprint"`
	PublishedBy    string          `json:"publishedBy"`
	Files          []publishedFile `json:"files"`
	Served         bool            `json:"served"`
}

type publishedFile struct {
	Role     string `json:"role"`
	Platform string `json:"platform"`
}

func TestPublishFlow(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sg := pgpfixture.NewSigner(t)
	f.trust(t, "acme", sg)

	s := buildSet(t, sg)
	body := f.publishBody(t, s)
	target := base + "/providers/payments/versions/1.0.0"

	got := f.do(t, request{
		token: "publisher", method: http.MethodPut, target: target, body: body,
		headers: map[string]string{"Idempotency-Key": "publish-1"},
	})
	wantCode(t, "publish", got, http.StatusOK)

	var doc publishedWire

	if err := json.Unmarshal([]byte(got.body), &doc); err != nil || doc.Version != "1.0.0" ||
		doc.KeyFingerprint != sg.Key.Fingerprint || doc.PublishedBy != "publisher" || len(doc.Files) != 4 {
		t.Fatalf("publish answer = %q, %v", got.body, err)
	}

	// The same key and the same body replay the first answer.
	again := f.do(t, request{
		token: "publisher", method: http.MethodPut, target: target, body: body,
		headers: map[string]string{"Idempotency-Key": "publish-1"},
	})
	if again.code != http.StatusOK || again.body != got.body {
		t.Errorf("replay = %d %s, want the first answer", again.code, again.body)
	}

	// A reader lists the release.
	list := f.call(t, "reader", http.MethodGet, base+"/providers/payments/versions", nil)
	wantCode(t, "list", list, http.StatusOK)

	if !strings.Contains(list.body, `"version":"1.0.0"`) {
		t.Errorf("list = %s", list.body)
	}

	wantCode(t, "get", f.call(t, "reader", http.MethodGet, target, nil), http.StatusOK)

	// The audit log of the namespace holds the key and the publish, once each.
	entries, err := f.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: 100})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	actions := make([]string, 0, len(entries.Entries))
	for i := range entries.Entries {
		actions = append(actions, entries.Entries[i].Action)
	}

	slices.Sort(actions)

	if !slices.Equal(actions, []string{"registry:manage-keys", "registry:publish"}) {
		t.Errorf("audit actions = %v", actions)
	}
}

// TestPublishAnswerServed proves that the answer of a publish says that the
// release is served: a publish passed the check of a trusted signing key.
func TestPublishAnswerServed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sg := pgpfixture.NewSigner(t)
	f.trust(t, "acme", sg)

	got := f.do(t, request{
		token: "publisher", method: http.MethodPut, target: base + "/providers/payments/versions/1.0.0",
		body: f.publishBody(t, buildSet(t, sg)), headers: map[string]string{"Idempotency-Key": "publish-served"},
	})
	wantCode(t, "publish", got, http.StatusOK)

	var doc publishedWire
	if err := json.Unmarshal([]byte(got.body), &doc); err != nil {
		t.Fatalf("publish answer = %q, %v", got.body, err)
	}

	if !doc.Served {
		t.Fatalf("publish answer = %q, want served true", got.body)
	}
}

// servedRelease is the part of a release answer that the served tests read.
type servedRelease struct {
	Version string `json:"version"`
	Served  bool   `json:"served"`
}

// servedPage is the list answer of the served test.
type servedPage struct {
	Releases []servedRelease `json:"releases"`
}

// publishVersion publishes version of the provider, signed by sg.
func (f fixture) publishVersion(t *testing.T, sg pgpfixture.Signer, version string) {
	t.Helper()

	body := f.publishBodyVersion(t, buildSetVersion(t, sg, providerName, version), providerName, version)
	wantCode(t, "publish "+version, f.call(t, "admin", http.MethodPut,
		base+"/providers/"+providerName+"/versions/"+version, body), http.StatusOK)
}

// listServed returns the served flag of each release of the provider, by
// version.
func (f fixture) listServed(t *testing.T) map[string]bool {
	t.Helper()

	list := f.call(t, "reader", http.MethodGet, base+"/providers/"+providerName+"/versions", nil)
	wantCode(t, "list", list, http.StatusOK)

	var page servedPage
	if err := json.Unmarshal([]byte(list.body), &page); err != nil {
		t.Fatalf("list answer = %q, %v", list.body, err)
	}

	out := make(map[string]bool, len(page.Releases))
	for _, rel := range page.Releases {
		out[rel.Version] = rel.Served
	}

	return out
}

// protocolServed returns the versions of the provider that the registry
// protocol serves.
func (f fixture) protocolServed(t *testing.T) map[string]bool {
	t.Helper()

	rels, err := f.service.ServableReleases(t.Context(), "acme", providerName)
	if err != nil && !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("ServableReleases: %v", err)
	}

	out := make(map[string]bool, len(rels))
	for i := range rels {
		out[rels[i].Version] = true
	}

	return out
}

// sameServed reports whether two maps agree on the served flag of each version.
// A version that a map does not hold is not served.
func sameServed(a, b map[string]bool) bool {
	for version, served := range a {
		if b[version] != served {
			return false
		}
	}

	for version, served := range b {
		if a[version] != served {
			return false
		}
	}

	return true
}

// TestListReleasesServed proves that the management versions list keeps a
// release whose signing key was removed, marks it not served, and marks the
// releases that the registry protocol serves. Each flag agrees with the list
// that the protocol serves.
func TestListReleasesServed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remove     []string
		wantServed map[string]bool
	}{
		{"the keys are trusted", nil, map[string]bool{"1.0.0": true, "2.0.0": true}},
		{"the key of the later release is removed", []string{"current"}, map[string]bool{
			"1.0.0": true, "2.0.0": false,
		}},
		{"every key is removed", []string{"old", "current"}, map[string]bool{
			"1.0.0": false, "2.0.0": false,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			old, current := pgpfixture.NewSigner(t), pgpfixture.NewSigner(t)
			keys := map[string]pgpfixture.Signer{"old": old, "current": current}

			f.trust(t, "acme", old)
			f.trust(t, "acme", current)
			f.publishVersion(t, old, "1.0.0")
			f.publishVersion(t, current, "2.0.0")

			for _, label := range tt.remove {
				wantCode(t, "remove the "+label+" key", f.call(t, "admin", http.MethodDelete,
					base+"/keys/"+keys[label].Key.Fingerprint, nil), http.StatusNoContent)
			}

			served := f.listServed(t)
			if !maps.Equal(served, tt.wantServed) {
				t.Fatalf("served = %v, want %v", served, tt.wantServed)
			}

			// The list of the protocol is the rule: a version is served
			// exactly when the protocol offers its download.
			if protocol := f.protocolServed(t); !sameServed(served, protocol) {
				t.Errorf("served = %v, but the protocol serves %v", served, protocol)
			}
		})
	}
}

// TestGetReleaseServed proves that the management read of one release marks a
// release that the registry protocol serves as served, and a release whose
// signing key the namespace removed as not served.
func TestGetReleaseServed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remove     []string
		version    string
		wantServed bool
	}{
		{"the keys are trusted", nil, "1.0.0", true},
		{"the key of another release is removed", []string{"current"}, "1.0.0", true},
		{"the key of the release is removed", []string{"old"}, "1.0.0", false},
		{"every key is removed", []string{"old", "current"}, "2.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			old, current := pgpfixture.NewSigner(t), pgpfixture.NewSigner(t)
			keys := map[string]pgpfixture.Signer{"old": old, "current": current}

			f.trust(t, "acme", old)
			f.trust(t, "acme", current)
			f.publishVersion(t, old, "1.0.0")
			f.publishVersion(t, current, "2.0.0")

			for _, label := range tt.remove {
				wantCode(t, "remove the "+label+" key", f.call(t, "admin", http.MethodDelete,
					base+"/keys/"+keys[label].Key.Fingerprint, nil), http.StatusNoContent)
			}

			got := f.call(t, "reader", http.MethodGet,
				base+"/providers/"+providerName+"/versions/"+tt.version, nil)
			wantCode(t, "get the release", got, http.StatusOK)

			var rel servedRelease
			if err := json.Unmarshal([]byte(got.body), &rel); err != nil {
				t.Fatalf("release answer = %q, %v", got.body, err)
			}

			if rel.Version != tt.version || rel.Served != tt.wantServed {
				t.Fatalf("release = %+v, want version %s served %t", rel, tt.version, tt.wantServed)
			}

			// The list of the protocol is the rule: a version is served
			// exactly when the protocol offers its download.
			if protocol := f.protocolServed(t); rel.Served != protocol[tt.version] {
				t.Errorf("served = %t, but the protocol serves %t", rel.Served, protocol[tt.version])
			}
		})
	}
}

// TestPublishRefusals sends hostile requests straight to the handler.
func TestPublishRefusals(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sg := pgpfixture.NewSigner(t)
	f.trust(t, "acme", sg)

	good := buildSet(t, sg)

	tampered := buildSet(t, sg)
	tampered.sums = append(tampered.sums, '\n') // changed after the signature

	stranger := buildSet(t, pgpfixture.NewSigner(t))

	swapped := buildSet(t, sg)
	swapped.archive[swapped.names[0]] = []byte("other bytes")

	tests := []struct {
		name   string
		target string
		set    archiveSet
		want   int
		code   string
		text   string
	}{
		{
			"bad signature", base + "/providers/payments/versions/1.0.0", tampered, http.StatusUnprocessableEntity,
			"registry.signature-invalid", "SHA256SUMS.sig",
		},
		{
			"signature by an unknown key", base + "/providers/payments/versions/1.0.0", stranger,
			http.StatusUnprocessableEntity, "registry.signature-untrusted-key", "SHA256SUMS.sig",
		},
		{
			"archive that is not the signed archive", base + "/providers/payments/versions/1.0.0", swapped,
			http.StatusUnprocessableEntity, "registry.archive-set-refused", "linux_amd64.zip",
		},
		{
			"name outside the safe subset", base + "/providers/Payments/versions/1.0.0", good, http.StatusBadRequest,
			"registry.invalid", "",
		},
		{
			"name with an underscore", base + "/providers/pay_ments/versions/1.0.0", good, http.StatusBadRequest,
			"registry.invalid", "",
		},
		{
			"version that is not a version", base + "/providers/payments/versions/latest", good, http.StatusBadRequest,
			"registry.invalid", "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := f.publishBody(t, tc.set)

			got := f.call(t, "publisher", http.MethodPut, tc.target, body)
			if got.code != tc.want || !strings.Contains(got.body, tc.code) || !strings.Contains(got.body, tc.text) {
				t.Fatalf("publish = %d %s, want %d %s naming %q", got.code, got.body, tc.want, tc.code, tc.text)
			}

			// No key byte is in an error.
			if strings.Contains(got.body, "BEGIN PGP") {
				t.Fatalf("the error holds key bytes: %s", got.body)
			}

			list := f.call(t, "reader", http.MethodGet, base+"/providers/payments/versions", nil)
			wantCode(t, "list after a refusal", list, http.StatusNotFound)
		})
	}
}

func TestKeyOfAnotherNamespaceDoesNotSign(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	other := pgpfixture.NewSigner(t)
	f.trust(t, "beta", other)
	f.trust(t, "acme", pgpfixture.NewSigner(t))

	body := f.publishBody(t, buildSet(t, other))

	got := f.call(t, "publisher", http.MethodPut, base+"/providers/payments/versions/1.0.0", body)
	if got.code != http.StatusUnprocessableEntity || !strings.Contains(got.body, "registry.signature-untrusted-key") {
		t.Fatalf("publish = %d %s", got.code, got.body)
	}
}

func TestStageLimitsAndKeys(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	big := make([]byte, maxUpload+1)
	got := f.call(t, "publisher", http.MethodPost, base+"/uploads", big)
	wantCode(t, "a body over the limit", got, http.StatusRequestEntityTooLarge)

	got = f.call(t, "publisher", http.MethodPost, base+"/uploads", nil)
	wantCode(t, "an empty body", got, http.StatusBadRequest)

	// The body of an upload is binary. Any other media type, and a missing one,
	// is refused before the body is read.
	for name, header := range map[string]string{
		"JSON": "application/json", "a form": "application/x-www-form-urlencoded", "text": "text/plain",
		"a bad value": "application/octet-stream;;;=", "empty": "",
	} {
		got = f.do(t, request{
			token: "publisher", method: http.MethodPost, target: base + "/uploads", body: []byte("x"),
			headers: map[string]string{"Content-Type": header},
		})
		wantCode(t, "an upload as "+name, got, http.StatusUnsupportedMediaType)
	}

	// A route that takes no key refuses the header and does not ignore it.
	got = f.do(t, request{
		token: "publisher", method: http.MethodPost, target: base + "/uploads", body: []byte("x"),
		headers: map[string]string{"Idempotency-Key": "k"},
	})
	if got.code != http.StatusBadRequest || !strings.Contains(got.body, "idempotency-key-unsupported") {
		t.Errorf("upload with a key: %d %s", got.code, got.body)
	}

	wantCode(t, "unknown namespace", f.call(t, "publisher", http.MethodPost,
		"/api/dustpan/v1/namespaces/gamma/registry/uploads", []byte("x")), http.StatusNotFound)
}

func TestKeyRoutes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sg := pgpfixture.NewSigner(t)

	for name, body := range map[string][]byte{
		"garbage":          js(t, map[string]string{"armoredPublicKey": "not a key"}),
		"an unknown field": js(t, map[string]string{"armoredPublicKey": string(sg.Key.Armored), "fingerprint": strings.Repeat("A", 40)}),
		"not json":         []byte("armor"),
	} {
		got := f.call(t, "keyholder", http.MethodPost, base+"/keys", body)
		wantCode(t, name, got, http.StatusBadRequest)
	}

	wantCode(t, "register", f.call(t, "keyholder", http.MethodPost, base+"/keys",
		js(t, map[string]string{"armoredPublicKey": string(sg.Key.Armored)})), http.StatusCreated)

	list := f.call(t, "keyholder", http.MethodGet, base+"/keys", nil)
	if list.code != http.StatusOK || !strings.Contains(list.body, sg.Key.Fingerprint) {
		t.Fatalf("list = %d %s", list.code, list.body)
	}

	wantCode(
		t,
		"a reader may not list keys",
		f.call(t, "reader", http.MethodGet, base+"/keys", nil),
		http.StatusForbidden,
	)
	wantCode(t, "remove a bad fingerprint", f.call(t, "keyholder", http.MethodDelete, base+"/keys/..%2F..", nil),
		http.StatusBadRequest)
	wantCode(
		t,
		"remove",
		f.call(t, "keyholder", http.MethodDelete, base+"/keys/"+sg.Key.Fingerprint, nil),
		http.StatusNoContent,
	)
	wantCode(
		t,
		"remove again",
		f.call(t, "keyholder", http.MethodDelete, base+"/keys/"+sg.Key.Fingerprint, nil),
		http.StatusNotFound,
	)
}

func TestAuthentication(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	wantCode(t, "no token", f.call(t, "", http.MethodGet, base+"/keys", nil), http.StatusUnauthorized)
	wantCode(t, "unknown token", f.call(t, "stranger", http.MethodGet, base+"/keys", nil), http.StatusUnauthorized)
	wantCode(t, "bad namespace", f.call(t, "reader", http.MethodGet,
		"/api/dustpan/v1/namespaces/bad%20name/registry/providers/a/versions", nil), http.StatusBadRequest)
	wantCode(t, "publish needs registry:publish", f.call(t, "reader", http.MethodPost, base+"/uploads", []byte("x")),
		http.StatusForbidden)
	wantCode(t, "read needs registry:read", f.call(t, "publisher", http.MethodGet,
		base+"/providers/payments/versions", nil), http.StatusForbidden)
}

// routeTarget fills the path values of a route.
func routeTarget(rt apiregistry.Route) string {
	return strings.NewReplacer(
		"{namespace}",
		"acme",
		"{name}",
		"payments",
		"{system}",
		"aws",
		"{version}",
		"1.0.0",
		"{fingerprint}",
		strings.Repeat("A", 40),
	).Replace(rt.Path)
}

func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	byPattern := map[string]apiregistry.Route{}

	for _, rt := range f.handler.Routes() {
		pattern := rt.Method + " " + rt.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = rt

		if rt.Scope != route.ScopeAuthorized || len(rt.Actions) == 0 || rt.Resolve == nil {
			t.Fatalf("route %q must be authorized with actions and a resolver", pattern)
		}

		if rt.IdempotencyKey != (rt.Answer != nil) {
			t.Errorf(
				"route %q: takes the key = %t, has an answer = %t",
				pattern,
				rt.IdempotencyKey,
				rt.Answer != nil,
			)
		}

		if rt.Method == http.MethodGet && rt.IdempotencyKey {
			t.Errorf("route %q reads and takes a key", pattern)
		}
	}

	for _, pattern := range f.handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("served route %q has no declared action", pattern)
		}
	}

	for pattern, rt := range byPattern {
		// A request without a token fails closed with 401, never 404.
		wantCode(t, "route "+pattern+" without a token", f.call(t, "", rt.Method, routeTarget(rt), nil),
			http.StatusUnauthorized)
	}
}

func TestRoutesEnforceDeclaredActions(t *testing.T) {
	t.Parallel()

	for _, rt := range newFixture(t).handler.Routes() {
		t.Run(rt.Method+" "+rt.Path, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, rt.Actions...)

			got := f.call(t, "admin", rt.Method, routeTarget(rt), []byte("{}"))
			wantCode(t, fmt.Sprintf("with %v denied", rt.Actions), got, http.StatusForbidden)
		})
	}
}

// TestNoMuxBypass proves every served route goes through the recorder.
func TestNoMuxBypass(t *testing.T) {
	t.Parallel()

	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "routes.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		content, readErr := packageSources.ReadFile(name)
		if readErr != nil {
			t.Fatalf("read file %s: %v", name, readErr)
		}

		for _, needle := range []string{"NewServeMux", "servemux"} {
			if strings.Contains(string(content), needle) {
				t.Fatalf("file %s reaches the mux without recording (contains %q)", name, needle)
			}
		}
	}
}

func TestNewHandlerNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	_, err := apiregistry.NewHandler(apiregistry.Config{
		Service: nil, Authenticator: nil, Authorizer: nil, MaxUploadBytes: 0, Audit: nil, Namespaces: nil,
		OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil, Idempotency: nil, Logger: nil,
	})
	if err == nil {
		t.Error("NewHandler accepted an empty config")
	}
}

// providerPage is the part of a provider list that the tests read.
type providerPage struct {
	Providers     []providerWire `json:"providers"`
	NextPageToken string         `json:"nextPageToken"`
}

type providerWire struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// providerToken is the wire shape of the provider page token. The tests build
// one to send a cursor that names no provider.
type providerToken struct {
	After string `json:"a"`
}

// providerTokenOf returns a page token that holds the cursor after.
func providerTokenOf(t *testing.T, after string) string {
	t.Helper()

	token, err := pagetoken.Encode(providerToken{After: after})
	if err != nil {
		t.Fatalf("encode a page token: %v", err)
	}

	return token
}

// pageNames returns the names of a provider page, and fails on a bad answer.
func pageNames(t *testing.T, got response) ([]string, string) {
	t.Helper()

	wantCode(t, "list providers", got, http.StatusOK)

	var page providerPage
	if err := json.Unmarshal([]byte(got.body), &page); err != nil {
		t.Fatalf("list providers answer = %q, %v", got.body, err)
	}

	if !strings.Contains(got.body, `"providers":[`) {
		t.Fatalf("providers must be an array, never null: %s", got.body)
	}

	names := make([]string, 0, len(page.Providers))
	for _, p := range page.Providers {
		if p.Namespace != "acme" {
			t.Errorf("provider %q is in namespace %q, want acme", p.Name, p.Namespace)
		}

		names = append(names, p.Name)
	}

	return names, page.NextPageToken
}

func TestListProviders(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sg := pgpfixture.NewSigner(t)
	f.trust(t, "acme", sg)

	// Published out of order: the list is ordered by name.
	want := []string{"dns", "payments", "vault"}

	for _, name := range []string{"vault", "payments", "dns"} {
		body := f.publishBodyFor(t, buildSetFor(t, sg, name), name)
		wantCode(t, "publish "+name, f.call(t, "admin", http.MethodPut,
			base+"/providers/"+name+"/versions/"+providerVersion, body), http.StatusOK)
	}

	t.Run("the pages of the list", func(t *testing.T) {
		t.Parallel()

		checkProviderPages(t, f, want)
	})

	t.Run("a cursor that names no provider", func(t *testing.T) {
		t.Parallel()

		checkProviderCursors(t, f)
	})

	t.Run("an empty namespace", func(t *testing.T) {
		t.Parallel()

		got := f.call(t, "admin", http.MethodGet,
			"/api/dustpan/v1/namespaces/beta/registry/providers", nil)
		wantCode(t, "list", got, http.StatusOK)

		if !strings.Contains(got.body, `"providers":[]`) || !strings.Contains(got.body, `"nextPageToken":""`) {
			t.Errorf("empty namespace answer = %s", got.body)
		}
	})

	t.Run("a namespace that does not exist", func(t *testing.T) {
		t.Parallel()

		wantCode(t, "list", f.call(t, "admin", http.MethodGet,
			"/api/dustpan/v1/namespaces/gamma/registry/providers", nil), http.StatusNotFound)
	})

	t.Run("a caller that cannot read", func(t *testing.T) {
		t.Parallel()

		checkProviderRefusals(t, f)
	})
}

// checkProviderPages reads one page and the whole list of the providers of
// acme. A page that holds every provider carries no token, also when the page
// size is an exact fit.
func checkProviderPages(t *testing.T, f fixture, want []string) {
	t.Helper()

	providers := base + "/providers"

	for _, tc := range []struct {
		name  string
		query string
	}{
		{"all on one page", ""},
		{"an exact fit has no token", "?pageSize=3"},
	} {
		names, token := pageNames(t, f.call(t, "reader", http.MethodGet, providers+tc.query, nil))
		if !slices.Equal(names, want) || token != "" {
			t.Errorf("%s: page = %v, token %q; want %v, no token", tc.name, names, token, want)
		}
	}

	if got := walkProviderPages(t, f, 1, len(want)); !slices.Equal(got, want) {
		t.Errorf("providers in page order = %v, want %v", got, want)
	}
}

// checkProviderCursors sends page tokens that name no provider.
func checkProviderCursors(t *testing.T, f fixture) {
	t.Helper()

	for _, tc := range []struct {
		name  string
		after string
		want  []string
	}{
		{"a cursor between two names", "e", []string{"payments", "vault"}},
		{"a cursor past the last name", "zzz", []string{}},
	} {
		names, token := pageNames(t, f.call(t, "reader", http.MethodGet,
			base+"/providers?pageToken="+url.QueryEscape(providerTokenOf(t, tc.after)), nil))
		if !slices.Equal(names, tc.want) || token != "" {
			t.Errorf("%s: page = %v, token %q; want %v, no token", tc.name, names, token, tc.want)
		}
	}
}

// walkProviderPages reads every page of the provider list with the page size,
// and returns the names in page order. It fails when the pages do not end
// after maxPages pages.
func walkProviderPages(t *testing.T, f fixture, pageSize, maxPages int) []string {
	t.Helper()

	var (
		got   []string
		token string
	)

	for pages := 0; ; pages++ {
		if pages > maxPages {
			t.Fatalf("the pages did not end after %d pages", pages)
		}

		target := base + "/providers?pageSize=" + strconv.Itoa(pageSize)
		if token != "" {
			target += "&pageToken=" + url.QueryEscape(token)
		}

		names, next := pageNames(t, f.call(t, "reader", http.MethodGet, target, nil))
		got = append(got, names...)

		if next == "" {
			return got
		}

		token = next
	}
}

// checkProviderRefusals proves that a caller without the read grant is
// refused, and that the refusal names no provider.
func checkProviderRefusals(t *testing.T, f fixture) {
	t.Helper()

	providers := base + "/providers"

	for _, subject := range []string{"publisher", "keyholder"} {
		got := f.call(t, subject, http.MethodGet, providers, nil)
		wantCode(t, subject, got, http.StatusForbidden)

		if strings.Contains(got.body, "payments") {
			t.Errorf("a refusal for %s names a provider: %s", subject, got.body)
		}
	}

	wantCode(t, "no token", f.call(t, "", http.MethodGet, providers, nil), http.StatusUnauthorized)
}

// TestListProvidersHostileQuery sends hostile page sizes and tokens straight to
// the handler. The token is untrusted input, so each value must answer 400
// and none may reach the store.
func TestListProvidersHostileQuery(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	enc := base64.RawURLEncoding.EncodeToString

	// The parallel rows share one fixture and one call counter. Put only rows
	// that make no store call in this table: a valid row would break the
	// zero-calls check of every other row.
	tests := []struct {
		name  string
		query string
	}{
		{"zero page size", "?pageSize=0"},
		{"negative page size", "?pageSize=-1"},
		{"page size over the maximum", "?pageSize=1001"},
		{"page size that is not a number", "?pageSize=ten"},
		{"huge page size", "?pageSize=99999999999999999999"},
		{"token that is not base64", "?pageToken=%25%25%25"},
		{"padded base64", "?pageToken=" + url.QueryEscape(base64.URLEncoding.EncodeToString([]byte("ab")))},
		{"token in the old raw form", "?pageToken=" + enc([]byte("payments"))},
		{"token that is not JSON", "?pageToken=" + enc([]byte("{"))},
		{"token with another field", "?pageToken=" + enc([]byte(`{"b":"payments"}`))},
		{"empty cursor", "?pageToken=" + url.QueryEscape(providerTokenOf(t, ""))},
		{"path traversal", "?pageToken=" + url.QueryEscape(providerTokenOf(t, "../../etc/passwd"))},
		{"separator in the name", "?pageToken=" + url.QueryEscape(providerTokenOf(t, "a/b"))},
		{"upper case name", "?pageToken=" + url.QueryEscape(providerTokenOf(t, "Payments"))},
		{"control characters", "?pageToken=" + url.QueryEscape(providerTokenOf(t, "a\r\nb"))},
		{"null byte", "?pageToken=" + url.QueryEscape(providerTokenOf(t, "a\x00b"))},
		{"SQL text", "?pageToken=" + url.QueryEscape(providerTokenOf(t, "x' OR '1'='1"))},
		{"name over the length limit", "?pageToken=" + url.QueryEscape(providerTokenOf(t, strings.Repeat("a", 300)))},
		{"token over the length limit", "?pageToken=" + strings.Repeat("A", 100000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := f.call(t, "reader", http.MethodGet, base+"/providers"+tt.query, nil)
			wantCode(t, tt.name, got, http.StatusBadRequest)

			if calls := f.registry.addresses.Load(); calls != 0 {
				t.Errorf("the store got %d address list calls, want none", calls)
			}

			if !strings.Contains(got.body, `"errorCode"`) {
				t.Errorf("answer has no error code: %s", got.body)
			}
		})
	}
}

// TestParseProviderTokenGuards proves each guard of the provider page token
// parser alone: each row sends a token that only one guard refuses, and
// asserts the cause of that guard. The last row proves that the guards accept
// a valid token.
func TestParseProviderTokenGuards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		raw   string
		cause error
		after string
	}{
		{
			"a token over the length limit",
			strings.Repeat("A", pagetoken.MaxLen+1),
			apiregistry.ErrProviderTokenLength,
			"",
		},
		{"a token that is not a page token", "!!!", apiregistry.ErrProviderTokenDecode, ""},
		{"a token with an empty cursor", providerTokenOf(t, ""), apiregistry.ErrProviderTokenEmpty, ""},
		{
			"a token whose cursor is no provider name",
			providerTokenOf(t, "Payments"),
			apiregistry.ErrProviderTokenAddress,
			"",
		},
		{"a token of a provider", providerTokenOf(t, "payments"), nil, "payments"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			after, err := apiregistry.ParseProviderPageToken(tt.raw, "acme")

			if tt.cause != nil {
				if !errors.Is(err, tt.cause) {
					t.Fatalf("parse %q = %q, %v, want %v", tt.raw, after, err, tt.cause)
				}

				return
			}

			if err != nil || after != tt.after {
				t.Fatalf("parse %q = %q, %v, want %q", tt.raw, after, err, tt.after)
			}
		})
	}
}

// TestProviderPageTokenLengthCap proves that a page token over the length limit
// answers 400 by its length alone. The cursor in the token is valid, and the
// base64 decoder ignores line breaks, so only the length refuses the token.
func TestProviderPageTokenLengthCap(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	token := strings.Repeat("\n", pagetoken.MaxLen) + providerTokenOf(t, "payments")
	got := f.call(t, "reader", http.MethodGet, base+"/providers?pageToken="+url.QueryEscape(token), nil)
	wantCode(t, "a valid cursor over the length limit", got, http.StatusBadRequest)
}

// TestPublishRepeatReplaysTheFirstRelease proves that the replay of a repeat
// publish answers what the live repeat answered: the first release, with the
// first publisher. A repeat of the same content changes nothing, so its stored
// answer must not name the caller of the repeat.
func TestPublishRepeatReplaysTheFirstRelease(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sg := pgpfixture.NewSigner(t)
	f.trust(t, "acme", sg)

	s := buildSet(t, sg)
	target := base + "/providers/payments/versions/1.0.0"

	first := f.do(t, request{
		token: "publisher", method: http.MethodPut, target: target, body: f.publishBody(t, s),
		headers: map[string]string{"Idempotency-Key": "first"},
	})
	wantCode(t, "publish", first, http.StatusOK)

	repeat := request{
		token: "admin", method: http.MethodPut, target: target, body: f.publishBody(t, s),
		headers: map[string]string{"Idempotency-Key": "repeat-1"},
	}

	live := f.do(t, repeat)
	wantCode(t, "repeat", live, http.StatusOK)

	var doc publishedWire
	if err := json.Unmarshal([]byte(live.body), &doc); err != nil || doc.PublishedBy != "publisher" {
		t.Fatalf("repeat answer = %q, %v, want the first publisher", live.body, err)
	}

	replay := f.do(t, repeat)
	if replay.code != http.StatusOK || replay.body != live.body {
		t.Fatalf("replay of the repeat = %d %s, want the live answer %s", replay.code, replay.body, live.body)
	}
}
