package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// hostileFloorNames holds the hostile text of the refusal text test.
type hostileFloorNames struct {
	subjectA string
	nameA    string
	subjectX string
	nameX    string
}

// hostileFloorText returns the text of the principals of the refusal text
// test. Each value is distinct and holds a character that a naive message
// could carry.
func hostileFloorText() hostileFloorNames {
	return hostileFloorNames{
		subjectA: "a\"quote\"<script>\nline@example.com",
		nameA:    "Account A \"quoted\" <script>alert(1)</script>\n" + strings.Repeat("a", 120),
		subjectX: "x\"quote\"<script>\nline@example.com",
		nameX:    "Site administrator X \"quoted\" <script>alert(2)</script>\n" + strings.Repeat("x", 120),
	}
}

// refusalTextCase is one refusal of the refusal text test.
type refusalTextCase struct {
	name   string
	body   string
	names  hostileFloorNames
	ids    []string
	status int
}

// TestBreakGlassRefusalNamesNothing proves that the refusal of a change that
// leaves too few working break-glass accounts answers the fixed message of
// its code and that neither the message nor the reason of the denied entry
// holds a subject, a display name or an ID of the principals.
func TestBreakGlassRefusalNamesNothing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		method string
	}{
		{name: "disable", method: http.MethodPost},
		{name: "delete", method: http.MethodDelete},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runRefusalTextCase(t, tc.name, tc.method)
		})
	}
}

// runRefusalTextCase runs one refusal route of the refusal text test.
func runRefusalTextCase(t *testing.T, name, method string) {
	t.Helper()

	svc, st := floorStore(t)
	names := hostileFloorText()

	const (
		idA = "prn-floor-a-hostile"
		idX = "prn-floor-x-hostile"
	)

	addFloorPrincipal(t, st, floorPerson{
		ID: idA, Subject: names.subjectA, DisplayName: names.nameA, BreakGlass: true,
	})
	addFloorSiteAdministrator(t, st, floorPerson{
		ID: idX, Subject: names.subjectX, DisplayName: names.nameX, BreakGlass: false,
	})

	h := newReachPrincipalHandlerWith(t, svc, st, reachConfigOf(svc), principalHandlerOptions{
		now:            floorClock,
		authenticator:  nil,
		logger:         nil,
		onAuditFailure: nil,
	})

	status, body := floorCall(t, h, method, floorPath(method, idA), "test-token")
	assertRefusalText(t, st, refusalTextCase{
		name:   name,
		body:   body,
		names:  names,
		ids:    []string{idA, idX},
		status: status,
	})
}

// assertRefusalText checks the refusal body and the denied entry of one
// refusal route.
func assertRefusalText(t *testing.T, st *sqlite.Store, tc refusalTextCase) {
	t.Helper()

	if tc.status != http.StatusConflict {
		t.Fatalf("%s answers %d %q, want 409", tc.name, tc.status, tc.body)
	}

	var answer errorAnswer
	if err := json.Unmarshal([]byte(tc.body), &answer); err != nil {
		t.Fatalf("decode the refusal: %v", err)
	}

	wantStatus, wantMessage := apiserver.ClassifyForTest(
		fmt.Errorf("the break-glass floor: %w", principal.ErrNoWorkingBreakGlass),
	)
	if wantStatus != http.StatusConflict || answer.Code != wantStatus {
		t.Fatalf("%s answers the status %d, want %d", tc.name, answer.Code, wantStatus)
	}

	if answer.ErrorCode != errcode.PrincipalNoWorkingBreakGlass {
		t.Fatalf("%s answers the code %q, want %q", tc.name, answer.ErrorCode, errcode.PrincipalNoWorkingBreakGlass)
	}

	if answer.Message != wantMessage {
		t.Fatalf("%s answers the message %q, want %q", tc.name, answer.Message, wantMessage)
	}

	object := principal.Ref(tc.ids[0])
	denied := floorEntries(t, st, identity.ActionServerManagePrincipals, audit.OutcomeDenied, object)
	if len(denied) != 1 {
		t.Fatalf("%s writes %d denied entries for %q, want 1: %+v", tc.name, len(denied), object, denied)
	}

	if denied[0].Reason != errcode.PrincipalNoWorkingBreakGlass {
		t.Fatalf("%s writes the reason %q, want %q", tc.name, denied[0].Reason, errcode.PrincipalNoWorkingBreakGlass)
	}

	seeded := make([]string, 0, 4+len(tc.ids))
	seeded = append(seeded, tc.names.subjectA, tc.names.nameA, tc.names.subjectX, tc.names.nameX)
	seeded = append(seeded, tc.ids...)

	for _, text := range seeded {
		if strings.Contains(answer.Message, text) {
			t.Fatalf("%s answers the message %q, which holds the seeded text %q", tc.name, answer.Message, text)
		}

		if strings.Contains(denied[0].Reason, text) {
			t.Fatalf("%s writes the reason %q, which holds the seeded text %q", tc.name, denied[0].Reason, text)
		}
	}
}
