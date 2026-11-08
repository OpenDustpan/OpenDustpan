package dustpan

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Bounds of the text of a job token refusal that an audit entry keeps.
const (
	maxJobTokenSubjectBytes = 255
	maxJobTokenObjectBytes  = 512
)

// jobTokenFallbackName stands in for a job ID or a unit that a refusal does
// not carry, so that the audit entry of an inconsistent token stays valid.
const jobTokenFallbackName = "job-token"

// Errors of the job token refusal recorder.
var (
	errNoJobTokenAuditStore  = errors.New("no audit store")
	errNoJobTokenServerStore = errors.New("no audit store for the installation scope")
	errNoJobTokenClock       = errors.New("no clock")
	errNoJobStore            = errors.New("no job store")
)

// jobFactsOf adapts a job store to identity.JobFacts.
type jobFactsOf struct {
	store job.Store
}

// JobFact implements identity.JobFacts.
func (f jobFactsOf) JobFact(ctx context.Context, jobID string) (identity.JobFact, error) {
	if f.store == nil {
		return identity.JobFact{}, errNoJobStore
	}

	stored, err := f.store.Get(ctx, job.ID(jobID))
	if err != nil {
		return identity.JobFact{}, fmt.Errorf("read job: %w", err)
	}

	return identity.JobFact{
		Unit:    stored.Spec.Unit,
		Changes: stored.Spec.Changes(),
		Source:  string(stored.Source),
	}, nil
}

// jobTokenRefusals writes the audit entry of a refusal of the job token rule.
type jobTokenRefusals struct {
	entries    audit.Store
	server     audit.ServerStore
	namespaces namespace.Store
	now        func() time.Time
}

// RecordJobTokenRefusal implements identity.JobTokenRefusals.
func (r jobTokenRefusals) RecordJobTokenRefusal(ctx context.Context, rec identity.JobTokenRefusal) error {
	if r.entries == nil {
		return errNoJobTokenAuditStore
	}

	if r.now == nil {
		return errNoJobTokenClock
	}

	scope := rec.Unit.Namespace()

	entry := jobTokenRefusalEntry(rec, scope, r.now)
	if scope == "" {
		entry.Namespace = audit.ServerScope

		return r.appendServer(ctx, entry)
	}

	ns, err := r.namespaces.GetNamespace(ctx, scope)
	if err != nil {
		return fmt.Errorf("read the namespace of the refusal: %w", err)
	}

	entry.Namespace = ns.Name

	if _, err := r.entries.Append(ctx, ns.Fence(), entry); err != nil {
		return fmt.Errorf("append the refusal: %w", err)
	}

	return nil
}

// appendServer writes entry in the installation scope.
func (r jobTokenRefusals) appendServer(ctx context.Context, entry audit.Entry) error {
	if r.server == nil {
		return errNoJobTokenServerStore
	}

	if _, err := r.server.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("append the refusal: %w", err)
	}

	return nil
}

// jobTokenRefusalEntry returns the security entry of a refusal of the job
// token rule. now returns the time of the entry and must not be nil.
func jobTokenRefusalEntry(rec identity.JobTokenRefusal, scope namespace.Name, now func() time.Time) audit.Entry {
	subject, object := rec.Subject, rec.Unit.String()
	if subject == "" {
		subject = jobTokenFallbackName
	}

	if object == "" {
		object = jobTokenFallbackName
	}

	return audit.Entry{
		At:            now().UTC(),
		Namespace:     scope,
		Subject:       refusal.Clean(subject, maxJobTokenSubjectBytes),
		IDPSubject:    "",
		TokenID:       "",
		Action:        refusal.Clean(string(rec.Action), maxJobTokenSubjectBytes),
		Object:        refusal.Clean(object, maxJobTokenObjectBytes),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindSecurity,
		Outcome:       audit.OutcomeDenied,
		RequestID:     "",
		ObjectID:      refusal.Clean(rec.Unit.UnitID(), maxJobTokenSubjectBytes),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        refusal.Clean(rec.Reason, audit.MaxReasonLen),
		PrincipalKind: "",
		JobID:         refusal.Clean(rec.JobID, audit.MaxJobIDLen),
	}
}

// jobTokenRule returns the job token rule of the authorizer chain. The rule
// reads the live job when a job store is set, and records each refusal when
// the namespace store keeps audit entries.
func jobTokenRule(cfg *settings, next identity.Authorizer) identity.JobTokenRule {
	return identity.JobTokenRule{
		Next: next, Jobs: jobTokenFacts(cfg), Refusals: jobTokenRefusalRecorder(cfg),
	}
}

// jobTokenFacts returns the job facts port of cfg. A server with no job store
// refuses every read, so a job token then asks for no action.
func jobTokenFacts(cfg *settings) jobFactsOf {
	return jobFactsOf{store: cfg.jobStore}
}

// jobTokenRefusalRecorder returns the recorder of job token refusals of cfg.
// A namespace store that keeps no audit entries makes each record an error,
// so each refusal still refuses.
func jobTokenRefusalRecorder(cfg *settings) jobTokenRefusals {
	var entries audit.Store
	if typed, ok := cfg.namespaceStore.(audit.Store); ok {
		entries = typed
	}

	var server audit.ServerStore
	if typed, ok := cfg.namespaceStore.(audit.ServerStore); ok {
		server = typed
	}

	return jobTokenRefusals{
		entries: entries, server: server, namespaces: cfg.namespaceStore, now: cfg.now,
	}
}
