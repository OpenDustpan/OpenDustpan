package unitimport_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/unitimport"
)

const canary = "canary-source-token-7f3a"

func TestCredentialNeverPrints(t *testing.T) {
	t.Parallel()

	cred := unitimport.NewCredential([]byte(canary))

	var logged bytes.Buffer

	slog.New(slog.NewJSONHandler(&logged, nil)).Info("run", slog.Any("credential", cred))

	printed := []string{
		cred.String(),
		fmt.Sprintf("%v %+v %#v %s", cred, cred, cred, cred),
		logged.String(),
	}

	for _, out := range printed {
		if strings.Contains(out, canary) {
			t.Errorf("printed form holds the credential: %q", out)
		}
	}

	wrapped := struct {
		C unitimport.Credential `json:"c"`
	}{cred}

	if _, err := json.Marshal(wrapped); err == nil {
		t.Error("json.Marshal of a credential succeeded, want an error")
	}

	if _, err := cred.MarshalText(); err == nil {
		t.Error("MarshalText of a credential succeeded, want an error")
	}
}

func TestCredentialBytesIsACopy(t *testing.T) {
	t.Parallel()

	src := []byte(canary)
	cred := unitimport.NewCredential(src)
	src[0] = 'X'

	got := cred.Bytes()
	if string(got) != canary {
		t.Fatalf("Bytes = %q, want %q", got, canary)
	}

	got[0] = 'Y'

	if string(cred.Bytes()) != canary {
		t.Error("a change of the answer of Bytes changed the credential")
	}

	if cred.Empty() {
		t.Error("Empty = true for a credential with bytes")
	}

	if !unitimport.NewCredential(nil).Empty() {
		t.Error("Empty = false for a credential with no bytes")
	}
}
