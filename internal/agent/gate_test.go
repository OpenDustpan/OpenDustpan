package agent_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/agent"
)

// openSelfHosted is a self-hosted call by the built-in agent with every stage
// open.
func openSelfHosted() agent.SecretAccess {
	return agent.SecretAccess{
		Hosted:         false,
		StartFlag:      true,
		WindowAccepted: true,
		AccountOptIn:   false,
		NamespaceOptIn: true,
		UserOptIn:      true,
		Channel:        agent.ChannelAgent,
		UseConsent:     false,
	}
}

func TestSecretAccessCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(s *agent.SecretAccess)
		want   error
	}{
		{name: "every stage open", change: func(*agent.SecretAccess) {}, want: nil},
		{
			name:   "no start flag",
			change: func(s *agent.SecretAccess) { s.StartFlag = false },
			want:   agent.ErrSecretsNoFlag,
		},
		{
			name:   "flag but no window acceptance",
			change: func(s *agent.SecretAccess) { s.WindowAccepted = false },
			want:   agent.ErrSecretsNotAccepted,
		},
		{
			name:   "window acceptance without the flag is no flag",
			change: func(s *agent.SecretAccess) { s.StartFlag = false },
			want:   agent.ErrSecretsNoFlag,
		},
		{
			name:   "account opt-in does not replace the flag on a self-hosted server",
			change: func(s *agent.SecretAccess) { s.StartFlag, s.WindowAccepted, s.AccountOptIn = false, false, true },
			want:   agent.ErrSecretsNoFlag,
		},
		{
			name:   "no namespace opt-in",
			change: func(s *agent.SecretAccess) { s.NamespaceOptIn = false },
			want:   agent.ErrSecretsNoNamespaceOptIn,
		},
		{
			name:   "no person opt-in",
			change: func(s *agent.SecretAccess) { s.UserOptIn = false },
			want:   agent.ErrSecretsNoUserOptIn,
		},
		{
			name:   "first closed stage wins",
			change: func(s *agent.SecretAccess) { s.WindowAccepted, s.UserOptIn = false, false },
			want:   agent.ErrSecretsNotAccepted,
		},
		{
			name:   "mcp call without consent",
			change: func(s *agent.SecretAccess) { s.Channel = agent.ChannelMCP },
			want:   agent.ErrSecretsNoUseConsent,
		},
		{
			name:   "proxy call without consent",
			change: func(s *agent.SecretAccess) { s.Channel = agent.ChannelProxy },
			want:   agent.ErrSecretsNoUseConsent,
		},
		{
			name:   "mcp call with consent",
			change: func(s *agent.SecretAccess) { s.Channel, s.UseConsent = agent.ChannelMCP, true },
			want:   nil,
		},
		{
			name: "hosted with account opt-in needs no flag",
			change: func(s *agent.SecretAccess) {
				s.Hosted, s.StartFlag, s.WindowAccepted, s.AccountOptIn = true, false, false, true
			},
			want: nil,
		},
		{
			name:   "hosted without account opt-in",
			change: func(s *agent.SecretAccess) { s.Hosted, s.AccountOptIn = true, false },
			want:   agent.ErrSecretsNoAccountOptIn,
		},
		{
			name:   "hosted still needs the namespace opt-in",
			change: func(s *agent.SecretAccess) { s.Hosted, s.AccountOptIn, s.NamespaceOptIn = true, true, false },
			want:   agent.ErrSecretsNoNamespaceOptIn,
		},
		{
			name:   "unknown channel",
			change: func(s *agent.SecretAccess) { s.Channel = "console" },
			want:   agent.ErrInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := openSelfHosted()
			tt.change(&s)
			err := s.Check()

			if tt.want == nil {
				if err != nil {
					t.Fatalf("Check() = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, agent.ErrSecretsClosed) || !errors.Is(err, tt.want) {
				t.Fatalf("Check() = %v, want an error that wraps ErrSecretsClosed and %v", err, tt.want)
			}
		})
	}
}

func TestExposed(t *testing.T) {
	t.Parallel()

	now := time.Now()
	active := agent.AccountSecretOptIn{
		Acceptance: agent.Acceptance{Subject: "admin", Text: agent.AcceptanceText, MFAAt: now, At: now},
		EndedAt:    time.Time{},
	}
	ended := active
	ended.EndedAt = now

	var none agent.AccountSecretOptIn

	tests := []struct {
		name    string
		hosted  bool
		account agent.AccountSecretOptIn
		windows bool
		want    bool
	}{
		{name: "an accepted window covers a live instance", hosted: false, account: none, windows: true, want: true},
		{name: "no window covers an instance", hosted: false, account: none, windows: false, want: false},
		{name: "self-hosted ignores the account", hosted: false, account: active, windows: false, want: false},
		{name: "hosted opt-in", hosted: true, account: active, windows: false, want: true},
		{name: "hosted opt-out", hosted: true, account: ended, windows: false, want: false},
		{name: "hosted ignores the windows", hosted: true, account: none, windows: true, want: false},
	}

	for _, tt := range tests {
		e := agent.Exposure{Hosted: tt.hosted, Account: tt.account, Windows: tt.windows}
		if got := e.Exposed(); got != tt.want {
			t.Fatalf("%s: Exposed() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestConsentPath(t *testing.T) {
	t.Parallel()

	if got := agent.ConsentPath("c/1"); got != "/agent/secret-consents/c%2F1" {
		t.Fatalf("ConsentPath() = %q, want the ID escaped in one path segment", got)
	}
}

func TestSecretStage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		change func(s *agent.SecretAccess)
		want   string
	}{
		{func(s *agent.SecretAccess) { s.StartFlag = false }, agent.StageFlag},
		{func(s *agent.SecretAccess) { s.WindowAccepted = false }, agent.StageWindow},
		{func(s *agent.SecretAccess) { s.Hosted = true }, agent.StageAccount},
		{func(s *agent.SecretAccess) { s.NamespaceOptIn = false }, agent.StageNamespace},
		{func(s *agent.SecretAccess) { s.UserOptIn = false }, agent.StagePerson},
		{func(s *agent.SecretAccess) { s.Channel = agent.ChannelProxy }, agent.StageConsent},
		{func(*agent.SecretAccess) {}, ""},
	}

	for _, tt := range tests {
		s := openSelfHosted()
		tt.change(&s)

		if got := agent.SecretStage(s.Check()); got != tt.want {
			t.Fatalf("SecretStage() = %q, want %q", got, tt.want)
		}
	}
}
