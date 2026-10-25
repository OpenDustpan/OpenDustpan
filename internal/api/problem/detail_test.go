package problem_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
)

// TestDetail checks that Detail returns the text after the sentinel, and never
// the wrap prefix that comes before it.
func TestDetail(t *testing.T) {
	t.Parallel()

	const sentinel = textError("executor does not support this operation")

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			"detail after the sentinel",
			textError("plan job: executor does not support this operation: the unit has no code location"),
			"the unit has no code location",
		},
		{
			"bare sentinel",
			sentinel,
			"executor does not support this operation",
		},
		{
			"sentinel not followed by a colon and a space",
			textError("plan job: executor does not support this operation."),
			"executor does not support this operation",
		},
		{
			"empty detail",
			textError("plan job: executor does not support this operation: "),
			"executor does not support this operation",
		},
		{
			"error that does not wrap the sentinel",
			textError("plan job: something else"),
			"executor does not support this operation",
		},
		{
			"sentinel twice keeps the text after the first",
			textError("a: executor does not support this operation: b: executor does not support this operation: c"),
			"b: executor does not support this operation: c",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := problem.Detail(tc.err, sentinel); got != tc.want {
				t.Errorf("Detail = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRefusedTextCutsTheText checks that RefusedText cleans and cuts the text
// that it is given, as Refused does for the text of an error.
func TestRefusedTextCutsTheText(t *testing.T) {
	t.Parallel()

	p := problem.RefusedText("bad\x07name", http.StatusUnprocessableEntity, errcode.JobUnsupported)

	if p.Status != http.StatusUnprocessableEntity || p.Code != errcode.JobUnsupported {
		t.Fatalf("problem = %+v, want 422 %s", p, errcode.JobUnsupported)
	}

	if want := `bad\u0007name`; p.Message != want {
		t.Errorf("message = %q, want %q", p.Message, want)
	}
}
