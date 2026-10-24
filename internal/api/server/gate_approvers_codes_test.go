package server_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestApproverRefusalCodes proves that each reason why no one can meet an
// approver list answers 422 with its own code of the catalog, for the default
// and for a rule, and that the count code carries the number of people, for a
// caller who may read the principals. A row fails
// when its code is swapped for another.
func TestApproverRefusalCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		code string
		args map[string]any
	}{
		{
			"group name not valid", `{"count":1,"approvers":{"groups":["<b>x</b>"]}}`,
			errcode.GateApproversGroupInvalid, nil,
		},
		{
			"group does not exist", `{"count":1,"approvers":{"groups":["nogroup"]}}`,
			errcode.GateApproversGroupMissing, nil,
		},
		{
			"user name with a control character", `{"count":1,"approvers":{"users":["al\u0001ice"]}}`,
			errcode.GateApproversUserInvalid, nil,
		},
		{
			"user name with white space at its end", `{"count":1,"approvers":{"users":["alice "]}}`,
			errcode.GateApproversUserInvalid, nil,
		},
		{
			"count above the named users", `{"count":3,"approvers":{"users":["alice"]}}`,
			errcode.GateApproversCountAbove,
			map[string]any{"people": int64(1)},
		},
		{
			"count above users and group members", `{"count":10,"approvers":{"groups":["platform"],"users":["alice"]}}`,
			errcode.GateApproversCountAbove,
			map[string]any{"people": int64(6)},
		},
	}

	targets := map[string]string{
		"default": gateBase + "acme" + gateDefault,
		"rule":    gateBase + "acme" + gateRulesURL + "/ops",
	}

	for _, tt := range tests {
		for kind, target := range targets {
			t.Run(kind+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				_, h := newGateHandler(t, principalReaderGates())

				rec := serveGate(t, h, gateCall{http.MethodPut, target, tt.body, ""})

				errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusUnprocessableEntity,
					rec.Body.Bytes(), errcodetest.Want{Code: tt.code, Args: tt.args, RequestBody: []byte(tt.body)})
			})
		}
	}
}

// principalReaderGates allows both gate actions on acme and the read of the
// principals of the installation.
func principalReaderGates() gateAuthorizer {
	a := adminGates()
	a.allow[identity.ActionServerReadPrincipals] = ""

	return a
}

// failingPrincipalsCheck allows both gate actions on acme and fails the check
// of the read of the principals with an error that is not a denial.
type failingPrincipalsCheck struct{ gateAuthorizer }

func (a failingPrincipalsCheck) Authorize(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionServerReadPrincipals {
		return errPrincipalsDown
	}

	return a.gateAuthorizer.Authorize(ctx, p, action, obj)
}

var errPrincipalsDown = errors.New("the grant store is down")

// TestApproverRefusalHidesGroups sends approver lists that probe the groups of
// the installation, as a caller who may manage the gates of acme but may not
// read the principals. A group that does not exist and a count above the
// members of a group give one code with no arguments, and the message names no
// group and no number. A count above the named users keeps its own code. A
// failed check of the read of the principals gives the one code too.
func TestApproverRefusalHidesGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		authz identity.Authorizer
		body  string
		code  string
		args  map[string]any
	}{
		{
			"a group that does not exist", adminGates(), `{"count":1,"approvers":{"groups":["nogroup"]}}`,
			errcode.GateApproversUnmet, nil,
		},
		{
			"a count above the members of a group", adminGates(),
			`{"count":10,"approvers":{"groups":["platform"]}}`, errcode.GateApproversUnmet, nil,
		},
		{
			"a count above users and group members", adminGates(),
			`{"count":10,"approvers":{"groups":["platform"],"users":["alice"]}}`, errcode.GateApproversUnmet, nil,
		},
		{
			"a failed check of the principals read",
			failingPrincipalsCheck{adminGates()},
			`{"count":1,"approvers":{"groups":["nogroup"]}}`, errcode.GateApproversUnmet, nil,
		},
		{
			"a count above the named users only", adminGates(), `{"count":3,"approvers":{"users":["alice"]}}`,
			errcode.GateApproversCountAbove,
			map[string]any{"people": int64(1)},
		},
	}

	targets := map[string]string{
		"default": gateBase + "acme" + gateDefault,
		"rule":    gateBase + "acme" + gateRulesURL + "/ops",
	}

	for _, tt := range tests {
		for kind, target := range targets {
			t.Run(kind+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				_, h := newGateHandler(t, tt.authz)

				rec := serveGate(t, h, gateCall{http.MethodPut, target, tt.body, ""})

				errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusUnprocessableEntity,
					rec.Body.Bytes(), errcodetest.Want{Code: tt.code, Args: tt.args, RequestBody: []byte(tt.body)})

				if tt.code == errcode.GateApproversUnmet {
					checkNoLeak(t, rec.Body.String())
				}
			})
		}
	}
}

// checkNoLeak fails when body names a group of the probe or a number of
// members.
func checkNoLeak(t *testing.T, body string) {
	t.Helper()

	for _, leak := range []string{"nogroup", "platform", "6", "5"} {
		if strings.Contains(body, leak) {
			t.Errorf("answer %s names %q", body, leak)
		}
	}
}
