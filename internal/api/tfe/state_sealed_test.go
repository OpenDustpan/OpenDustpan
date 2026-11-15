package tfe_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" //nolint:gosec // The protocol under test names state by MD5 for integrity.
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// sealedTestWrapper wraps data keys with a fresh key-encryption key for one
// test, so the sealed adapter tests seal state without test credentials.
type sealedTestWrapper struct {
	aead cipher.AEAD
	id   string
}

func newSealedTestWrapper(t *testing.T) *sealedTestWrapper {
	t.Helper()

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("generate key-encryption key: %v", err)
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("new GCM: %v", err)
	}

	return &sealedTestWrapper{aead: aead, id: "kek-sealed-test"}
}

func (w *sealedTestWrapper) ID() string { return w.id }

func (w *sealedTestWrapper) Wrap(_ context.Context, key, aad []byte) ([]byte, error) {
	nonce := make([]byte, w.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	return w.aead.Seal(nonce, nonce, key, aad), nil
}

func (w *sealedTestWrapper) Unwrap(_ context.Context, sealed, aad []byte) ([]byte, error) {
	n := w.aead.NonceSize()
	if len(sealed) < n {
		return nil, secret.ErrDecrypt
	}

	key, err := w.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, secret.ErrDecrypt
	}

	return key, nil
}

// sealedTestEnv holds one sealed adapter test's handler with the stores
// behind it, so tests prove the responses a sealer changes.
type sealedTestEnv struct {
	handler *tfe.Handler
	store   *sqlite.Store
	blobs   *fs.Store
	ws      workspace.Workspace
}

// sealedTestHandler wires a Handler over a real workspace service that seals
// state with a real secret service on SQLite.
func sealedTestHandler(t *testing.T) sealedTestEnv {
	t.Helper()

	store := sealedTestStore(t)
	namespaces := sealedTestNamespaces(t, store)
	secrets := sealedTestSecrets(t, store, namespaces)
	blobs := sealedTestBlobs(t)
	workspaces := sealedTestWorkspaces(t, store, namespaces, secrets, blobs)
	ws := sealedTestWorkspace(t, store, namespaces, workspaces)

	return sealedTestEnv{
		handler: newWriteHandler(t, workspaces, baseurl.Resolver{}),
		store:   store,
		blobs:   blobs,
		ws:      ws,
	}
}

// sealedTestStore opens a SQLite store in a temporary directory.
func sealedTestStore(t *testing.T) *sqlite.Store {
	t.Helper()

	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return store
}

// sealedTestNamespaces returns a namespace service holding the acme
// namespace.
func sealedTestNamespaces(t *testing.T, store *sqlite.Store) *namespace.Service {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	if _, createErr := namespaces.Create(t.Context(), namespace.Name("acme")); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	return namespaces
}

// sealedTestSecrets returns a secret service with a fresh key-encryption
// key.
func sealedTestSecrets(
	t *testing.T, store *sqlite.Store, namespaces *namespace.Service,
) *secret.Service {
	t.Helper()

	secrets, err := secret.NewService(secret.Config{
		Keys: store, Wrapper: newSealedTestWrapper(t), Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}

	return secrets
}

// sealedTestBlobs opens a blob store in a temporary directory.
func sealedTestBlobs(t *testing.T) *fs.Store {
	t.Helper()

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return blobs
}

// sealedTestWorkspaces returns a workspace service that seals state.
func sealedTestWorkspaces(
	t *testing.T,
	store *sqlite.Store,
	namespaces *namespace.Service,
	secrets *secret.Service,
	blobs *fs.Store,
) *workspace.Service {
	t.Helper()

	workspaces, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         store,
		States:             store,
		Blobs:              blobs,
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             secrets,
		Logger:             slog.New(slog.DiscardHandler),
		Now:                nil,
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("workspaces: %v", err)
	}

	return workspaces
}

// sealedTestWorkspace creates the acme/web workspace locked by the test
// principal.
func sealedTestWorkspace(
	t *testing.T, store *sqlite.Store, namespaces *namespace.Service, workspaces *workspace.Service,
) workspace.Workspace {
	t.Helper()

	ctx := t.Context()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	acme, err := namespaces.Get(ctx, ns)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	ws, err := workspaces.Create(
		ctx,
		workspace.Ref{Namespace: ns, Name: name},
		placementtest.Workspace(t, store, acme.Fence()),
	)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	if _, lockErr := workspaces.Lock(ctx, ws.ID, "tester", "apply"); lockErr != nil {
		t.Fatalf("lock: %v", lockErr)
	}

	return ws
}

// sealedStateContent is the state the sealed adapter tests upload: a small
// document with a known sensitive value.
func sealedStateContent() string {
	return stateWithOutputs(stateCreateOutputs())
}

// stateWithOutputs returns a state of serial 4 whose top-level outputs are doc.
// The outputs of a version come from its body (ADR 49, decision 8).
func stateWithOutputs(doc string) string {
	return `{"version":4,"serial":4,"lineage":"seed-lineage","outputs":` + doc + `,"resources":[]}`
}

// sealedCreateBody returns a state version create body for content with the
// greeting outputs document.
func sealedCreateBody(content string) string {
	sum := md5.Sum([]byte(content)) //nolint:gosec // The protocol under test names state by MD5 for integrity.

	return fmt.Sprintf(
		`{"data":{"type":"state-versions","attributes":{`+
			`"serial":4,"lineage":"seed-lineage","md5":%q,"json-state-outputs":%q}}}`,
		hex.EncodeToString(sum[:]),
		base64.StdEncoding.EncodeToString([]byte(stateCreateOutputs())),
	)
}

// sealedCreateRef decodes one relationship target in tests.
type sealedCreateRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// sealedCreateRelationship decodes one relationship of a create document in
// tests.
type sealedCreateRelationship struct {
	Data []sealedCreateRef `json:"data"`
}

// sealedCreateData decodes the resource of a create document in tests.
type sealedCreateData struct {
	ID            string                              `json:"id"`
	Type          string                              `json:"type"`
	Attributes    createTestAttributes                `json:"attributes"`
	Relationships map[string]sealedCreateRelationship `json:"relationships"`
}

// sealedCreateDoc decodes the relationships of a state version create
// document in tests.
type sealedCreateDoc struct {
	Data sealedCreateData `json:"data"`
}

// sealedCreate posts a sealed state version create and returns its document.
// The create must answer 201: a 500 here means the pending version renders
// sealed outputs.
func sealedCreate(t *testing.T, env sealedTestEnv, content string) sealedCreateDoc {
	t.Helper()

	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(
		rec,
		createStateRequest(
			t,
			"/api/v2/workspaces/"+env.ws.ID.String()+"/state-versions",
			sealedCreateBody(content),
		),
	)

	created := decodeCreateDoc(t, rec)

	var doc sealedCreateDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode create relationships: %v", err)
	}

	doc.Data.ID = created.Data.ID
	doc.Data.Type = created.Data.Type
	doc.Data.Attributes = created.Data.Attributes

	return doc
}

// putUploadPath PUTs content to the path of rawURL without an Authorization
// header, as the CLI uploads state through the capability in the path.
func putUploadPath(t *testing.T, handler *tfe.Handler, rawURL, content string) {
	t.Helper()

	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() {
		t.Fatalf("upload URL = %q, want an absolute URL", rawURL)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodPut, u.EscapedPath(), strings.NewReader(content)),
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// sealUploads uploads the state content and its JSON rendering through the
// capabilities of a created version.
func sealUploads(t *testing.T, env sealedTestEnv, doc sealedCreateDoc, content string) {
	t.Helper()

	putUploadPath(t, env.handler, doc.Data.Attributes.UploadURL, content)
	putUploadPath(t, env.handler, doc.Data.Attributes.JSONUploadURL, `{"rendering":1}`)
}

// authenticatedGet builds an authenticated GET request in tests.
func authenticatedGet(t *testing.T, target string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer test-token")

	return req
}

// TestSealedStateVersionCreate proves a create with a sealer answers 201 and
// stores no outputs from the declared rendering: the outputs of the version come
// from its body, which the upload stores sealed (ADR 49, decision 8).
func TestSealedStateVersionCreate(t *testing.T) {
	t.Parallel()

	env := sealedTestHandler(t)
	content := sealedStateContent()

	doc := sealedCreate(t, env, content)
	assertCreateHasNoOutputs(t, doc)

	sealUploads(t, env, doc, content)

	stored := readStoredVersion(t, env, doc.Data.ID)
	if len(stored.Outputs) == 0 {
		t.Fatal("the upload stored no outputs from the body")
	}

	assertOutputsSealed(t, stored)
	assertUploadedBlobSealed(t, env, doc.Data.ID, content)
}

// assertCreateHasNoOutputs proves the create response names no output: the
// declared rendering is not stored, so the pending version has none.
func assertCreateHasNoOutputs(t *testing.T, doc sealedCreateDoc) {
	t.Helper()

	if outputs, ok := doc.Data.Relationships["outputs"]; ok && len(outputs.Data) > 0 {
		t.Fatalf("create response names %d outputs before the body is uploaded, want none", len(outputs.Data))
	}
}

// readStoredVersion returns the stored row of a created version.
func readStoredVersion(t *testing.T, env sealedTestEnv, id string) workspace.StateVersion {
	t.Helper()

	stored, err := env.store.GetStateVersion(t.Context(), workspace.StateVersionID(id))
	if err != nil {
		t.Fatalf("get stored version: %v", err)
	}

	if !stored.Sealed {
		t.Fatal("stored version reads unsealed, want sealed")
	}

	return stored
}

// assertOutputsSealed proves the outputs column holds no output value.
func assertOutputsSealed(t *testing.T, stored workspace.StateVersion) {
	t.Helper()

	if bytes.Contains(stored.Outputs, []byte("hello")) {
		t.Fatal("stored outputs hold the output value, want sealed bytes only")
	}
}

// assertUploadedBlobSealed proves the blob store holds no state bytes after
// the upload.
func assertUploadedBlobSealed(t *testing.T, env sealedTestEnv, id, content string) {
	t.Helper()

	landed := readStoredVersion(t, env, id)

	raw, err := env.blobs.Get(t.Context(), landed.Content)
	if err != nil {
		t.Fatalf("get blob: %v", err)
	}

	sealed, readErr := io.ReadAll(raw)
	closeErr := raw.Close()

	if readErr != nil {
		t.Fatalf("read blob: %v", readErr)
	}

	if closeErr != nil {
		t.Fatalf("close blob: %v", closeErr)
	}

	if bytes.Contains(sealed, []byte(content)) {
		t.Fatal("blob store holds the state bytes, want sealed bytes only")
	}
}

// TestSealedStateVersionRestore proves a restore with a sealer answers 200:
// the restored version renders plain outputs while the committed row keeps
// them sealed.
func TestSealedStateVersionRestore(t *testing.T) {
	t.Parallel()

	env := sealedTestHandler(t)
	content := sealedStateContent()

	created := sealedCreate(t, env, content)
	sealUploads(t, env, created, content)
	assertDownloadRoundTrip(t, env, created.Data.ID, content)
	assertSingleOutputPlain(t, env, created.Data.ID)

	restored := httptest.NewRecorder()
	env.handler.ServeHTTP(restored, restoreRequest(t, created.Data.ID))

	restoredDoc := decodeRestoreDoc(t, restored, http.StatusOK)

	if restoredDoc.Data.Attributes.RestoredFrom != created.Data.ID {
		t.Fatalf(
			"restored from = %q, want %q",
			restoredDoc.Data.Attributes.RestoredFrom,
			created.Data.ID,
		)
	}
}

// assertDownloadRoundTrip proves the download answers the uploaded bytes.
func assertDownloadRoundTrip(t *testing.T, env sealedTestEnv, id, content string) {
	t.Helper()

	download := httptest.NewRecorder()
	env.handler.ServeHTTP(download, authenticatedGet(t, "/api/v2/state-versions/"+id+"/download"))

	if download.Code != http.StatusOK {
		t.Fatalf("download answers %d, want %d: %s", download.Code, http.StatusOK, download.Body.String())
	}

	if download.Body.String() != content {
		t.Fatalf("download = %q, want %q", download.Body.String(), content)
	}
}

// assertSingleOutputPlain proves the single output route answers the plain
// value.
func assertSingleOutputPlain(t *testing.T, env sealedTestEnv, id string) {
	t.Helper()

	output := httptest.NewRecorder()
	env.handler.ServeHTTP(output, authenticatedGet(t, "/api/v2/state-version-outputs/"+id+".greeting"))

	if output.Code != http.StatusOK {
		t.Fatalf("output answers %d, want %d: %s", output.Code, http.StatusOK, output.Body.String())
	}

	if !strings.Contains(output.Body.String(), "hello") {
		t.Fatalf("output = %s, want the plain value", output.Body.String())
	}
}

// largeOutputs is one boundary outputs document with its plain value.
type largeOutputs struct {
	doc   string
	value string
}

// largeSealedOutputs returns a flat outputs document of want bytes holding
// one non-sensitive output whose value starts with marker, with the value.
func largeSealedOutputs(want int, marker string) largeOutputs {
	const prefix = `{"big":{"sensitive":false,"value":"`
	const suffix = `","type":"string"}}`

	fill := want - len(prefix) - len(suffix)
	value := marker + strings.Repeat("x", fill-len(marker))

	return largeOutputs{doc: prefix + value + suffix, value: value}
}

// largeSealedCreateBody returns a state version create body for content with
// the outputs document doc.
func largeSealedCreateBody(content, doc string) string {
	sum := md5.Sum([]byte(content)) //nolint:gosec // The protocol under test names state by MD5 for integrity.

	return fmt.Sprintf(
		`{"data":{"type":"state-versions","attributes":{`+
			`"serial":4,"lineage":"seed-lineage","md5":%q,"json-state-outputs":%q}}}`,
		hex.EncodeToString(sum[:]),
		base64.StdEncoding.EncodeToString([]byte(doc)),
	)
}

// TestSealedLargeOutputsRoundTrip proves an outputs document just under
// 16 MiB seals, stores, and reads back through the list route and the
// one-output route, while a document over the limit still fails.
func TestSealedLargeOutputsRoundTrip(t *testing.T) {
	t.Parallel()

	const limit = 16 << 20
	const marker = "large-output-marker-"

	env := sealedTestHandler(t)

	doc := largeSealedOutputs(limit-1024, marker)
	content := stateWithOutputs(doc.doc)
	createdDoc := createLargeVersion(t, env, content, doc.doc)

	assertLargeStoredSealed(t, env, createdDoc.Data.ID, marker)
	sealUploads(t, env, createdDoc, content)
	assertLargeListRoute(t, env)
	assertLargeSingleRoute(t, env, createdDoc.Data.ID, doc.value[:len(marker)+16])
	assertOversizedOutputsDenied(t, env, content, limit+1, marker)
}

// createLargeVersion posts a state version create with the outputs document
// doc and returns its document. The create must answer 201.
func createLargeVersion(t *testing.T, env sealedTestEnv, content, doc string) sealedCreateDoc {
	t.Helper()

	target := "/api/v2/workspaces/" + env.ws.ID.String() + "/state-versions"
	body := largeSealedCreateBody(content, doc)

	created := httptest.NewRecorder()
	env.handler.ServeHTTP(created, createStateRequest(t, target, body))

	if created.Code != http.StatusCreated {
		t.Fatalf("create answers %d, want %d", created.Code, http.StatusCreated)
	}

	var createdDoc sealedCreateDoc

	if err := json.Unmarshal(created.Body.Bytes(), &createdDoc); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	return createdDoc
}

// assertLargeStoredSealed proves the stored outputs hold sealed bytes only.
func assertLargeStoredSealed(t *testing.T, env sealedTestEnv, id, marker string) {
	t.Helper()

	stored := readStoredVersion(t, env, id)

	if bytes.Contains(stored.Outputs, []byte(marker)) {
		t.Fatal("stored outputs hold the output value, want sealed bytes only")
	}
}

// assertLargeListRoute proves the list route names the big output.
func assertLargeListRoute(t *testing.T, env sealedTestEnv) {
	t.Helper()

	target := "/api/v2/workspaces/" + env.ws.ID.String() + "/current-state-version-outputs"

	listed := httptest.NewRecorder()
	env.handler.ServeHTTP(listed, authenticatedGet(t, target))

	if listed.Code != http.StatusOK {
		t.Fatalf("outputs list answers %d, want %d", listed.Code, http.StatusOK)
	}

	if !strings.Contains(listed.Body.String(), `"big"`) {
		t.Fatal("outputs list lacks the big output")
	}
}

// assertLargeSingleRoute proves the one-output route answers the plain value.
func assertLargeSingleRoute(t *testing.T, env sealedTestEnv, id, want string) {
	t.Helper()

	single := httptest.NewRecorder()
	env.handler.ServeHTTP(single, authenticatedGet(t, "/api/v2/state-version-outputs/"+id+".big"))

	if single.Code != http.StatusOK {
		t.Fatalf("output answers %d, want %d", single.Code, http.StatusOK)
	}

	if !strings.Contains(single.Body.String(), want) {
		t.Fatal("single output lacks the plain value")
	}
}

// assertOversizedOutputsDenied proves an outputs document over the limit
// still fails the create.
func assertOversizedOutputsDenied(t *testing.T, env sealedTestEnv, content string, want int, marker string) {
	t.Helper()

	over := largeSealedOutputs(want, marker)

	target := "/api/v2/workspaces/" + env.ws.ID.String() + "/state-versions"
	body := largeSealedCreateBody(content, over.doc)

	denied := httptest.NewRecorder()
	env.handler.ServeHTTP(denied, createStateRequest(t, target, body))

	if denied.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"oversized create answers %d, want %d: %s",
			denied.Code,
			http.StatusRequestEntityTooLarge,
			denied.Body.String(),
		)
	}
}
