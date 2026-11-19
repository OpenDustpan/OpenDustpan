package objectcontext

import (
	"errors"
	"net/http"
)

// ResolveUnitByID runs the resolver of the unit routes on r and returns its
// error, so that a test sees the error before the classifier maps it.
func ResolveUnitByID(h *Handler, r *http.Request) error {
	_, err := h.resolveUnitByID(nil, r)

	return err
}

// ResolveNamespace runs the resolver of the namespace routes on r and returns
// its error.
func ResolveNamespace(h *Handler, r *http.Request) error {
	_, err := h.resolveNamespace(nil, r)

	return err
}

// IsNotFoundAnswer reports whether err is the one answer for an object that
// the caller cannot see, with no other error in its chain.
func IsNotFoundAnswer(err error) bool {
	return errors.Is(err, errNotFound)
}
