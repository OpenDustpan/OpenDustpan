package issuersvc

import (
	"context"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/profile"
)

// attrNamespace is the name of the attribute and of the log field that carry
// the namespace.
const attrNamespace = "namespace"

// noticeSpec is one notice of the issuer.
type noticeSpec struct {
	attrs    map[string]string
	typ      notify.Type
	summary  string
	dedupe   string
	severity notify.Severity
}

// notice appends a notice. A failure is logged and never fails the change that
// it reports.
func (s *Service) notice(ctx context.Context, n *namespace.Namespace, spec *noticeSpec) {
	if s.notifier == nil {
		s.skipped(ctx, n, spec)

		return
	}

	note := notify.Notification{
		ID: ident.NewID(notify.IDPrefix), Type: spec.typ, Scope: n.Name,
		Object: identity.NamespaceObject(n.Name).String(), ObjectID: n.ID.String(), Subject: history.ServerSubject,
		Agent: "", Severity: spec.severity, Summary: spec.summary, Links: nil, Attributes: spec.attrs,
		Recipients: []notify.Recipient{{Principal: "", Group: "", Admins: true}},
		DedupeKey:  spec.dedupe, At: s.now().UTC(), Sequence: 0,
	}

	if err := s.notifier.Append(ctx, n.Fence(), []notify.Notification{note.Fit()}); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn, "issuer: could not append a notice",
			slog.String("type", string(spec.typ)), slog.String(attrNamespace, n.Name.String()),
			slog.String("error", err.Error()))
	}
}

// skipped logs a notice that the server did not send because it has no outbox.
// The notice would warn a person, so a silent skip would hide the event: a
// revoked key or a failed copy check logs at WARN, a new next key at INFO.
func (s *Service) skipped(ctx context.Context, n *namespace.Namespace, spec *noticeSpec) {
	level := slog.LevelWarn
	if spec.severity == notify.SeverityInfo {
		level = slog.LevelInfo
	}

	s.log.LogAttrs(ctx, level, "issuer: the notice was not sent: the server has no outbox for notices",
		slog.String("type", string(spec.typ)), slog.String(attrNamespace, n.Name.String()),
		slog.String("summary", spec.summary))
}

// rotationChange is what a rotation did, for its notices.
type rotationChange struct {
	now    time.Time
	plan   profile.RotationPlan
	before []profile.Key
	added  []profile.Key
	kind   profile.RotationKind
}

func (s *Service) notifyRotation(ctx context.Context, n *namespace.Namespace, ch *rotationChange) {
	if ch.kind == profile.RotateEmergency {
		s.notice(ctx, n, &noticeSpec{
			typ: notify.IssuerKeyRevoked, severity: notify.SeverityCritical,
			summary: "The signing keys of the issuer of namespace " + n.Name.String() +
				" were replaced in an emergency rotation.",
			dedupe: "issuer.key-revoked:" + n.ID.String() + ":" + ch.now.Format(time.RFC3339),
			attrs:  map[string]string{attrNamespace: n.Name.String()},
		})
	}

	for i := range ch.added {
		if ch.added[i].State != profile.KeyNext {
			continue
		}

		becomes := ch.now.Add(profile.ActiveKeyLifetime)

		if cur, ok := findState(ch.before, profile.KeyActive); ok && !ch.plan.Promoted() &&
			ch.kind != profile.RotateEmergency {
			becomes = cur.ActivatedAt.Add(profile.ActiveKeyLifetime)
		}

		when := becomes.Format(time.RFC3339)

		s.notice(ctx, n, &noticeSpec{
			typ: notify.IssuerKeyNext, severity: notify.SeverityInfo,
			summary: "The issuer of namespace " + n.Name.String() + " has a new next key. It becomes active at " +
				when + ".",
			dedupe: "issuer.key-next:" + string(ch.added[i].ID),
			attrs:  map[string]string{attrNamespace: n.Name.String(), "kid": string(ch.added[i].ID), "active-at": when},
		})
	}
}

// checkFailed reports the failed daily copy check: a notice each day, and a
// log line at WARN from the day 90 of the active key.
func (s *Service) checkFailed(
	ctx context.Context, n *namespace.Namespace, set *profile.IssuerSettings, keys []profile.Key, c *profile.CopyCheck,
) {
	now := s.now().UTC()

	s.notice(ctx, n, &noticeSpec{
		typ: notify.IssuerKeyCopyMissing, severity: notify.SeverityWarning,
		summary: "The copy of the issuer files at " + set.IssuerURL + " does not hold the keys of namespace " +
			n.Name.String() + ".",
		dedupe: "issuer.key-copy-missing:" + n.ID.String() + ":" + now.Format(time.DateOnly),
		attrs:  map[string]string{attrNamespace: n.Name.String()},
	})

	active, ok := findState(keys, profile.KeyActive)
	if !ok || now.Sub(active.ActivatedAt) < profile.ActiveKeyLifetime {
		return
	}

	s.log.LogAttrs(ctx, slog.LevelWarn, "issuer: the copy check failed and the active key is past its 90 days",
		slog.String(attrNamespace, n.Name.String()), slog.String("kid", string(active.ID)),
		slog.Any("steps", failedSteps(c)))
}

func failedSteps(c *profile.CopyCheck) []string {
	var out []string

	for i := range c.Steps {
		if !c.Steps[i].OK {
			out = append(out, c.Steps[i].Name)
		}
	}

	return out
}
