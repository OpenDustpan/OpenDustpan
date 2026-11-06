package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// The limits of the begin of a register ceremony, for the tests.
const (
	PasskeyBeginLimitForTest  = passkeyBeginLimit
	PasskeyBeginWindowForTest = passkeyBeginWindow
	PasskeyBeginPeopleForTest = passkeyBeginPeople
	MaxPasskeyBodyForTest     = maxPasskeyBody
)

// BeginLimiterForTest is the limiter of the begins of a register ceremony.
type BeginLimiterForTest struct {
	limiter *beginLimiter
}

// NewBeginLimiterForTest returns an empty limiter.
func NewBeginLimiterForTest() *BeginLimiterForTest {
	return &BeginLimiterForTest{limiter: newBeginLimiter()}
}

// Allow counts one begin of the person at now.
func (b *BeginLimiterForTest) Allow(person string, now time.Time) (time.Duration, bool) {
	return b.limiter.allow(person, now)
}

// PasskeyOwnerForTest names a passkey route (begin, finish, list or remove)
// and the owner of the console session that calls it.
type PasskeyOwnerForTest struct {
	Route string
	Kind  principal.Kind
	ID    string
}

// ServePasskeyAsOwnerForTest calls the handler of the passkey route of o
// directly, as the console session of its owner. The route table, the policy
// check and the step-up check are not in the way, so the test reaches the
// check of the session owner alone.
func ServePasskeyAsOwnerForTest(
	t *testing.T, o PasskeyOwnerForTest, entries audit.ServerStore, svc PasskeyService,
) *httptest.ResponseRecorder {
	t.Helper()

	var h Handler

	h.audit, h.logger, h.now, h.passkeys, h.passkeyBegins = entries, slog.New(slog.DiscardHandler), time.Now, svc,
		newBeginLimiter()

	method, body := http.MethodPost, `{"name":"laptop","response":{}}`

	switch o.Route {
	case "begin":
		body = ""
	case "list":
		method, body = http.MethodGet, ""
	case "remove":
		method, body = http.MethodDelete, ""
	default:
	}

	req := httptest.NewRequestWithContext(t.Context(), method, "/api/dustpan/v1/me/passkeys", strings.NewReader(body))
	req.SetPathValue("id", "pky-fake")

	var caller sessionCaller

	caller.Match.Owner.ID, caller.Match.Owner.Kind, caller.Match.Owner.Subject = o.ID, o.Kind, "someone"
	req = req.WithContext(context.WithValue(req.Context(), sessionKey{}, caller))

	var p identity.Principal

	p.Subject = "someone"

	rec := httptest.NewRecorder()
	obj := identity.CallerObject()

	switch o.Route {
	case "begin":
		h.beginPasskey(rec, req, p, obj)
	case "finish":
		h.finishPasskey(rec, req, p, obj)
	case "list":
		h.listPasskeys(rec, req, p, obj)
	default:
		h.removePasskey(rec, req, p, obj)
	}

	return rec
}
