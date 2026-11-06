package pulumi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errMoveFailed is the error that a fakePlacer gives for a move.
var errMoveFailed = errors.New("the store is down")

// fakePlacer has one rule: the project "web" goes into the collection
// "payments". It records the moves that the handler asks for.
type fakePlacer struct {
	moveErr error
	moves   []collection.Move
	entries []audit.Entry
	missing bool
}

func (f *fakePlacer) Place(
	_ context.Context, ns namespace.Name, tool, project, stackName string,
) (collection.Rule, error) {
	if project != "web" {
		return collection.Rule{}, &collection.PlacementError{
			Namespace: ns, Tool: tool, Project: project, Stack: stackName,
		}
	}

	return collection.Rule{
		CreatedAt: time.Unix(1, 0), ID: "plr-1", Namespace: ns, Tool: tool, Project: "web", Stack: "*",
		Collection: "payments", CreatedBy: "tester", Position: 1,
	}, nil
}

func (f *fakePlacer) CollectionID(_ context.Context, _ identity.Object) (string, bool, error) {
	if f.missing {
		return "", false, nil
	}

	return "col-1", true, nil
}

func (f *fakePlacer) SetUnit(_ context.Context, m collection.Move, by audit.Entry) error {
	if f.moveErr != nil {
		return f.moveErr
	}

	f.moves = append(f.moves, m)
	f.entries = append(f.entries, by)

	return nil
}

// placer returns the Placer that uses f.
func (f *fakePlacer) placer() *pulumi.Placer {
	return &pulumi.Placer{Rules: f, Collections: f}
}

// newFake returns a fakePlacer with no failure.
func newFake() *fakePlacer {
	return &fakePlacer{moveErr: nil, moves: nil, entries: nil, missing: false}
}

// postStack sends the request that "pulumi stack init" sends for the stack
// "prod" of project in the namespace acme.
func postStack(t *testing.T, h http.Handler, project string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/stacks/acme/"+project, strings.NewReader(`{"stackName":"prod"}`),
	)
	req.Header.Set("Authorization", "token test-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// placementCase is one request to make a stack and what the server does.
type placementCase struct {
	authz       identity.Authorizer
	placer      *fakePlacer
	name        string
	project     string
	wantCode    string
	wantStatus  int
	wantCreated int
	wantMoves   int
	wantDeleted int
}

// TestCreateStackPlacement proves that a new stack goes into the collection of
// the first matching rule, that a stack no rule matches is refused with the
// named error and is not made, that a caller without collection:create-unit
// gets 403 and no stack is made, and that a failed move leaves no stack.
func TestCreateStackPlacement(t *testing.T) {
	t.Parallel()

	allow := selectiveAuthorizer{deny: nil}
	denyCreateUnit := selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionCollectionCreateUnit: true}}

	tests := []placementCase{
		{
			name: "a match places the stack", project: "web", authz: allow, placer: newFake(),
			wantStatus: http.StatusOK, wantCode: "", wantCreated: 1, wantMoves: 1, wantDeleted: 0,
		},
		{
			name: "no match is refused and makes no stack", project: "other", authz: allow, placer: newFake(),
			wantStatus: http.StatusUnprocessableEntity, wantCode: errcode.PlacementNoMatch,
			wantCreated: 0, wantMoves: 0, wantDeleted: 0,
		},
		{
			name: "a caller without collection:create-unit gets 403", project: "web", authz: denyCreateUnit,
			placer: newFake(), wantStatus: http.StatusForbidden, wantCode: "",
			wantCreated: 0, wantMoves: 0, wantDeleted: 0,
		},
		{
			name:    "a missing collection refuses the stack",
			project: "web",
			authz:   allow,
			placer: &fakePlacer{
				moveErr: nil,
				moves:   nil,
				entries: nil,
				missing: true,
			},
			wantStatus:  http.StatusConflict,
			wantCode:    errcode.CollectionNotFound,
			wantCreated: 0,
			wantMoves:   0,
			wantDeleted: 0,
		},
		{
			name:    "a failed move deletes the new stack",
			project: "web",
			authz:   allow,
			placer: &fakePlacer{
				moveErr: errMoveFailed,
				moves:   nil,
				entries: nil,
				missing: false,
			},
			wantStatus:  http.StatusInternalServerError,
			wantCode:    "",
			wantCreated: 1,
			wantMoves:   0,
			wantDeleted: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, stores := newPlacingHandler(
				t,
				tt.authz,
				nil,
				stategate.Off("the test keeps no gates"),
				tt.placer.placer(),
			)
			rec := postStack(t, h, tt.project)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tt.wantStatus)
			}

			checkErrorCode(t, rec.Body.Bytes(), tt.wantCode)

			if len(stores.created) != tt.wantCreated || len(tt.placer.moves) != tt.wantMoves ||
				len(stores.deleted) != tt.wantDeleted {
				t.Fatalf("created %d, moves %d, deleted %d; want %d, %d, %d", len(stores.created),
					len(tt.placer.moves), len(stores.deleted), tt.wantCreated, tt.wantMoves, tt.wantDeleted)
			}
		})
	}
}

// checkErrorCode fails unless the error answer body holds the errorCode want.
// An empty want accepts any body.
func checkErrorCode(t *testing.T, body []byte, want string) {
	t.Helper()

	if want == "" {
		return
	}

	var doc struct {
		ErrorCode string `json:"errorCode"`
		Message   string `json:"message"`
	}

	if err := json.Unmarshal(body, &doc); err != nil || doc.ErrorCode != want {
		t.Fatalf("errorCode = %q (%v), want %q", doc.ErrorCode, err, want)
	}

	if want == errcode.PlacementNoMatch && !strings.Contains(doc.Message, "placement-rules") {
		t.Fatalf("message %q does not name the placement rules", doc.Message)
	}
}

// TestCreateStackPlacementRecordsRule proves that the move names the unit, the
// collection that the rule chose, the stable collection ID and the rule, and
// that the audit entry says why the stack is there.
func TestCreateStackPlacementRecordsRule(t *testing.T) {
	t.Parallel()

	fake := newFake()
	h, _ := newPlacingHandler(
		t, selectiveAuthorizer{deny: nil}, nil, stategate.Off("the test keeps no gates"), fake.placer(),
	)

	if rec := postStack(t, h, "web"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}

	move := fake.moves[0]
	if move.Unit.String() != "pulumi:acme/web/prod" || move.To.String() != "acme/payments" ||
		move.ToID != "col-1" || move.From != "" {
		t.Fatalf("move = %+v", move)
	}

	if got := fake.entries[0]; got.Version != "from=none;to=payments;rule=plr-1" ||
		got.Action != string(identity.ActionCollectionCreateUnit) || got.Subject != "tester" {
		t.Fatalf("entry = %+v", got)
	}
}

// TestCreateStackWithoutPlacer proves that a handler with no Placer makes the
// stack and puts it in no collection.
func TestCreateStackWithoutPlacer(t *testing.T) {
	t.Parallel()

	h, stores := newPlacingHandler(
		t, selectiveAuthorizer{deny: nil}, nil, stategate.Off("the test keeps no gates"), nil,
	)

	if rec := postStack(t, h, "web"); rec.Code != http.StatusOK || len(stores.created) != 1 {
		t.Fatalf("status = %d, created %d", rec.Code, len(stores.created))
	}
}
