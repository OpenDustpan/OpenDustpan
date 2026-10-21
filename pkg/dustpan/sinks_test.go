package dustpan_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const sinkBase = "/api/dustpan/v1/namespaces/acme/sinks"

// webhookSender is a notify.Sender for the kind webhook that records what it
// gets.
type webhookSender struct {
	mu   sync.Mutex
	sent []notify.Message
}

func (*webhookSender) Kind() notify.SinkKind { return notify.SinkWebhook }

func (w *webhookSender) Send(_ context.Context, m notify.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	m.Secrets = [][]byte{append([]byte(nil), m.Secrets[0]...)}
	w.sent = append(w.sent, m)

	return nil
}

func (w *webhookSender) got() []notify.Message {
	w.mu.Lock()
	defer w.mu.Unlock()

	return append([]notify.Message(nil), w.sent...)
}

// sinkServer starts a server over a fresh SQLite database. withStore sets the
// sink store; extra adds options.
func sinkServer(t *testing.T, withStore bool, extra ...dustpan.Option) fixture {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	opts := []dustpan.Option{
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 21, 9, 0, 0, 0, time.UTC) }),
	}
	if withStore {
		opts = append(opts, dustpan.WithSinkStore(st.NotificationSinks()))
	}

	srv, err := dustpan.NewServer(append(opts, extra...)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	f := fixture{store: st, server: ts, srv: srv}
	f.createNamespace(t, "acme", region)

	return f
}

// doJSON sends a request with the admin token and decodes the answer.
func (f fixture) doJSON(t *testing.T, method, path, key, body string) (int, map[string]any) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "token "+adminToken)

	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}

	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	out := map[string]any{}
	if len(data) > 0 {
		if err = json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode %q: %v", data, err)
		}
	}

	return resp.StatusCode, out
}

func TestSinkRoutesNeedTheStore(t *testing.T) {
	t.Parallel()

	f := sinkServer(t, false)

	code, _ := f.doJSON(t, http.MethodGet, sinkBase, "", "")
	if code != http.StatusNotFound {
		t.Fatalf("GET without a sink store = %d, want 404", code)
	}
}

func TestSinkRoutesWithTheBuiltInSender(t *testing.T) {
	t.Parallel()

	f := sinkServer(t, true)

	// The outbound rules refuse a webhook on the machine of the server.
	code, body := f.doJSON(t, http.MethodPost, sinkBase, "",
		`{"kind":"webhook","name":"Local","target":"https://127.0.0.1:8443/hook"}`)
	if code != http.StatusBadRequest || body["errorCode"] != "sink.invalid" {
		t.Fatalf("create on loopback = %d %v, want 400 sink.invalid", code, body)
	}

	code, made := f.doJSON(t, http.MethodPost, sinkBase, "",
		`{"kind":"webhook","name":"Ops","target":"https://hooks.example.invalid/dustpan"}`)
	if code != http.StatusCreated || len(made["signingKey"].(string)) != 64 {
		t.Fatalf("create = %d %v, want 201 with a signing key", code, made)
	}

	id, _ := made["id"].(string)

	// The built-in signed webhook sender serves the test route. The host name
	// does not resolve, so the message does not arrive: a temporary result.
	code, res := f.doJSON(t, http.MethodPost, sinkBase+"/"+id+"/test", "", "")
	if code != http.StatusOK || res["delivered"] != false || res["class"] != "temporary" {
		t.Fatalf("test = %d %v, want 200 with the class temporary", code, res)
	}

	if text, _ := json.Marshal(res); strings.Contains(string(text), "example.invalid") {
		t.Fatalf("test answer %s holds the host", text)
	}

	code, list := f.doJSON(t, http.MethodGet, sinkBase, "", "")
	if items, _ := list["sinks"].([]any); code != http.StatusOK || len(items) != 1 {
		t.Fatalf("list = %d %v, want one sink", code, list)
	}

	if code, _ = f.doJSON(t, http.MethodDelete, sinkBase+"/"+id, "", ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", code)
	}
}

func TestSinkSenderOptionReplacesTheBuiltInSender(t *testing.T) {
	t.Parallel()

	hook := &webhookSender{mu: sync.Mutex{}, sent: nil}
	f := sinkServer(t, true, dustpan.WithSinkSenders(hook))

	_, made := f.doJSON(t, http.MethodPost, sinkBase, "",
		`{"kind":"webhook","name":"Ops","target":"https://hooks.example.com/dustpan"}`)
	id, _ := made["id"].(string)
	key, _ := made["signingKey"].(string)

	code, res := f.doJSON(t, http.MethodPost, sinkBase+"/"+id+"/test", "", "")
	if code != http.StatusOK || res["delivered"] != true {
		t.Fatalf("test = %d %v, want 200 delivered", code, res)
	}

	sent := hook.got()
	if len(sent) != 1 || sent[0].To != "https://hooks.example.com/dustpan" || string(sent[0].Secrets[0]) != key ||
		sent[0].Notification.Type != notify.SinkTest {
		t.Fatalf("sender got %+v, want one sink.test message to the target with the signing key", sent)
	}

	// The server seals the key: the store holds no plaintext of it.
	sealed, err := f.store.NotificationSinks().SinkSecrets(t.Context(), "acme", id)
	if err != nil || len(sealed) != 1 || strings.Contains(string(sealed[0].Ciphertext()), key) {
		t.Fatalf("stored secret = %d items (%v), want one sealed item", len(sealed), err)
	}

	// The audit log of the namespace names the actions, without secrets.
	listing, err := f.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	var actions []string

	for _, e := range listing.Entries {
		actions = append(actions, e.Action)

		if strings.Contains(fmt.Sprintf("%+v", e), key) {
			t.Fatalf("audit entry %+v holds the signing key", e)
		}
	}

	if !slices.Contains(actions, "sink:create") || !slices.Contains(actions, "sink:test") {
		t.Fatalf("audit actions = %v, want sink:create and sink:test", actions)
	}
}
