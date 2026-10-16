package session_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

func validProviderConfig() session.ProviderConfig {
	at := time.Date(2026, time.October, 16, 9, 0, 0, 0, time.UTC)

	return session.ProviderConfig{
		CreatedAt: at, UpdatedAt: at, ID: "corp", Issuer: "https://idp.example/realms/corp", DisplayName: "Corp",
		ClientID: "dustpan", SealedSecret: []byte("sealed"), Scopes: []string{"profile"}, Version: 1,
		AutoCreate: true, Enabled: true,
	}
}

func TestProviderConfigValidate(t *testing.T) {
	t.Parallel()

	if err := validProviderConfig().Validate(); err != nil {
		t.Fatalf("valid provider: %v", err)
	}

	for _, tc := range []struct {
		name   string
		change func(*session.ProviderConfig)
	}{
		{"upper-case ID", func(p *session.ProviderConfig) { p.ID = "Corp" }},
		{"ID over 32 bytes", func(p *session.ProviderConfig) { p.ID = "a" + strings.Repeat("b", 32) }},
		{"relative issuer", func(p *session.ProviderConfig) { p.Issuer = "idp.example" }},
		{"issuer with a query", func(p *session.ProviderConfig) { p.Issuer = "https://idp.example?x=1" }},
		{"other scheme", func(p *session.ProviderConfig) { p.Issuer = "ftp://idp.example" }},
		{"http issuer", func(p *session.ProviderConfig) { p.Issuer = "http://idp.example" }},
		{"http issuer with a fragment", func(p *session.ProviderConfig) { p.Issuer = "http://127.0.0.1:9000#x" }},
		{"no display name", func(p *session.ProviderConfig) { p.DisplayName = "" }},
		{"control character", func(p *session.ProviderConfig) { p.DisplayName = "Corp\n" }},
		{"no client ID", func(p *session.ProviderConfig) { p.ClientID = "" }},
		{"no secret", func(p *session.ProviderConfig) { p.SealedSecret = nil }},
		{"version 0", func(p *session.ProviderConfig) { p.Version = 0 }},
		{"update before creation", func(p *session.ProviderConfig) { p.UpdatedAt = p.CreatedAt.Add(-time.Second) }},
		{"scope with a space", func(p *session.ProviderConfig) { p.Scopes = []string{"a b"} }},
		{"empty scope", func(p *session.ProviderConfig) { p.Scopes = []string{""} }},
		{"17 scopes", func(p *session.ProviderConfig) { p.Scopes = strings.Split(strings.Repeat("s ", 17)[:33], " ") }},
	} {
		p := validProviderConfig()
		tc.change(&p)

		if err := p.Validate(); !errors.Is(err, session.ErrInvalidProvider) {
			t.Errorf("%s: Validate = %v, want ErrInvalidProvider", tc.name, err)
		}
	}

	for _, issuer := range []string{"http://127.0.0.1:9000", "http://localhost:9000/realms/corp", "http://[::1]:9000"} {
		p := validProviderConfig()
		p.Issuer = issuer

		if err := p.Validate(); err != nil {
			t.Errorf("issuer %q on a loopback host: Validate = %v, want nil", issuer, err)
		}
	}

	if got := session.SubjectPrefix("corp"); got != "corp:" {
		t.Fatalf("SubjectPrefix = %q, want corp:", got)
	}
}
