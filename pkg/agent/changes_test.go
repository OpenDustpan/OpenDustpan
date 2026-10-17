package agent_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/agent"
)

func TestChangePolicyMode(t *testing.T) {
	t.Parallel()

	allDirect := agent.ChangePolicy{ServerDirect: true, NamespaceDirect: true, UserDirect: true}
	writeBack := agent.KindSupport{WriteBack: true}
	noWriteBack := agent.KindSupport{WriteBack: false}

	none := agent.ChangePolicy{ServerDirect: false, NamespaceDirect: false, UserDirect: false}

	tests := []struct {
		name      string
		policy    agent.ChangePolicy
		kind      agent.KindSupport
		requested agent.ChangeMode
		want      agent.ChangeMode
		wantErr   error
	}{
		{"default with write-back", allDirect, writeBack, "", agent.ModeReviewRequest, nil},
		{"default without write-back", allDirect, noWriteBack, "", agent.ModeApproveFirst, nil},
		{"review request", none, writeBack, agent.ModeReviewRequest, agent.ModeReviewRequest, nil},
		{
			"review request without write-back",
			none,
			noWriteBack,
			agent.ModeReviewRequest,
			"",
			agent.ErrModeNotSupported,
		},
		{"approve-first is always allowed", none, writeBack, agent.ModeApproveFirst, agent.ModeApproveFirst, nil},
		{"direct with every switch", allDirect, noWriteBack, agent.ModeDirect, agent.ModeDirect, nil},
		{
			"direct without the server flag",
			agent.ChangePolicy{ServerDirect: false, NamespaceDirect: true, UserDirect: true},
			noWriteBack, agent.ModeDirect, "", agent.ErrDirectNotAllowed,
		},
		{
			"direct without the namespace opt-in",
			agent.ChangePolicy{ServerDirect: true, NamespaceDirect: false, UserDirect: true},
			noWriteBack, agent.ModeDirect, "", agent.ErrDirectNotAllowed,
		},
		{
			"direct without the person opt-in",
			agent.ChangePolicy{ServerDirect: true, NamespaceDirect: true, UserDirect: false},
			noWriteBack, agent.ModeDirect, "", agent.ErrDirectNotAllowed,
		},
		{"unknown mode", allDirect, writeBack, "apply-now", "", agent.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.policy.Mode(tt.kind, tt.requested)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Mode() error = %v, want %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Fatalf("Mode() = %q, want %q", got, tt.want)
			}
		})
	}
}
