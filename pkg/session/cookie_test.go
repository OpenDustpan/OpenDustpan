package session_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

func TestForgeTokenRedacted(t *testing.T) {
	t.Parallel()

	const plain = "example-git-host-token-value"

	tok := session.NewForgeToken(plain)
	if tok.Reveal() != plain {
		t.Fatal("Reveal() did not return the token")
	}

	c := session.ForgeClaims{
		SignInAt: time.UnixMilli(1).UTC(), ExpiresAt: time.UnixMilli(2).UTC(), ConnectionID: "fc-1", ForgeUserID: "42",
		SignInID: "sin-1", KeyID: "sk-1", Token: tok,
	}

	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(format, c)
		if strings.Contains(out, plain) {
			t.Errorf("format %s shows the token: %s", format, out)
		}
	}
}

func TestCSRFRedacted(t *testing.T) {
	t.Parallel()

	var v session.CSRF
	for i := range v {
		v[i] = 0xAB
	}

	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%x"} {
		out := fmt.Sprintf(format, v)
		if strings.Contains(strings.ToLower(out), "abab") {
			t.Errorf("format %s shows the CSRF value: %s", format, out)
		}
	}
}

func TestValidBumpCause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cause session.BumpCause
		ok    bool
	}{
		{session.BumpSignOutEverywhere, true},
		{session.BumpAdministrator, true},
		{session.BumpDisabled, true},
		{session.BumpPassword, true},
		{session.BumpFactor, true},
		{session.BumpDirectory, true},
		{"", false},
		{"sign-out", false},
		{"DISABLED", false},
		{"disabled\n", false},
	}

	for _, tt := range tests {
		err := session.ValidBumpCause(tt.cause)
		if tt.ok && err != nil {
			t.Errorf("ValidBumpCause(%q) = %v, want nil", tt.cause, err)
		}

		if !tt.ok && !errors.Is(err, session.ErrInvalid) {
			t.Errorf("ValidBumpCause(%q) = %v, want ErrInvalid", tt.cause, err)
		}
	}
}
