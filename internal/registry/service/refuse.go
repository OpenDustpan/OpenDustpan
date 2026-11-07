package service

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/registry/modulecheck"
	"opendustpan.org/opendustpan/internal/registry/releasecheck"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// errReleaseRefused marks a release that the checks refuse.
var errReleaseRefused = errors.New("release refused")

// refusalReason returns the reason class of a refused publish, and reports
// whether the error is a refusal.
func refusalReason(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrTooLarge):
		return "archive-too-large", true
	case errors.Is(err, registry.ErrBadSignature), errors.Is(err, registry.ErrUntrustedKey):
		return "signature", true
	case errors.Is(err, releasecheck.ErrRefused):
		return "archive-set", true
	case errors.Is(err, modulecheck.ErrRefused):
		return "module-archive", true
	case errors.Is(err, registry.ErrVersionExists):
		return "version-exists", true
	case errors.Is(err, errReleaseRefused):
		return "release", true
	}

	return "", false
}

// refusal records a refused publish in the namespace audit log and returns
// the error of the publish. The entry names the caller, the namespace, the
// version and the reason class. The audit log is the only record of a refused
// publish: a refused publish stores no release and no content blob.
//
// An error that names no reason class is returned unchanged. Request-field,
// not-staged, no-keys, integrity and store errors stay unrecorded.
//
// A failed append fails the publish: the caller must not see a refusal that
// the audit log does not hold. The append uses the context of the request, so
// a done context fails the publish too.
func (s *Service) refusal(ctx context.Context, c *Caller, rec namespace.Namespace, version string, err error) error {
	if err == nil {
		return nil
	}

	reason, ok := refusalReason(err)
	if !ok {
		return err
	}

	e := s.entry(c, rec.Name, identity.ActionRegistryPublish, rec.Name.String())
	e.Version = version
	e.Outcome = audit.OutcomeDenied
	e.Reason = reason
	e.Changes = nil

	if _, appendErr := s.auditor.Append(ctx, rec.Fence(), e); appendErr != nil {
		return fmt.Errorf("%w: record the refused publish: %w", err, appendErr)
	}

	return err
}
