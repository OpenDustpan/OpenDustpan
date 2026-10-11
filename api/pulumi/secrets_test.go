package pulumi_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/api/pulumi"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/secret"
	"opendustpan.org/opendustpan/stack"
)

const (
	// testSecretKey is the fixed key-encryption key every secrets fixture
	// wraps data keys with. It is a test value only.
	testSecretKey = "0123456789abcdef0123456789abcdef"
	// smallBatch bounds the batches of the size-limit fixtures: small enough
	// that over-limit bodies stay fast.
	smallBatch = 1024
)

// secretPatterns lists every secrets route as "METHOD path". The generic
// enforcement test skips them: like update routes they need live stack state,
// so TestSecretsEnforceAuthorization covers them instead.
func secretPatterns() []string {
	return []string{
		"POST /api/stacks/{namespace}/{project}/{stack}/encrypt",
		"POST /api/stacks/{namespace}/{project}/{stack}/batch-encrypt",
		"POST /api/stacks/{namespace}/{project}/{stack}/decrypt",
		"POST /api/stacks/{namespace}/{project}/{stack}/batch-decrypt",
		"POST /api/stacks/{namespace}/{project}/{stack}/decrypt/log-decryption",
		"POST /api/stacks/{namespace}/{project}/{stack}/decrypt/log-batch-decryption",
	}
}

// stubSecretKeys answers no data key, for handlers whose tested routes never
// encrypt.
type stubSecretKeys struct{}

func (stubSecretKeys) CreateKey(
	_ context.Context, _ secret.WrappedKey, _ namespace.Fence,
) (secret.WrappedKey, error) {
	return secret.WrappedKey{}, secret.ErrKeyNotFound
}

func (stubSecretKeys) GetKey(_ context.Context, _ secret.Scope) (secret.WrappedKey, error) {
	return secret.WrappedKey{}, secret.ErrKeyNotFound
}

func (stubSecretKeys) ListWrapperIDs(_ context.Context) ([]string, error) {
	return nil, nil
}

// newTestSecrets returns a Secrets service with no key-encryption key, for
// handlers whose tested routes never encrypt.
func newTestSecrets(t *testing.T, namespaces *namespace.Service) *secret.Service {
	t.Helper()

	secrets, err := secret.NewService(secret.Config{
		Keys: stubSecretKeys{}, Wrapper: nil, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	return secrets
}

// memSecretKeys is a secret.KeyStore in memory.
type memSecretKeys struct {
	keys map[secret.Scope]secret.WrappedKey
	mu   sync.Mutex
}

func (m *memSecretKeys) CreateKey(
	_ context.Context, k secret.WrappedKey, _ namespace.Fence,
) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.keys[k.Scope]; ok {
		return existing, nil
	}

	m.keys[k.Scope] = k

	return k, nil
}

func (m *memSecretKeys) GetKey(_ context.Context, scope secret.Scope) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[scope]
	if !ok {
		return secret.WrappedKey{}, secret.ErrKeyNotFound
	}

	return k, nil
}

func (m *memSecretKeys) ListWrapperIDs(_ context.Context) ([]string, error) {
	return nil, nil
}

// mapStackStore keeps stacks in memory with a distinct stable ID per stack,
// so ciphertexts of one stack never open as another's.
type mapStackStore struct {
	stacks map[stack.Ref]stack.Stack
	mu     sync.Mutex
}

func newMapStackStore() *mapStackStore {
	return &mapStackStore{stacks: map[stack.Ref]stack.Stack{}, mu: sync.Mutex{}}
}

func (m *mapStackStore) CreateStack(_ context.Context, st stack.Stack, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.stacks[st.Ref]; ok {
		return stack.ErrAlreadyExists
	}

	m.stacks[st.Ref] = st

	return nil
}

func (m *mapStackStore) GetStack(_ context.Context, ref stack.Ref) (stack.Stack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.stacks[ref]
	if !ok {
		return stack.Stack{}, stack.ErrNotFound
	}

	return st, nil
}

func (m *mapStackStore) ListStacks(_ context.Context, filter stack.Filter) ([]stack.Stack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []stack.Stack

	for ref := range m.stacks {
		st := m.stacks[ref]
		if st.Ref.Namespace != filter.Namespace {
			continue
		}

		if filter.Project != "" && st.Ref.Project != filter.Project {
			continue
		}

		out = append(out, st)
	}

	return out, nil
}

func (m *mapStackStore) DeleteStack(_ context.Context, ref stack.Ref, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.stacks[ref]; !ok {
		return stack.ErrNotFound
	}

	delete(m.stacks, ref)

	return nil
}

func (m *mapStackStore) RenameStack(
	_ context.Context, oldRef stack.Ref, newName stack.Name, _ namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.stacks[oldRef]
	if !ok {
		return stack.ErrNotFound
	}

	delete(m.stacks, oldRef)

	st.Ref.Stack = newName
	m.stacks[st.Ref] = st

	return nil
}

func (m *mapStackStore) UpdateStackTags(
	_ context.Context, ref stack.Ref, tags map[string]string, updatedAt time.Time, _ namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.stacks[ref]
	if !ok {
		return stack.ErrNotFound
	}

	st.Tags = maps.Clone(tags)
	st.UpdatedAt = updatedAt
	m.stacks[ref] = st

	return nil
}

// secretFixture is a Handler whose secrets routes work end to end, with the
// prod and other stacks of the web project already created.
type secretFixture struct {
	handler *pulumi.Handler
	stacks  *stack.Service
}

// newSecretFixture builds a Handler with a working key-encryption key over
// empty stores. maxBatch bounds one batch; zero means the domain default.
// authz decides the authorization policy under test.
func newSecretFixture(t *testing.T, authz identity.Authorizer, maxBatch int) secretFixture {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stacks, err := stack.NewService(stack.Config{Store: newMapStackStore(), Namespaces: namespaces, Now: nil})
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	for _, name := range []string{"prod", "other"} {
		ref, refErr := stack.ParseRef("acme", "web", name)
		if refErr != nil {
			t.Fatalf("parse ref: %v", refErr)
		}

		if _, createErr := stacks.Create(t.Context(), ref, nil); createErr != nil {
			t.Fatalf("create stack %s: %v", ref, createErr)
		}
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte(testSecretKey))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys:          &memSecretKeys{keys: map[secret.Scope]secret.WrappedKey{}, mu: sync.Mutex{}},
		Wrapper:       wrapper,
		Namespaces:    namespaces,
		Now:           nil,
		MaxBatchBytes: maxBatch,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		Stacks:        stacks,
		Updates:       newEnforcingUpdates(t, namespaces),
		Secrets:       secrets,
		Namespaces:    namespaces,
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Logger:        slog.New(slog.DiscardHandler),
		BaseURL:       baseurl.Resolver{},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return secretFixture{handler: handler, stacks: stacks}
}

// sendSecret sends one secrets-route request with the test access token and
// the given content encoding.
func (f secretFixture) sendSecret(t *testing.T, method, target, raw, encoding string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(raw))
	req.Header.Set("Authorization", "token test-token")

	if encoding != "" {
		req.Header.Set("Content-Encoding", encoding)
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// plain sends one secrets-route request with a plain body.
func (f secretFixture) plain(t *testing.T, method, target, body string) (int, string) {
	t.Helper()

	return f.sendSecret(t, method, target, body, "")
}

// gzipped sends one secrets-route request with the body gzipped and the
// matching content encoding, as the CLI sends its batch calls.
func (f secretFixture) gzipped(t *testing.T, method, target, body string) (int, string) {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(body)); err != nil {
		t.Fatalf("gzip body: %v", err)
	}

	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	return f.sendSecret(t, method, target, buf.String(), "gzip")
}

// encryptOne encrypts value on prod through HTTP and returns its ciphertext.
func (f secretFixture) encryptOne(t *testing.T, value string) string {
	t.Helper()

	raw, err := json.Marshal(map[string]string{"plaintext": base64.StdEncoding.EncodeToString([]byte(value))})
	if err != nil {
		t.Fatalf("encode encrypt body: %v", err)
	}

	status, body := f.plain(t, http.MethodPost, "/api/stacks/acme/web/prod/encrypt", string(raw))
	if status != http.StatusOK {
		t.Fatalf("encrypt answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var got struct {
		Ciphertext string `json:"ciphertext"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Ciphertext == "" {
		t.Fatalf("encrypt answers no ciphertext (%s): %v", body, err)
	}

	return got.Ciphertext
}

// decryptOne decrypts ciphertext on target through HTTP and returns its plaintext.
func (f secretFixture) decryptOne(t *testing.T, target, ciphertext string) (int, string) {
	t.Helper()

	raw, encodeErr := json.Marshal(map[string]string{"ciphertext": ciphertext})
	if encodeErr != nil {
		t.Fatalf("encode decrypt body: %v", encodeErr)
	}

	status, body := f.plain(t, http.MethodPost, target, string(raw))
	if status != http.StatusOK {
		return status, body
	}

	var got struct {
		Plaintext string `json:"plaintext"`
	}

	if decodeErr := json.Unmarshal([]byte(body), &got); decodeErr != nil {
		t.Fatalf("decode decrypt: %v (%s)", decodeErr, body)
	}

	// The wire carries bytes as base64.
	plain, plainErr := base64.StdEncoding.DecodeString(got.Plaintext)
	if plainErr != nil {
		t.Fatalf("decode plaintext: %v (%s)", plainErr, body)
	}

	return status, string(plain)
}

// wantSecretAction maps every secrets route to the action it must declare:
// encrypt routes need unit:encrypt, decrypt routes need unit:decrypt.
func wantSecretAction() map[string]identity.Action {
	return map[string]identity.Action{
		"POST /api/stacks/{namespace}/{project}/{stack}/encrypt":                      identity.ActionUnitEncrypt,
		"POST /api/stacks/{namespace}/{project}/{stack}/batch-encrypt":                identity.ActionUnitEncrypt,
		"POST /api/stacks/{namespace}/{project}/{stack}/decrypt":                      identity.ActionUnitDecrypt,
		"POST /api/stacks/{namespace}/{project}/{stack}/batch-decrypt":                identity.ActionUnitDecrypt,
		"POST /api/stacks/{namespace}/{project}/{stack}/decrypt/log-decryption":       identity.ActionUnitDecrypt,
		"POST /api/stacks/{namespace}/{project}/{stack}/decrypt/log-batch-decryption": identity.ActionUnitDecrypt,
	}
}

// TestSecretRoutesDeclareActions proves every secrets route declares its
// action on the unit with a resolver and authorized scope.
func TestSecretRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, allowAll(), 0)
	want := wantSecretAction()
	seen := map[string]bool{}

	for _, route := range f.handler.Routes() {
		pattern := route.Method + " " + route.Path
		action, ok := want[pattern]

		if !ok {
			continue
		}

		seen[pattern] = true

		if route.Scope != pulumi.ScopeAuthorized {
			t.Errorf("route %q scope = %v, want authorized", pattern, route.Scope)
		}

		if len(route.Actions) != 1 || route.Actions[0] != action {
			t.Errorf("route %q actions = %v, want exactly [%s]", pattern, route.Actions, action)
		}

		if route.Resolve == nil {
			t.Errorf("route %q declares actions but no resolver", pattern)
		}
	}

	for pattern := range want {
		if !seen[pattern] {
			t.Errorf("route %q is not declared", pattern)
		}
	}
}

// TestSecretsEnforceAuthorization proves every secrets route answers 200 when
// allowed and 403 when its action is denied. The audit routes answer 204 when
// allowed.
func TestSecretsEnforceAuthorization(t *testing.T) {
	t.Parallel()

	bodies := map[string]string{
		"POST /api/stacks/{namespace}/{project}/{stack}/encrypt":       `{"plaintext":"aGk="}`,
		"POST /api/stacks/{namespace}/{project}/{stack}/batch-encrypt": `{"plaintexts":["aGk="]}`,
	}

	for _, pattern := range secretPatterns() {
		t.Run(pattern, func(t *testing.T) {
			t.Parallel()
			checkSecretRoute(t, pattern, bodies[pattern])
		})
	}
}

// checkSecretRoute proves one secrets route answers success when allowed and
// 403 when its action is denied. The audit routes answer 204 when allowed.
func checkSecretRoute(t *testing.T, pattern, body string) {
	t.Helper()

	route := secretRoute(t, newSecretFixture(t, allowAll(), 0).handler, pattern)
	target := "/api/stacks/acme/web/prod" + strings.TrimPrefix(
		pattern, "POST /api/stacks/{namespace}/{project}/{stack}",
	)

	allowed := newSecretFixture(t, allowAll(), 0)

	status, resp := allowed.secretCall(t, route.Method, target, pattern, body)
	if want := wantSecretSuccess(pattern); status != want {
		t.Fatalf("allowed request answers %d, want %d (%s)", status, want, resp)
	}

	denied := newSecretFixture(
		t, selectiveAuthorizer{deny: map[identity.Action]bool{route.Actions[0]: true}}, 0,
	)

	status, resp = denied.secretCall(t, route.Method, target, pattern, body)
	if status != http.StatusForbidden {
		t.Fatalf("denied request answers %d, want %d (%s)", status, http.StatusForbidden, resp)
	}
}

// secretCall sends one request for pattern. Decrypt routes mint a real
// ciphertext first so they reach the domain. Authorization runs before the
// handler, so the same body proves both the allowed and the denied case.
func (f secretFixture) secretCall(t *testing.T, method, target, pattern, body string) (int, string) {
	t.Helper()

	switch {
	case strings.HasSuffix(pattern, "decryption"):
		return f.plain(t, method, target, `{"secretName":"apiKey"}`)
	case strings.HasSuffix(pattern, "/batch-decrypt"):
		return f.plain(t, method, target, f.batchDecryptBody(t, f.encryptOne(t, "hi")))
	case strings.HasSuffix(pattern, "/decrypt"):
		return f.plain(t, method, target, f.decryptBody(t, f.encryptOne(t, "hi")))
	default:
		return f.plain(t, method, target, body)
	}
}

// decryptBody encodes one decrypt body with ciphertext.
func (f secretFixture) decryptBody(t *testing.T, ciphertext string) string {
	t.Helper()

	raw, err := json.Marshal(map[string]string{"ciphertext": ciphertext})
	if err != nil {
		t.Fatalf("encode decrypt body: %v", err)
	}

	return string(raw)
}

// batchDecryptBody encodes one batch decrypt body with ciphertext.
func (f secretFixture) batchDecryptBody(t *testing.T, ciphertext string) string {
	t.Helper()

	raw, err := json.Marshal(map[string][]string{"ciphertexts": {ciphertext}})
	if err != nil {
		t.Fatalf("encode batch decrypt body: %v", err)
	}

	return string(raw)
}

// wantSecretSuccess returns the status an allowed request to pattern answers:
// the audit routes answer 204 with no content, every other secrets route
// answers 200.
func wantSecretSuccess(pattern string) int {
	if strings.HasSuffix(pattern, "decryption") {
		return http.StatusNoContent
	}

	return http.StatusOK
}

// secretRoute returns the declared route for pattern.
func secretRoute(t *testing.T, handler *pulumi.Handler, pattern string) pulumi.Route {
	t.Helper()

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == pattern {
			return route
		}
	}

	t.Fatalf("route %q is not declared", pattern)

	return pulumi.Route{Method: "", Path: "", Scope: 0, Actions: nil, Resolve: nil, Serve: nil}
}

// TestSecretsRoundTrip encrypts and decrypts one value through HTTP.
func TestSecretsRoundTrip(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, allowAll(), 0)

	ciphertext := f.encryptOne(t, "s3cr3t")

	if status, plain := f.decryptOne(t, "/api/stacks/acme/web/prod/decrypt", ciphertext); status != http.StatusOK ||
		plain != "s3cr3t" {
		t.Fatalf("decrypt answers %d %q, want 200 with the plaintext", status, plain)
	}
}

// TestSecretsBatchRoundTrip encrypts and decrypts a batch through HTTP. The
// answer maps the base64 of each ciphertext to its plaintext.
func TestSecretsBatchRoundTrip(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, allowAll(), 0)

	raw, encodeErr := json.Marshal(
		map[string][]string{"plaintexts": {
			base64.StdEncoding.EncodeToString([]byte("first")),
			base64.StdEncoding.EncodeToString([]byte("second")),
		}},
	)
	if encodeErr != nil {
		t.Fatalf("encode batch encrypt body: %v", encodeErr)
	}

	status, body := f.plain(t, http.MethodPost, "/api/stacks/acme/web/prod/batch-encrypt", string(raw))
	if status != http.StatusOK {
		t.Fatalf("batch encrypt answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var encrypted struct {
		Ciphertexts []string `json:"ciphertexts"`
	}

	if decodeErr := json.Unmarshal([]byte(body), &encrypted); decodeErr != nil || len(encrypted.Ciphertexts) != 2 {
		t.Fatalf("batch encrypt answers no pair of ciphertexts (%s): %v", body, decodeErr)
	}

	raw, encodeErr = json.Marshal(map[string][]string{"ciphertexts": encrypted.Ciphertexts})
	if encodeErr != nil {
		t.Fatalf("encode batch decrypt body: %v", encodeErr)
	}

	status, body = f.plain(t, http.MethodPost, "/api/stacks/acme/web/prod/batch-decrypt", string(raw))
	if status != http.StatusOK {
		t.Fatalf("batch decrypt answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var decrypted struct {
		Plaintexts map[string]string `json:"plaintexts"`
	}

	if decodeErr := json.Unmarshal([]byte(body), &decrypted); decodeErr != nil {
		t.Fatalf("decode batch decrypt: %v (%s)", decodeErr, body)
	}

	for i, want := range []string{"first", "second"} {
		got, ok := decrypted.Plaintexts[encrypted.Ciphertexts[i]]
		if !ok || got != base64.StdEncoding.EncodeToString([]byte(want)) {
			t.Fatalf("batch decrypt lacks the plaintext of value %d (%s)", i, body)
		}
	}
}

// TestSecretsFollowRename proves a rename keeps secrets readable: the scope
// uses the stack ID, so the new name decrypts ciphertexts of the old name.
func TestSecretsFollowRename(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, allowAll(), 0)

	ciphertext := f.encryptOne(t, "s3cr3t")

	oldRef, refErr := stack.ParseRef("acme", "web", "prod")
	if refErr != nil {
		t.Fatalf("parse ref: %v", refErr)
	}

	name, nameErr := stack.ParseName("renamed")
	if nameErr != nil {
		t.Fatalf("parse name: %v", nameErr)
	}

	if _, renameErr := f.stacks.Rename(t.Context(), oldRef, name); renameErr != nil {
		t.Fatalf("rename: %v", renameErr)
	}

	status, plain := f.decryptOne(t, "/api/stacks/acme/web/renamed/decrypt", ciphertext)
	if status != http.StatusOK || plain != "s3cr3t" {
		t.Fatalf("decrypt after rename answers %d %q, want 200 with the plaintext", status, plain)
	}
}

// TestSecretsAreBoundToTheirStack proves a ciphertext of one stack does not
// decrypt as another stack.
func TestSecretsAreBoundToTheirStack(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, allowAll(), 0)

	ciphertext := f.encryptOne(t, "s3cr3t")

	status, body := f.decryptOne(t, "/api/stacks/acme/web/other/decrypt", ciphertext)
	if status != http.StatusBadRequest {
		t.Fatalf("foreign decrypt answers %d, want %d (%s)", status, http.StatusBadRequest, body)
	}

	var decoded struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode error: %v (%s)", err, body)
	}

	if decoded.Code != http.StatusBadRequest || decoded.Message == "" {
		t.Fatalf("error = %+v, want code 400 with a message", decoded)
	}
}

// TestSecretsNeedDecryptAction proves a principal with unit:read only cannot
// decrypt: decrypt routes answer 403 without unit:decrypt.
func TestSecretsNeedDecryptAction(t *testing.T) {
	t.Parallel()

	// The Authorizer allows unit:read and denies every other action.
	readOnly := selectiveAuthorizer{deny: map[identity.Action]bool{
		identity.ActionCallerRead:    true,
		identity.ActionNamespaceRead: true,
		identity.ActionUnitWrite:     true,
		identity.ActionUnitManage:    true,
		identity.ActionUnitEncrypt:   true,
		identity.ActionUnitDecrypt:   true,
	}}

	f := newSecretFixture(t, readOnly, 0)

	for _, tc := range []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "decrypt", method: http.MethodPost, target: "/api/stacks/acme/web/prod/decrypt", body: `{"ciphertext":"aGk="}`},
		{name: "batch decrypt", method: http.MethodPost, target: "/api/stacks/acme/web/prod/batch-decrypt", body: `{"ciphertexts":["aGk="]}`},
		{
			name:   "log decryption",
			method: http.MethodPost,
			target: "/api/stacks/acme/web/prod/decrypt/log-decryption",
			body:   `{"secretName":"apiKey"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if status, body := f.plain(t, tc.method, tc.target, tc.body); status != http.StatusForbidden {
				t.Fatalf("read-only decrypt answers %d, want %d (%s)", status, http.StatusForbidden, body)
			}
		})
	}
}

// TestSecretsNeedExistingStack proves encrypt on a missing stack answers 404.
func TestSecretsNeedExistingStack(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, allowAll(), 0)

	status, body := f.plain(
		t, http.MethodPost, "/api/stacks/acme/web/ghost/encrypt", `{"plaintext":"aGk="}`,
	)
	if status != http.StatusNotFound {
		t.Fatalf("encrypt on a missing stack answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}
}

// TestSecretsBodyLimit proves a body over the wire limit and a batch over the
// domain limit both answer 413.
func TestSecretsBodyLimit(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, allowAll(), smallBatch)

	big := strings.Repeat("a", 2<<20)
	raw, encodeErr := json.Marshal(map[string]string{"plaintext": big})
	if encodeErr != nil {
		t.Fatalf("encode body: %v", encodeErr)
	}

	status, body := f.plain(t, http.MethodPost, "/api/stacks/acme/web/prod/encrypt", string(raw))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body answers %d, want %d (%s)", status, http.StatusRequestEntityTooLarge, body)
	}

	var decoded struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if decodeErr := json.Unmarshal([]byte(body), &decoded); decodeErr != nil {
		t.Fatalf("decode error: %v (%s)", decodeErr, body)
	}

	if decoded.Code != http.StatusRequestEntityTooLarge || decoded.Message == "" {
		t.Fatalf("error = %+v, want code 413 with a message", decoded)
	}

	// Two values of smallBatch bytes each fit the wire body but pass the
	// domain batch limit, so the domain answers 413.
	large := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'x'}, smallBatch))

	raw, encodeErr = json.Marshal(map[string][]string{"plaintexts": {large, large}})
	if encodeErr != nil {
		t.Fatalf("encode batch body: %v", encodeErr)
	}

	status, body = f.plain(t, http.MethodPost, "/api/stacks/acme/web/prod/batch-encrypt", string(raw))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized batch answers %d, want %d (%s)", status, http.StatusRequestEntityTooLarge, body)
	}
}

// TestSecretsGzipBomb proves a body that decompresses over the wire limit
// answers 413, while a small gzipped batch still works.
func TestSecretsGzipBomb(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, allowAll(), smallBatch)

	bomb, bombErr := json.Marshal(map[string]string{"plaintext": strings.Repeat("a", 2<<20)})
	if bombErr != nil {
		t.Fatalf("encode bomb: %v", bombErr)
	}

	status, body := f.gzipped(t, http.MethodPost, "/api/stacks/acme/web/prod/encrypt", string(bomb))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("gzip bomb answers %d, want %d (%s)", status, http.StatusRequestEntityTooLarge, body)
	}

	raw, encodeErr := json.Marshal(map[string][]string{"plaintexts": {"aGk="}})
	if encodeErr != nil {
		t.Fatalf("encode batch body: %v", encodeErr)
	}

	status, body = f.gzipped(t, http.MethodPost, "/api/stacks/acme/web/prod/batch-encrypt", string(raw))
	if status != http.StatusOK {
		t.Fatalf("gzipped batch answers %d, want %d (%s)", status, http.StatusOK, body)
	}
}

// TestSecretsAuditRoutes prove the audit routes answer 204 and store nothing.
func TestSecretsAuditRoutes(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, allowAll(), 0)

	for _, tc := range []struct {
		name   string
		target string
		body   string
	}{
		{
			name:   "log decryption",
			target: "/api/stacks/acme/web/prod/decrypt/log-decryption",
			body:   `{"secretName":"apiKey"}`,
		},
		{
			name:   "log batch decryption",
			target: "/api/stacks/acme/web/prod/decrypt/log-batch-decryption",
			body:   `{"commandName":"preview"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := f.plain(t, http.MethodPost, tc.target, tc.body)
			if status != http.StatusNoContent {
				t.Fatalf("audit route answers %d, want %d (%s)", status, http.StatusNoContent, body)
			}

			if body != "" {
				t.Fatalf("audit route answers %q, want no content", body)
			}
		})
	}
}

// TestCapabilitiesAdvertiseBatchEncryption proves the server tells the CLI
// that it encrypts in batches, in the shape the CLI reads.
func TestCapabilitiesAdvertiseBatchEncryption(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, allowAll(), 0)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/capabilities", http.NoBody)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("capabilities answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got struct {
		Capabilities []capabilityConfig `json:"capabilities"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode capabilities: %v (%s)", err, rec.Body.String())
	}

	if !slices.ContainsFunc(got.Capabilities, func(c capabilityConfig) bool {
		return c.Capability == "batch-encrypt"
	}) {
		t.Fatalf("capabilities = %+v, want batch-encrypt among them", got.Capabilities)
	}
}

// capabilityConfig is one entry of the capabilities answer as the CLI reads
// it.
type capabilityConfig struct {
	Capability string `json:"capability"`
}

// TestSecretsMissingField proves a body without the value field answers 400
// instead of crypting an empty value.
func TestSecretsMissingField(t *testing.T) {
	t.Parallel()

	f := newSecretFixture(t, allowAll(), 0)

	for _, tc := range []struct {
		name   string
		target string
		body   string
	}{
		{name: "encrypt", target: "/api/stacks/acme/web/prod/encrypt", body: `{}`},
		{name: "decrypt", target: "/api/stacks/acme/web/prod/decrypt", body: `{}`},
		{name: "batch encrypt", target: "/api/stacks/acme/web/prod/batch-encrypt", body: `{}`},
		{name: "batch decrypt", target: "/api/stacks/acme/web/prod/batch-decrypt", body: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if status, body := f.plain(t, http.MethodPost, tc.target, tc.body); status != http.StatusBadRequest {
				t.Fatalf("%s answers %d, want %d (%s)", tc.name, status, http.StatusBadRequest, body)
			}
		})
	}
}

// TestSecretsMissingKey proves crypting without a key-encryption key answers
// 500 naming the setting, not 400.
func TestSecretsMissingKey(t *testing.T) {
	t.Parallel()

	namespaces, nsErr := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if nsErr != nil {
		t.Fatalf("namespace service: %v", nsErr)
	}

	stacks, stacksErr := stack.NewService(stack.Config{Store: newMapStackStore(), Namespaces: namespaces, Now: nil})
	if stacksErr != nil {
		t.Fatalf("stack service: %v", stacksErr)
	}

	ref, refErr := stack.ParseRef("acme", "web", "prod")
	if refErr != nil {
		t.Fatalf("parse ref: %v", refErr)
	}

	if _, createErr := stacks.Create(t.Context(), ref, nil); createErr != nil {
		t.Fatalf("create stack: %v", createErr)
	}

	secrets, secretsErr := secret.NewService(secret.Config{
		Keys:          &memSecretKeys{keys: map[secret.Scope]secret.WrappedKey{}, mu: sync.Mutex{}},
		Wrapper:       nil,
		Namespaces:    namespaces,
		Now:           nil,
		MaxBatchBytes: 0,
	})
	if secretsErr != nil {
		t.Fatalf("secret service: %v", secretsErr)
	}

	handler, handlerErr := pulumi.NewHandler(pulumi.Config{
		Stacks:        stacks,
		Updates:       newEnforcingUpdates(t, namespaces),
		Secrets:       secrets,
		Namespaces:    namespaces,
		Authenticator: tokenAuthenticator{},
		Authorizer:    allowAll(),
		Logger:        slog.New(slog.DiscardHandler),
		BaseURL:       baseurl.Resolver{},
	})
	if handlerErr != nil {
		t.Fatalf("handler: %v", handlerErr)
	}

	f := secretFixture{handler: handler, stacks: stacks}

	status, body := f.plain(t, http.MethodPost, "/api/stacks/acme/web/prod/encrypt", `{"plaintext":"aGk="}`)
	if status != http.StatusInternalServerError {
		t.Fatalf("encrypt without a key answers %d, want %d (%s)", status, http.StatusInternalServerError, body)
	}

	if !strings.Contains(body, "DUSTPAN_KEY_ENCRYPTION_KEY") {
		t.Fatalf("encrypt without a key answers %s, want it to name the key setting", body)
	}

	status, body = f.plain(t, http.MethodPost, "/api/stacks/acme/web/prod/decrypt", `{"ciphertext":"aGk="}`)
	if status != http.StatusInternalServerError {
		t.Fatalf("decrypt without a key answers %d, want %d (%s)", status, http.StatusInternalServerError, body)
	}

	if !strings.Contains(body, "DUSTPAN_KEY_ENCRYPTION_KEY") {
		t.Fatalf("decrypt without a key answers %s, want it to name the key setting", body)
	}
}
