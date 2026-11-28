package trustrulesvc

import (
	"context"
	"fmt"
	"strconv"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// SaveBundle checks the fresh authentication of the caller and stores the
// PEM text of f as a new version of the trust bundle of ns. The first save
// makes the bundle; a later save keeps the active pointer.
func (s *Service) SaveBundle(
	ctx context.Context, caller identity.Principal, e audit.Entry, ns namespace.Name, f trustrule.BundleFields,
) (trustrule.Bundle, error) {
	return s.writeBundle(ctx, caller, ns, "save trust bundle",
		func(st trustrule.BundleStore, fence namespace.Fence) (trustrule.Bundle, error) {
			return st.SaveBundle(ctx, ns, f, fence, s.bundleSaveEntry(e, ns, &f))
		})
}

// RotateBundle checks the fresh authentication of the caller, the revision
// and the overlap, stores the PEM text of f as a new version, and moves the
// active pointer to it with overlapEndsAt = now + overlap. The revision that
// the rotate expects is f.Revision.
func (s *Service) RotateBundle(
	ctx context.Context, caller identity.Principal, e audit.Entry, ns namespace.Name, f trustrule.BundleFields,
) (trustrule.Bundle, error) {
	return s.writeBundle(ctx, caller, ns, "rotate trust bundle",
		func(st trustrule.BundleStore, fence namespace.Fence) (trustrule.Bundle, error) {
			return st.RotateBundle(ctx, ns, f, fence, s.bundleRotateEntry(e, ns, &f))
		})
}

// writeBundle runs one change of the bundle of ns that returns the bundle, as
// changeBundle does. The error of do names what in its text.
func (s *Service) writeBundle(
	ctx context.Context, caller identity.Principal, ns namespace.Name, what string,
	do func(st trustrule.BundleStore, fence namespace.Fence) (trustrule.Bundle, error),
) (trustrule.Bundle, error) {
	var b trustrule.Bundle

	err := s.changeBundle(ctx, caller, ns, func(st trustrule.BundleStore, fence namespace.Fence) error {
		var doErr error

		b, doErr = do(st, fence)
		if doErr != nil {
			return fmt.Errorf("%s: %w", what, doErr)
		}

		return nil
	})
	if err != nil {
		return trustrule.Bundle{}, err
	}

	return b, nil
}

// GetBundle returns the bundle of ns. An unknown namespace gives
// namespace.ErrNotFound, and a namespace with no bundle gives
// trustrule.ErrNotFound.
func (s *Service) GetBundle(ctx context.Context, ns namespace.Name) (trustrule.Bundle, error) {
	if _, err := s.namespaces.Get(ctx, ns); err != nil {
		return trustrule.Bundle{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	st := s.bundles
	if st == nil {
		return trustrule.Bundle{}, errNoBundleStore()
	}

	b, err := st.GetBundle(ctx, ns)
	if err != nil {
		return trustrule.Bundle{}, fmt.Errorf("get trust bundle: %w", err)
	}

	return b, nil
}

// DeleteBundleVersion removes a retired version of the bundle of ns. The
// active version is refused with trustrule.ErrBundleActive.
func (s *Service) DeleteBundleVersion(
	ctx context.Context, caller identity.Principal, e audit.Entry, ns namespace.Name, version int64,
) error {
	return s.changeBundle(ctx, caller, ns, func(st trustrule.BundleStore, fence namespace.Fence) error {
		err := st.DeleteBundleVersion(ctx, ns, version, fence, s.bundleEntry(e, ns, "", ActionBundleDelete))
		if err != nil {
			return fmt.Errorf("delete trust bundle version: %w", err)
		}

		return nil
	})
}

// changeBundle runs one change of the bundle of ns. It checks the caller and
// the fresh authentication, and that the server keeps a bundle store, before
// it reads the namespace and calls do with the store and the current fence of
// the namespace.
func (s *Service) changeBundle(
	ctx context.Context, caller identity.Principal, ns namespace.Name,
	do func(st trustrule.BundleStore, fence namespace.Fence) error,
) error {
	if err := s.requireWriter(ctx, caller); err != nil {
		return err
	}

	st := s.bundles
	if st == nil {
		return errNoBundleStore()
	}

	n, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return do(st, n.Fence())
}

// errNoBundleStore is the refusal of a bundle change or read when the server
// keeps no bundle. It comes before any check runs.
func errNoBundleStore() error {
	return fmt.Errorf("%w: the server keeps no trust bundle", trustrule.ErrBundle)
}

// bundleSaveEntry is the entry of a bundle save. The PEM text is long, so
// the entry names the version and the active pointer and not the CAs.
func (s *Service) bundleSaveEntry(e audit.Entry, ns namespace.Name, f *trustrule.BundleFields) audit.Entry {
	out := s.bundleEntry(e, ns, "", ActionBundleSave)
	out.Changes = []audit.Change{
		{Key: "bytes", Old: nil, New: strconv.Itoa(len(f.PEM)), Layer: "", TakesEffect: ""},
	}

	return out
}

// bundleRotateEntry is the entry of a bundle rotation.
func (s *Service) bundleRotateEntry(e audit.Entry, ns namespace.Name, f *trustrule.BundleFields) audit.Entry {
	out := s.bundleEntry(e, ns, "", ActionBundleRotate)
	out.Changes = []audit.Change{
		{Key: "bytes", Old: nil, New: strconv.Itoa(len(f.PEM)), Layer: "", TakesEffect: ""},
		{Key: "overlap", Old: nil, New: f.Overlap.String(), Layer: "", TakesEffect: ""},
	}

	return out
}

// requireWriter returns ErrSignInRequired for a caller that authenticated with
// a stored access token or with a token of a trust rule, and the error of
// requireFresh for a request with no fresh authentication.
func (s *Service) requireWriter(ctx context.Context, caller identity.Principal) error {
	// A token of a trust rule always has a token ID. The second test stays so
	// that a caller with a trust token and no ID never changes a rule or a
	// bundle.
	if caller.TokenID != "" || caller.Credential == identity.CredentialTrustToken {
		return ErrSignInRequired
	}

	return requireFresh(ctx, s.freshGuard)
}
