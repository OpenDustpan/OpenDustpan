package unitimport_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/unitimport"
)

const canary = "canary-source-token-7f3a"

func TestCredentialNeverPrints(t *testing.T) {
	t.Parallel()

	cred := unitimport.NewCredential([]byte(canary))

	var logged bytes.Buffer

	slog.New(slog.NewJSONHandler(&logged, nil)).Info("run", slog.Any("credential", cred))

	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%o", "%b", "%c", "%U"}

	printed := make([]string, 0, 3+3*len(verbs))
	printed = append(printed, cred.String(), cred.GoString(), logged.String())

	wrapped := struct {
		C unitimport.Credential `json:"c"`
	}{cred}

	// fmt cannot call a method of an unexported field, so it prints the
	// fields of this Credential.
	hidden := struct{ c unitimport.Credential }{cred}

	for _, verb := range verbs {
		if got := fmt.Sprintf(verb, cred); got != "[credential]" {
			t.Errorf("Sprintf(%q) = %q, want %q", verb, got, "[credential]")
		}

		printed = append(printed, fmt.Sprintf(verb, wrapped), fmt.Sprintf(verb, hidden), fmt.Sprintf(verb, &cred))
	}

	for _, out := range printed {
		if strings.Contains(out, canary) || strings.Contains(out, "99 97 110") || strings.Contains(out, "63616e61") {
			t.Errorf("printed form holds the credential: %q", out)
		}
	}

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
	if got == nil {
		t.Fatal("Bytes = nil, want the credential")
	}

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

func TestCredentialClear(t *testing.T) {
	t.Parallel()

	cred := unitimport.NewCredential([]byte(canary))
	shared := cred

	cred.Clear()

	if got := shared.Bytes(); !bytes.Equal(got, make([]byte, len(canary))) {
		t.Errorf("Bytes after Clear = %q, want only zeros", got)
	}
}

func TestCheckAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr string
		ok   bool
	}{
		{"https://app.example.com", true},
		{"https://tfe.example.com:8443/api/v2", true},
		{"s3://bucket/prefix", true},
		{"", false},
		{"app.example.com", false},
		{"/relative/path", false},
		{"mailto:someone@example.com", false},
		{"http://app.example.com", false},
		{"file://host/x", false},
		{"gopher://h/x", false},
		{"https://user:" + "pass@app.example.com", false},
		{"https://token@app.example.com", false},
		{"https://bucket.example.com/key?X-Amz-Signature=abc", false},
		{"https://app.example.com/?", false},
		{"https://app.example.com/#frag", false},
		{"https://app.example.com/#", false},
		{"https://%zz", false},
	}

	for _, tt := range tests {
		err := unitimport.CheckAddress(tt.addr)
		if tt.ok && err != nil {
			t.Errorf("CheckAddress(%q) = %v, want nil", tt.addr, err)
		}

		if !tt.ok && !errors.Is(err, unitimport.ErrAddressInvalid) {
			t.Errorf("CheckAddress(%q) = %v, want ErrAddressInvalid", tt.addr, err)
		}

		if err != nil && strings.Contains(err.Error(), "pass") {
			t.Errorf("CheckAddress(%q) error holds the address: %v", tt.addr, err)
		}
	}
}

func TestSanitizeSourceActor(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("é", unitimport.MaxSourceActorBytes)

	tests := []struct {
		name, in, want string
	}{
		{"plain", "alice@example.com", "alice@example.com"},
		{"control characters", "ali\x1b[31mce\r\n\t", "ali[31mce"},
		{"invalid UTF-8", "bob\xff", "bob\uFFFD"},
		{"empty", "", ""},
		{"long", long, strings.Repeat("é", unitimport.MaxSourceActorBytes/2)},
	}

	for _, tt := range tests {
		got := unitimport.SanitizeSourceActor(tt.in)
		if got != tt.want {
			t.Errorf("%s: SanitizeSourceActor = %q, want %q", tt.name, got, tt.want)
		}

		if len(got) > unitimport.MaxSourceActorBytes || !utf8.ValidString(got) {
			t.Errorf("%s: SanitizeSourceActor = %q, want valid UTF-8 of at most %d bytes",
				tt.name, got, unitimport.MaxSourceActorBytes)
		}
	}
}
