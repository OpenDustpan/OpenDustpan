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
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errPlaceFailed is the error that the stack store gives when the transaction
// of the create and the placement fails.
var errPlaceFailed = errors.New("the store is down")

// fakePlacer has one rule: the project "web" goes into the collection
// "payments".
type fakePlacer struct {
	missing bool
}

// newFake returns a fakePlacer whose collection exists.
func newFake() *fakePlacer {
	return &fakePlacer{missing: false}
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

// placer returns the Placer that uses f.
func (f *fakePlacer) placer() *pulumi.Placer {
	return &pulumi.Placer{Rules: f, Collections: f}
}

// postStack sends the request that "pulumi stack init" sends for the stack
// "prod" of project in the namespace acme.
func postStack(t *testing.T, h http.Handler, project, stackName string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/stacks/acme/"+project, strings.NewReader(`{"stackName":"`+stackName+`"}`),
	)
	req.Header.Set("Authorization", "token test-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
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

// placementCase is one request to make a stack and what the server does.
type placementCase struct {
	authz       identity.Authorizer
	placer      *pulumi.Placer
	placeErr    error
	name        string
	project     string
	stackName   string
	wantCode    string
	wantStatus  int
	wantCreated int
}

// TestCreateStackPlacement proves that a new stack goes into the collection of
// the first matching rule, that a stack no rule matches is refused with the
// named error and is not made, that a caller without collection:create-unit
// gets 403 and no stack is made, that a failed create-and-place leaves no
// stack, and that a server with no Placer makes no stack.
func TestCreateStackPlacement(t *testing.T) {
	t.Parallel()

	allow := selectiveAuthorizer{deny: nil}
	denyCreateUnit := selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionCollectionCreateUnit: true}}

	tests := []placementCase{
		{
			name:        "a match places the stack",
			project:     "web",
			stackName:   "prod",
			authz:       allow,
			placer:      newFake().placer(),
			placeErr:    nil,
			wantStatus:  http.StatusOK,
			wantCode:    "",
			wantCreated: 1,
		},
		{
			name: "no match is refused and makes no stack", project: "other", stackName: "absent", authz: allow,
			placer: newFake().placer(), placeErr: nil, wantStatus: http.StatusUnprocessableEntity,
			wantCode: errcode.PlacementNoMatch, wantCreated: 0,
		},
		{
			name: "a stack that exists is a conflict before a missing rule", project: "other", stackName: "prod",
			authz: allow, placer: newFake().placer(), placeErr: nil, wantStatus: http.StatusConflict,
			wantCode: errcode.StackAlreadyExists, wantCreated: 0,
		},
		{
			name:        "a caller without collection:create-unit gets 403",
			project:     "web",
			stackName:   "prod",
			authz:       denyCreateUnit,
			placer:      newFake().placer(),
			placeErr:    nil,
			wantStatus:  http.StatusForbidden,
			wantCode:    "",
			wantCreated: 0,
		},
		{
			name: "a missing collection refuses the stack", project: "web", stackName: "prod", authz: allow,
			placer: (&fakePlacer{missing: true}).placer(), placeErr: nil, wantStatus: http.StatusConflict,
			wantCode: errcode.CollectionNotFound, wantCreated: 0,
		},
		{
			name: "a failed create and placement makes no stack", project: "web", stackName: "prod", authz: allow,
			placer: newFake().placer(), placeErr: errPlaceFailed, wantStatus: http.StatusInternalServerError,
			wantCode: "", wantCreated: 0,
		},
		{
			name: "a server with no Placer makes no stack", project: "web", stackName: "prod", authz: allow,
			placer: nil, placeErr: nil, wantStatus: http.StatusNotImplemented,
			wantCode: errcode.PlacementUnavailable, wantCreated: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, stores := newPlacingHandler(t, tt.authz, nil, stategate.Off("the test keeps no gates"), tt.placer)
			stores.placeErr = tt.placeErr

			rec := postStack(t, h, tt.project, tt.stackName)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tt.wantStatus)
			}

			checkErrorCode(t, rec.Body.Bytes(), tt.wantCode)

			if len(stores.created) != tt.wantCreated || len(stores.placed) != tt.wantCreated {
				t.Fatalf(
					"created %d, placed %d; want %d of each",
					len(stores.created),
					len(stores.placed),
					tt.wantCreated,
				)
			}
		})
	}
}

// TestCreateStackPlacementMissingCollection proves that a rule whose
// collection does not exist answers with the Pulumi error shape: code 409,
// the message that names the collection, and the code of a missing
// collection.
func TestCreateStackPlacementMissingCollection(t *testing.T) {
	t.Parallel()

	h, _ := newPlacingHandler(
		t, selectiveAuthorizer{deny: nil}, nil, stategate.Off("the test keeps no gates"),
		(&fakePlacer{missing: true}).placer(),
	)

	rec := postStack(t, h, "web", "prod")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d %s, want 409", rec.Code, rec.Body.String())
	}

	var doc struct {
		Message   string `json:"message"`
		Code      int    `json:"code"`
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode the answer %s: %v", rec.Body.String(), err)
	}

	if doc.Code != http.StatusConflict || doc.ErrorCode != errcode.CollectionNotFound {
		t.Fatalf("answer = %+v, want code 409 and %s", doc, errcode.CollectionNotFound)
	}

	if !strings.Contains(doc.Message, "payments") {
		t.Fatalf("message %q does not name the collection", doc.Message)
	}
}

// TestCreateStackPlacementPlainNotFound proves that a plain
// collection.ErrNotFound on the placement path does not get the answer of a
// rule whose collection is missing: only the typed error of that condition
// does.
func TestCreateStackPlacementPlainNotFound(t *testing.T) {
	t.Parallel()

	h, stores := newPlacingHandler(
		t, selectiveAuthorizer{deny: nil}, nil, stategate.Off("the test keeps no gates"), newFake().placer(),
	)
	stores.placeErr = collection.ErrNotFound

	rec := postStack(t, h, "web", "prod")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d %s, want 500", rec.Code, rec.Body.String())
	}

	var doc struct {
		Code      int    `json:"code"`
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode the answer %s: %v", rec.Body.String(), err)
	}

	if doc.Code != http.StatusInternalServerError || doc.ErrorCode != errcode.ServerInternal {
		t.Fatalf("answer = %+v, want code 500 and %s", doc, errcode.ServerInternal)
	}
}

// TestCreateStackPlacementRecordsRule proves that the placement names the
// collection that the rule chose and its stable ID, and that the audit entry
// of the placement says which rule put the stack there and who made it.
func TestCreateStackPlacementRecordsRule(t *testing.T) {
	t.Parallel()

	h, stores := newPlacingHandler(
		t, selectiveAuthorizer{deny: nil}, nil, stategate.Off("the test keeps no gates"), newFake().placer(),
	)

	if rec := postStack(t, h, "web", "prod"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}

	got := stores.placed[0]
	if got.Collection.String() != "acme/payments" || got.CollectionID != "col-1" {
		t.Fatalf("placement = %+v", got)
	}

	entry := got.Entry
	if entry.Version != "from=none;to=payments;rule=plr-1" || entry.Subject != "tester" ||
		entry.Action != string(identity.ActionCollectionCreateUnit) || entry.Object != "pulumi:acme/web/prod" {
		t.Fatalf("entry = %+v", entry)
	}
}
