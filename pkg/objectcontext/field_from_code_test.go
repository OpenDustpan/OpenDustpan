package objectcontext_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// TestCheckPatchNamesTheFieldFromCode proves that a patch of a field that the
// output of the code sets fails with a *FieldFromCodeError that names the
// field, matches ErrFieldFromCode and keeps the text of the sentinel.
func TestCheckPatchNamesTheFieldFromCode(t *testing.T) {
	t.Parallel()

	fromCode := out(
		unitRef,
		objectcontext.Fields{Owners: []string{"group:web"}, Summary: "code", Runbook: "", OnCall: "", Links: nil},
	)

	cases := []struct {
		name  string
		patch objectcontext.Patch
		want  objectcontext.Field
	}{
		{
			"owners",
			objectcontext.Patch{
				Owners: &[]string{"group:ops"}, Summary: nil, Runbook: nil, OnCall: nil, Links: nil,
			},
			objectcontext.FieldOwners,
		},
		{
			"summary",
			objectcontext.Patch{
				Summary: new("x"), Owners: nil, Runbook: nil, OnCall: nil, Links: nil,
			},
			objectcontext.FieldSummary,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := objectcontext.CheckPatch(fromCode, tc.patch)

			field, ok := errors.AsType[*objectcontext.FieldFromCodeError](err)
			if !ok || field.Field != tc.want || !errors.Is(err, objectcontext.ErrFieldFromCode) {
				t.Fatalf("CheckPatch = %v, want a *FieldFromCodeError for %s", err, tc.want)
			}

			if want := objectcontext.ErrFieldFromCode.Error() + ": " + string(tc.want); err.Error() != want {
				t.Errorf("text = %q, want %q", err.Error(), want)
			}
		})
	}
}
