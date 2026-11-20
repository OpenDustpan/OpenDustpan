package objectcontext

import (
	"errors"
	"net/http"
	"time"

	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// LiveTarget runs liveTarget of h on r for the live unit e of the namespace
// named ns, so that a test sees the guards of the function alone.
func LiveTarget(h *Handler, r *http.Request, ns namespace.Name, e coreinventory.Entry) error {
	_, err := h.liveTarget(r, namespaceNamed(ns), e)

	return err
}

// GoneTarget runs goneTarget of h on r for the soft-deleted unit gone of the
// namespace named ns.
func GoneTarget(h *Handler, r *http.Request, ns namespace.Name, gone coreinventory.DeletedEntry) error {
	_, err := h.goneTarget(r, namespaceNamed(ns), gone)

	return err
}

// namespaceNamed returns a namespace that holds only its name.
func namespaceNamed(name namespace.Name) namespace.Namespace {
	return namespace.Namespace{
		CreatedAt: time.Time{}, ID: "", Name: name, PrimaryInstallation: "",
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
		FailoverVersion: 0,
	}
}
