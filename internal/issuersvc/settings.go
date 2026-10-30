package issuersvc

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// scheduleReason starts the reason of each entry that the schedule writes.
const scheduleReason = "issuer.rotate: "

// State is what a read of the issuer of a namespace answers. EffectiveIssuerURL
// is empty when the server has no issuer host and the namespace sets no
// issuerURL. Keys holds the metadata of every key, the deleted ones too, and
// no key material.
type State struct {
	EffectiveIssuerURL string
	Settings           profile.IssuerSettings
	Keys               []profile.KeyInfo
	Warnings           []string
	HostConfigured     bool
}

// Get reads the settings, the metadata of the keys and the last copy check.
func (s *Service) Get(ctx context.Context, a *Actor, ns namespace.Name) (State, error) {
	if err := s.require(ctx, a, ns, identity.ActionNamespaceRead); err != nil {
		return State{}, err
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return State{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	set, err := s.store.GetIssuerSettings(ctx, ns)
	if err != nil {
		return State{}, fmt.Errorf("read issuer settings: %w", err)
	}

	hist, err := s.store.History(ctx, ns)
	if err != nil {
		return State{}, fmt.Errorf("read key history: %w", err)
	}

	st := State{Settings: set, Keys: hist, EffectiveIssuerURL: "", HostConfigured: true, Warnings: nil}

	url, urlErr := s.issuerURL(&n, &set)

	switch {
	case urlErr == nil:
		st.EffectiveIssuerURL = url
	case isNotConfigured(urlErr):
		st.HostConfigured = false
	default:
		return State{}, urlErr
	}

	live, err := s.store.Live(ctx, ns)
	if err != nil {
		return State{}, fmt.Errorf("read signing keys: %w", err)
	}

	if active, ok := findState(live, profile.KeyActive); ok {
		st.Warnings = s.warnings(&set, &active, s.now().UTC())
	}

	return st, nil
}

// PatchRequest is a change of the settings. IfVersion is the version that the
// caller read. A change of issuerURL, a change of the subject template,
// allowBlockSubjects from false to true and includeActorName from false to
// true need a fresh authentication: Fresh returns nil when the caller has one.
// A nil Fresh refuses these changes.
type PatchRequest struct {
	Fresh     func() error
	Patch     profile.SettingsPatch
	IfVersion int64
}

// Patch changes the settings of the namespace ns as req says.
func (s *Service) Patch(
	ctx context.Context, a *Actor, ns namespace.Name, req *PatchRequest,
) (profile.IssuerSettings, error) {
	if err := s.require(ctx, a, ns, identity.ActionNamespaceManageIssuer); err != nil {
		return profile.IssuerSettings{}, err
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return profile.IssuerSettings{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	old, err := s.store.GetIssuerSettings(ctx, ns)
	if err != nil {
		return profile.IssuerSettings{}, fmt.Errorf("read issuer settings: %w", err)
	}

	if old.Version != req.IfVersion {
		return profile.IssuerSettings{}, fmt.Errorf("%w: the issuer settings are at version %d, not %d",
			profile.ErrConflict, old.Version, req.IfVersion)
	}

	next, err := req.Patch.Apply(old)
	if err != nil {
		return profile.IssuerSettings{}, err
	}

	if freshErr := checkFresh(&old, &next, req.Fresh); freshErr != nil {
		return profile.IssuerSettings{}, freshErr
	}

	next.UpdatedAt, next.UpdatedBy = s.now().UTC(), a.Principal.Subject

	e := s.entry(a, ns, "", settingsChanges(&old, &next))
	e.Version = strconv.FormatInt(req.IfVersion+1, 10)

	out, err := s.store.PutIssuerSettings(ctx, next, req.IfVersion, n.Fence(), []audit.Entry{e})
	if err != nil {
		return profile.IssuerSettings{}, fmt.Errorf("store issuer settings: %w", err)
	}

	return out, nil
}

// checkFresh refuses a change that reduces security when the caller has no
// fresh authentication.
func checkFresh(old, next *profile.IssuerSettings, fresh func() error) error {
	if !profile.NeedsFreshStep(*old, *next) {
		return nil
	}

	if fresh == nil {
		return profile.ErrFreshRequired
	}

	return fresh()
}

func settingsChanges(old, next *profile.IssuerSettings) []audit.Change {
	var out []audit.Change

	add := func(key, before, after string) {
		if before != after {
			out = append(out, audit.Change{Key: key, Old: &before, New: after, Layer: "", TakesEffect: ""})
		}
	}

	add("issuerURL", old.IssuerURL, next.IssuerURL)
	add("subjectTemplate", strings.Join(old.SubjectTemplate, ","), strings.Join(next.SubjectTemplate, ","))
	add("allowBlockSubjects", strconv.FormatBool(old.AllowBlockSubjects), strconv.FormatBool(next.AllowBlockSubjects))
	add("includeActorName", strconv.FormatBool(old.IncludeActorName), strconv.FormatBool(next.IncludeActorName))

	if len(out) == 0 {
		out = append(out, audit.Change{Key: "issuer", Old: nil, New: "unchanged", Layer: "", TakesEffect: ""})
	}

	return out
}

// entry makes the audit entry of a change that a person asked for. The action
// of each such entry is namespace:manage-issuer.
func (s *Service) entry(a *Actor, ns namespace.Name, reason string, changes []audit.Change) audit.Entry {
	e := a.By
	e.At = s.now().UTC()
	e.Namespace = ns
	e.Subject = a.Principal.Subject
	e.TokenID = a.Principal.TokenID
	e.Action = string(identity.ActionNamespaceManageIssuer)
	e.Object = identity.NamespaceObject(ns).String()
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Reason = reason
	e.Changes = changes

	return e
}

// serverEntry makes the audit entry of an automatic change: the server is the
// subject and the reason names the schedule.
func (s *Service) serverEntry(ns namespace.Name, why string, changes []audit.Change) audit.Entry {
	return audit.Entry{
		At: s.now().UTC(), Namespace: ns, Subject: history.ServerSubject, IDPSubject: "", TokenID: "",
		Action: string(identity.ActionNamespaceManageIssuer), Object: identity.NamespaceObject(ns).String(),
		Version: "", ValuePath: "", ValueCount: nil, Changes: changes, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: audit.PrincipalKindService,
		JobID: "", Reason: scheduleReason + why,
	}
}
