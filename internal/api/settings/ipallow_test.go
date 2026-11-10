package settings_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/settings"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/unitsettingssvc"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// addressRefused refuses every check as the IP allowlist gate does for a
// client address outside the allowlist of the namespace.
type addressRefused struct{}

func (addressRefused) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return errors.Join(identity.ErrDenied, ipallow.ErrAddressNotAllowed)
}

func (addressRefused) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoList
}

// newAddressHandler returns a handler whose authorizer refuses every check as
// an address outside the IP allowlist of the namespace does.
func newAddressHandler(t *testing.T) *settings.Handler {
	t.Helper()

	records := idempotencytest.NewMemory()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	svc, err := unitsettingssvc.New(unitsettingssvc.Config{
		Store: newMemStore(records), Fences: fences{}, Connections: connections{}, Pools: pools{},
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	idem, err := idempotent.New(idempotent.Config{
		Store: records, Logger: slog.New(slog.DiscardHandler), Now: nil, MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("idempotency middleware: %v", err)
	}

	handler, err := settings.NewHandler(settings.Config{
		Idempotency: idem, Records: records,
		Namespaces: namespaces, Units: fakeInventory{}, Service: svc, Suggestions: suggester{err: nil},
		Authenticator: tokenAuthenticator{}, Authorizer: addressRefused{},
		Audit: &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}, OnAuditFailure: nil,
		DenialLimiter: nil, Sessions: nil, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// TestAddressOutsideTheAllowlistAnswers403 proves that a read of the unit
// settings answers 403 with access.ip-not-allowed for a client address outside
// the IP allowlist, as the other routes of the namespace do, and not the 404
// of a unit that the caller cannot read. The refusal does not tell whether the
// namespace or the unit exists.
func TestAddressOutsideTheAllowlistAnswers403(t *testing.T) {
	t.Parallel()

	const base = "/api/dustpan/v1/namespaces/"

	handler := newAddressHandler(t)

	for _, path := range []string{
		base + "acme/units/" + netID + "/settings",
		base + "acme/units/ws-absent/settings",
		base + "absent/units/" + netID + "/settings",
		base + "acme/units/" + strings.Repeat("x", 101) + "/settings",
	} {
		rec := serve(t, handler, request{method: http.MethodGet, path: path, token: testToken, body: "", header: nil})
		errcodetest.Check(t, rec.Code, rec.Body.Bytes(), errcodetest.Cause{
			Name: "read " + path, Err: nil, Code: errcode.AccessIPNotAllowed, Message: "forbidden", Fixed: true,
		})

		for _, named := range []string{"acme", "absent", netID, "ws-absent"} {
			if strings.Contains(rec.Body.String(), named) {
				t.Errorf("read %s: the refusal names %q: %s", path, named, rec.Body.String())
			}
		}
	}
}
