package registry_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	apiregistry "opendustpan.org/opendustpan/internal/api/registry"
	"opendustpan.org/opendustpan/pkg/audit"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry/pgpverify"
	"opendustpan.org/opendustpan/pkg/registry/releasecheck"
	"opendustpan.org/opendustpan/pkg/registry/service"
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

const maxUpload = 4096

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

func (a testAuthz) Authorize(_ context.Context, p identity.Principal, action identity.Action, obj identity.Object) error {
	if slices.Contains(grantsOf(p.Subject), action) && !a.deny[action] {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (testAuthz) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

type fixture struct {
	handler *apiregistry.Handler
	store   *sqlite.Store
}

func newFixture(t *testing.T, deny ...identity.Action) fixture {
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

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Region: "test-region"})
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

	t.Cleanup(func() { _ = blobs.Close() })

	verifier := pgpverify.Verifier{Now: nil}

	svc, err := service.New(service.Config{
		Store: store.Registry(), Keys: store.RegistryKeys(), Verifier: verifier, Inspector: verifier,
		Blobs: blobs, Namespaces: store, Now: nil, MaxArchiveBytes: maxUpload, MaxReleaseBytes: 8 * maxUpload,
	})
	if err != nil {
		t.Fatalf("registry service: %v", err)
	}

	denied := map[identity.Action]bool{}
	for _, a := range deny {
		denied[a] = true
	}

	authz := testAuthz{deny: denied}

	idem, err := idempotent.New(idempotent.Config{
		Store:  store.Idempotency(idempotencytest.TestWrapper(t, 1)),
		Logger: slog.New(slog.DiscardHandler), Now: nil, MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("idempotency middleware: %v", err)
	}

	handler, err := apiregistry.NewHandler(apiregistry.Config{
		Service: svc, Authenticator: testAuthn{}, Authorizer: authz, MaxUploadBytes: maxUpload, Audit: store,
		Namespaces: nil, OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil, Idempotency: idem,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return fixture{handler: handler, store: store}
}

const base = "/api/dustpan/v1/namespaces/acme/registry"

type request struct {
	token, method, target string
	body                  []byte
	headers               map[string]string
}

type response struct {
	code int
	body string
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

	for k, v := range r.headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return response{code: rec.Code, body: rec.Body.String()}
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

// signer and the archive set follow the layout of a build tool.
type signer struct {
	entity *openpgp.Entity
	armor  string
	fpr    string
}

func newSigner(t *testing.T) signer {
	t.Helper()

	e, err := openpgp.NewEntity("Release", "", "release@example.invalid", &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA})
	if err != nil {
		t.Fatalf("new entity: %v", err)
	}

	var buf bytes.Buffer

	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatalf("armor: %v", err)
	}

	if err = e.Serialize(w); err != nil {
		t.Fatalf("serialize: %v", err)
	}

	_ = w.Close()

	return signer{entity: e, armor: buf.String(), fpr: strings.ToUpper(hex.EncodeToString(e.PrimaryKey.Fingerprint))}
}

type archiveSet struct {
	sums, sig []byte
	archive   map[string][]byte
	names     []string
}

func buildSet(t *testing.T, sg signer, name, version string) archiveSet {
	t.Helper()

	s := archiveSet{archive: map[string][]byte{}}

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

	var sig bytes.Buffer
	if err := openpgp.DetachSign(&sig, sg.entity, bytes.NewReader(s.sums), nil); err != nil {
		t.Fatalf("sign: %v", err)
	}

	s.sig = sig.Bytes()

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
func (f fixture) trust(t *testing.T, ns string, sg signer) {
	t.Helper()

	got := f.call(t, "admin", http.MethodPost, "/api/dustpan/v1/namespaces/"+ns+"/registry/keys",
		js(t, map[string]string{"armoredPublicKey": sg.armor}))
	wantCode(t, "register the key", got, http.StatusCreated)
}

// stage uploads data and returns the token.
func (f fixture) stage(t *testing.T, ns string, data []byte) string {
	t.Helper()

	got := f.call(t, "admin", http.MethodPost, "/api/dustpan/v1/namespaces/"+ns+"/registry/uploads", data)
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

func (f fixture) publishBody(t *testing.T, ns, name, version string, s archiveSet) []byte {
	t.Helper()

	files := []map[string]string{
		{"role": "sums", "name": releasecheck.SumsName(name, version), "upload": f.stage(t, ns, s.sums)},
		{"role": "signature", "name": releasecheck.SignatureName(name, version), "upload": f.stage(t, ns, s.sig)},
	}

	for _, n := range s.names {
		files = append(files, map[string]string{"role": "archive", "name": n, "upload": f.stage(t, ns, s.archive[n])})
	}

	return js(t, map[string]any{"protocols": []string{"5.0"}, "files": files})
}

func TestPublishFlow(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sg := newSigner(t)
	f.trust(t, "acme", sg)

	s := buildSet(t, sg, "payments", "1.0.0")
	body := f.publishBody(t, "acme", "payments", "1.0.0", s)
	target := base + "/providers/payments/versions/1.0.0"

	got := f.do(t, request{token: "publisher", method: http.MethodPut, target: target, body: body,
		headers: map[string]string{"Idempotency-Key": "publish-1"}})
	wantCode(t, "publish", got, http.StatusOK)

	var doc struct {
		Version        string   `json:"version"`
		Protocols      []string `json:"protocols"`
		KeyFingerprint string   `json:"keyFingerprint"`
		PublishedBy    string   `json:"publishedBy"`
		Files          []struct {
			Role     string `json:"role"`
			Platform string `json:"platform"`
		} `json:"files"`
	}

	if err := json.Unmarshal([]byte(got.body), &doc); err != nil || doc.Version != "1.0.0" ||
		doc.KeyFingerprint != sg.fpr || doc.PublishedBy != "publisher" || len(doc.Files) != 4 {
		t.Fatalf("publish answer = %q, %v", got.body, err)
	}

	// The same key and the same body replay the first answer.
	again := f.do(t, request{token: "publisher", method: http.MethodPut, target: target, body: body,
		headers: map[string]string{"Idempotency-Key": "publish-1"}})
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

	var actions []string
	for i := range entries.Entries {
		actions = append(actions, entries.Entries[i].Action)
	}

	slices.Sort(actions)

	if !slices.Equal(actions, []string{"registry:manage-keys", "registry:publish"}) {
		t.Errorf("audit actions = %v", actions)
	}
}

// TestPublishRefusals sends hostile requests straight to the handler.
func TestPublishRefusals(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sg := newSigner(t)
	f.trust(t, "acme", sg)

	good := buildSet(t, sg, "payments", "1.0.0")

	tampered := buildSet(t, sg, "payments", "1.0.0")
	tampered.sums = append(tampered.sums, '\n') // changed after the signature

	stranger := buildSet(t, newSigner(t), "payments", "1.0.0")

	swapped := buildSet(t, sg, "payments", "1.0.0")
	swapped.archive[swapped.names[0]] = []byte("other bytes")

	tests := []struct {
		name   string
		target string
		set    archiveSet
		want   int
		code   string
		text   string
	}{
		{"bad signature", base + "/providers/payments/versions/1.0.0", tampered, http.StatusUnprocessableEntity,
			"registry.signature-invalid", "SHA256SUMS.sig"},
		{"signature by an unknown key", base + "/providers/payments/versions/1.0.0", stranger,
			http.StatusUnprocessableEntity, "registry.signature-untrusted-key", "SHA256SUMS.sig"},
		{"archive that is not the signed archive", base + "/providers/payments/versions/1.0.0", swapped,
			http.StatusUnprocessableEntity, "registry.archive-set-refused", "linux_amd64.zip"},
		{"name outside the safe subset", base + "/providers/Payments/versions/1.0.0", good, http.StatusBadRequest,
			"registry.invalid", ""},
		{"name with an underscore", base + "/providers/pay_ments/versions/1.0.0", good, http.StatusBadRequest,
			"registry.invalid", ""},
		{"version that is not a version", base + "/providers/payments/versions/latest", good, http.StatusBadRequest,
			"registry.invalid", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := f.publishBody(t, "acme", "payments", "1.0.0", tc.set)

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
	other := newSigner(t)
	f.trust(t, "beta", other)
	f.trust(t, "acme", newSigner(t))

	body := f.publishBody(t, "acme", "payments", "1.0.0", buildSet(t, other, "payments", "1.0.0"))

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

	// A route that takes no key refuses the header and does not ignore it.
	got = f.do(t, request{token: "publisher", method: http.MethodPost, target: base + "/uploads", body: []byte("x"),
		headers: map[string]string{"Idempotency-Key": "k"}})
	if got.code != http.StatusBadRequest || !strings.Contains(got.body, "idempotency-key-unsupported") {
		t.Errorf("upload with a key: %d %s", got.code, got.body)
	}

	wantCode(t, "unknown namespace", f.call(t, "publisher", http.MethodPost,
		"/api/dustpan/v1/namespaces/gamma/registry/uploads", []byte("x")), http.StatusNotFound)
}

func TestKeyRoutes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sg := newSigner(t)

	for name, body := range map[string][]byte{
		"garbage":          js(t, map[string]string{"armoredPublicKey": "not a key"}),
		"an unknown field": js(t, map[string]string{"armoredPublicKey": sg.armor, "fingerprint": strings.Repeat("A", 40)}),
		"not json":         []byte("armor"),
	} {
		got := f.call(t, "keyholder", http.MethodPost, base+"/keys", body)
		wantCode(t, name, got, http.StatusBadRequest)
	}

	wantCode(t, "register", f.call(t, "keyholder", http.MethodPost, base+"/keys",
		js(t, map[string]string{"armoredPublicKey": sg.armor})), http.StatusCreated)

	list := f.call(t, "keyholder", http.MethodGet, base+"/keys", nil)
	if list.code != http.StatusOK || !strings.Contains(list.body, sg.fpr) {
		t.Fatalf("list = %d %s", list.code, list.body)
	}

	wantCode(t, "a reader may not list keys", f.call(t, "reader", http.MethodGet, base+"/keys", nil), http.StatusForbidden)
	wantCode(t, "remove a bad fingerprint", f.call(t, "keyholder", http.MethodDelete, base+"/keys/..%2F..", nil),
		http.StatusBadRequest)
	wantCode(t, "remove", f.call(t, "keyholder", http.MethodDelete, base+"/keys/"+sg.fpr, nil), http.StatusNoContent)
	wantCode(t, "remove again", f.call(t, "keyholder", http.MethodDelete, base+"/keys/"+sg.fpr, nil), http.StatusNotFound)
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
func routeTarget(route apiregistry.Route) string {
	return strings.NewReplacer(
		"{namespace}", "acme", "{name}", "payments", "{version}", "1.0.0", "{fingerprint}", strings.Repeat("A", 40),
	).Replace(route.Path)
}

func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	byPattern := map[string]apiregistry.Route{}

	for _, route := range f.handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		if route.Scope != apiregistry.ScopeAuthorized || len(route.Actions) == 0 || route.Resolve == nil {
			t.Fatalf("route %q must be authorized with actions and a resolver", pattern)
		}

		if route.IdempotencyKey != (route.Answer != nil) {
			t.Errorf("route %q: takes the key = %t, has an answer = %t", pattern, route.IdempotencyKey, route.Answer != nil)
		}

		if route.Method == http.MethodGet && route.IdempotencyKey {
			t.Errorf("route %q reads and takes a key", pattern)
		}
	}

	for _, pattern := range f.handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("served route %q has no declared action", pattern)
		}
	}

	for pattern, route := range byPattern {
		// A request without a token fails closed with 401, never 404.
		wantCode(t, "route "+pattern+" without a token", f.call(t, "", route.Method, routeTarget(route), nil),
			http.StatusUnauthorized)
	}
}

func TestRoutesEnforceDeclaredActions(t *testing.T) {
	t.Parallel()

	for _, route := range newFixture(t).handler.Routes() {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, route.Actions...)

			got := f.call(t, "admin", route.Method, routeTarget(route), []byte("{}"))
			wantCode(t, fmt.Sprintf("with %v denied", route.Actions), got, http.StatusForbidden)
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
