package agent_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/agent"
)

// openSelfHosted is a self-hosted call by the built-in agent with every stage
// open.
func openSelfHosted() agent.SecretAccess {
	return agent.SecretAccess{
		Hosted:         false,
		StartFlag:      true,
		Accepted:       true,
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
			name:   "flag but no acceptance",
			change: func(s *agent.SecretAccess) { s.Accepted = false },
			want:   agent.ErrSecretsNotAccepted,
		},
		{
			name:   "acceptance without the flag is no flag",
			change: func(s *agent.SecretAccess) { s.StartFlag = false },
			want:   agent.ErrSecretsNoFlag,
		},
		{
			name:   "account opt-in does not replace the flag on a self-hosted server",
			change: func(s *agent.SecretAccess) { s.StartFlag, s.Accepted, s.AccountOptIn = false, false, true },
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
			change: func(s *agent.SecretAccess) { s.Accepted, s.UserOptIn = false, false },
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
				s.Hosted, s.StartFlag, s.Accepted, s.AccountOptIn = true, false, false, true
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

func TestSecretAccessExposed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(s *agent.SecretAccess)
		want   bool
	}{
		{name: "flag and acceptance", change: func(*agent.SecretAccess) {}, want: true},
		{name: "flag only", change: func(s *agent.SecretAccess) { s.Accepted = false }, want: false},
		{
			name:   "opt-ins of the namespace and the person do not matter",
			change: func(s *agent.SecretAccess) { s.NamespaceOptIn, s.UserOptIn = false, false },
			want:   true,
		},
		{
			name:   "hosted opt-in",
			change: func(s *agent.SecretAccess) { s.Hosted, s.AccountOptIn = true, true },
			want:   true,
		},
		{name: "hosted ignores the flag", change: func(s *agent.SecretAccess) { s.Hosted = true }, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := openSelfHosted()
			tt.change(&s)

			if got := s.Exposed(); got != tt.want {
				t.Fatalf("Exposed() = %v, want %v", got, tt.want)
			}
		})
	}
}
