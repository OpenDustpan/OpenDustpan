package objectcontext

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// TestEntryWireIsTheJSONOfAnEntry proves that the type which the description
// uses for an entry reads and writes the JSON that Entry writes, with each
// member set and with the optional members left out.
func TestEntryWireIsTheJSONOfAnEntry(t *testing.T) {
	t.Parallel()

	ref := objectcontext.Ref{
		Namespace: "acme", Kind: objectcontext.KindNamespace, Name: "", Resource: "",
		Unit: graph.Unit{Namespace: "", Tool: "", ID: ""},
	}

	full := objectcontext.Entry{
		UpdatedAt: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC), Object: ref, Source: objectcontext.SourceAPI,
		Readme: "readme", Item: "item", Warning: "warning", UpdatedBy: "alice",
		Fields: objectcontext.Fields{
			Summary: "s", Runbook: "r", Owners: []string{"group:ops"}, OnCall: "pager",
			Links: []objectcontext.Link{{Title: "t", URL: "https://example.org", Kind: objectcontext.LinkDocs}},
		},
	}

	sparse := objectcontext.Entry{
		UpdatedAt: time.Time{}, Object: ref, Source: objectcontext.SourceOutput, Readme: "", Item: "",
		Warning: "", UpdatedBy: "",
		Fields: objectcontext.Fields{Summary: "", Runbook: "", Owners: nil, OnCall: "", Links: nil},
	}

	for name, entry := range map[string]objectcontext.Entry{"every member": full, "optional members left out": sparse} {
		if err := apidesc.VerifyAs[objectcontext.Entry, entryWire](entry); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestAnswerWireIsTheJSONOfAnAnswer proves that the type which the description
// uses for the answer of a section reads and writes the JSON that Answer writes.
func TestAnswerWireIsTheJSONOfAnAnswer(t *testing.T) {
	t.Parallel()

	answers := map[string]objectcontext.Answer{
		"a section with data": {
			Data:   objectcontext.ReadmeData{Readme: "text"},
			Action: identity.ActionUnitRead, Object: objectcontext.RoleTarget, Status: objectcontext.StatusPartial,
			Layers: []objectcontext.LayerStatus{{
				Object: objectcontext.KindUnit, Action: identity.ActionUnitRead, Status: objectcontext.StatusOK,
			}},
			Hidden: 2,
		},
		"a denied section": {
			Data: nil, Action: identity.ActionUnitRead, Object: objectcontext.RoleTarget,
			Status: objectcontext.StatusDenied, Layers: nil, Hidden: 0,
		},
	}

	for name, answer := range answers {
		if err := apidesc.VerifyAs[objectcontext.Answer, answerWire](answer); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
