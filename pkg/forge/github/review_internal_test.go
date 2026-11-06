package github

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

func goodReview() forge.ReviewRequest {
	return forge.ReviewRequest{Branch: "dustpan/j-1", Target: "main", Title: "Update the stacks", Body: "Body.\n"}
}

// TestCheckReviewLocalStopsHostileRequests sends hostile requests to the checks
// of the adapter alone, as if the check of the port and the check of GitHub were
// not there.
func TestCheckReviewLocalStopsHostileRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*forge.ReviewRequest)
	}{
		{"a branch with a dot-dot", func(r *forge.ReviewRequest) { r.Branch = "a/../b" }},
		{"a branch that is a full ref", func(r *forge.ReviewRequest) { r.Branch = "refs/heads/x" }},
		{"a branch with a query", func(r *forge.ReviewRequest) { r.Branch = "x?y=z" }},
		{"a target with a space", func(r *forge.ReviewRequest) { r.Target = "a b" }},
		{"the same branch and target", func(r *forge.ReviewRequest) { r.Target = r.Branch }},
		{"an empty title", func(r *forge.ReviewRequest) { r.Title = " \t" }},
		{"a title with a line break", func(r *forge.ReviewRequest) { r.Title = "a\nb" }},
		{"a title with a NUL", func(r *forge.ReviewRequest) { r.Title = "a\x00b" }},
		{"a long title", func(r *forge.ReviewRequest) { r.Title = strings.Repeat("t", forge.MaxReviewTitleBytes+1) }},
		{"a description with an escape", func(r *forge.ReviewRequest) { r.Body = "a\x1b[2J" }},
		{"a description that is not UTF-8", func(r *forge.ReviewRequest) { r.Body = "a\xc3" }},
		{
			"a long description",
			func(r *forge.ReviewRequest) { r.Body = strings.Repeat("b", forge.MaxReviewBodyBytes+1) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := goodReview()
			tt.edit(&req)

			if err := checkReviewLocal(req); !errors.Is(err, forge.ErrInvalid) {
				t.Fatalf("CheckReviewLocal = %v, want ErrInvalid", err)
			}
		})
	}

	if err := checkReviewLocal(goodReview()); err != nil {
		t.Fatalf("CheckReviewLocal of a good request = %v", err)
	}
}
