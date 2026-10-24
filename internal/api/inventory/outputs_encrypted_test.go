package inventory_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
)

// clientEncryptedText is the message of the 409 answer for a client-encrypted
// state. It tells the caller where to read the outputs.
const clientEncryptedText = "the client encrypted the state of the unit, " +
	"so the server cannot read its outputs: read them with the tool that holds the encryption key"

// checkClientEncryptedMessage checks that the body of a 409 answer carries the
// message of a client-encrypted state.
func checkClientEncryptedMessage(t *testing.T, body []byte) {
	t.Helper()

	var got errorBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("error body = %s: %v", body, err)
	}

	if got.Message != clientEncryptedText {
		t.Errorf("message = %q, want %q", got.Message, clientEncryptedText)
	}
}

// TestOutputsOfAClientEncryptedStateAnswer409ForEveryCaller proves that the
// outputs route answers 409 unit.client-encrypted for a state that the client
// encrypted, for a caller with unit:read and for a caller with outputs only,
// and never an empty list. A caller without unit:read-outputs gets the 404 of
// an unknown unit first, so the answer tells it nothing about the state. A
// plain state still reads.
func TestOutputsOfAClientEncryptedStateAnswer409ForEveryCaller(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		deny      map[identity.Action]bool
		encrypted bool
		status    int
		code      string
	}{
		{
			name: "unit:read and unit:read-outputs", deny: map[identity.Action]bool{}, encrypted: true,
			status: http.StatusConflict, code: errcode.UnitClientEncrypted,
		},
		{
			name: "unit:read-outputs only", deny: map[identity.Action]bool{identity.ActionUnitRead: true},
			encrypted: true, status: http.StatusConflict, code: errcode.UnitClientEncrypted,
		},
		{
			name:      "no unit:read-outputs",
			deny:      map[identity.Action]bool{identity.ActionUnitRead: true, identity.ActionUnitReadOutputs: true},
			encrypted: true, status: http.StatusNotFound, code: errcode.UnitNotFound,
		},
		{
			name: "a plain state", deny: map[identity.Action]bool{}, encrypted: false,
			status: http.StatusOK, code: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newOutputsFixture(t, selectiveAuthorizer{deny: tc.deny})
			f.states.encrypted["ws-1"] = tc.encrypted

			rec := f.get(t, outputsTofu)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			if tc.code == "" {
				return
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})

			if tc.status == http.StatusConflict {
				checkClientEncryptedMessage(t, rec.Body.Bytes())
			}

			if f.source.reads != 0 {
				t.Errorf("a refused read called the source %d times", f.source.reads)
			}
		})
	}
}
