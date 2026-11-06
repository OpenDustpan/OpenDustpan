package forge_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

func validStatus() forge.Status {
	return forge.Status{
		State: forge.StatusSuccess, Context: "opendustpan/plan", Description: "Plan ready",
		TargetURL: "https://dustpan.example/runs/1?tab=plan",
	}
}

func TestValidateStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*forge.Status)
		want error
	}{
		{"a valid status", func(*forge.Status) {}, nil},
		{"the state pending", func(s *forge.Status) { s.State = forge.StatusPending }, nil},
		{"the state failure", func(s *forge.Status) { s.State = forge.StatusFailure }, nil},
		{"the state error", func(s *forge.Status) { s.State = forge.StatusError }, nil},
		{"no description and no address", func(s *forge.Status) { s.Description, s.TargetURL = "", "" }, nil},
		{"a context with a space and a slash", func(s *forge.Status) { s.Context = "Dustpan / acme/prod" }, nil},
		{"a context with letters of another script", func(s *forge.Status) { s.Context = "Prüfung" }, nil},
		{
			"a context at the limit",
			func(s *forge.Status) { s.Context = strings.Repeat("c", forge.MaxStatusContextBytes) },
			nil,
		},
		{
			"a description at the limit",
			func(s *forge.Status) { s.Description = strings.Repeat("d", forge.MaxStatusDescriptionBytes) },
			nil,
		},
		{"no state", func(s *forge.Status) { s.State = "" }, forge.ErrInvalid},
		{"a state in upper case", func(s *forge.Status) { s.State = "SUCCESS" }, forge.ErrInvalid},
		{"an unknown state", func(s *forge.Status) { s.State = "queued" }, forge.ErrInvalid},
		{"no context", func(s *forge.Status) { s.Context = "" }, forge.ErrInvalid},
		{"a context of spaces", func(s *forge.Status) { s.Context = " \t " }, forge.ErrInvalid},
		{"a line break in the context", func(s *forge.Status) { s.Context = "a\nb" }, forge.ErrInvalid},
		{"a null byte in the context", func(s *forge.Status) { s.Context = "a\x00b" }, forge.ErrInvalid},
		{"a line separator in the context", func(s *forge.Status) { s.Context = "a b" }, forge.ErrInvalid},
		{"a context that is not UTF-8", func(s *forge.Status) { s.Context = "a\xffb" }, forge.ErrInvalid},
		{
			"a context above the limit",
			func(s *forge.Status) { s.Context = strings.Repeat("c", forge.MaxStatusContextBytes+1) },
			forge.ErrTooLarge,
		},
		{"a line break in the description", func(s *forge.Status) { s.Description = "a\r\nb" }, forge.ErrInvalid},
		{"a description that is not UTF-8", func(s *forge.Status) { s.Description = "\xc3" }, forge.ErrInvalid},
		{
			"a description above the limit",
			func(s *forge.Status) { s.Description = strings.Repeat("d", forge.MaxStatusDescriptionBytes+1) },
			forge.ErrTooLarge,
		},
		{"an address with http", func(s *forge.Status) { s.TargetURL = "http://dustpan.example/" }, forge.ErrInvalid},
		{
			"an address with a script scheme",
			func(s *forge.Status) { s.TargetURL = "javascript:alert(1)" },
			forge.ErrInvalid,
		},
		{"an address with a data scheme", func(s *forge.Status) { s.TargetURL = "data:text/html,x" }, forge.ErrInvalid},
		{
			"an address with no scheme",
			func(s *forge.Status) { s.TargetURL = "dustpan.example/runs/1" },
			forge.ErrInvalid,
		},
		{"an address with no host", func(s *forge.Status) { s.TargetURL = "https:///runs" }, forge.ErrInvalid},
		{
			"an address with user information",
			func(s *forge.Status) { s.TargetURL = "https://u:p@dustpan.example/" },
			forge.ErrInvalid,
		},
		{
			"an address with a fragment",
			func(s *forge.Status) { s.TargetURL = "https://dustpan.example/#x" },
			forge.ErrInvalid,
		},
		{
			"an address with a space",
			func(s *forge.Status) { s.TargetURL = "https://dustpan.example/a b" },
			forge.ErrInvalid,
		},
		{
			"an address with a line break",
			func(s *forge.Status) { s.TargetURL = "https://dustpan.example/\nx" },
			forge.ErrInvalid,
		},
		{
			"an address above the limit",
			func(s *forge.Status) {
				s.TargetURL = "https://dustpan.example/" + strings.Repeat("p", forge.MaxStatusURLBytes)
			},
			forge.ErrTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := validStatus()
			tt.edit(&s)

			err := forge.ValidateStatus(s)
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("ValidateStatus = %v, want %v", err, tt.want)
			}
		})
	}
}
