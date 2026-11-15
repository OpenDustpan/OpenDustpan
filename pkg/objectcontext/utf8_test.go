package objectcontext_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// TestValidateRefusesHostileText proves that the domain check refuses each
// text field that holds bytes that are not valid UTF-8 or a hidden rune (for
// example a format character or a line separator), also when the HTTP
// adapter does not refuse them first.
func TestValidateRefusesHostileText(t *testing.T) {
	t.Parallel()

	const bad = "a\xffb"

	cases := []struct {
		name   string
		fields objectcontext.Fields
	}{
		{name: "summary", fields: objectcontext.Fields{
			Summary: bad, Runbook: "", Owners: nil, OnCall: "", Links: nil,
		}},
		{name: "runbook", fields: objectcontext.Fields{
			Summary: "", Runbook: bad, Owners: nil, OnCall: "", Links: nil,
		}},
		{name: "on-call", fields: objectcontext.Fields{
			Summary: "", Runbook: "", Owners: nil, OnCall: bad, Links: nil,
		}},
		{name: "owner", fields: objectcontext.Fields{
			Summary: "", Runbook: "", Owners: []string{bad}, OnCall: "", Links: nil,
		}},
		{name: "link title", fields: objectcontext.Fields{
			Summary: "", Runbook: "", Owners: nil, OnCall: "",
			Links: []objectcontext.Link{{Title: bad, URL: "https://example.org", Kind: objectcontext.LinkDocs}},
		}},
		{name: "summary with a line separator", fields: objectcontext.Fields{
			Summary: "a\u2028b", Runbook: "", Owners: nil, OnCall: "", Links: nil,
		}},
		{name: "runbook with a line separator", fields: objectcontext.Fields{
			Summary: "", Runbook: "a\u2028b", Owners: nil, OnCall: "", Links: nil,
		}},
		{name: "on-call with a format character", fields: objectcontext.Fields{
			Summary: "", Runbook: "", Owners: nil, OnCall: "a\u202eb", Links: nil,
		}},
		{name: "owner with a format character", fields: objectcontext.Fields{
			Summary: "", Runbook: "", Owners: []string{"a\u202eb"}, OnCall: "", Links: nil,
		}},
		{name: "link title with a line separator", fields: objectcontext.Fields{
			Summary: "", Runbook: "", Owners: nil, OnCall: "",
			Links: []objectcontext.Link{{Title: "a\u2028b", URL: "https://example.org", Kind: objectcontext.LinkDocs}},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := tc.fields.Validate(); !errors.Is(err, objectcontext.ErrInvalidContext) {
				t.Errorf("Validate() = %v, want ErrInvalidContext", err)
			}
		})
	}
}
