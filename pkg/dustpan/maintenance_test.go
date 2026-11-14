package dustpan_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// waitForToolStatus polls GET /api/user until it answers want or the wait
// times out. The maintenance watcher caches the installation state with a
// short limit, so a state change reaches the tool routes only after the
// cache expires: the poll waits that out instead of sleeping a fixed time.
func waitForToolStatus(t *testing.T, f fixture, want int) (int, http.Header, string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		status, header, body := f.doRaw(t, http.MethodGet, "/api/user", "token "+adminToken)
		if status == want {
			return status, header, body
		}

		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-timer.C:
		case <-t.Context().Done():
			timer.Stop()
			t.Fatalf("test ended while waiting for tool status %d (last %d)", want, status)
		}
	}

	t.Fatalf("tool status never reached %d", want)

	return 0, nil, ""
}

// enterMaintenanceMode stores maintenance in the database and waits until
// the tool routes refuse with 503.
func enterMaintenanceMode(t *testing.T, f fixture) {
	t.Helper()

	if err := f.store.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	waitForToolStatus(t, f, http.StatusServiceUnavailable)
}

// leaveMaintenanceMode stores normal in the database and waits until the
// tool routes serve again.
func leaveMaintenanceMode(t *testing.T, f fixture) {
	t.Helper()

	if err := f.store.SetState(t.Context(), maintenance.StateNormal); err != nil {
		t.Fatalf("leave maintenance mode: %v", err)
	}

	waitForToolStatus(t, f, http.StatusOK)
}

// checkRetryAfter proves the refusal carries a Retry-After wait in seconds.
func checkRetryAfter(t *testing.T, header http.Header) {
	t.Helper()

	if got := header.Get("Retry-After"); got == "" {
		t.Fatal("maintenance refusal holds no Retry-After header")
	}
}

// TestMaintenanceGatePulumi proves the Pulumi CLI adapter answers 503 with
// Retry-After and a message that names maintenance, in its own error shape,
// and serves again after the installation leaves maintenance mode.
func TestMaintenanceGatePulumi(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", installationName)

	enterMaintenanceMode(t, f)

	status, header, body := f.doRaw(t, http.MethodGet, "/api/user", "token "+adminToken)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/user in maintenance answers %d, want %d (%s)",
			status, http.StatusServiceUnavailable, body)
	}

	checkRetryAfter(t, header)

	if got := header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("media type = %q, want the Pulumi JSON error", got)
	}

	var doc struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode 503 of GET /api/user: %v (%s)", err, body)
	}

	if doc.Code != http.StatusServiceUnavailable || !strings.Contains(doc.Message, "maintenance") {
		t.Fatalf("GET /api/user error = %+v, want code 503 naming maintenance", doc)
	}

	leaveMaintenanceMode(t, f)
}

// TestMaintenanceGateTFE proves the TFE-compatible adapter answers 503 with
// Retry-After and a maintenance error document, without any token: the gate
// runs before authentication.
func TestMaintenanceGateTFE(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", installationName)

	enterMaintenanceMode(t, f)

	status, header, body := f.doRaw(t, http.MethodGet, "/api/v2/ping", "")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/v2/ping in maintenance answers %d, want %d (%s)",
			status, http.StatusServiceUnavailable, body)
	}

	checkRetryAfter(t, header)

	if got := header.Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want the TFE JSON:API error", got)
	}

	var doc maintenanceTFEDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode 503 of GET /api/v2/ping: %v (%s)", err, body)
	}

	if len(doc.Errors) != 1 || doc.Errors[0].Status != "503" ||
		!strings.Contains(doc.Errors[0].Title+doc.Errors[0].Detail, "maintenance") {
		t.Fatalf("GET /api/v2/ping error = %+v, want one 503 naming maintenance", doc)
	}

	leaveMaintenanceMode(t, f)
}

// maintenanceTFEError is one entry of the JSON:API error document the
// TFE-compatible adapter answers in maintenance mode.
type maintenanceTFEError struct {
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// maintenanceTFEDoc is the error document the TFE-compatible adapter
// answers in maintenance mode.
type maintenanceTFEDoc struct {
	Errors []maintenanceTFEError `json:"errors"`
}

// TestMaintenanceGateInventory proves the inventory adapter answers 503 with
// Retry-After and a message that names maintenance, in its own error shape.
func TestMaintenanceGateInventory(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", installationName)

	enterMaintenanceMode(t, f)

	status, header, body := f.doRaw(
		t, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units", "token "+adminToken,
	)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/dustpan/v1/namespaces/{namespace}/units in maintenance answers %d, want %d (%s)",
			status, http.StatusServiceUnavailable, body)
	}

	checkRetryAfter(t, header)

	if got := header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("media type = %q, want the inventory JSON error", got)
	}

	var doc struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode 503 of GET /api/dustpan/v1/namespaces/{namespace}/units: %v (%s)", err, body)
	}

	if doc.Code != http.StatusServiceUnavailable || !strings.Contains(doc.Message, "maintenance") {
		t.Fatalf("GET /api/dustpan/v1/namespaces/{namespace}/units error = %+v, want code 503 naming maintenance", doc)
	}

	leaveMaintenanceMode(t, f)
}

// TestMaintenanceGateAnswersBeforeAuth proves the gate runs before
// authentication on every tool adapter: without any token each one answers
// the 503 refusal that names maintenance, not 401. The discovery document
// stays public in both states, so it is not in this list. Each subtest runs
// its own server, so they run in parallel.
func TestMaintenanceGateAnswersBeforeAuth(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, path, mediaType string
	}{
		{name: "Pulumi", path: "/api/user", mediaType: "application/json"},
		{name: "TFE", path: "/api/v2/ping", mediaType: "application/vnd.api+json"},
		{name: "inventory", path: "/api/dustpan/v1/namespaces/acme/units", mediaType: "application/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.createNamespace(t, "acme", installationName)

			enterMaintenanceMode(t, f)

			status, header, body := f.doRaw(t, http.MethodGet, tc.path, "")

			if status != http.StatusServiceUnavailable {
				t.Fatalf("GET %s without a token in maintenance answers %d, want %d (%s)",
					tc.path, status, http.StatusServiceUnavailable, body)
			}

			checkRetryAfter(t, header)

			if got := header.Get("Content-Type"); got != tc.mediaType {
				t.Fatalf("GET %s media type = %q, want %q", tc.path, got, tc.mediaType)
			}

			if !strings.Contains(body, "maintenance") {
				t.Fatalf("GET %s body %q names no maintenance", tc.path, body)
			}

			leaveMaintenanceMode(t, f)
		})
	}
}

// TestMaintenanceKeepsDiscovery proves service discovery stays available in
// maintenance mode: OpenTofu reads only the service addresses from it, and
// the gated endpoints under /api/v2/ still refuse with 503.
func TestMaintenanceKeepsDiscovery(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", installationName)

	status, _, body := f.doRaw(t, http.MethodGet, "/.well-known/terraform.json", "")
	if status != http.StatusOK || !strings.Contains(body, "tfe.v2") {
		t.Fatalf("GET /.well-known/terraform.json answers %d, want 200 with tfe.v2 (%s)", status, body)
	}

	enterMaintenanceMode(t, f)

	status, _, body = f.doRaw(t, http.MethodGet, "/.well-known/terraform.json", "")
	if status != http.StatusOK || !strings.Contains(body, "tfe.v2") {
		t.Fatalf("GET /.well-known/terraform.json in maintenance answers %d, want 200 with tfe.v2 (%s)",
			status, body)
	}

	status, _, body = f.doRaw(t, http.MethodGet, "/api/v2/ping", "")
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "maintenance") {
		t.Fatalf("GET /api/v2/ping in maintenance answers %d, want 503 naming maintenance (%s)",
			status, body)
	}

	leaveMaintenanceMode(t, f)
}

// TestMaintenanceKeepsServerAPIAndProbes proves the installation API and the
// probes stay available in maintenance mode: the state reads maintenance,
// leaving works through the API, and the probes answer as before.
func TestMaintenanceKeepsServerAPIAndProbes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", installationName)

	if err := f.store.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	status, _, body := f.doRaw(t, http.MethodGet, "/api/dustpan/v1/maintenance", "Bearer "+adminToken)
	if status != http.StatusOK || !strings.Contains(body, `"state":"maintenance"`) {
		t.Fatalf("GET /api/dustpan/v1/maintenance answers %d, want 200 with maintenance (%s)",
			status, body)
	}

	status, _, body = f.doRaw(t, http.MethodGet, "/health/ready", "")
	if status != http.StatusOK {
		t.Fatalf("GET /health/ready in maintenance answers %d, want %d (%s)",
			status, http.StatusOK, body)
	}

	status, _, _ = f.doRaw(t, http.MethodGet, "/health/live", "")
	if status != http.StatusNoContent {
		t.Fatalf("GET /health/live in maintenance answers %d, want %d", status, http.StatusNoContent)
	}

	status, _, body = f.doRaw(t, http.MethodPost, "/api/dustpan/v1/maintenance/leave", "Bearer "+adminToken)
	if status != http.StatusOK || !strings.Contains(body, `"state":"normal"`) {
		t.Fatalf("POST /api/dustpan/v1/maintenance/leave answers %d, want 200 with normal (%s)",
			status, body)
	}

	leaveMaintenanceMode(t, f)
}

// TestMaintenanceEnterThroughAPI proves the installation API enters
// maintenance mode: after POST enter the tool routes refuse with 503.
func TestMaintenanceEnterThroughAPI(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", installationName)

	status, _, body := f.doRaw(t, http.MethodPost, "/api/dustpan/v1/maintenance/enter", "Bearer "+adminToken)
	if status != http.StatusOK || !strings.Contains(body, `"state":"maintenance"`) {
		t.Fatalf("POST /api/dustpan/v1/maintenance/enter answers %d, want 200 with maintenance (%s)",
			status, body)
	}

	waitForToolStatus(t, f, http.StatusServiceUnavailable)

	status, _, body = f.doRaw(t, http.MethodPost, "/api/dustpan/v1/maintenance/leave", "Bearer "+adminToken)
	if status != http.StatusOK {
		t.Fatalf("POST /api/dustpan/v1/maintenance/leave answers %d, want %d (%s)",
			status, http.StatusOK, body)
	}

	leaveMaintenanceMode(t, f)
}

// namespaceOnlyStore serves only the namespace port over an SQLite store. It
// exists to prove NewServer refuses a namespace store without the
// maintenance port: the embedded methods stay hidden behind this type.
type namespaceOnlyStore struct {
	store interface {
		CreateNamespace(ctx context.Context, ns namespace.Namespace) error
		GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
		ListNamespaces(ctx context.Context) ([]namespace.Namespace, error)
		Ping(ctx context.Context) error
		AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error)
		Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error)
		List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
		Check(ctx context.Context, ns namespace.Name) (audit.CheckResult, error)
	}
}

// AppendServer serves the audit log of the server scope, which the workspace
// service needs to record a refused blob key, so the maintenance check stays
// the first to refuse.
func (s namespaceOnlyStore) AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error) {
	return s.store.AppendServer(ctx, e)
}

// Append, List and Check serve the audit log of the namespaces, which the
// server needs before it checks the maintenance port.
func (s namespaceOnlyStore) Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	return s.store.Append(ctx, fence, e)
}

func (s namespaceOnlyStore) List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error) {
	return s.store.List(ctx, ns, page)
}

func (s namespaceOnlyStore) Check(ctx context.Context, ns namespace.Name) (audit.CheckResult, error) {
	return s.store.Check(ctx, ns)
}

func (s namespaceOnlyStore) CreateNamespace(ctx context.Context, ns namespace.Namespace) error {
	return s.store.CreateNamespace(ctx, ns)
}

func (s namespaceOnlyStore) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	return s.store.GetNamespace(ctx, name)
}

func (s namespaceOnlyStore) ListNamespaces(ctx context.Context) ([]namespace.Namespace, error) {
	return s.store.ListNamespaces(ctx)
}

func (s namespaceOnlyStore) Ping(ctx context.Context) error {
	return s.store.Ping(ctx)
}

// TestNewServerRequiresMaintenanceStore proves NewServer refuses a namespace
// store that cannot keep the installation state, so no instance serves tool
// calls without the maintenance gate.
func TestNewServerRequiresMaintenanceStore(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)

	_, err := dustpan.NewServer(
		dustpan.WithInstallation(installationName),
		dustpan.WithNamespaceStore(namespaceOnlyStore{store: st}),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(testWrapper(t)),
		dustpan.WithAuthenticator(authn),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
	)
	if err == nil {
		t.Fatal("NewServer without the maintenance port succeeded")
	}

	if !strings.Contains(err.Error(), "installation state") {
		t.Fatalf("NewServer without the maintenance port: err = %v, want it to name the installation state", err)
	}
}

// SetOverrides is not used by this fake.
func (namespaceOnlyStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}
