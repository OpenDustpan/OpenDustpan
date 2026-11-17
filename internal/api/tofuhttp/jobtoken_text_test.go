package tofuhttp_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/pkg/identity"
)

// endedJobToken is a job token whose claim ended.
const endedJobToken = "job-token-ended-0123"

// endedAuth answers endedJobToken with the error of a job token that ended,
// and every other token as tokenAuth does.
type endedAuth struct{}

func (endedAuth) AuthenticateToken(ctx context.Context, token string) (identity.Principal, error) {
	if token == endedJobToken {
		return identity.Principal{}, fmt.Errorf("authenticate: %w: the claim was taken over", identity.ErrJobTokenEnded)
	}

	p, err := tokenAuth{}.AuthenticateToken(ctx, token)
	if err != nil {
		return identity.Principal{}, fmt.Errorf("authenticate: %w", err)
	}

	return p, nil
}

// otherUnitAuthz refuses every action of a job token as a token of another
// unit, and asks userAuthz for every other principal.
type otherUnitAuthz struct{}

func (otherUnitAuthz) Authorize(ctx context.Context, p identity.Principal, action identity.Action,
	obj identity.Object,
) error {
	if p.Credential == identity.CredentialJobToken {
		return fmt.Errorf("%w: %w", identity.ErrJobTokenOtherUnit,
			&identity.DeniedError{Subject: p.Subject, Action: action, Object: obj})
	}

	return userAuthz{}.Authorize(ctx, p, action, obj)
}

func (otherUnitAuthz) ListObjects(ctx context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	return userAuthz{}.ListObjects(ctx, p, action)
}

// TestJobTokenAnswerTexts proves that the state routes answer a job token
// that ended with 401 and the text of a token that ended, and a job token of
// another unit with 403 and the text of a token for one unit. Each answer
// holds the fixed text only: no subject, no job, no unit and no reason.
func TestJobTokenAnswerTexts(t *testing.T) {
	t.Parallel()

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator: endedAuth{}, Authorizer: otherUnitAuthz{}, Logger: slog.New(slog.DiscardHandler),
		Workspaces: newFakeUnits(true), Namespaces: fakeNamespaces{}, Audit: newTestAudit(), Gate: nil,
		OnAuditFailure: nil, DenialLimiter: nil, StateGuard: stategate.Off("the test keeps no gates"),
		Placer: testPlacer(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	for _, tc := range []struct {
		name   string
		token  string
		status int
		text   string
	}{
		{"ended job token", endedJobToken, http.StatusUnauthorized, identity.JobTokenEndedText},
		{"job token of another unit", jobToken, http.StatusForbidden, identity.JobTokenOtherUnitText},
		{"unknown access token", "nope", http.StatusUnauthorized, "unauthenticated"},
	} {
		rec := do(h, http.MethodGet, unitPath, "", bearer(tc.token))
		if rec.Code != tc.status {
			t.Errorf("%s: status = %d, want %d: %s", tc.name, rec.Code, tc.status, rec.Body.String())
		}

		if got := strings.TrimSpace(rec.Body.String()); got != tc.text {
			t.Errorf("%s: body = %q, want %q", tc.name, got, tc.text)
		}
	}
}
