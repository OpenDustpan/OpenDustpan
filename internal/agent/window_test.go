package agent_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/agent"
)

// t0 is the time of the cases.
func t0() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC) }

func acceptance() agent.Acceptance {
	return agent.Acceptance{Subject: "ana", Text: agent.AcceptanceText, MFAAt: t0().Add(-time.Minute), At: t0()}
}

func TestAcceptanceValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(a *agent.Acceptance)
		want   error
	}{
		{name: "valid", change: func(*agent.Acceptance) {}, want: nil},
		{name: "no subject", change: func(a *agent.Acceptance) { a.Subject = "" }, want: agent.ErrInvalid},
		{name: "no time", change: func(a *agent.Acceptance) { a.At = time.Time{} }, want: agent.ErrInvalid},
		{name: "other text", change: func(a *agent.Acceptance) { a.Text = "i accept" }, want: agent.ErrAcceptanceText},
		{name: "no text", change: func(a *agent.Acceptance) { a.Text = "" }, want: agent.ErrAcceptanceText},
		{name: "no MFA step", change: func(a *agent.Acceptance) { a.MFAAt = time.Time{} }, want: agent.ErrMFANotFresh},
		{
			name:   "MFA step too old",
			change: func(a *agent.Acceptance) { a.MFAAt = t0().Add(-agent.MaxMFAAge - time.Second) },
			want:   agent.ErrMFANotFresh,
		},
		{
			name:   "MFA step at the age limit",
			change: func(a *agent.Acceptance) { a.MFAAt = t0().Add(-agent.MaxMFAAge) },
			want:   nil,
		},
		{
			name:   "MFA step after the acceptance",
			change: func(a *agent.Acceptance) { a.MFAAt = t0().Add(time.Second) },
			want:   agent.ErrMFANotFresh,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := acceptance()
			tt.change(&a)

			if err := a.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSecretWindow(t *testing.T) {
	t.Parallel()

	accepted := acceptance()

	tests := []struct {
		name     string
		window   agent.SecretWindow
		instance string
		open     bool
		covers   bool
	}{
		{
			name:     "open window covers nothing",
			window:   window(nil, time.Time{}, "i1", "i2"),
			instance: "i1", open: true, covers: false,
		},
		{
			name:     "one acceptance covers every instance of the window",
			window:   window(&accepted, time.Time{}, "i1", "i2"),
			instance: "i2", open: false, covers: true,
		},
		{
			name:     "an instance outside the window is not covered",
			window:   window(&accepted, time.Time{}, "i1"),
			instance: "i3", open: false, covers: false,
		},
		{
			name:     "a window closed with no acceptance keeps the hatch shut",
			window:   window(nil, t0(), "i1"),
			instance: "i1", open: false, covers: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.window.Open(); got != tt.open {
				t.Fatalf("Open() = %v, want %v", got, tt.open)
			}

			if got := tt.window.Covers(tt.instance); got != tt.covers {
				t.Fatalf("Covers(%q) = %v, want %v", tt.instance, got, tt.covers)
			}
		})
	}
}

func TestConsentUsable(t *testing.T) {
	t.Parallel()

	const unit = "pulumi:acme/web"

	tests := []struct {
		name   string
		change func(c *agent.Consent)
		now    time.Time
		want   error
	}{
		{name: "approved and fresh", change: func(*agent.Consent) {}, now: t0().Add(time.Minute), want: nil},
		{
			name:   "not approved",
			change: func(c *agent.Consent) { c.ApprovedAt = time.Time{} },
			now:    t0(), want: agent.ErrConsentNotUsable,
		},
		{name: "used", change: func(c *agent.Consent) { c.UsedAt = t0() }, now: t0(), want: agent.ErrConsentNotUsable},
		{
			name:   "expired",
			change: func(*agent.Consent) {},
			now:    t0().Add(agent.ConsentTTL + time.Second), want: agent.ErrConsentNotUsable,
		},
		{
			name:   "other person",
			change: func(c *agent.Consent) { c.Subject = "eve" },
			now:    t0(),
			want:   agent.ErrConsentNotUsable,
		},
		{
			name:   "other token",
			change: func(c *agent.Consent) { c.TokenID = "t2" },
			now:    t0(),
			want:   agent.ErrConsentNotUsable,
		},
		{
			name:   "other tool",
			change: func(c *agent.Consent) { c.Tool = agent.ToolDescribeUnit },
			now:    t0(), want: agent.ErrConsentNotUsable,
		},
		{
			name:   "other unit",
			change: func(c *agent.Consent) { c.Unit = "pulumi:acme/db" },
			now:    t0(), want: agent.ErrConsentNotUsable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := agent.Consent{
				ID: "c1", Subject: "ana", TokenID: "t1", SessionID: "s1", Tool: agent.ToolReadState, Unit: unit,
				CreatedAt: t0(), ApprovedAt: t0(), UsedAt: time.Time{},
			}
			tt.change(&c)

			use := agent.ConsentUse{
				ID:        "c1",
				Subject:   "ana",
				TokenID:   "t1",
				SessionID: "s1",
				Tool:      agent.ToolReadState,
				Unit:      unit,
				At:        tt.now,
			}
			if err := c.Usable(use); !errors.Is(err, tt.want) {
				t.Fatalf("Usable() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestAccountSecretOptInActive(t *testing.T) {
	t.Parallel()

	weak := acceptance()
	weak.MFAAt = time.Time{}

	tests := []struct {
		name string
		in   agent.AccountSecretOptIn
		want bool
	}{
		{"valid acceptance", agent.AccountSecretOptIn{Acceptance: acceptance(), EndedAt: time.Time{}}, true},
		{"opted out", agent.AccountSecretOptIn{Acceptance: acceptance(), EndedAt: t0()}, false},
		{"acceptance with no MFA step", agent.AccountSecretOptIn{Acceptance: weak, EndedAt: time.Time{}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.in.Active(); got != tt.want {
				t.Fatalf("Active() = %v, want %v", got, tt.want)
			}
		})
	}
}

func window(a *agent.Acceptance, closedAt time.Time, instances ...string) agent.SecretWindow {
	return agent.SecretWindow{ID: "w1", OpenedAt: t0(), Instances: instances, Acceptance: a, ClosedAt: closedAt}
}
