package registry_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	apiregistry "opendustpan.org/opendustpan/internal/api/registry"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry/pgpverify"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// guardFixture serves the registry API with a blob store that the test can
// list, so a test can prove that a refused upload stages no bytes.
type guardFixture struct {
	served http.Handler
	blobs  blob.Store
	nsID   namespace.ID
}

func newGuardFixture(t *testing.T, deny ...identity.Action) guardFixture {
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

	rec, err := namespaces.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	blobs, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blobs: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blobs: %v", closeErr)
		}
	})

	svc, err := service.New(service.Config{
		Store: store.Registry(), Keys: store.RegistryKeys(), Verifier: pgpverify.Verifier{Now: nil},
		Inspector: pgpverify.Verifier{Now: nil}, Blobs: blobs, Namespaces: store, Audit: store,
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

	handler, err := apiregistry.NewHandler(apiregistry.Config{
		Service: svc, Authenticator: testAuthn{}, Authorizer: testAuthz{deny: denied}, MaxUploadBytes: maxUpload,
		Audit: store, Namespaces: namespaces, OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil,
		Idempotency: nil, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return guardFixture{served: apidesctest.CheckStatus(t, handler), blobs: blobs, nsID: rec.ID}
}

// stage uploads body to the uploads route with the given token, media type
// and content length. A negative length sends the body chunked.
func (f guardFixture) stage(
	t *testing.T, token, mediaType string, body []byte, length int64,
) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, base+"/uploads", bytes.NewReader(body))
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", mediaType)

	if length >= 0 {
		req.ContentLength = length
	} else {
		req.ContentLength = -1
	}

	rec := httptest.NewRecorder()
	f.served.ServeHTTP(rec, req)

	answer, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read the answer: %v", err)
	}

	return rec.Code, string(answer)
}

// staged lists the staged blobs of the namespace.
func (f guardFixture) staged(t *testing.T) []string {
	t.Helper()

	objects, err := blobtest.ListAll(context.Background(), f.blobs, "namespaces/"+f.nsID.String()+"/registry/staging")
	if err != nil {
		t.Fatalf("list staged blobs: %v", err)
	}

	out := make([]string, 0, len(objects))
	for _, o := range objects {
		out = append(out, o.Key.String())
	}

	return out
}

// TestPublish_handlerGuards checks that the uploads route refuses in the
// order 403 then 415 then 413, and stages no bytes for a refused upload. It
// fails on main: oversize and wrong type reach unpack.
func TestPublish_handlerGuards(t *testing.T) {
	t.Parallel()

	big := make([]byte, 2*maxUpload)

	f := newGuardFixture(t)

	if code, _ := f.stage(t, "reader", "text/plain", big, int64(len(big))); code != http.StatusForbidden {
		t.Fatalf("denied principal with wrong type and oversize body: got %d, want %d", code, http.StatusForbidden)
	}

	if code, _ := f.stage(t, "publisher", "text/plain", big, int64(len(big))); code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong type with oversize body: got %d, want %d", code, http.StatusUnsupportedMediaType)
	}

	chunkedCode, _ := f.stage(t, "publisher", "application/octet-stream", big, -1)
	if chunkedCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked oversize body: got %d, want %d", chunkedCode, http.StatusRequestEntityTooLarge)
	}

	if left := f.staged(t); len(left) != 0 {
		t.Fatalf("staged blobs after refused uploads: %v", left)
	}

	// Defense in depth: the uploads route takes archives as zip files. A zip
	// body with the zip media type is staged, not refused as a wrong type.
	if code, answer := f.stage(t, "publisher", "application/zip", []byte("PK"), 2); code != http.StatusCreated {
		t.Fatalf("zip body with zip media type: got %d %s, want %d", code, answer, http.StatusCreated)
	}
}
