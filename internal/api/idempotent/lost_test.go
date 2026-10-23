package idempotent_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/errcode"
)

// TestLost fails when the refusal for a lost record has another status or
// code, or holds an argument.
func TestLost(t *testing.T) {
	t.Parallel()

	refusal := idempotent.Lost()

	if refusal.Status != http.StatusConflict || refusal.Answer.Code() != errcode.RequestIdempotencyKeyLost {
		t.Fatalf("Lost() = %d %q", refusal.Status, refusal.Answer.Code())
	}

	if args := refusal.Answer.Args(); args != nil {
		t.Errorf("args = %v, want none", args)
	}

	if refusal.Message == "" {
		t.Error("message is empty")
	}
}
