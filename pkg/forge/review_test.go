package forge_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

func validReview() forge.ReviewRequest {
	return forge.ReviewRequest{
		Branch: "dustpan/j-1",
		Target: "main",
		Title:  "Update the stacks",
		Body:   "Body.\nLine two.\n",
	}
}

func TestValidateReviewRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*forge.ReviewRequest)
		want error
	}{
		{"a valid request", func(*forge.ReviewRequest) {}, nil},
		{"an empty description", func(r *forge.ReviewRequest) { r.Body = "" }, nil},
		{"a description with a tab and line breaks", func(r *forge.ReviewRequest) { r.Body = "a\tb\r\nc" }, nil},
		{"an empty branch", func(r *forge.ReviewRequest) { r.Branch = "" }, forge.ErrInvalid},
		{"a branch with a dot-dot", func(r *forge.ReviewRequest) { r.Branch = "a/../b" }, forge.ErrInvalid},
		{
			"a target that is a full ref",
			func(r *forge.ReviewRequest) { r.Target = "refs/heads/main" },
			forge.ErrInvalid,
		},
		{"the same branch and target", func(r *forge.ReviewRequest) { r.Target = r.Branch }, forge.ErrInvalid},
		{"an empty title", func(r *forge.ReviewRequest) { r.Title = "  " }, forge.ErrInvalid},
		{"a title with a line break", func(r *forge.ReviewRequest) { r.Title = "a\nb" }, forge.ErrInvalid},
		{"a title with an escape", func(r *forge.ReviewRequest) { r.Title = "a\x1b[31m" }, forge.ErrInvalid},
		{"a title that is not UTF-8", func(r *forge.ReviewRequest) { r.Title = "a\xff" }, forge.ErrInvalid},
		{
			"a long title",
			func(r *forge.ReviewRequest) { r.Title = strings.Repeat("t", forge.MaxReviewTitleBytes+1) },
			forge.ErrTooLarge,
		},
		{"a description with a NUL", func(r *forge.ReviewRequest) { r.Body = "a\x00" }, forge.ErrInvalid},
		{
			"a long description",
			func(r *forge.ReviewRequest) { r.Body = strings.Repeat("b", forge.MaxReviewBodyBytes+1) },
			forge.ErrTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := validReview()
			tt.edit(&req)

			err := forge.ValidateReviewRequest(req)
			if !errors.Is(err, tt.want) && (tt.want != nil || err != nil) {
				t.Fatalf("ValidateReviewRequest = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestIsWriteBackBranch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		branch string
		want   bool
	}{
		{"dustpan/j-1", true},
		{"dustpan/j-1/part", true},
		{"dustpan/", false},
		{"dustpan", false},
		{"main", false},
		{"Dustpan/j-1", false},
		{"feature/dustpan/j-1", false},
		{"dustpan/../main", false},
		{"dustpan//j-1", false},
		{"dustpan/j 1", false},
		{"dustpan/j-1\n", false},
		{"refs/heads/dustpan/j-1", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := forge.IsWriteBackBranch(tt.branch); got != tt.want {
			t.Errorf("IsWriteBackBranch(%q) = %v, want %v", tt.branch, got, tt.want)
		}
	}
}

func TestReviewFromFork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		base, head string
		want       bool
	}{
		{"the same repository", "10", "10", false},
		{"another repository", "10", "11", true},
		{"a head with no ID", "10", "", true},
		{"a base with no ID", "", "10", true},
		{"no ID at all", "", "", true},
	}

	for _, tt := range tests {
		var r forge.Review

		r.BaseRepoID, r.HeadRepoID = tt.base, tt.head
		if got := r.FromFork(); got != tt.want {
			t.Errorf("FromFork with %s = %v, want %v", tt.name, got, tt.want)
		}
	}
}
