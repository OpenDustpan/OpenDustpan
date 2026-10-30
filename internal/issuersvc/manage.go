package issuersvc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
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

	history, err := s.store.History(ctx, ns)
	if err != nil {
		return State{}, fmt.Errorf("read key history: %w", err)
	}

	st := State{Settings: set, Keys: history, EffectiveIssuerURL: "", HostConfigured: true, Warnings: nil}

	url, urlErr := s.issuerURL(n, set)

	switch {
	case urlErr == nil:
		st.EffectiveIssuerURL = url
	case errors.Is(urlErr, profile.ErrIssuerNotConfigured):
		st.HostConfigured = false
	default:
		return State{}, urlErr
	}

	live, err := s.store.Live(ctx, ns)
	if err != nil {
		return State{}, fmt.Errorf("read signing keys: %w", err)
	}

	if active, ok := findState(live, profile.KeyActive); ok {
		st.Warnings = s.warnings(set, active, s.now().UTC())
	}

	return st, nil
}

// Patch changes the settings. ifVersion is the version that the caller read.
// A change of issuerURL, a change of the subject template, allowBlockSubjects
// from false to true and includeActorName from false to true need a fresh
// authentication: fresh returns nil when the caller has one. A nil fresh
// refuses these changes.
func (s *Service) Patch(
	ctx context.Context, a *Actor, ns namespace.Name, p profile.SettingsPatch, ifVersion int64, fresh func() error,
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

	if old.Version != ifVersion {
		return profile.IssuerSettings{}, fmt.Errorf("%w: the issuer settings are at version %d, not %d",
			profile.ErrConflict, old.Version, ifVersion)
	}

	next, err := p.Apply(old)
	if err != nil {
		return profile.IssuerSettings{}, err
	}

	if profile.NeedsFreshStep(old, next) {
		if fresh == nil {
			return profile.IssuerSettings{}, profile.ErrFreshRequired
		}

		if freshErr := fresh(); freshErr != nil {
			return profile.IssuerSettings{}, freshErr
		}
	}

	now := s.now().UTC()
	next.UpdatedAt, next.UpdatedBy = now, a.Principal.Subject

	entry := s.entry(a, ns, identity.ActionNamespaceManageIssuer, "", settingsChanges(old, next))
	entry.Version = fmt.Sprint(ifVersion + 1)

	return s.putSettings(ctx, n, next, ifVersion, entry)
}

func (s *Service) putSettings(
	ctx context.Context, n namespace.Namespace, set profile.IssuerSettings, ifVersion int64, e audit.Entry,
) (profile.IssuerSettings, error) {
	out, err := s.store.PutIssuerSettings(ctx, set, ifVersion, n.Fence(), []audit.Entry{e})
	if err != nil {
		return profile.IssuerSettings{}, fmt.Errorf("store issuer settings: %w", err)
	}

	return out, nil
}

func settingsChanges(old, next profile.IssuerSettings) []audit.Change {
	var out []audit.Change

	add := func(key, before, after string) {
		if before != after {
			b := before
			out = append(out, audit.Change{Key: key, Old: &b, New: after, Layer: "", TakesEffect: ""})
		}
	}

	add("issuerURL", old.IssuerURL, next.IssuerURL)
	add("subjectTemplate", strings.Join(old.SubjectTemplate, ","), strings.Join(next.SubjectTemplate, ","))
	add("allowBlockSubjects", fmt.Sprint(old.AllowBlockSubjects), fmt.Sprint(next.AllowBlockSubjects))
	add("includeActorName", fmt.Sprint(old.IncludeActorName), fmt.Sprint(next.IncludeActorName))

	if len(out) == 0 {
		out = append(out, audit.Change{Key: "issuer", Old: nil, New: "unchanged", Layer: "", TakesEffect: ""})
	}

	return out
}

// entry makes the audit entry of a change that a person asked for.
func (s *Service) entry(
	a *Actor, ns namespace.Name, action identity.Action, reason string, changes []audit.Change,
) audit.Entry {
	e := a.By
	e.At = s.now().UTC()
	e.Namespace = ns
	e.Subject = a.Principal.Subject
	e.TokenID = a.Principal.TokenID
	e.Action = string(action)
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

func keyChanges(add []profile.Key, moves []profile.KeyMove) []audit.Change {
	var out []audit.Change

	for _, k := range add {
		out = append(out, audit.Change{Key: "key." + string(k.ID), Old: nil, New: string(k.State), Layer: "", TakesEffect: ""})
	}

	for _, m := range moves {
		from := string(m.From)
		out = append(out, audit.Change{Key: "key." + string(m.ID), Old: &from, New: string(m.To), Layer: "", TakesEffect: ""})
	}

	return out
}

// CreateKeys makes the active key and the next key of a namespace that has no
// key. ErrConflict when it has keys.
func (s *Service) CreateKeys(ctx context.Context, a *Actor, ns namespace.Name) ([]profile.KeyInfo, error) {
	if err := s.require(ctx, a, ns, identity.ActionNamespaceManageIssuer); err != nil {
		return nil, err
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	keys, err := s.store.Live(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("read signing keys: %w", err)
	}

	if len(keys) > 0 {
		return nil, fmt.Errorf("%w: the namespace has signing keys: rotate them with POST %s/rotate",
			profile.ErrConflict, keysRoute(ns))
	}

	made, err := s.makeFirstKeys(ctx, n, func(add []profile.Key) audit.Entry {
		return s.entry(a, ns, identity.ActionNamespaceManageIssuer, "", keyChanges(add, nil))
	})
	if err != nil {
		return nil, err
	}

	return infos(made), nil
}

// firstKeys makes the keys of a namespace at the first open of a login block.
// The entry names the principal of the open and why the server acted. It
// returns the keys of the namespace, also when another instance made them
// first.
func (s *Service) firstKeys(ctx context.Context, n namespace.Namespace, by audit.Entry, why string) ([]profile.Key, error) {
	made, err := s.makeFirstKeys(ctx, n, func(add []profile.Key) audit.Entry {
		e := by
		e.At = s.now().UTC()
		e.Namespace = n.Name
		e.Action = string(identity.ActionNamespaceManageIssuer)
		e.Object = identity.NamespaceObject(n.Name).String()
		e.ObjectID = ""
		e.Kind = audit.KindChange
		e.Outcome = audit.OutcomeSuccess
		e.Reason = why
		e.Changes = keyChanges(add, nil)

		return e
	})
	if errors.Is(err, profile.ErrConflict) {
		return s.store.Live(ctx, n.Name)
	}

	return made, err
}

func (s *Service) makeFirstKeys(
	ctx context.Context, n namespace.Namespace, entry func([]profile.Key) audit.Entry,
) ([]profile.Key, error) {
	now := s.now().UTC()

	active, err := s.newKey(ctx, n.Name, profile.KeyActive, now)
	if err != nil {
		return nil, err
	}

	next, err := s.newKey(ctx, n.Name, profile.KeyNext, now)
	if err != nil {
		return nil, err
	}

	add := []profile.Key{active, next}

	err = s.store.Apply(ctx, n.Name, profile.KeyChange{Add: add, Move: nil, Initial: true}, n.Fence(),
		[]audit.Entry{entry(add)})
	if err != nil {
		return nil, fmt.Errorf("store signing keys: %w", err)
	}

	return add, nil
}

func infos(keys []profile.Key) []profile.KeyInfo {
	out := make([]profile.KeyInfo, 0, len(keys))
	for _, k := range sortedKeys(keys) {
		out = append(out, k.Info())
	}

	return out
}

// RotateRequest is the body of a manual rotation. An emergency rotation
// needs a reason.
type RotateRequest struct {
	Reason    string
	Emergency bool
}

// RotationResult is the answer of a rotation: the keys after it, and the keys
// that it deleted.
type RotationResult struct {
	Keys     []profile.KeyInfo
	Deleted  []profile.KeyID
	Promoted bool
}

// Rotate rotates the keys of a namespace now. A normal rotation makes the
// next key active. For a namespace with an issuerURL the copy check runs
// first, and a failed check changes nothing.
func (s *Service) Rotate(ctx context.Context, a *Actor, ns namespace.Name, req RotateRequest) (RotationResult, error) {
	if err := s.require(ctx, a, ns, identity.ActionNamespaceManageIssuer); err != nil {
		return RotationResult{}, err
	}

	kind, reason := profile.RotateNormal, ""

	if req.Emergency {
		kind = profile.RotateEmergency

		if err := checkReason(req.Reason); err != nil {
			return RotationResult{}, err
		}

		reason = req.Reason
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return RotationResult{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return s.rotate(ctx, n, rotation{kind: kind, actor: a, reason: reason})
}

func checkReason(reason string) error {
	switch {
	case strings.TrimSpace(reason) == "":
		return fmt.Errorf("%w: an emergency rotation needs a reason", profile.ErrInvalid)
	case utf8.RuneCountInString(reason) > profile.MaxEmergencyReason:
		return fmt.Errorf("%w: a reason has at most %d characters", profile.ErrInvalid, profile.MaxEmergencyReason)
	case strings.ContainsFunc(reason, unicode.IsControl):
		return fmt.Errorf("%w: a reason holds a control character", profile.ErrInvalid)
	default:
		return nil
	}
}

// rotation says who rotates and why. actor is nil for the schedule.
type rotation struct {
	actor  *Actor
	reason string
	kind   profile.RotationKind
}

func (s *Service) rotate(ctx context.Context, n namespace.Namespace, r rotation) (RotationResult, error) {
	set, err := s.store.GetIssuerSettings(ctx, n.Name)
	if err != nil {
		return RotationResult{}, fmt.Errorf("read issuer settings: %w", err)
	}

	keys, err := s.store.Live(ctx, n.Name)
	if err != nil {
		return RotationResult{}, fmt.Errorf("read signing keys: %w", err)
	}

	if len(keys) == 0 {
		return RotationResult{}, fmt.Errorf("%w: the namespace has no key: call POST %s to make the keys",
			profile.ErrIssuerKeyMissing, keysRoute(n.Name))
	}

	promote, err := s.copyBefore(ctx, n, set, keys, r)
	if err != nil {
		return RotationResult{}, err
	}

	now := s.now().UTC()

	plan, err := profile.PlanRotation(keys, now, r.kind, promote)
	if err != nil {
		return RotationResult{}, fmt.Errorf("%w: %s changes nothing; export the files with GET %s, copy them, "+
			"then call POST %s", err, "the rotation", exportRoute(n.Name), checkCopyRoute(n.Name))
	}

	if plan.Empty() {
		return RotationResult{Keys: infos(keys), Deleted: nil, Promoted: false}, nil
	}

	add, err := s.planKeys(ctx, n.Name, plan, now)
	if err != nil {
		return RotationResult{}, err
	}

	entry := s.rotationEntry(n.Name, r, keyChanges(add, plan.Moves))

	err = s.store.Apply(ctx, n.Name, profile.KeyChange{Add: add, Move: plan.Moves, Initial: false}, n.Fence(),
		[]audit.Entry{entry})
	if err != nil {
		return RotationResult{}, fmt.Errorf("store rotation: %w", err)
	}

	// The private keys that the signer opened may be keys that this rotation
	// deleted or retired.
	s.forget(n.Name)
	s.notifyRotation(ctx, n, keys, add, plan, r.kind, now)

	after, err := s.store.Live(ctx, n.Name)
	if err != nil {
		return RotationResult{}, fmt.Errorf("read signing keys: %w", err)
	}

	return RotationResult{Keys: infos(after), Deleted: plan.Deleted(), Promoted: plan.Promoted()}, nil
}

func (s *Service) rotationEntry(ns namespace.Name, r rotation, changes []audit.Change) audit.Entry {
	if r.actor == nil {
		return s.serverEntry(ns, string(r.kind)+" rotation", changes)
	}

	return s.entry(r.actor, ns, identity.ActionNamespaceManageIssuer, r.reason, changes)
}

// planKeys makes the keys that the plan adds.
func (s *Service) planKeys(ctx context.Context, ns namespace.Name, plan profile.RotationPlan, now time.Time) ([]profile.Key, error) {
	var add []profile.Key

	if plan.NewActive {
		k, err := s.newKey(ctx, ns, profile.KeyActive, now)
		if err != nil {
			return nil, err
		}

		add = append(add, k)
	}

	if plan.NewNext {
		k, err := s.newKey(ctx, ns, profile.KeyNext, now)
		if err != nil {
			return nil, err
		}

		add = append(add, k)
	}

	return add, nil
}

// copyBefore runs the copy check that comes before a promotion, for a
// namespace with an issuerURL. It returns whether a promotion may run. An
// emergency rotation needs no check: it promotes nothing.
func (s *Service) copyBefore(
	ctx context.Context, n namespace.Namespace, set profile.IssuerSettings, keys []profile.Key, r rotation,
) (bool, error) {
	if set.IssuerURL == "" || r.kind == profile.RotateEmergency {
		return true, nil
	}

	next, hasNext := findState(keys, profile.KeyNext)
	if !hasNext && r.kind == profile.RotateNormal {
		// A normal rotation without a next key makes one and promotes nothing.
		return true, nil
	}

	check := s.runCheck(ctx, set, keys, hasNext)

	if err := s.recordCheck(ctx, n, check, r); err != nil {
		return false, err
	}

	if r.kind == profile.RotateScheduled && !check.OK {
		s.checkFailed(ctx, n, set, keys, check)
	}

	if !hasNext {
		return true, nil
	}

	return check.Holds(next.ID), nil
}

func (s *Service) recordCheck(ctx context.Context, n namespace.Namespace, c profile.CopyCheck, r rotation) error {
	changes := []audit.Change{change("copyCheck", fmt.Sprint(c.OK))}

	var e audit.Entry
	if r.actor == nil {
		e = s.serverEntry(n.Name, "copy check", changes)
	} else {
		e = s.entry(r.actor, n.Name, identity.ActionNamespaceManageIssuer, "", changes)
	}

	if err := s.store.RecordCopyCheck(ctx, n.Name, c, n.Fence(), []audit.Entry{e}); err != nil {
		return fmt.Errorf("store copy check: %w", err)
	}

	return nil
}

// CheckCopy runs the copy check at once and stores and answers its outcome.
func (s *Service) CheckCopy(ctx context.Context, a *Actor, ns namespace.Name) (profile.CopyCheck, error) {
	if err := s.require(ctx, a, ns, identity.ActionNamespaceManageIssuer); err != nil {
		return profile.CopyCheck{}, err
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return profile.CopyCheck{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	set, err := s.store.GetIssuerSettings(ctx, ns)
	if err != nil {
		return profile.CopyCheck{}, fmt.Errorf("read issuer settings: %w", err)
	}

	if set.IssuerURL == "" {
		return profile.CopyCheck{}, fmt.Errorf("%w: the namespace has no issuerURL: set it with PATCH /api/dustpan/v1/namespaces/%s/issuer",
			profile.ErrInvalid, ns)
	}

	keys, err := s.store.Live(ctx, ns)
	if err != nil {
		return profile.CopyCheck{}, fmt.Errorf("read signing keys: %w", err)
	}

	if len(keys) == 0 {
		return profile.CopyCheck{}, fmt.Errorf("%w: the namespace has no key: call POST %s to make the keys",
			profile.ErrIssuerKeyMissing, keysRoute(ns))
	}

	_, hasNext := findState(keys, profile.KeyNext)
	check := s.runCheck(ctx, set, keys, hasNext)

	if err = s.recordCheck(ctx, n, check, rotation{actor: a, reason: "", kind: profile.RotateNormal}); err != nil {
		return profile.CopyCheck{}, err
	}

	return check, nil
}

// Files are the two files of an issuerURL, for a person to copy.
type Files struct {
	Discovery []byte
	JWKS      []byte
}

// Export answers the discovery document and the JWKS for the issuerURL of the
// namespace. It reads only: it never makes a key.
func (s *Service) Export(ctx context.Context, a *Actor, ns namespace.Name) (Files, error) {
	if err := s.require(ctx, a, ns, identity.ActionNamespaceManageIssuer); err != nil {
		return Files{}, err
	}

	set, err := s.store.GetIssuerSettings(ctx, ns)
	if err != nil {
		return Files{}, fmt.Errorf("read issuer settings: %w", err)
	}

	if set.IssuerURL == "" {
		return Files{}, fmt.Errorf("%w: the namespace has no issuerURL: set it with PATCH /api/dustpan/v1/namespaces/%s/issuer",
			profile.ErrInvalid, ns)
	}

	keys, err := s.store.Published(ctx, ns)
	if err != nil {
		return Files{}, fmt.Errorf("read signing keys: %w", err)
	}

	if len(keys) == 0 {
		return Files{}, fmt.Errorf("%w: the namespace has no key: call POST %s to make the keys",
			profile.ErrIssuerKeyMissing, keysRoute(ns))
	}

	return files(set.IssuerURL, keys)
}

func files(issuer string, keys []profile.Key) (Files, error) {
	set, err := profile.BuildJWKS(keys)
	if err != nil {
		return Files{}, fmt.Errorf("build JWKS: %w", err)
	}

	disc, err := marshal(profile.NewDiscovery(issuer))
	if err != nil {
		return Files{}, err
	}

	jwks, err := marshal(set)
	if err != nil {
		return Files{}, err
	}

	return Files{Discovery: disc, JWKS: jwks}, nil
}

// Discovery answers the discovery document of the namespace with the stable ID
// id. It needs no sign-in and holds public data only.
func (s *Service) Discovery(ctx context.Context, id namespace.ID) (profile.Discovery, error) {
	n, err := s.store.NamespaceByID(ctx, id)
	if err != nil {
		return profile.Discovery{}, fmt.Errorf("find namespace: %w", err)
	}

	set, err := s.store.GetIssuerSettings(ctx, n.Name)
	if err != nil {
		return profile.Discovery{}, fmt.Errorf("read issuer settings: %w", err)
	}

	url, err := s.issuerURL(n, set)
	if err != nil {
		return profile.Discovery{}, err
	}

	return profile.NewDiscovery(url), nil
}

// JWKS answers the public keys of the namespace with the stable ID id: the
// states next, active and retired. A namespace with no key answers an empty
// set.
func (s *Service) JWKS(ctx context.Context, id namespace.ID) (profile.JWKS, error) {
	n, err := s.store.NamespaceByID(ctx, id)
	if err != nil {
		return profile.JWKS{}, fmt.Errorf("find namespace: %w", err)
	}

	keys, err := s.store.Published(ctx, n.Name)
	if err != nil {
		return profile.JWKS{}, fmt.Errorf("read signing keys: %w", err)
	}

	out, err := profile.BuildJWKS(keys)
	if err != nil {
		return profile.JWKS{}, fmt.Errorf("build JWKS: %w", err)
	}

	return out, nil
}

// RotateAll runs the daily step of the schedule issuer.rotate for every
// namespace that has keys. A namespace that fails does not stop the others.
func (s *Service) RotateAll(ctx context.Context) error {
	all, err := s.namespaces.ListNamespaces(ctx)
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	var errs []error

	for _, n := range all {
		keys, keyErr := s.store.Live(ctx, n.Name)
		if keyErr != nil {
			errs = append(errs, fmt.Errorf("namespace %s: %w", n.Name, keyErr))

			continue
		}

		if len(keys) == 0 {
			continue
		}

		_, rotErr := s.rotate(ctx, n, rotation{actor: nil, reason: "", kind: profile.RotateScheduled})

		switch {
		case rotErr == nil:
		case errors.Is(rotErr, profile.ErrConflict), errors.Is(rotErr, namespace.ErrFenced):
			// Another instance changed the keys first. The next tick looks again.
			s.log.Debug("issuer.rotate: skipped a namespace that changed", "namespace", n.Name.String())
		default:
			errs = append(errs, fmt.Errorf("namespace %s: %w", n.Name, rotErr))
		}
	}

	return errors.Join(errs...)
}

// notice appends a notice. A failure is logged and never fails the change that
// it reports.
func (s *Service) notice(ctx context.Context, n namespace.Namespace, t notify.Type, sev notify.Severity, summary, dedupe string, attrs map[string]string) {
	if s.notifier == nil {
		return
	}

	note := notify.Notification{
		ID: ident.NewID(notify.IDPrefix), Type: t, Scope: n.Name, Object: identity.NamespaceObject(n.Name).String(),
		ObjectID: n.ID.String(), Subject: history.ServerSubject, Agent: "", Severity: sev, Summary: summary,
		Links: nil, Attributes: attrs, Recipients: []notify.Recipient{{Principal: "", Group: "", Admins: true}},
		DedupeKey: dedupe, At: s.now().UTC(), Sequence: 0,
	}

	if err := s.notifier.Append(ctx, n.Fence(), []notify.Notification{note.Fit()}); err != nil {
		s.log.Warn("issuer: could not append a notice", "type", string(t), "namespace", n.Name.String(), "error", err.Error())
	}
}

func (s *Service) notifyRotation(
	ctx context.Context, n namespace.Namespace, before, added []profile.Key, plan profile.RotationPlan,
	kind profile.RotationKind, now time.Time,
) {
	if kind == profile.RotateEmergency {
		s.notice(ctx, n, notify.IssuerKeyRevoked, notify.SeverityCritical,
			"The signing keys of the issuer of namespace "+n.Name.String()+" were replaced in an emergency rotation.",
			"issuer.key-revoked:"+n.ID.String()+":"+now.Format(time.RFC3339), map[string]string{"namespace": n.Name.String()})
	}

	for _, k := range added {
		if k.State != profile.KeyNext {
			continue
		}

		becomes := now.Add(profile.ActiveKeyLifetime)

		if cur, ok := findState(before, profile.KeyActive); ok && !plan.Promoted() && kind != profile.RotateEmergency {
			becomes = cur.ActivatedAt.Add(profile.ActiveKeyLifetime)
		}

		s.notice(ctx, n, notify.IssuerKeyNext, notify.SeverityInfo,
			"The issuer of namespace "+n.Name.String()+" has a new next key. It becomes active at "+becomes.Format(time.RFC3339)+".",
			"issuer.key-next:"+string(k.ID),
			map[string]string{"namespace": n.Name.String(), "kid": string(k.ID), "activeAt": becomes.Format(time.RFC3339)})
	}
}

// checkFailed reports the failed daily copy check: a notice each day, and a
// log line at WARN from the day 90 of the active key.
func (s *Service) checkFailed(ctx context.Context, n namespace.Namespace, set profile.IssuerSettings, keys []profile.Key, c profile.CopyCheck) {
	now := s.now().UTC()

	s.notice(ctx, n, notify.IssuerKeyCopyMissing, notify.SeverityWarning,
		"The copy of the issuer files at "+set.IssuerURL+" does not hold the keys of namespace "+n.Name.String()+".",
		"issuer.key-copy-missing:"+n.ID.String()+":"+now.Format(time.DateOnly), map[string]string{"namespace": n.Name.String()})

	if active, ok := findState(keys, profile.KeyActive); ok && now.Sub(active.ActivatedAt) >= profile.ActiveKeyLifetime {
		s.log.Warn("issuer: the copy check failed and the active key is past its 90 days",
			"namespace", n.Name.String(), "kid", string(active.ID), "steps", failedSteps(c))
	}
}

func failedSteps(c profile.CopyCheck) []string {
	var out []string

	for _, st := range c.Steps {
		if !st.OK {
			out = append(out, st.Name)
		}
	}

	return slices.Clip(out)
}
