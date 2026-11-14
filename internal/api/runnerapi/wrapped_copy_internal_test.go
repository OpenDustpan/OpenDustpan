package runnerapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// errUnknownDriver is an error that no case of a switch knows.
var errUnknownDriver = errors.New("driver: password=hunter2")

// wrappedCase is one error of a scope with the status of its answer. The
// message of the answer is the sentence of the sentinel.
type wrappedCase struct {
	name     string
	scope    Scope
	sentinel error
	status   int
}

// answerMessage returns the message of the JSON body of an answer. The runner
// protocol and the management API both use the "message" key.
func answerMessage(t *testing.T, body string) string {
	t.Helper()

	var got struct {
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("body %q is not JSON: %v", body, err)
	}

	return got.Message
}

// TestWriteErrorWrappedCopy proves that every case of the protocol switch and
// of the management switch answers the sentence of the sentinel and nothing
// else, also when code wraps the sentinel once or twice. The wrap text stays in
// the log of a Warn refusal only: a Debug refusal logs no text. An error that no
// case knows answers "internal error".
func TestWriteErrorWrappedCopy(t *testing.T) {
	t.Parallel()

	cases := []wrappedCase{
		{"protocol bad proof", ScopeRunnerKey, runner.ErrBadProof, http.StatusUnauthorized},
		{"protocol replay", ScopeRunnerKey, runner.ErrReplay, http.StatusUnauthorized},
		{"protocol key expired", ScopeRunnerKey, runner.ErrKeyExpired, http.StatusUnauthorized},
		{"protocol retired", ScopeRunnerKey, runner.ErrRetired, http.StatusForbidden},
		{"protocol token used", ScopeRunnerKey, runner.ErrTokenUsed, http.StatusUnauthorized},
		{"protocol token expired", ScopeRunnerKey, runner.ErrTokenExpired, http.StatusUnauthorized},
		{"protocol version unsupported", ScopeRunnerKey, runner.ErrVersionUnsupported, http.StatusUpgradeRequired},
		{"protocol version ahead", ScopeRunnerKey, runner.ErrVersionAhead, http.StatusUpgradeRequired},
		{"protocol runner claim lost", ScopeRunnerKey, runner.ErrClaimLost, http.StatusConflict},
		{"protocol durable claim lost", ScopeRunnerKey, durable.ErrClaimLost, http.StatusConflict},
		{"protocol job canceled", ScopeRunnerKey, job.ErrCanceled, http.StatusConflict},
		{"protocol durable canceled", ScopeRunnerKey, durable.ErrCanceled, http.StatusConflict},
		{"protocol conflict", ScopeRunnerKey, runner.ErrConflict, http.StatusConflict},
		{"protocol runner online", ScopeRunnerKey, runner.ErrRunnerOnline, http.StatusConflict},
		{"protocol runner not found", ScopeRunnerKey, runner.ErrNotFound, http.StatusNotFound},
		{"protocol durable not found", ScopeRunnerKey, durable.ErrNotFound, http.StatusNotFound},
		{"protocol namespace not found", ScopeRunnerKey, namespace.ErrNotFound, http.StatusNotFound},
		{"protocol invalid", ScopeRunnerKey, runner.ErrInvalid, http.StatusBadRequest},
		{"protocol bad body", ScopeRunnerKey, errBadBody, http.StatusBadRequest},
		{"protocol limit", ScopeRunnerKey, runner.ErrLimit, http.StatusTooManyRequests},
		{"protocol not home", ScopeRunnerKey, namespace.ErrNotHome, http.StatusServiceUnavailable},
		{"management csrf", ScopeAuthorized, sessionauth.ErrCSRF, http.StatusForbidden},
		{"management other site", ScopeAuthorized, sessionauth.ErrOtherSite, http.StatusForbidden},
		{"management invalid", ScopeAuthorized, runner.ErrInvalid, http.StatusBadRequest},
		{"management bad body", ScopeAuthorized, errBadBody, http.StatusBadRequest},
		{"management immutable", ScopeAuthorized, runner.ErrImmutable, http.StatusConflict},
		{"management pool in use", ScopeAuthorized, runner.ErrPoolInUse, http.StatusConflict},
		{"management token used", ScopeAuthorized, runner.ErrTokenUsed, http.StatusConflict},
		{"management limit", ScopeAuthorized, runner.ErrLimit, http.StatusConflict},
	}

	for _, tc := range cases {
		forms := []struct {
			name string
			err  error
			wrap string
		}{
			{"sentinel", tc.sentinel, ""},
			{"wrapped", fmt.Errorf("claim job 7: %w", tc.sentinel), "claim job 7"},
			{"wrapped twice", fmt.Errorf("poll: %w", fmt.Errorf("lease row 9: %w", tc.sentinel)), "lease row 9"},
		}

		for _, f := range forms {
			t.Run(tc.name+"/"+f.name, func(t *testing.T) {
				t.Parallel()

				checkWrappedAnswer(t, tc, f.err, f.wrap)
			})
		}
	}
}

// checkWrappedAnswer proves the answer to err: the sentence of the sentinel in
// the body, and the wrap text in the log only.
func checkWrappedAnswer(t *testing.T, tc wrappedCase, err error, wrap string) {
	t.Helper()

	rec, logged := writeErrorOf(t, tc.scope, err)

	if rec.Code != tc.status {
		t.Fatalf("status = %d, want %d", rec.Code, tc.status)
	}

	if got, want := answerMessage(t, rec.Body.String()), tc.sentinel.Error(); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}

	if wrap == "" {
		return
	}

	if strings.Contains(rec.Body.String(), wrap) {
		t.Fatalf("body = %q, must not hold the wrap text %q", rec.Body.String(), wrap)
	}

	// A refusal that an anonymous caller can cause logs its line at Debug without
	// the wrap text. Defense in depth: this check stays so that a new Debug case
	// cannot log the text of its error.
	if refusedLevel(err) == slog.LevelDebug {
		if strings.Contains(logged, wrap) {
			t.Fatalf("log = %q, must not hold the wrap text %q", logged, wrap)
		}

		return
	}

	if !strings.Contains(logged, wrap) {
		t.Fatalf("log = %q, want the wrap text %q", logged, wrap)
	}
}

// TestWriteErrorUnknownAnswer proves the negative row: an error that no case
// knows answers "internal error" in both scopes, with no text of the error in
// the body.
func TestWriteErrorUnknownAnswer(t *testing.T) {
	t.Parallel()

	for _, scope := range []Scope{ScopeRunnerKey, ScopeAuthorized} {
		t.Run(fmt.Sprint(scope), func(t *testing.T) {
			t.Parallel()

			rec, logged := writeErrorOf(t, scope, errUnknownDriver)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
			}

			if got := answerMessage(t, rec.Body.String()); got != internalMessage {
				t.Fatalf("message = %q, want %q", got, internalMessage)
			}

			if strings.Contains(logged, "hunter2") {
				t.Fatalf("log = %q, must not hold the text of the unknown error", logged)
			}
		})
	}
}

// TestWriteErrorBareSentinelNoRefusedLine proves that a bare sentinel, which
// the answer holds whole, writes no "runner request refused" line.
func TestWriteErrorBareSentinelNoRefusedLine(t *testing.T) {
	t.Parallel()

	for _, scope := range []Scope{ScopeRunnerKey, ScopeAuthorized} {
		t.Run(fmt.Sprint(scope), func(t *testing.T) {
			t.Parallel()

			_, logged := writeErrorOf(t, scope, errBadBody)

			if strings.Contains(logged, "runner request refused") {
				t.Fatalf("log = %q, must hold no refused line", logged)
			}
		})
	}
}

// TestRefusedLevel proves the level of a refusal: Debug for an error that an
// anonymous caller can cause, Warn for the others.
func TestRefusedLevel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want slog.Level
	}{
		{"unauthenticated", fmt.Errorf("sign-in: %w", identity.ErrUnauthenticated), slog.LevelDebug},
		{"forged proof", fmt.Errorf("verify: %w", runner.ErrBadProof), slog.LevelDebug},
		{"unknown runner", fmt.Errorf("lookup: %w", runner.ErrNotFound), slog.LevelDebug},
		{"replay", fmt.Errorf("verify: %w", runner.ErrReplay), slog.LevelWarn},
		{"claim lost", fmt.Errorf("claim: %w", runner.ErrClaimLost), slog.LevelWarn},
		{"invalid", fmt.Errorf("body: %w", runner.ErrInvalid), slog.LevelWarn},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := refusedLevel(tc.err); got != tc.want {
				t.Fatalf("level = %v, want %v", got, tc.want)
			}

			var (
				h   Handler
				buf bytes.Buffer
			)

			h.logger = slog.New(slog.NewTextHandler(&buf, nil))
			h.logRefused(t.Context(), tc.err, http.StatusBadRequest, "test.code", "sent text")

			if got := strings.Contains(buf.String(), "runner request refused"); got != (tc.want == slog.LevelWarn) {
				t.Fatalf("log = %q, refused line at default level = %t", buf.String(), got)
			}
		})
	}
}
