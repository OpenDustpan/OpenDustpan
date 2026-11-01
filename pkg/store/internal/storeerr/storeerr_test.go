package storeerr_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
)

// errDriver stands for the error of the driver, with text that must not
// reach the error of the store.
var errDriver = errors.New("driver text with secret detail")

// TestErrorWrapsTheSentinelsOfItsClass proves that each class wraps
// namespace.ErrUnavailable, so the API answers 503, and that only the classes
// of a database that cannot be reached wrap namespace.ErrUnreachable, so
// background work waits for those and counts the others as failures. The text
// of each error holds the class and no driver text.
func TestErrorWrapsTheSentinelsOfItsClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		class       storeerr.Class
		unreachable bool
	}{
		{storeerr.ConnectionFailed, true},
		{storeerr.Shutdown, true},
		{storeerr.ReadOnly, true},
		{storeerr.Capacity, false},
		{storeerr.Busy, false},
		{storeerr.StatementTimeout, false},
		{storeerr.Class("not a class"), false},
	}

	for _, tc := range tests {
		t.Run(string(tc.class), func(t *testing.T) {
			t.Parallel()

			err := storeerr.New(tc.class, errDriver)
			checkText(t, err, tc.class)

			if got := errors.Is(err, namespace.ErrUnreachable); got != tc.unreachable {
				t.Errorf("%q wraps ErrUnreachable = %t, want %t", tc.class, got, tc.unreachable)
			}
		})
	}
}

// checkText checks that err of class wraps ErrUnavailable and the driver
// error, and that its text holds the class and no driver text.
func checkText(t *testing.T, err *storeerr.Error, class storeerr.Class) {
	t.Helper()

	if !errors.Is(err, namespace.ErrUnavailable) {
		t.Errorf("%q does not wrap ErrUnavailable", class)
	}

	if !errors.Is(err, errDriver) {
		t.Errorf("%q lost the driver error", class)
	}

	if strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), string(class)) {
		t.Errorf("text %q, want the class and no driver text", err)
	}

	if !storeerr.Is(err) {
		t.Errorf("Is(%v) = false", err)
	}
}
