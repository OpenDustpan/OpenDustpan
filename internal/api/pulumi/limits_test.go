package pulumi_test

import (
	"bytes"
	"compress/gzip"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// newUpdateFixtureWithGate builds an update fixture with checkpoints bounded
// by limit and the gate bounded by inFlight.
func newUpdateFixtureWithGate(
	t *testing.T, authz identity.Authorizer, limit int, inFlight int64,
) updateFixture {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stores := &recordingStackStore{
		placed: nil, placeErr: nil,
		created:   nil,
		renamed:   nil,
		tags:      nil,
		deleted:   nil,
		forced:    nil,
		entries:   nil,
		deleteErr: nil,
	}

	stacks, err := stack.NewService(
		stack.Config{Store: stores, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	mem := newMemUpdateStore()

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            mem.resolving(stacks),
		Tokens:             nil,
		Checkpoints:        mem,
		Events:             mem,
		Namespaces:         namespaces,
		Stacks:             stacks,
		Blobs:              newMemBlob(),
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: limit,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		Placer:                  nil,
		StateGuard:              stategate.Off("the test keeps no gates"),
		SessionPolicy:           nil,
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 updates,
		Secrets:                 newTestSecrets(t, namespaces),
		Namespaces:              namespaces,
		Authenticator:           tokenAuthenticator{},
		Authorizer:              authz,
		Sessions:                nil,
		AddressGate:             nil,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 baseurl.Resolver{},
		MaxBodyBytesInFlight:    inFlight,
		Gate:                    nil,
		Audit:                   newTestAudit(),
		OnAuditFailure:          nil,
		CheckpointDeltaCutoff:   0,
		DenialLimiter:           nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	return updateFixture{handler: handler, updates: updates, ref: ref, mem: nil, sealer: nil, audit: nil}
}

// newSecretFixtureWithGate builds a secrets fixture with batches bounded by
// maxBatch and the gate bounded by inFlight.
func newSecretFixtureWithGate(t *testing.T, authz identity.Authorizer, maxBatch int, inFlight int64) secretFixture {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stacks, err := stack.NewService(
		stack.Config{
			Store:       newMapStackStore(),
			Namespaces:  namespaces,
			Now:         nil,
			State:       nil,
			Retention:   0,
			ManualLocks: nil,
		},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	for _, name := range []string{"prod", "other"} {
		ref, refErr := stack.ParseRef("acme", "web", name)
		if refErr != nil {
			t.Fatalf("parse ref: %v", refErr)
		}

		if _, createErr := stacks.CreatePlaced(t.Context(), ref, nil, acmePlacement()); createErr != nil {
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
		Placer:                  nil,
		StateGuard:              stategate.Off("the test keeps no gates"),
		SessionPolicy:           nil,
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 newEnforcingUpdates(t, namespaces),
		Secrets:                 secrets,
		Namespaces:              namespaces,
		Authenticator:           tokenAuthenticator{},
		Authorizer:              authz,
		Sessions:                nil,
		AddressGate:             nil,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 baseurl.Resolver{},
		MaxBodyBytesInFlight:    inFlight,
		Gate:                    nil,
		Audit:                   newTestAudit(),
		OnAuditFailure:          nil,
		CheckpointDeltaCutoff:   0,
		DenialLimiter:           nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return secretFixture{handler: handler, stacks: stacks}
}

// TestLoweredDefaults proves large bodies use less memory by default.
func TestLoweredDefaults(t *testing.T) {
	t.Parallel()

	if stack.DefaultMaxCheckpointBytes != 32<<20 {
		t.Fatalf("DefaultMaxCheckpointBytes = %d, want %d", stack.DefaultMaxCheckpointBytes, 32<<20)
	}

	if secret.DefaultMaxBatchBytes != 32<<20 {
		t.Fatalf("DefaultMaxBatchBytes = %d, want %d", secret.DefaultMaxBatchBytes, 32<<20)
	}

	if pulumi.DefaultMaxBodyBytesInFlight != 128<<20 {
		t.Fatalf("DefaultMaxBodyBytesInFlight = %d, want %d", pulumi.DefaultMaxBodyBytesInFlight, 128<<20)
	}
}

// TestGzippedCheckpointStreams proves a gzipped checkpoint decodes without
// keeping both forms: the import path shares the decoder.
func TestGzippedCheckpointStreams(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	paths, lease := checkpointPaths(t, f)

	payload := `{"version":3,"deployment":{"resources":[]}}`

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(payload)); err != nil {
		t.Fatalf("gzip body: %v", err)
	}

	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPatch, paths.Checkpoint, bytes.NewReader(buf.Bytes()),
	)
	req.Header.Set("Authorization", "update-token "+lease)
	req.Header.Set("Content-Encoding", "gzip")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("gzipped checkpoint answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// TestGateBusy proves a large body answers 503 with Retry-After when the
// bytes in flight do not fit.
func TestGateBusy(t *testing.T) {
	t.Parallel()

	f := newUpdateFixtureWithGate(t, allowAll(), 64, 1)
	paths, lease := checkpointPaths(t, f)

	status, body := f.doUpdate(t, leaseRequest(
		http.MethodPatch, paths.Checkpoint, lease, `{"version":3,"deployment":{}}`,
	))
	if status != http.StatusServiceUnavailable {
		t.Fatalf("busy checkpoint answers %d, want %d (%s)", status, http.StatusServiceUnavailable, body)
	}

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPatch, paths.Checkpoint, strings.NewReader(`{"version":3,"deployment":{}}`),
	)
	req.Header.Set("Authorization", "update-token "+lease)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatal("busy checkpoint answers no Retry-After")
	}
}

// TestGateBusySecrets proves a secrets batch answers 503 with Retry-After
// when the gate is full.
func TestGateBusySecrets(t *testing.T) {
	t.Parallel()

	f := newSecretFixtureWithGate(t, allowAll(), smallBatch, 1)

	status, body := f.plain(t, http.MethodPost, "/api/stacks/acme/web/prod/encrypt", `{"plaintext":"aGk="}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("busy encrypt answers %d, want %d (%s)", status, http.StatusServiceUnavailable, body)
	}

	if !strings.Contains(body, "503") && !strings.Contains(body, "busy") {
		t.Fatalf("busy encrypt body %q, want a busy message", body)
	}
}
