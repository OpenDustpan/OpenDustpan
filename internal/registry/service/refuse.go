package service

import (
	"context"
	"errors"
	"log/slog"

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
	case errors.Is(err, ErrIntegrity):
		return "integrity", true
	case errors.Is(err, ErrNoKeys):
		return "no-signing-key", true
	}

	return "", false
}

// refusal records a refused publish in the namespace audit log and returns
// the error of the publish. The entry names the caller, the namespace, the
// version and the reason class. The audit log is the only record of a refused
// publish: a refused publish stores no release and no content blob.
//
// An error that names no reason class is returned unchanged. Request-field,
// not-staged and store errors stay unrecorded. Integrity and no-keys refusals
// are recorded.
//
// The publish stays refused when the append fails: the release is not stored,
// so the refusal is the answer. The answer carries the refusal text only, never
// the text of the audit store. The loss is logged and counted by the hooks of
// the service. The append uses the context of the request.
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
		// The cause is a fixed text: the error of the audit store can hold a host,
		// a user or a statement, and the log must not carry it.
		s.logger.ErrorContext(ctx, "audit append failed for a refused publish",
			slog.String("namespace", rec.Name.String()), slog.String("reason", reason),
			slog.String("cause", "audit store append failed"))
		s.onAudit()
	}

	return err
}
