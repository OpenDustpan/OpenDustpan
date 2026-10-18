package agent_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/agent"
)

func TestParseContextOutput(t *testing.T) {
	t.Parallel()

	var noContext agent.ContextOutput

	tests := []struct {
		name    string
		raw     string
		want    agent.ContextOutput
		wantErr error
	}{
		{
			name: "object",
			raw: `{"schemaVersion":1,"summary":"The billing database.","owners":["team-billing"],` +
				`"onCall":"billing-primary","links":[{"title":"Runbook","url":"https://wiki.example/billing"}]}`,
			want: agent.ContextOutput{
				SchemaVersion: 1,
				Summary:       "The billing database.",
				Runbook:       "",
				Owners:        []string{"team-billing"},
				OnCall:        "billing-primary",
				Links:         []agent.ContextLink{{Title: "Runbook", URL: "https://wiki.example/billing"}},
			},
			wantErr: nil,
		},
		{
			name: "string that holds an object",
			raw:  `"{\"schemaVersion\":1,\"summary\":\"Edge proxies.\"}"`,
			want: agent.ContextOutput{
				SchemaVersion: 1,
				Summary:       "Edge proxies.",
				Runbook:       "",
				Owners:        nil,
				OnCall:        "",
				Links:         nil,
			},
			wantErr: nil,
		},
		{name: "no schema version", raw: `{"summary":"x"}`, want: noContext, wantErr: agent.ErrContextInvalid},
		{name: "newer schema version", raw: `{"schemaVersion":2}`, want: noContext, wantErr: agent.ErrContextVersion},
		{
			name:    "unknown field",
			raw:     `{"schemaVersion":1,"title":"x"}`,
			want:    noContext,
			wantErr: agent.ErrContextInvalid,
		},
		{name: "not JSON", raw: `billing database`, want: noContext, wantErr: agent.ErrContextInvalid},
		{
			name:    "two values",
			raw:     `{"schemaVersion":1} {"schemaVersion":1}`,
			want:    noContext,
			wantErr: agent.ErrContextInvalid,
		},
		{name: "array", raw: `[1]`, want: noContext, wantErr: agent.ErrContextInvalid},
		{
			name:    "link with another scheme",
			raw:     `{"schemaVersion":1,"links":[{"title":"x","url":"javascript:alert(1)"}]}`,
			want:    noContext,
			wantErr: agent.ErrContextInvalid,
		},
		{
			name:    "link with no title",
			raw:     `{"schemaVersion":1,"links":[{"title":" ","url":"https://wiki.example"}]}`,
			want:    noContext,
			wantErr: agent.ErrContextInvalid,
		},
		{
			name:    "too large",
			raw:     `{"schemaVersion":1,"summary":"` + strings.Repeat("a", agent.MaxContextBytes) + `"}`,
			want:    noContext,
			wantErr: agent.ErrContextInvalid,
		},
		{
			name:    "too many owners",
			raw:     `{"schemaVersion":1,"owners":["a"` + strings.Repeat(`,"a"`, agent.MaxContextOwners) + `]}`,
			want:    noContext,
			wantErr: agent.ErrContextInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := agent.ParseContextOutput([]byte(tt.raw))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ParseContextOutput() error = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr != nil {
				return
			}

			if !sameContext(got, tt.want) {
				t.Fatalf("ParseContextOutput() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func sameContext(a, b agent.ContextOutput) bool {
	if a.SchemaVersion != b.SchemaVersion || a.Summary != b.Summary || a.Runbook != b.Runbook ||
		a.OnCall != b.OnCall || len(a.Owners) != len(b.Owners) || len(a.Links) != len(b.Links) {
		return false
	}

	for i := range a.Owners {
		if a.Owners[i] != b.Owners[i] {
			return false
		}
	}

	for i := range a.Links {
		if a.Links[i] != b.Links[i] {
			return false
		}
	}

	return true
}
