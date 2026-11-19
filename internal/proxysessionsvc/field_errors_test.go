package proxysessionsvc_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/proxysessionsvc"
	"opendustpan.org/opendustpan/pkg/runner"
)

// TestPutSettingsNamesTheInvalidCredentialField is the hostile table of the
// service layer: a credential that the save must refuse names its field and
// never repeats the value.
func TestPutSettingsNamesTheInvalidCredentialField(t *testing.T) {
	t.Parallel()

	const marker = "ZZ-marker"

	tests := []struct {
		name      string
		upstream  string
		user      string
		password  string
		wantField string
	}{
		{"colon in the user name", upstreamURL, marker + ":x", "p", "upstreamCredential.username"},
		{"empty user name", upstreamURL, "", "p", "upstreamCredential.username"},
		{
			"long user name", upstreamURL, strings.Repeat("u", proxysessionsvc.MaxUsernameBytes+1), "p",
			"upstreamCredential.username",
		},
		{
			"long password", upstreamURL, "u", strings.Repeat("p", proxysessionsvc.MaxPasswordBytes+1),
			"upstreamCredential.password",
		},
		{"control character", upstreamURL, "u" + marker + "\x07", "p", "upstreamCredential"},
		{"no upstream", "", "u" + marker, "p", "upstreamCredential"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			req := putRequest(pool(poolA, nsAcme))
			req.UpstreamURL = tc.upstream
			req.Credential = proxysessionsvc.CredentialChange{
				Present: true, Value: &proxysessionsvc.Credential{Username: tc.user, Password: tc.password},
			}

			_, err := f.svc.PutSettings(t.Context(), req)

			fe, ok := errors.AsType[*runner.FieldError](err)
			if !ok || fe.Field() != tc.wantField {
				t.Fatalf("PutSettings error = %v, want a field error of %s", err, tc.wantField)
			}

			if !errors.Is(err, runner.ErrInvalid) || strings.Contains(err.Error(), marker) {
				t.Errorf("PutSettings error = %q, want ErrInvalid with no input", err)
			}
		})
	}
}
