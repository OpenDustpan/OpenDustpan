package server_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
)

// The tests of this file send hostile input to the checks of the agent
// routes and of the egress allowlist route. Each refusal keeps the text of
// the check that refused it: the text proves which layer refused the input,
// so a test fails when only that check is removed and the layer behind it
// answers instead.

// handlerRefusalCase is one hostile request that a check of the handler
// layer refuses with a bad request.
type handlerRefusalCase struct {
	method string
	target string
	body   string
	// wantCode is the code of the catalog that the refusal carries.
	wantCode string
	// wantText is the part of the refusal text that the check of the
	// handler writes and that the layer behind it does not.
	wantText string
}

// wantHandlerRefusal serves the request of tc and checks the refusal that
// the handler layer gives: the bad request, the code, the text that tc
// names, and one denied audit entry. It returns the refusal text.
func wantHandlerRefusal(t *testing.T, f agentFixture, tc handlerRefusalCase) string {
	t.Helper()

	before := len(f.audit.entries)

	rec := serveAgent(t, f.handler, tc.method, tc.target, tc.body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("%s %s answers %d, want %d: %s",
			tc.method, tc.target, rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	got := decodeErrorBody(t, rec)
	if got.ErrorCode != tc.wantCode {
		t.Fatalf("%s %s errorCode = %q, want %q: %s",
			tc.method, tc.target, got.ErrorCode, tc.wantCode, rec.Body.String())
	}

	if !strings.Contains(got.Message, tc.wantText) {
		t.Fatalf("%s %s message = %q, want the text of the handler check: %s",
			tc.method, tc.target, got.Message, tc.wantText)
	}

	if len(f.audit.entries) != before+1 {
		t.Fatalf("%s %s wrote %d audit entries, want the one denial", tc.method, tc.target, len(f.audit.entries)-before)
	}

	if last := f.audit.entries[len(f.audit.entries)-1]; last.Outcome != audit.OutcomeDenied {
		t.Fatalf("%s %s denial outcome = %q, want denied", tc.method, tc.target, last.Outcome)
	}

	return got.Message
}

// TestSecretRoutes_HandlerRefusesBadItem sends a secret item with an empty
// value to the create and the replace route. The check of the handler refuses
// them before the service: the answer keeps the text of the value rule, and
// the refusal of the service names no value rule. Each subtest builds its own
// fixture, so the count of the audit entries is its own.
func TestSecretRoutes_HandlerRefusesBadItem(t *testing.T) {
	t.Parallel()

	t.Run("create", func(t *testing.T) {
		t.Parallel()

		f := allowAgent(t)

		wantHandlerRefusal(t, f, handlerRefusalCase{
			method: http.MethodPost, target: "/api/agent/secrets", body: `{"name":"api-key","value":""}`,
			wantCode: "agent-connection.secret-invalid", wantText: "the value of a secret item is empty",
		})
	})

	t.Run("replace", func(t *testing.T) {
		t.Parallel()

		f := allowAgent(t)

		created, _ := createAgentItem(t, f, "guard-test-name", "guard-test-value-1")

		wantHandlerRefusal(t, f, handlerRefusalCase{
			method: http.MethodPut, target: "/api/agent/secrets/" + created.Ref, body: `{"value":""}`,
			wantCode: "agent-connection.secret-invalid", wantText: "the value of a secret item is empty",
		})
	})
}

// TestEgressAllowlistRoutes_HandlerRefusesTheRule sends the hostile bodies to
// the change of the egress allowlist. Each refusal comes from a check of the
// handler, and each answer keeps the text of that check. Each subtest builds
// its own fixture, so the count of the audit entries is its own.
func TestEgressAllowlistRoutes_HandlerRefusesTheRule(t *testing.T) {
	t.Parallel()

	t.Run("body without both lists", func(t *testing.T) {
		t.Parallel()

		f := allowAgent(t)

		// The conversion of the input refuses a null list too; the check of
		// the handler refuses it first, and its answer keeps the text of the
		// body error.
		wantHandlerRefusal(t, f, handlerRefusalCase{
			method: http.MethodPut, target: "/api/server/egress-allowlist", body: `{"hosts":null,"cidrs":null}`,
			wantCode: "agent-connection.allowlist-invalid", wantText: "invalid egress allowlist body",
		})
	})

	t.Run("rule that the allowlist rules refuse", func(t *testing.T) {
		t.Parallel()

		f := allowAgent(t)

		// The rule breaks the allowlist rules and the range rule at once.
		// The check of the allowlist rules runs first, so the answer names
		// the port. The check of the range, the service and the store each
		// name the range or wrap the text, so this answer proves which layer
		// refused.
		wantHandlerRefusal(t, f, handlerRefusalCase{
			method: http.MethodPut, target: "/api/server/egress-allowlist",
			body:     `{"hosts":[],"cidrs":[{"cidr":"169.254.0.0/16","port":0,"http":false}]}`,
			wantCode: "agent-connection.allowlist-invalid", wantText: "the port 0 of the rule 169.254.0.0/16",
		})
	})

	t.Run("rule with host bits", func(t *testing.T) {
		t.Parallel()

		f := allowAgent(t)

		wantHandlerRefusal(t, f, handlerRefusalCase{
			method: http.MethodPut, target: "/api/server/egress-allowlist",
			body:     `{"hosts":[],"cidrs":[{"cidr":"10.20.5.5/16","port":443,"http":false}]}`,
			wantCode: "agent-connection.allowlist-invalid", wantText: "host bits set",
		})
	})

	t.Run("rule in a range that no rule opens", func(t *testing.T) {
		t.Parallel()

		f := allowAgent(t)

		message := wantHandlerRefusal(t, f, handlerRefusalCase{
			method: http.MethodPut, target: "/api/server/egress-allowlist",
			body:     `{"hosts":[],"cidrs":[{"cidr":"169.254.0.0/16","port":443,"http":false}]}`,
			wantCode: "agent-connection.allowlist-invalid", wantText: "starts in an address that no range opens",
		})

		// The service refuses the same range and the route maps its error to
		// the same code, but the mapped text carries the wrapper of the
		// service; the answer of the check of the handler carries the text of
		// the range rule alone.
		if strings.Contains(message, "agent request is not valid") {
			t.Fatalf("message = %q, want the text of the check of the handler", message)
		}
	})
}
