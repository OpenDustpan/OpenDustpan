package forge_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

func validCommit() forge.CommitRequest {
	return forge.CommitRequest{
		Branch:    "dustpan/j-1",
		NewBranch: true,
		Base:      "0123456789abcdef0123456789abcdef01234567",
		Message:   "Set the instance size\n\nDustpan-Job: j-1",
		Author:    forge.Person{Name: "Ada", Email: "ada@example.com"},
		Changes: []forge.FileChange{
			{Path: "infra/prod.tfvars.json", Content: []byte(`{"size":"large"}`), Delete: false},
		},
	}
}

func TestValidateCommit(t *testing.T) {
	t.Parallel()

	tooMany := make([]forge.FileChange, forge.MaxChanges+1)
	for i := range tooMany {
		tooMany[i] = forge.FileChange{Path: "f" + strings.Repeat("x", i), Content: nil, Delete: false}
	}

	cases := []struct {
		name string
		edit func(r *forge.CommitRequest)
		want error
	}{
		{"a valid request", func(*forge.CommitRequest) {}, nil},
		{"an empty branch", func(r *forge.CommitRequest) { r.Branch = "" }, forge.ErrInvalid},
		{"a full ref as the branch", func(r *forge.CommitRequest) { r.Branch = "refs/heads/main" }, forge.ErrInvalid},
		{"a branch with two dots", func(r *forge.CommitRequest) { r.Branch = "a..b" }, forge.ErrInvalid},
		{"a branch with a space", func(r *forge.CommitRequest) { r.Branch = "a b" }, forge.ErrInvalid},
		{"no base", func(r *forge.CommitRequest) { r.Base = "" }, forge.ErrInvalid},
		{"no message", func(r *forge.CommitRequest) { r.Message = "" }, forge.ErrInvalid},
		{
			"a long message",
			func(r *forge.CommitRequest) { r.Message = strings.Repeat("m", forge.MaxMessageBytes+1) },
			forge.ErrTooLarge,
		},
		{"no change", func(r *forge.CommitRequest) { r.Changes = nil }, forge.ErrInvalid},
		{"too many changes", func(r *forge.CommitRequest) { r.Changes = tooMany }, forge.ErrInvalid},
		{
			"a path twice",
			func(r *forge.CommitRequest) { r.Changes = append(r.Changes, r.Changes[0]) },
			forge.ErrInvalid,
		},
		{
			"a delete with content",
			func(r *forge.CommitRequest) { r.Changes[0].Delete = true },
			forge.ErrInvalid,
		},
		{
			"content above the limit",
			func(r *forge.CommitRequest) {
				r.Changes[0].Content = make([]byte, forge.MaxChangeBytes+1)
			},
			forge.ErrTooLarge,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			r := validCommit()
			c.edit(&r)

			err := forge.ValidateCommit(r)
			if c.want == nil && err != nil {
				t.Fatalf("ValidateCommit = %v, want nil", err)
			}

			if c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("ValidateCommit = %v, want %v", err, c.want)
			}
		})
	}
}

func TestValidateCommitPaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path string
		ok   bool
	}{
		{"main.tf", true},
		{"infra/prod/Pulumi.prod.yaml", true},
		{"", false},
		{".", false},
		{"/etc/passwd", false},
		{"..", false},
		{"../x", false},
		{"a/../b", false},
		{"a//b", false},
		{"a/", false},
		{"a\x00b", false},
	}

	for _, c := range cases {
		r := validCommit()
		r.Changes = []forge.FileChange{{Path: c.path, Content: []byte("x"), Delete: false}}

		err := forge.ValidateCommit(r)
		if c.ok && err != nil {
			t.Errorf("ValidateCommit with the path %q = %v, want nil", c.path, err)
		}

		if !c.ok && !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("ValidateCommit with the path %q = %v, want ErrInvalid", c.path, err)
		}
	}
}

// TestNeeds holds the table of consumers of the design document, row by row.
func TestNeeds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		consumer forge.Consumer
		need     forge.Capabilities
	}{
		{forge.ConsumerWriteBack, forge.Capabilities{forge.CapFiles, forge.CapCompare, forge.CapCommit}},
		{
			forge.ConsumerWriteBackReview,
			forge.Capabilities{forge.CapFiles, forge.CapCompare, forge.CapCommit, forge.CapReview},
		},
		{forge.ConsumerFetch, forge.Capabilities{forge.CapGitAccess}},
		{forge.ConsumerRunnerGit, forge.Capabilities{forge.CapRepoToken}},
		{forge.ConsumerCatalog, forge.Capabilities{forge.CapFiles}},
		{forge.ConsumerCauseDiff, forge.Capabilities{forge.CapCompare}},
		{forge.ConsumerTriggers, forge.Capabilities{forge.CapEvents, forge.CapFiles, forge.CapCompare}},
	}

	for _, c := range cases {
		if got := forge.Needs(c.consumer); !slices.Equal(got, c.need) {
			t.Errorf("Needs(%q) = %v, want %v", c.consumer, got, c.need)
		}
	}

	if got := forge.Needs("unknown"); got != nil {
		t.Errorf("Needs(unknown) = %v, want nil", got)
	}
}

func TestMissing(t *testing.T) {
	t.Parallel()

	tokenConnection := forge.Capabilities{
		forge.CapFiles, forge.CapCompare, forge.CapCommit, forge.CapGitAccess, forge.CapEvents,
	}

	cases := []struct {
		consumer forge.Consumer
		missing  forge.Capabilities
	}{
		{forge.ConsumerWriteBack, nil},
		{forge.ConsumerWriteBackReview, forge.Capabilities{forge.CapReview}},
		{forge.ConsumerFetch, nil},
		{forge.ConsumerRunnerGit, forge.Capabilities{forge.CapRepoToken}},
		{forge.ConsumerCatalog, nil},
		{forge.ConsumerCauseDiff, nil},
		{forge.ConsumerTriggers, nil},
	}

	for _, c := range cases {
		if got := tokenConnection.Missing(forge.Needs(c.consumer)); !slices.Equal(got, c.missing) {
			t.Errorf("Missing(Needs(%q)) = %v, want %v", c.consumer, got, c.missing)
		}
	}
}

func TestUnavailable(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("list files: %w", forge.Unavailable(30*time.Second, "rate limit"))
	if !errors.Is(err, forge.ErrUnavailable) {
		t.Fatalf("errors.Is(%v, ErrUnavailable) = false", err)
	}

	after, ok := forge.RetryAfter(err)
	if !ok || after != 30*time.Second {
		t.Errorf("RetryAfter = %v, %v, want 30s, true", after, ok)
	}

	if _, ok := forge.RetryAfter(forge.ErrUnavailable); ok {
		t.Error("RetryAfter of the bare sentinel reports a time")
	}

	if _, ok := forge.RetryAfter(forge.Unavailable(0, "")); ok {
		t.Error("RetryAfter of an error with no time reports a time")
	}

	if got := forge.Unavailable(0, "").Error(); got != forge.ErrUnavailable.Error() {
		t.Errorf("Error() = %q, want the sentinel text", got)
	}
}
