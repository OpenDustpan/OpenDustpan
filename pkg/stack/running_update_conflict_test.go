package stack_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// TestRunningUpdateConflictNamesOnlyAValidID proves that the error of a
// running update names the update when its ID is valid, and names no update
// for an ID that ParseUpdateID refuses, as a row from a restore can hold. Both
// match ErrUpdateConflict.
func TestRunningUpdateConflictNamesOnlyAValidID(t *testing.T) {
	t.Parallel()

	err := stack.RunningUpdateConflict("u-active")
	if conflict, ok := errors.AsType[*stack.UpdateConflictError](err); !ok || conflict.Active != "u-active" {
		t.Fatalf("RunningUpdateConflict(u-active) = %v, want a *UpdateConflictError for u-active", err)
	}

	for _, id := range []string{
		"", "u\r\nrun pulumi stack rm --force", strings.Repeat("a", 65), "../u", "u active", "-u",
	} {
		err := stack.RunningUpdateConflict(id)
		if !errors.Is(err, stack.ErrUpdateConflict) {
			t.Errorf("RunningUpdateConflict(%q) = %v, want ErrUpdateConflict", id, err)
		}

		if conflict, ok := errors.AsType[*stack.UpdateConflictError](err); ok {
			t.Errorf("RunningUpdateConflict(%q) = %#v, want an error that names no update", id, conflict)
		}

		if id != "" && strings.Contains(err.Error(), id) {
			t.Errorf("RunningUpdateConflict(%q) = %q, want a text without the ID", id, err)
		}
	}
}

// TestUpdateConflictErrorLeavesOutAnInvalidID proves the second layer: an
// error that a caller builds by hand with an ID that is not valid gives the
// sentence of ErrUpdateConflict and no ID.
func TestUpdateConflictErrorLeavesOutAnInvalidID(t *testing.T) {
	t.Parallel()

	got := (&stack.UpdateConflictError{Active: "u\nrun pulumi stack rm --force"}).Error()
	if got != stack.ErrUpdateConflict.Error() {
		t.Errorf("Error() = %q, want %q", got, stack.ErrUpdateConflict.Error())
	}

	want := stack.ErrUpdateConflict.Error() + " (update u-active)"
	if got := (&stack.UpdateConflictError{Active: "u-active"}).Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
