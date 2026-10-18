package codec_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/objectcontext"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
)

// keyOf returns a context key with all its parts.
func keyOf(kind, objectID, tool, resourceKey string) codec.ContextKey {
	return codec.ContextKey{Kind: kind, ObjectID: objectID, Tool: tool, ResourceKey: resourceKey}
}

// rowOf returns a context row with the source, the fields text and the warning.
func rowOf(source, fields, warning string) codec.ContextRow {
	return codec.ContextRow{
		Tool: "", Source: source, Fields: fields, Warning: warning, ItemID: "", UpdatedBy: "", ItemSeq: 0,
		UpdatedAtMs: 0,
	}
}

func TestContextKeyOf(t *testing.T) {
	t.Parallel()

	unit := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-1"}

	cases := []struct {
		name         string
		ref          objectcontext.Ref
		collectionID string
		want         codec.ContextKey
		wantErr      bool
	}{
		{"namespace", objectcontext.NamespaceRef("acme"), "", keyOf("namespace", "", "", ""), false},
		{
			"collection", objectcontext.CollectionRef("acme", "web"), "col-1",
			keyOf("collection", "col-1", "", ""), false,
		},
		{"unit", objectcontext.UnitRef(unit), "", keyOf("unit", "ws-1", "opentofu", ""), false},
		{
			"resource", objectcontext.ResourceRef(unit, "aws_s3_bucket.logs"), "",
			keyOf("resource", "ws-1", "opentofu", "aws_s3_bucket.logs"), false,
		},
		{
			"blueprint has no stored context yet",
			objectcontext.BlueprintRef("acme", "svc"),
			"",
			keyOf("", "", "", ""),
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := codec.ContextKeyOf(tc.ref, tc.collectionID)
			if (err != nil) != tc.wantErr || (err != nil && !errors.Is(err, objectcontext.ErrInvalidObject)) {
				t.Fatalf("ContextKeyOf: %v, want error %v", err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("ContextKeyOf = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestContextEntryRoundTrip(t *testing.T) {
	t.Parallel()

	ref := objectcontext.NamespaceRef("acme")
	want := objectcontext.Entry{
		UpdatedAt: time.UnixMilli(5).UTC(), Object: ref, Source: objectcontext.SourceAPI, Readme: "", Item: "",
		Warning: "", UpdatedBy: "user:ada",
		Fields: objectcontext.Fields{
			Summary: "s", Runbook: "", Owners: []string{"group:a"}, OnCall: "p", Links: nil,
		},
	}

	text, err := codec.EncodeContextFields(want)
	if err != nil {
		t.Fatalf("EncodeContextFields: %v", err)
	}

	row := rowOf("api", text, "")
	row.UpdatedAtMs, row.UpdatedBy = 5, "user:ada"

	got, hidden, err := codec.ContextEntry(ref, row)
	if err != nil || hidden {
		t.Fatalf("ContextEntry: hidden %v, %v", hidden, err)
	}

	if got.Fields.Summary != "s" || got.Fields.OnCall != "p" || got.Fields.Owners[0] != "group:a" ||
		got.UpdatedBy != "user:ada" || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("ContextEntry = %+v, want %+v", got, want)
	}

	if _, _, err = codec.ContextEntry(ref, rowOf("api", "not json", "")); err == nil {
		t.Error("ContextEntry with a broken fields column: want an error")
	}
}

// TestContextEntryHidden proves which rows a reader never sees: the order
// marker that ReplaceOutput keeps for a unit whose output is empty, and no
// other row.
func TestContextEntryHidden(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		row    codec.ContextRow
		hidden bool
	}{
		{"an output with no fields is the order marker", rowOf("output", "{}", ""), true},
		{"an API entry with no fields is a real entry", rowOf("api", "{}", ""), false},
		{"an output with a warning is a real entry", rowOf("output", "{}", "bad"), false},
		{"an output with a readme is a real entry", rowOf("output", `{"readme":"x"}`, ""), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, hidden, err := codec.ContextEntry(objectcontext.NamespaceRef("acme"), tc.row)
			if err != nil || hidden != tc.hidden {
				t.Errorf("ContextEntry: hidden %v, %v; want hidden %v", hidden, err, tc.hidden)
			}
		})
	}
}

func TestContextCursor(t *testing.T) {
	t.Parallel()

	in := codec.ContextCursor{Kind: "unit", ObjectID: "ws-1", ResourceKey: "", Source: "api"}

	out, err := codec.DecodeContextCursor(codec.EncodeContextCursor(in))
	if err != nil || out != in {
		t.Fatalf("round trip = %+v, %v; want %+v", out, err, in)
	}

	start, err := codec.DecodeContextCursor("")
	if err != nil || start != (codec.ContextCursor{Kind: "", ObjectID: "", ResourceKey: "", Source: ""}) {
		t.Errorf("empty cursor = %+v, %v; want the start of the list", start, err)
	}

	for _, bad := range []string{"not a cursor", "e30", "!!!", strings.Repeat("a", 5000)} {
		if _, err := codec.DecodeContextCursor(bad); !errors.Is(err, objectcontext.ErrInvalidContext) {
			t.Errorf("DecodeContextCursor(%.20q): %v, want ErrInvalidContext", bad, err)
		}
	}
}
