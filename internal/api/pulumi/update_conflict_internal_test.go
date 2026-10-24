package pulumi

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestWriteErrorUpdateConflictNamesTheCommand proves that a call that another
// update of the stack blocks answers 409 with the sentence of the sentinel and
// the command that ends a stuck update. The context of the wrap, such as the
// ID of the refused update or a store prefix, stays out of the answer.
func TestWriteErrorUpdateConflictNamesTheCommand(t *testing.T) {
	t.Parallel()

	const want = "another update of this stack is in progress: wait for it to end, or run pulumi cancel"

	body := fmt.Sprintf("{\"message\":%q,\"code\":%d,\"errorCode\":%q}\n", want, http.StatusConflict,
		errcode.UpdateConflict)

	for _, err := range []error{
		stack.ErrUpdateConflict,
		fmt.Errorf("start update riar7 of acme/demo/dev: transaction: %w", stack.ErrUpdateConflict),
	} {
		var h Handler

		h.logger = slog.New(slog.DiscardHandler)

		rec := httptest.NewRecorder()
		h.writeError(t.Context(), rec, err)

		if rec.Code != http.StatusConflict || rec.Body.String() != body {
			t.Errorf("writeError(%v) = %d %q, want 409 %q", err, rec.Code, rec.Body.String(), body)
		}
	}
}

// TestWriteErrorUpdateConflictNamesTheActiveUpdate proves that a refusal that
// names the update that holds the stack answers 409 with the ID of that
// update, the sentence of the sentinel and the command that ends a stuck
// update. The ID of the refused update stays out of the answer.
func TestWriteErrorUpdateConflictNamesTheActiveUpdate(t *testing.T) {
	t.Parallel()

	const want = "another update of this stack is in progress (update u-active): " +
		"wait for it to end, or run pulumi cancel"

	body := fmt.Sprintf("{\"message\":%q,\"code\":%d,\"errorCode\":%q}\n", want, http.StatusConflict,
		errcode.UpdateConflict)
	conflict := &stack.UpdateConflictError{Active: "u-active"}

	for _, err := range []error{
		conflict,
		fmt.Errorf("start update u-refused of acme/demo/dev: transaction: %w", conflict),
	} {
		var h Handler

		h.logger = slog.New(slog.DiscardHandler)

		rec := httptest.NewRecorder()
		h.writeError(t.Context(), rec, err)

		if rec.Code != http.StatusConflict || rec.Body.String() != body {
			t.Errorf("writeError(%v) = %d %q, want 409 %q", err, rec.Code, rec.Body.String(), body)
		}
	}
}
