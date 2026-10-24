package server_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestApproverRefusalCodes proves that each reason why no one can meet an
// approver list answers 422 with its own code of the catalog, for the default
// and for a rule, and that the count code carries the number of people. A row fails
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

				_, h := newGateHandler(t, adminGates())

				rec := serveGate(t, h, gateCall{http.MethodPut, target, tt.body, ""})

				errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusUnprocessableEntity,
					rec.Body.Bytes(), errcodetest.Want{Code: tt.code, Args: tt.args, RequestBody: []byte(tt.body)})
			})
		}
	}
}
