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

// sinkServer starts a server over a fresh SQLite database with the sink store
// of the database. extra adds options.
func sinkServer(t *testing.T, extra ...dustpan.Option) fixture {
	t.Helper()

	st := openSinkDatabase(t)

	return startSinkServer(t, st, append([]dustpan.Option{dustpan.WithSinkStore(st.NotificationSinks())}, extra...)...)
}

// plainServer starts the same server with no sink store.
func plainServer(t *testing.T) fixture {
	t.Helper()

	return startSinkServer(t, openSinkDatabase(t))
}

// openSinkDatabase opens a fresh SQLite database.
func openSinkDatabase(t *testing.T) *sqlite.Store {
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

	return st
}

// startSinkServer starts a server over st with the options extra.
func startSinkServer(t *testing.T, st *sqlite.Store, extra ...dustpan.Option) fixture {
	t.Helper()

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
	srv, err := dustpan.NewServer(append(opts, extra...)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	f := fixture{store: st, server: ts, srv: srv}
	f.createNamespace(t, "acme", region)

	return f
}

// doInto sends a request with the admin token and decodes the answer into out.
// It returns the status.
func (f fixture) doInto(t *testing.T, method, path, body string, out any) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "token "+adminToken)

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

	if len(data) > 0 && out != nil {
		if err = json.Unmarshal(data, out); err != nil {
			t.Fatalf("decode %q: %v", data, err)
		}
	}

	return resp.StatusCode
}

// sinkWire is the part of an answer that the tests read.
type sinkWire struct {
	ID         string `json:"id"`
	SigningKey string `json:"signingKey"`
	ErrorCode  string `json:"errorCode"`
	Delivered  bool   `json:"delivered"`
	Class      string `json:"class"`
}

// listWire is the answer of the list.
type listWire struct {
	Sinks []sinkWire `json:"sinks"`
}

func TestSinkRoutesNeedTheStore(t *testing.T) {
	t.Parallel()

	f := plainServer(t)

	if code := f.doInto(t, http.MethodGet, sinkBase, "", nil); code != http.StatusNotFound {
		t.Fatalf("GET without a sink store = %d, want 404", code)
	}
}

func TestSinkRoutesWithTheBuiltInSender(t *testing.T) {
	t.Parallel()

	f := sinkServer(t)

	// The outbound rules refuse a webhook on the machine of the server.
	var refused sinkWire

	code := f.doInto(t, http.MethodPost, sinkBase,
		`{"kind":"webhook","name":"Local","target":"https://127.0.0.1:8443/hook"}`, &refused)
	if code != http.StatusBadRequest || refused.ErrorCode != "sink.invalid" {
		t.Fatalf("create on loopback = %d %+v, want 400 sink.invalid", code, refused)
	}

	var made sinkWire

	code = f.doInto(t, http.MethodPost, sinkBase,
		`{"kind":"webhook","name":"Ops","target":"https://hooks.example.invalid/dustpan"}`, &made)
	if code != http.StatusCreated || len(made.SigningKey) != 64 {
		t.Fatalf("create = %d %+v, want 201 with a signing key", code, made)
	}

	// The built-in signed webhook sender serves the test route. The host name
	// does not resolve, so the message does not arrive: a temporary result.
	var res sinkWire

	code = f.doInto(t, http.MethodPost, sinkBase+"/"+made.ID+"/test", "", &res)
	if code != http.StatusOK || res.Delivered || res.Class != "temporary" {
		t.Fatalf("test = %d %+v, want 200 with the class temporary", code, res)
	}

	var list listWire

	if code = f.doInto(t, http.MethodGet, sinkBase, "", &list); code != http.StatusOK || len(list.Sinks) != 1 {
		t.Fatalf("list = %d %+v, want one sink", code, list)
	}

	if code = f.doInto(t, http.MethodDelete, sinkBase+"/"+made.ID, "", nil); code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", code)
	}
}

func TestSinkSenderOptionReplacesTheBuiltInSender(t *testing.T) {
	t.Parallel()

	hook := &webhookSender{mu: sync.Mutex{}, sent: nil}
	f := sinkServer(t, dustpan.WithSinkSenders(hook))

	var made sinkWire

	f.doInto(t, http.MethodPost, sinkBase,
		`{"kind":"webhook","name":"Ops","target":"https://hooks.example.com/dustpan"}`, &made)

	var res sinkWire

	code := f.doInto(t, http.MethodPost, sinkBase+"/"+made.ID+"/test", "", &res)
	if code != http.StatusOK || !res.Delivered {
		t.Fatalf("test = %d %+v, want 200 delivered", code, res)
	}

	sent := hook.got()
	if len(sent) != 1 || sent[0].To != "https://hooks.example.com/dustpan" ||
		string(sent[0].Secrets[0]) != made.SigningKey || sent[0].Notification.Type != notify.SinkTest {
		t.Fatalf("sender got %+v, want one sink.test message to the target with the signing key", sent)
	}

	// The server seals the key: the store holds no plaintext of it.
	sealed, err := f.store.NotificationSinks().SinkSecrets(t.Context(), "acme", made.ID)
	if err != nil || len(sealed) != 2 {
		t.Fatalf("stored secrets = %d items (%v), want the sealed URL and the sealed key", len(sealed), err)
	}

	for _, item := range sealed {
		if strings.Contains(string(item.Ciphertext()), made.SigningKey) ||
			strings.Contains(string(item.Ciphertext()), "hooks.example.com/dustpan") {
			t.Fatal("the store holds the key or the URL in plaintext")
		}
	}

	checkSinkAudit(t, f, made.SigningKey)
}

// checkSinkAudit checks that the audit log of the namespace names the create
// and the test of a sink, and holds no copy of the signing key.
func checkSinkAudit(t *testing.T, f fixture, key string) {
	t.Helper()

	listing, err := f.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	actions := make([]string, 0, len(listing.Entries))

	for i := range listing.Entries {
		actions = append(actions, listing.Entries[i].Action)

		if strings.Contains(fmt.Sprintf("%+v", listing.Entries[i]), key) {
			t.Fatalf("audit entry %+v holds the signing key", listing.Entries[i])
		}
	}

	if !slices.Contains(actions, "sink:create") || !slices.Contains(actions, "sink:test") {
		t.Fatalf("audit actions = %v, want sink:create and sink:test", actions)
	}
}
