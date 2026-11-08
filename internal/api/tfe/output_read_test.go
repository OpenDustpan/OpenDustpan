package tfe_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// olderStateWorkspaces serves the sensitive stub, and also an older state
// version sv-second123 with the same outputs. The current version stays
// sv-test123.
type olderStateWorkspaces struct {
	sensitiveOutputsWorkspaces
}

func (o olderStateWorkspaces) ReadState(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	if id.String() != "sv-second123" {
		return o.sensitiveOutputsWorkspaces.ReadState(ctx, id)
	}

	sv := sensitiveStateVersion()
	sv.ID = id

	return sv, nil
}

// newOutputHandler returns a production Handler over the sensitive-output
// stub with authz as the authorizer and the states port of the test.
func newOutputHandler(t *testing.T, authz identity.Authorizer, checker outputshare.Checker) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     olderStateWorkspaces{sensitiveOutputsWorkspaces{stubWorkspaces: stubWorkspaces{}}},
		OutputChecker:  checker,
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// Targets of the outputs routes in the stub: the list, an output of the
// current state version and an output of an older one.
const (
	outputsList    = "/api/v2/workspaces/ws-test123/current-state-version-outputs"
	outputsCurrent = "/api/v2/state-version-outputs/sv-test123."
	outputsOlder   = "/api/v2/state-version-outputs/sv-second123."
)

// readCase is one row of TestOutputRoutesDecideTheRead.
type readCase struct {
	name   string
	target string
	// deny holds the actions that the authorizer refuses.
	deny map[identity.Action]bool
	// encrypted is true when the client encrypted the current state version.
	encrypted bool
	want      int
	wantCode  string
}

// readRow returns a case that reads target with every grant and expects the
// status 200, and lets edit change it.
func readRow(name, target string, edit func(*readCase)) readCase {
	c := readCase{
		name: name, target: target, deny: map[identity.Action]bool{}, encrypted: false,
		want: http.StatusOK, wantCode: "",
	}
	edit(&c)

	return c
}

// without refuses the actions.
func (c *readCase) without(actions ...identity.Action) {
	for _, action := range actions {
		c.deny[action] = true
	}
}

// fails sets the expected status and the machine code of the error.
func (c *readCase) fails(status int, code string) {
	c.want, c.wantCode = status, code
}

func readCases() []readCase {
	const clientEncrypted = "workspace.client-encrypted"

	return []readCase{
		readRow("list: outputs grant", outputsList, func(*readCase) {}),
		readRow("list: unit:read does not replace unit:read-outputs", outputsList, func(c *readCase) {
			c.without(identity.ActionUnitReadOutputs)
			c.fails(http.StatusForbidden, "")
		}),
		readRow("list: outputs only reads", outputsList, func(c *readCase) {
			c.without(identity.ActionUnitRead, identity.ActionUnitReadSecrets)
		}),
		readRow("list: outputs only, client-encrypted state", outputsList, func(c *readCase) {
			c.encrypted = true
			c.without(identity.ActionUnitRead)
			c.fails(http.StatusConflict, clientEncrypted)
		}),
		readRow("list: unit:read holder, client-encrypted state", outputsList, func(c *readCase) {
			c.encrypted = true
		}),
		readRow("current plain output", outputsCurrent+"greeting", func(*readCase) {}),
		readRow("current plain output without unit:read-secrets", outputsCurrent+"greeting", func(c *readCase) {
			c.without(identity.ActionUnitReadSecrets)
		}),
		readRow("current sensitive output without unit:read-secrets", outputsCurrent+"secret", func(c *readCase) {
			c.without(identity.ActionUnitReadSecrets)
			c.fails(http.StatusForbidden, "")
		}),
		readRow("current sensitive output with unit:read-secrets", outputsCurrent+"secret", func(*readCase) {}),
		readRow("current sensitive output, unit:read alone", outputsCurrent+"secret", func(c *readCase) {
			c.without(identity.ActionUnitReadOutputs)
			c.fails(http.StatusForbidden, "")
		}),
		readRow("current output, client-encrypted state, outputs only", outputsCurrent+"greeting", func(c *readCase) {
			c.encrypted = true
			c.without(identity.ActionUnitRead)
			c.fails(http.StatusConflict, clientEncrypted)
		}),
		readRow("older output, outputs only", outputsOlder+"greeting", func(c *readCase) {
			c.without(identity.ActionUnitRead)
			c.fails(http.StatusForbidden, "")
		}),
		readRow("older output, unit:read holder", outputsOlder+"greeting", func(*readCase) {}),
		readRow(
			"older sensitive output, unit:read without unit:read-secrets",
			outputsOlder+"secret",
			func(c *readCase) {
				c.without(identity.ActionUnitReadSecrets)
				c.fails(http.StatusForbidden, "")
			},
		),
		readRow("older sensitive output, both grants", outputsOlder+"secret", func(*readCase) {}),
		readRow("older unknown output, outputs only", outputsOlder+"missing", func(c *readCase) {
			c.without(identity.ActionUnitRead)
			c.fails(http.StatusForbidden, "")
		}),
		readRow("older unknown output, unit:read holder", outputsOlder+"missing", func(c *readCase) {
			c.fails(http.StatusNotFound, "")
		}),
		readRow("unknown output", outputsCurrent+"missing", func(c *readCase) { c.fails(http.StatusNotFound, "") }),
	}
}

// TestOutputRoutesDecideTheRead proves the answer of the two outputs routes for
// a principal, one row for each way a read can end: the route table needs
// unit:read-outputs and unit:read does not replace it, the checker needs
// unit:read-secrets for a sensitive output of the current state version, an
// older state version needs unit:read, and a state that the client encrypted
// answers 409 to a caller of outputs only.
func TestOutputRoutesDecideTheRead(t *testing.T) {
	t.Parallel()

	for _, tt := range readCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			authz := selectiveAuthorizer{deny: tt.deny}

			states := stubStates{
				workspaces: olderStateWorkspaces{sensitiveOutputsWorkspaces{stubWorkspaces: stubWorkspaces{}}},
				encrypted:  "",
			}
			if tt.encrypted {
				states.encrypted = "sv-test123"
			}

			handler := newOutputHandler(t, authz, newTestOutputCheckerWith(t, authz, states))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, stateRequest(t, tt.target))

			checkReadAnswer(t, rec, tt)
		})
	}
}

// checkReadAnswer proves the status of the answer, and for an error the
// JSON:API document and the machine code.
func checkReadAnswer(t *testing.T, rec *httptest.ResponseRecorder, tt readCase) {
	t.Helper()

	if rec.Code != tt.want {
		t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
	}

	if tt.want == http.StatusOK {
		return
	}

	checkJSONAPIError(t, rec, tt.want)

	if tt.wantCode != "" && !strings.Contains(rec.Body.String(), `"code":"`+tt.wantCode+`"`) {
		t.Errorf("error body %s does not hold the code %q", rec.Body.String(), tt.wantCode)
	}
}

// TestOutputRoutesFailClosedWithoutAChecker proves that a handler without the
// checker answers 500 and no output on both outputs routes, for a read of the
// current state version.
func TestOutputRoutesFailClosedWithoutAChecker(t *testing.T) {
	t.Parallel()

	handler := newOutputHandler(t, allowAll(), nil)

	for _, target := range []string{
		"/api/v2/workspaces/ws-test123/current-state-version-outputs",
		"/api/v2/state-version-outputs/sv-test123.greeting",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, stateRequest(t, target))

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s answers %d, want 500", target, rec.Code)
		}

		if strings.Contains(rec.Body.String(), "greeting") || strings.Contains(rec.Body.String(), "hello") {
			t.Errorf("%s answered an output: %s", target, rec.Body.String())
		}
	}
}

// TestOutputRoutesHideTheCheckFromTheBody proves that the 409 of a state the
// client encrypted names the reason and neither the check nor the unit.
func TestOutputRoutesHideTheCheckFromTheBody(t *testing.T) {
	t.Parallel()

	authz := selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitRead: true}}
	states := stubStates{
		workspaces: sensitiveOutputsWorkspaces{stubWorkspaces: stubWorkspaces{}},
		encrypted:  "sv-test123",
	}
	handler := newOutputHandler(t, authz, newTestOutputCheckerWith(t, authz, states))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, "/api/v2/workspaces/ws-test123/current-state-version-outputs"))

	var doc jsonAPIErrorDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if rec.Code != http.StatusConflict || len(doc.Errors) != 1 {
		t.Fatalf("status = %d, errors = %d; want 409 and one error", rec.Code, len(doc.Errors))
	}

	for _, leak := range []string{"client-encrypted check", "acme/web", "ws-test123", "opentofu:"} {
		if strings.Contains(doc.Errors[0].Detail, leak) {
			t.Errorf("detail %q leaks %q", doc.Errors[0].Detail, leak)
		}
	}

	if !strings.Contains(doc.Errors[0].Detail, "client encrypted the state") {
		t.Errorf("detail %q does not name the reason", doc.Errors[0].Detail)
	}
}

// jobTokenAuthenticator authenticates the test token as a job token.
type jobTokenAuthenticator struct{}

func (jobTokenAuthenticator) AuthenticateToken(ctx context.Context, raw string) (identity.Principal, error) {
	p, err := tokenAuthenticator{}.AuthenticateToken(ctx, raw)
	if err != nil {
		return identity.Principal{}, err
	}

	p.Credential = identity.CredentialJobToken
	p.JobID = "job-1"

	return p, nil
}

// TestOutputRoutesKeepWholeStateChecksForAJobToken proves that a job token
// still needs unit:read, and unit:read-secrets for a sensitive output, until
// the checker serves the job path: the move to unit:read-outputs adds a
// requirement for it and removes none.
func TestOutputRoutesKeepWholeStateChecksForAJobToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		deny   identity.Action
		want   int
	}{
		{"list with every grant", "/api/v2/workspaces/ws-test123/current-state-version-outputs", "", http.StatusOK},
		{
			"list without unit:read", "/api/v2/workspaces/ws-test123/current-state-version-outputs",
			identity.ActionUnitRead, http.StatusForbidden,
		},
		{"sensitive output with every grant", "/api/v2/state-version-outputs/sv-test123.secret", "", http.StatusOK},
		{
			"sensitive output without unit:read-secrets", "/api/v2/state-version-outputs/sv-test123.secret",
			identity.ActionUnitReadSecrets, http.StatusForbidden,
		},
		{
			"plain output without unit:read", "/api/v2/state-version-outputs/sv-test123.greeting",
			identity.ActionUnitRead, http.StatusForbidden,
		},
		{
			"unknown output without unit:read", "/api/v2/state-version-outputs/sv-test123.missing",
			identity.ActionUnitRead, http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			authz := selectiveAuthorizer{deny: map[identity.Action]bool{}}
			if tt.deny != "" {
				authz.deny[tt.deny] = true
			}

			workspaces := olderStateWorkspaces{sensitiveOutputsWorkspaces{stubWorkspaces: stubWorkspaces{}}}

			handler, err := tfe.NewHandler(tfe.Config{
				Registry:       nil,
				Authenticator:  jobTokenAuthenticator{},
				Authorizer:     authz,
				AddressGate:    nil,
				Logger:         slog.New(slog.DiscardHandler),
				Namespaces:     newTestNamespaces(t),
				Workspaces:     workspaces,
				OutputChecker:  newTestOutputChecker(t, authz, workspaces),
				BaseURL:        baseurl.Resolver{},
				Audit:          newTestAudit(),
				Gate:           nil,
				MaxStateBytes:  0,
				OnAuditFailure: nil,
				DenialLimiter:  nil,
				Sessions:       nil,
				SessionPolicy:  nil,
				StateGuard:     stategate.Off("the test keeps no gates"),
				Placer:         testPlacer(),
			})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, stateRequest(t, tt.target))

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

// TestStateVersionListOutputsOnlyIsForbidden proves that a caller with
// unit:read-outputs and not unit:read gets 403 from the routes that answer a
// whole state, and not the 404 of a workspace that does not exist: that caller
// reads the outputs of the workspace and knows that it exists. A caller with
// neither action gets 404 from the same routes.
func TestStateVersionListOutputsOnlyIsForbidden(t *testing.T) {
	t.Parallel()

	targets := []string{
		"/api/v2/state-versions?filter[organization][name]=acme&filter[workspace][name]=web",
		"/api/v2/workspaces/ws-test123/current-state-version",
		"/api/v2/state-versions/sv-test123",
		"/api/v2/state-versions/sv-test123/download",
	}

	tests := []struct {
		name string
		deny map[identity.Action]bool
		want int
	}{
		{
			name: "outputs only",
			deny: map[identity.Action]bool{identity.ActionUnitRead: true}, want: http.StatusForbidden,
		},
		{
			name: "no read of any kind",
			deny: map[identity.Action]bool{identity.ActionUnitRead: true, identity.ActionUnitReadOutputs: true},
			want: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		for _, target := range targets {
			t.Run(tt.name+" "+target, func(t *testing.T) {
				t.Parallel()

				handler := newStateHandler(t, selectiveAuthorizer{deny: tt.deny})

				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, stateRequest(t, target))

				checkJSONAPIError(t, rec, tt.want)
			})
		}
	}
}

// unknownProducerStates answers every read of a latest state with the error
// of a producer that the check refuses, as a workspace deleted between the
// resolve of the route and the check does.
type unknownProducerStates struct{}

func (unknownProducerStates) Latest(context.Context, identity.Object) (outputshare.State, error) {
	return outputshare.State{ID: "", ClientEncrypted: false},
		fmt.Errorf("read of the outputs of opentofu:acme/web denied by the producer check: %w",
			outputshare.ErrUnknownProducer)
}

// TestOutputRoutesAnswerAHiddenProducerAsAnUnknownWorkspace proves that the
// 404 of a producer that the check refuses has the whole body of the 404 of a
// workspace that does not exist: no detail names the check or the producer.
func TestOutputRoutesAnswerAHiddenProducerAsAnUnknownWorkspace(t *testing.T) {
	t.Parallel()

	authz := selectiveAuthorizer{deny: nil}
	hidden := newOutputHandler(t, authz, newTestOutputCheckerWith(t, authz, unknownProducerStates{}))
	plain := newOutputHandler(t, authz, newTestOutputCheckerWith(t, authz, stubStates{
		workspaces: stubWorkspaces{}, encrypted: "",
	}))

	hiddenRec := httptest.NewRecorder()
	hidden.ServeHTTP(hiddenRec, stateRequest(t, "/api/v2/workspaces/ws-test123/current-state-version-outputs"))

	missingRec := httptest.NewRecorder()
	plain.ServeHTTP(missingRec, stateRequest(t, "/api/v2/workspaces/ws-missing/current-state-version-outputs"))

	if hiddenRec.Code != http.StatusNotFound || missingRec.Code != http.StatusNotFound {
		t.Fatalf("status = %d and %d, want 404 for both", hiddenRec.Code, missingRec.Code)
	}

	if hiddenRec.Body.String() != missingRec.Body.String() {
		t.Errorf("hidden body = %q, want the body of an unknown workspace %q",
			hiddenRec.Body.String(), missingRec.Body.String())
	}
}
